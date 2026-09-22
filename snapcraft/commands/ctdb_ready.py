#!/usr/bin/env python3

import argparse


def mark_ready(data, identity, pnn):
    """Mark one matching CTDB metadata entry ready."""
    for node in data.get("nodes", []):
        if node.get("identity") == identity and node.get("pnn") == pnn:
            node["state"] = "ready"
            return data
    raise ValueError(f"CTDB node {identity!r} with PNN {pnn} is missing")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("metadata_uri")
    parser.add_argument("cluster_id")
    parser.add_argument("identity")
    parser.add_argument("pnn", type=int)
    args = parser.parse_args()

    from sambacc import url_opener
    from sambacc.ceph import rados

    ceph_id = f"client.smb.config.{args.cluster_id}"
    rados.enable_rados_opener(
        url_opener.URLOpener,
        client_name=ceph_id,
        full_name=True,
    )
    metadata = rados.ClusterMetaRADOSObject.create_from_uri(args.metadata_uri)
    with metadata.open(write=True, locked=True) as handle:
        data = handle.load()
        handle.dump(mark_ready(data, args.identity, args.pnn))


if __name__ == "__main__":
    main()

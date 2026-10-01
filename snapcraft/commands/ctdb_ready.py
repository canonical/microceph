#!/usr/bin/env python3

import argparse
import copy


def mark_ready(data, identity, pnn):
    """Mark one matching CTDB metadata entry ready."""
    for node in data.get("nodes", []):
        if node.get("identity") == identity and node.get("pnn") == pnn:
            node["state"] = "ready"
            return data
    raise ValueError(f"CTDB node {identity!r} with PNN {pnn} is missing")


def mark_removed(data, identity, pnn):
    """Retire only this member, retaining its PNN slot and all other members."""
    for node in data.get("nodes", []):
        if node.get("identity") == identity and node.get("pnn") == pnn:
            node["state"] = "gone"
            break
    # A failed startup may never have registered; removal is still idempotent.
    return data


def prepare_node(data, identity, pnn, address):
    """Reactivate a cleanly removed identity before CTDB needs its own PNN.

    In-place address migration remains unsupported. Check before ctdb-set-node
    can mark an existing address as changed and strand its startup path.
    """
    for node in data.get("nodes", []):
        if node.get("identity") == identity and node.get("pnn") == pnn:
            if node.get("node") != address or node.get("state") in ("changed", "replaced"):
                raise ValueError("CTDB address change for an existing identity is unsupported; use a new member identity")
            if node.get("state") == "gone":
                node["state"] = "ready"
            break
    return data


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("metadata_uri")
    parser.add_argument("cluster_id")
    parser.add_argument("identity")
    parser.add_argument("pnn", type=int)
    parser.add_argument("--state", choices=("ready", "gone", "prepare"), default="ready")
    parser.add_argument("--address")
    args = parser.parse_args()
    if args.state == "prepare" and not args.address:
        parser.error("--address is required for prepare")

    from sambacc import url_opener
    from sambacc.ceph import rados

    ceph_id = f"client.smb.config.{args.cluster_id}"
    rados.enable_rados_opener(
        url_opener.URLOpener,
        client_name=ceph_id,
        full_name=True,
    )
    metadata = rados.ClusterMetaRADOSObject.create_from_uri(args.metadata_uri)
    if args.state in ("gone", "prepare"):
        # Do not create metadata during cleanup or early preparation.
        # ctdb-set-node owns the first registration; upstream may already have
        # removed an old cluster's resource when cleanup is retried.
        from rados import ObjectNotFound
        try:
            with metadata.open(locked=False) as handle:
                current = handle.load()
        except ObjectNotFound:
            return
        if not any(node.get("identity") == args.identity and node.get("pnn") == args.pnn
                   for node in current.get("nodes", [])):
            return
    with metadata.open(write=True, locked=True) as handle:
        data = handle.load()
        before = copy.deepcopy(data)
        if args.state == "prepare":
            data = prepare_node(data, args.identity, args.pnn, args.address)
        else:
            update = mark_ready if args.state == "ready" else mark_removed
            data = update(data, args.identity, args.pnn)
        if data != before:
            handle.dump(data)


if __name__ == "__main__":
    main()

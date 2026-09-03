"""Robot Framework library: parsing of RGW replication API output.

Pure helpers (no Robot context needed) that keep RGW-specific JSON parsing
out of the shared harness, mirroring the cephfs_replication.py /
rbd_replication.py pattern. The fetch keyword (Get Rgw Replication Status)
lives in the harness and calls parse_rgw_replication_status to turn the raw
control-socket response into the status document.
"""

import json


def parse_rgw_replication_status(raw):
    """Returns the RGW replication status document from a raw API response.

    The ops/replication endpoints wrap the handler's JSON document in the
    microcluster envelope as a string, i.e. ``{"metadata": "{\\"realm\\": ...}"}``,
    so the metadata is decoded a second time when it arrives as a string.
    Raises AssertionError when the response carries no status document, so a
    failed request never reads as an empty status.
    """
    try:
        envelope = json.loads(raw)
    except (ValueError, TypeError):
        raise AssertionError(f"replication status response is not JSON: {raw!r}")

    metadata = envelope.get("metadata") if isinstance(envelope, dict) else None
    if isinstance(metadata, str):
        try:
            metadata = json.loads(metadata)
        except ValueError:
            raise AssertionError(f"replication status metadata is not JSON: {metadata!r}")
    if not isinstance(metadata, dict):
        raise AssertionError(f"replication status response has no document: {raw!r}")
    return metadata


def rgw_data_sync_states(status):
    """Returns {source_zone: state} for every data sync brief in a parsed
    status document, so a suite can assert per-peer states by name instead of
    relying on list order."""
    return {brief["source_zone"]: brief["state"] for brief in (status.get("data_sync") or [])}

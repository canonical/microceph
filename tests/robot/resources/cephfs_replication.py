"""Robot Framework library: CephFS replication list parsing and assertions.

These helpers keep JSON parsing and entry classification out of the .robot
suite. In Robot, nested loops over parsed JSON read poorly; expressing the
same logic as a small Python function is clearer and unit-testable.
"""

import json


def _classify_cephfs_list_entries(list_output):
    """Parse newline-delimited CephFS replication list JSON objects.

    Internal helper (leading underscore so Robot does not expose it as a
    keyword). *list_output* is the stdout of
    ``microceph replication list cephfs --json | jq -c '.<vol>[]'`` -- one
    compact JSON object per line. Returns the list of parsed dicts and raises
    AssertionError if no entries were produced.
    """
    items = [json.loads(line) for line in list_output.splitlines() if line.strip()]
    if not items:
        raise AssertionError("CephFS replication list returned no entries to classify")
    return items


def cephfs_replication_list_has_volume(list_output, volume):
    """Return True if the CephFS replication list has a non-empty entry for *volume*.

    *list_output* is the stdout of ``microceph replication list cephfs --json`` -- a
    JSON object keyed by volume name. Returns True only when *volume* is present and
    maps to a non-empty value. An absent key, an empty entry, or unparseable/empty
    output all return False, so a poller keeps waiting rather than treating a missing
    volume as a successful sync (jq's ``.<vol> == {}`` could not distinguish an absent
    key from a populated one, since ``null == {}`` is false).
    """
    try:
        data = json.loads(list_output)
    except (ValueError, TypeError):
        return False
    if not isinstance(data, dict):
        return False
    return bool(data.get(volume))


def verify_cephfs_list_entry_types(list_output):
    """Assert every CephFS replication list entry's type matches its path.

    Paths containing ``volumes`` (i.e. ``/volumes/...`` subvolume paths) must
    have resource_type ``subvolume``; every other entry is a ``directory``
    configured via a mirror dir-path. Raises AssertionError on the first
    mismatch, naming the offending entry. Returns the parsed entries so callers
    can log how many were classified.
    """
    items = _classify_cephfs_list_entries(list_output)
    for item in items:
        path = item["resource_path"]
        rtype = item["resource_type"]
        expected = "subvolume" if "volumes" in path else "directory"
        if rtype != expected:
            raise AssertionError(
                f"Expected {expected} type for path {path}, got {rtype}"
            )
    return items

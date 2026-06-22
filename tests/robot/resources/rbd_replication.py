"""Robot Framework library: parsing of RBD replication command output.

Pure helpers (no Robot context needed) that keep RBD-specific JSON/text parsing
out of the shared harness, mirroring the cephfs_replication.py / snap_services.py
pattern. The fetch keywords (Get Synced Image Count On Node, etc.) live in the
harness and call these to turn raw `microceph replication list rbd --json` /
`microceph.rbd mirror pool status --verbose` output into a value.
"""

import json


def rbd_synced_image_count(json_text):
    """Returns the total RBD images across all replication-list entries.

    Mirrors ``microceph replication list rbd --json |
    jq '[.[].Images | length] | add // 0'``: parse the JSON list and sum the
    length of each entry's ``Images`` list. Returns 0 on any parse error.
    """
    try:
        data = json.loads(json_text)
    except (ValueError, TypeError):
        return 0
    return sum(len(entry.get("Images") or []) for entry in data)


def rbd_primary_image_count(json_text):
    """Returns the count of primary RBD images across all replication-list entries.

    Mirrors ``microceph replication list rbd --json |
    jq '[.[].Images[] | select(.is_primary==true)] | length'``: parse the JSON
    list and count images whose ``is_primary`` is True. Returns 0 on any parse error.
    """
    try:
        data = json.loads(json_text)
    except (ValueError, TypeError):
        return 0
    count = 0
    for entry in data:
        for image in (entry.get("Images") or []):
            if image.get("is_primary") is True:
                count += 1
    return count


def rbd_mirror_health(verbose_text):
    """Returns the RBD mirror pool health from ``rbd mirror pool status --verbose`` text.

    Mirrors ``sed -n 's/^health: //p' | head -1`` plus the empty->UNKNOWN guard:
    return the value after the first line starting with "health: " (stripped),
    or "UNKNOWN" when no such line exists or the value is empty.
    """
    for line in verbose_text.splitlines():
        if line.startswith("health: "):
            health = line[len("health: "):].strip()
            return health if health else "UNKNOWN"
    return "UNKNOWN"

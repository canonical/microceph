"""Pure parsers for the microceph auth rotation and status commands and API bodies.

These helpers parse raw JSON and text outputs from 'microceph auth rotate'
and 'microceph auth status' (the "fetch raw, decide in Python" rule).
All functions are module-level and pure -- no self, no BuiltIn -- so they
are unit-testable from test_harness_helpers.py without a running Robot context.

This module is imported by microceph_harness.py; it is NOT loaded as a Robot
library, so its function names never collide with harness keyword names.
"""

import json


def _parse(raw):
    """Return the decoded JSON value, or None when raw is not valid JSON."""
    if not raw:
        return None
    try:
        return json.loads(raw)
    except (ValueError, TypeError):
        return None


def parse_auth_status(raw):
    """Parse JSON output from 'microceph auth status --json'.

    Returns a dict with keys:
    - status (str)
    - state (str)
    - blocker (str)
    - target_key_type (str)
    - client_distribution (dict of cipher -> list of clients)
    """
    data = _parse(raw)
    if not isinstance(data, dict):
        return {
            "status": "",
            "state": "",
            "blocker": "",
            "target_key_type": "",
            "client_distribution": {},
        }

    # If wrapped in metadata (API response) or direct CLI response:
    metadata = data.get("metadata", data)
    if not isinstance(metadata, dict):
        metadata = data

    return {
        "status": str(metadata.get("status", "")),
        "state": str(metadata.get("state", "")),
        "blocker": str(metadata.get("blocker", "")),
        "target_key_type": str(metadata.get("target_key_type", "")),
        "client_distribution": dict(metadata.get("client_distribution", {})),
    }


def parse_auth_status_text(text):
    """Parse text output from 'microceph auth status'.

    Extracts 'Status: ...' and 'Blocker: ...' into a dict.
    """
    result = {"status": "", "blocker": ""}
    if not text:
        return result

    for line in text.splitlines():
        line = line.strip()
        if line.startswith("Status:"):
            result["status"] = line.split("Status:", 1)[1].strip()
        elif line.startswith("Blocker:"):
            result["blocker"] = line.split("Blocker:", 1)[1].strip()

    return result


def is_auth_rotation_completed(raw):
    """Return True if the rotation state is 'completed'."""
    status = parse_auth_status(raw)
    return status["state"] == "completed"


def is_auth_rotation_blocked(raw):
    """Return True if the rotation state is 'blocked'."""
    status = parse_auth_status(raw)
    return status["state"] == "blocked" or status["status"] == "blocked"


def auth_status_blocker_contains(raw, substring):
    """Return True if the blocker contains the expected substring."""
    status = parse_auth_status(raw)
    blocker = status["blocker"]
    if not blocker:
        text_status = parse_auth_status_text(raw)
        blocker = text_status["blocker"]
    return substring.lower() in blocker.lower()


def all_clients_use_cipher(raw, cipher):
    """Return True if all clients in client_distribution use the specified cipher."""
    status = parse_auth_status(raw)
    dist = status["client_distribution"]
    if not dist:
        return status["status"].lower() == f"all client {cipher}".lower()
    return list(dist.keys()) == [cipher]

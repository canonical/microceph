"""Inspect RGW secret storage inside the guest without exporting secret bytes."""

import argparse
import base64
import json
from pathlib import Path
import subprocess

import placement_status


def material_needles(*materials):
    """Recognize raw PEM, JSON-escaped PEM, and the API's base64 representation."""
    needles = []
    for material in materials:
        needles.extend((material, base64.b64encode(material)))
        needles.append(json.dumps(material.decode("ascii"))[1:-1].encode())
        needles.extend(line for line in material.splitlines() if len(line) >= 32 and not line.startswith(b"-----"))
    return tuple(dict.fromkeys(needle for needle in needles if needle))


def file_contains_material(path, needles):
    """Scan bounded chunks while retaining matches across chunk boundaries."""
    overlap = max(map(len, needles)) - 1
    previous = b""
    with path.open("rb") as source:
        while True:
            chunk = source.read(65536)
            if not chunk:
                return False
            data = previous + chunk
            if any(needle in data for needle in needles):
                return True
            previous = data[-overlap:] if overlap else b""


def find_material_leaks(prefix, allowed):
    """Return leak locations only; never include the matching material."""
    source = Path(prefix)
    needles = material_needles((source / "server.crt").read_bytes(), (source / "server.key").read_bytes())
    allowed = {Path(path).resolve() for path in allowed}
    root = Path("/var/snap/microceph/common")
    locations = []
    response = subprocess.run(
        ["curl", "--fail", "--silent", "--show-error", "--unix-socket", str(root / "state/control.socket"), "http://localhost/1.0/placement"],
        check=True, capture_output=True,
    ).stdout
    if placement_status.placement_leaks_rgw_secrets(response.decode()) or any(needle in response for needle in needles):
        locations.append("GET /placement")

    files = {path for path in root.iterdir() if path.is_file()}
    for directory in ("state", "logs", "rgw-tls"):
        files.update(path for path in (root / directory).rglob("*") if path.is_file())
    for path in sorted(files):
        if path.resolve() in allowed:
            continue
        if file_contains_material(path, needles):
            locations.append(str(path))

    # Both the control daemon and the gateway itself log to their own units.
    for unit in ("snap.microceph.daemon", "snap.microceph.rgw"):
        journal = subprocess.run(
            ["journalctl", "--no-pager", "--output=cat", f"--unit={unit}"],
            check=True, capture_output=True,
        ).stdout
        if any(needle in journal for needle in needles):
            locations.append(f"{unit} journal")
    return locations


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("prefix")
    parser.add_argument("--allowed", action="append", default=[])
    args = parser.parse_args()
    print(json.dumps(find_material_leaks(args.prefix, args.allowed)))


if __name__ == "__main__":
    main()

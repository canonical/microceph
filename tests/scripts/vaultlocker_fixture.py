#!/usr/bin/env python3
"""Private, VM-local Vault/AppRole fixture for the Vaultlocker Robot suite.

Never accept an operator-supplied disk or print Vault responses/configuration.
Only sparse files and their verified loop devices in this disposable VM are used.
Credentials, server data and failure details stay private inside the VM; they are
not CI artifacts. This fixture intentionally does not model reboot recovery.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import traceback
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
import uuid

PRIVATE = Path("/run/microceph-vaultlocker-ci")
STATE = PRIVATE / "state.json"
COMMON = Path("/var/snap/vaultlocker/common/microceph-ci")
CONFIG = COMMON / "vaultlocker.conf"
VAULT_COMMON = Path("/var/snap/vault/common/microceph-ci")
VAULT_ADDR = "http://127.0.0.1:18200"
MOUNT = "vaultlocker-ci"
NAMES = ("fresh", "filesystem", "partitioned", "bluestore", "raw")


def parse_mapper_result(output):
    """Return only a validated mapper path and LUKS UUID, never arbitrary fields."""
    result = json.loads(output)
    identifier = str(uuid.UUID(result.get("luks_uuid", "")))
    mapper = result.get("mapper_path")
    if mapper != f"/dev/mapper/crypt-{identifier}":
        raise ValueError("Unexpected mapper path")
    return {"luks_uuid": identifier, "mapper_path": mapper}


def loop_binding_matches(records, device, backing_file):
    """Check that a loop device belongs to the fixture's own backing file."""
    return bool(re.fullmatch(r"/dev/loop[0-9]+", device)) and any(
        record.get("name") == device and record.get("back-file") == str(backing_file)
        for record in records.get("loopdevices", [])
    )


def safe_failure(error):
    """Describe a failed operation without exposing credentials or command output."""
    suffix = (
        f" (exit {error.returncode})" if isinstance(error, subprocess.CalledProcessError) else ""
    )
    return type(error).__name__ + suffix


def run(*args, input=None, timeout=180):
    return subprocess.run(
        args,
        input=input,
        capture_output=True,
        text=True,
        check=True,
        timeout=timeout,
    ).stdout


def write_private(path, text):
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(text)
    temporary.chmod(0o600)
    temporary.replace(path)


def save(state):
    write_private(STATE, json.dumps(state))


def vault(path, data=None, token=None, method=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["X-Vault-Token"] = token
    request = Request(
        f"{VAULT_ADDR}/v1/{path}",
        data=json.dumps(data).encode() if data is not None else None,
        headers=headers,
        method=method,
    )
    with urlopen(request, timeout=10) as response:
        body = response.read()
    return json.loads(body) if body else {}


def start_vault():
    PRIVATE.mkdir(mode=0o700)  # Refuse to reuse a previous fixture.
    COMMON.mkdir(mode=0o700)
    VAULT_COMMON.mkdir(mode=0o700)
    (VAULT_COMMON / "data").mkdir(mode=0o700)
    write_private(
        VAULT_COMMON / "vault.hcl",
        f'storage "file" {{ path = "{VAULT_COMMON}/data" }}\n'
        'listener "tcp" { address = "127.0.0.1:18200" tls_disable = true }\n'
        "disable_mlock = true\n",
    )
    write_private(
        Path("/etc/systemd/system/microceph-vaultlocker-ci.service"),
        "[Unit]\nDescription=Private Vaultlocker CI Vault\n"
        "[Service]\n"
        f"ExecStart=/snap/bin/vault server -config={VAULT_COMMON}/vault.hcl\n"
        "Restart=on-failure\n",
    )
    run("systemctl", "daemon-reload")
    run("systemctl", "start", "microceph-vaultlocker-ci")
    return {"started": True}


def vault_ready():
    try:
        vault("sys/init")
        return {"ready": True}
    except (URLError, HTTPError):
        return {"ready": False}


def setup():
    initialized = vault("sys/init", {"secret_shares": 1, "secret_threshold": 1}, method="PUT")
    state = {
        "token": initialized["root_token"],
        "unseal_key": initialized["keys_base64"][0],
        "devices": {},
        "mappers": {},
    }
    save(state)
    vault("sys/unseal", {"key": state["unseal_key"]}, method="PUT")
    token = state["token"]
    vault(f"sys/mounts/{MOUNT}", {"type": "kv", "options": {"version": "2"}}, token)
    vault("sys/auth/approle", {"type": "approle"}, token)
    policy = (
        f'path "{MOUNT}/data/*" {{ capabilities = ["create", "read", "update", "delete"] }}\n'
        f'path "{MOUNT}/metadata/*" {{ capabilities = ["read", "delete", "list"] }}\n'
    )
    vault(f"sys/policies/acl/{MOUNT}", {"policy": policy}, token, method="PUT")
    role = f"auth/approle/role/{MOUNT}"
    vault(role, {"token_policies": [MOUNT], "token_ttl": "1h", "token_max_ttl": "4h"}, token)
    role_id = vault(f"{role}/role-id", token=token)["data"]["role_id"]
    secret_id = vault(f"{role}/secret-id", {}, token)["data"]["secret_id"]
    # All secret material remains inside root-only guest paths, never shell argv.
    write_private(
        CONFIG,
        "[DEFAULT]\nhostname = microceph-ci\n[vault]\n"
        f"url = {VAULT_ADDR}\napprole = {role_id}\nsecret_id = {secret_id}\n"
        f"backend = {MOUNT}\nkv_version = 2\n",
    )
    return {"kv_version": 2, "auth": "approle"}


def owned_device(state, name):
    device = state["devices"][name]
    records = json.loads(run("losetup", "--json", "--list", "--output", "NAME,BACK-FILE"))
    if not loop_binding_matches(records, device, COMMON / f"{name}.img"):
        raise ValueError("Refusing an unowned loop device")
    return device


def create_device(state, name):
    image = COMMON / f"{name}.img"
    with image.open("xb") as stream:
        stream.truncate((8 if name in ("fresh", "bluestore") else 4) * 1024**3)
    device = run("losetup", "--find", "--show", str(image)).strip()
    state["devices"][name] = device
    save(state)
    owned_device(state, name)
    link = Path(f"/dev/disk/by-id/microceph-vaultlocker-ci-{name}")
    link.parent.mkdir(parents=True, exist_ok=True)
    link.symlink_to(device)
    return str(link)


def encrypt(state, name):
    device = create_device(state, name)
    result = parse_mapper_result(
        run(
            "vaultlocker",
            "--retry",
            "3",
            "--config",
            str(CONFIG),
            "encrypt",
            device,
            timeout=300,
        )
    )
    state["mappers"][name] = result
    save(state)
    # Read the real KV v2 response, but export only presence/version, never the key.
    escrow = vault(
        f"{MOUNT}/data/microceph-ci/{result['luks_uuid']}",
        token=state["token"],
    )["data"]
    return dict(
        result,
        kv_version=2,
        key_escrowed=bool(escrow["data"].get("dmcrypt_key")),
        secret_version=escrow["metadata"]["version"],
    )


def snapshot(state, name):
    device = owned_device(state, name)
    path = state["mappers"][name]["mapper_path"] if name != "raw" else device
    with open(path, "rb", buffering=0) as stream:
        sample = stream.read(4 * 1024**2)
    signatures = json.loads(run("wipefs", "--no-act", "--json", path))
    return {
        "path": path,
        "checksum": hashlib.sha256(sample).hexdigest(),
        "nonzero": any(sample),
        "signatures": signatures,
    }


def prepare(state, name):
    if name == "raw":
        create_device(state, name)
        with open(owned_device(state, name), "r+b", buffering=0) as stream:
            stream.write(b"Ordinary non-pristine disk: must not be erased.\n")
    else:
        result = encrypt(state, name)
        mapper = result["mapper_path"]
        if name == "filesystem":
            run("mkfs.ext4", "-F", mapper)
        elif name == "partitioned":
            run("sfdisk", "--no-reread", "--no-tell-kernel", mapper, input="label: gpt\n,256M,L\n")
        elif name == "bluestore":
            owned_device(state, "fresh")
            with open(state["mappers"]["fresh"]["mapper_path"], "rb", buffering=0) as source:
                with open(mapper, "r+b", buffering=0) as target:
                    target.write(source.read(4 * 1024**2))
        else:
            raise ValueError("Unsupported negative fixture")
    return snapshot(state, name)


def object_io():
    # MicroCeph's snap can only see paths in its own common directory, not host /tmp.
    directory = Path("/var/snap/microceph/common/vaultlocker-ci")
    directory.mkdir(mode=0o700)
    source, restored = directory / "source", directory / "restored"
    source.write_bytes(os.urandom(1024**2))
    run("microceph.ceph", "config", "set", "mon", "mon_allow_pool_size_one", "true")
    run("microceph.ceph", "osd", "pool", "create", "vaultlocker-ci", "8")
    run(
        "microceph.ceph",
        "osd",
        "pool",
        "set",
        "vaultlocker-ci",
        "size",
        "1",
        "--yes-i-really-mean-it",
    )
    run("microceph.ceph", "osd", "pool", "set", "vaultlocker-ci", "min_size", "1")
    run("microceph.rados", "-p", "vaultlocker-ci", "put", "proof", str(source), timeout=120)
    run("microceph.rados", "-p", "vaultlocker-ci", "get", "proof", str(restored), timeout=120)
    return {
        "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
        "restored_sha256": hashlib.sha256(restored.read_bytes()).hexdigest(),
        "restored_bytes": restored.stat().st_size,
    }


def diagnostics():
    # Deliberately exclude Vault/Vaultlocker logs, config, state and secret APIs.
    commands = [
        ("snaps", ["snap", "list"]),
        ("vaultlocker_interfaces", ["snap", "connections", "vaultlocker"]),
        ("microceph_interfaces", ["snap", "connections", "microceph"]),
        ("block_devices", ["lsblk", "-o", "NAME,TYPE,SIZE,FSTYPE"]),
        ("microceph_disks", ["microceph", "disk", "list", "--json"]),
        ("ceph_status", ["microceph.ceph", "status", "--format=json"]),
    ]
    results = {}
    for name, command in commands:
        try:
            results[name] = run(*command, timeout=30)
        except Exception as error:
            results[name] = safe_failure(error)
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "operation",
        choices=("start", "ready", "setup", "encrypt", "prepare", "snapshot", "io", "diagnostics"),
    )
    parser.add_argument("name", choices=NAMES, nargs="?", default="fresh")
    args = parser.parse_args()
    os.umask(0o077)
    try:
        if args.operation == "start":
            result = start_vault()
        elif args.operation == "ready":
            result = vault_ready()
        elif args.operation == "setup":
            result = setup()
        elif args.operation == "io":
            result = object_io()
        elif args.operation == "diagnostics":
            result = diagnostics()
        else:
            state = json.loads(STATE.read_text())
            result = {"encrypt": encrypt, "prepare": prepare, "snapshot": snapshot}[
                args.operation
            ](state, args.name)
        print(json.dumps(result))
    except Exception as error:
        # Keep useful private diagnostics in the guest; never emit an exception
        # message, subprocess argv/stdout/stderr, HTTP response or credentials.
        try:
            if PRIVATE.is_dir():
                detail = traceback.format_exc()
                if isinstance(error, subprocess.CalledProcessError):
                    detail += f"\nSTDOUT: {error.stdout}\nSTDERR: {error.stderr}\n"
                write_private(PRIVATE / "failure.log", detail)
        except OSError:
            pass  # A full/read-only disk must not fall back to an unsafe traceback.
        print(
            f"Fixture {args.operation}/{args.name}: {safe_failure(error)}",
            file=sys.stderr,
        )
        raise SystemExit(1)


if __name__ == "__main__":
    main()

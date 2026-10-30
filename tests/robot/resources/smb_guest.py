#!/usr/bin/env python3
"""Guest-only local-user fixture; no placement, restart or polling policy."""
import copy
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys

BASE = Path("/var/snap/microceph/common/robot-smb")
ROLES = ("smbd", "ctdbd", "daemon")
DATA_PATHS = {"robot-never": "/independent", "robot-other": "/independent-other", "robot-smb": "/clustered"}
PAYLOAD = b"MicroCeph Robot SMB sentinel\n" + bytes(range(256)) * 16


def resource_documents(addresses, password, refresh_password):
    """Direct, explicitly nonclustered resources; no future feature inputs."""
    users = {"resource_type": "ceph.smb.usersgroups", "users_groups_id": "robot-users",
             "values": {"users": [{"name": "alice", "password": password, "uid": 2201, "gid": 2200}],
                        "groups": [{"name": "robot-team", "gid": 2200}]}}
    def resources(name, port):
        return {"resources": [users,
            {"resource_type": "ceph.smb.cluster", "cluster_id": name, "auth_mode": "user",
             "clustering": "always" if name == "robot-smb" else "never", "user_group_settings": [{"source_type": "resource", "ref": "robot-users"}],
             "bind_addrs": [{"address": address} for address in addresses], "custom_ports": {"smb": port}},
            {"resource_type": "ceph.smb.share", "cluster_id": name, "share_id": "data",
             "cephfs": {"volume": "robotsmb", "path": DATA_PATHS[name], "provider": "samba-vfs/new"}}]}
    refreshed = copy.deepcopy(resources("robot-never", 3446))
    refreshed["resources"][0]["values"]["users"][0]["password"] = refresh_password
    refreshed["resources"][2]["readonly"] = True
    return {"nonclustered": resources("robot-never", 3445), "independent": resources("robot-other", 4445),
            "refresh": refreshed, "clustered": resources("robot-smb", 1445),
            "clustered-refresh": resources("robot-smb", 2445)}


def safe_apply_result(rc, stdout, stderr, passwords):
    """Never copy resource documents/passwords into Robot output."""
    try:
        result = json.loads(stdout)
        success = rc == 0 and result.get("success") is True
        message = "; ".join(str(item.get("msg", "")) for item in result.get("results", []))
    except (ValueError, TypeError, AttributeError):
        success, message = False, "SMB apply returned no valid result"
    message += " " + stderr
    for password in passwords:
        message = message.replace(password, "<redacted>")
    return {"success": success, "rc": rc, "message": message.strip()}


def initialize(addresses):
    BASE.mkdir(mode=0o700, parents=True, exist_ok=True)
    password, wrong, refreshed = (secrets.token_urlsafe(24) for _ in range(3))
    for name, value in (("alice", password), ("wrong", wrong), ("refreshed", refreshed)):
        (BASE / f"{name}.auth").write_text(f"username = alice\npassword = {value}\ndomain = WORKGROUP\n")
    for kind, document in resource_documents(addresses, password, refreshed).items():
        (BASE / f"{kind}.json").write_text(json.dumps(document))
    (BASE / "payload.bin").write_bytes(PAYLOAD)


def apply_resource(kind):
    if kind not in ("nonclustered", "independent", "refresh", "clustered", "clustered-refresh"):
        raise ValueError("unknown test resource")
    result = subprocess.run(["/snap/bin/microceph.ceph", "smb", "apply", "-i", str(BASE / f"{kind}.json"), "-f", "json"], capture_output=True, text=True, timeout=120)
    (BASE / f"{kind}.result").write_text(result.stdout + "\n" + result.stderr)
    passwords = [(BASE / f"{name}.auth").read_text().split("password = ", 1)[1].splitlines()[0] for name in ("alice", "wrong", "refreshed")]
    safe = safe_apply_result(result.returncode, result.stdout, result.stderr, passwords)
    print(json.dumps(safe))
    return 0 if safe["success"] else 1


def cephfs_operation(action, filename=None, cluster="robot-never"):
    import cephfs
    config = Path(os.environ["SNAP_DATA"]) / "conf"
    fs = cephfs.LibCephFS(conffile=str(config / "ceph.conf"), auth_id="admin")
    try:
        fs.conf_set("keyring", str(config / "ceph.keyring"))
        fs.conf_set("client_fs", "robotsmb")
        fs.mount()
        if action == "prepare-fs":
            fs.chmod("/", 0o755)
            for path in DATA_PATHS.values():
                fs.mkdir(path, 0o770)
                fs.chown(path, 2201, 2200)
                fs.chmod(path, 0o770)
        else:
            handle = fs.open(DATA_PATHS[cluster] + "/" + filename, "r")
            try:
                content = fs.read(handle, 0, 1024 * 1024)
            finally:
                fs.close(handle)
            print(hashlib.sha256(content).hexdigest())
    finally:
        fs.shutdown()


def retained_user_sid():
    # The caller established private NSS and checked prepared retained databases
    # before any Samba import: even SID queries must never reconstruct state.
    from sambacc.passdb_loader import PassDBLoader
    root = Path(os.environ["SMB_ROOT"])
    loader = PassDBLoader(str(root / "config/smb.conf"))
    print(str(loader._pdb.getsampwnam("alice").user_sid))


def snapshot(cluster):
    root = Path("/var/snap/microceph/common/smb") / cluster
    def text(path):
        try:
            return path.read_text().strip()
        except FileNotFoundError:
            return ""
    try:
        with socket.socket(socket.AF_UNIX) as client:
            client.settimeout(60)
            client.connect("/var/snap/microceph/common/state/control.socket")
            client.sendall(b"GET /1.0/services/smb HTTP/1.0\r\nHost: localhost\r\n\r\n")
            chunks = []
            while True:
                data = client.recv(65536)
                if not data:
                    break
                chunks.append(data)
            api = b"".join(chunks).split(b"\r\n\r\n", 1)[1].decode()
    except (OSError, IndexError):
        api = None
    context = json.loads(text(root / "config/context.json") or "{}")
    services = subprocess.run(["systemctl", "show", *[f"snap.microceph.{r}.service" for r in ROLES], "-p", "Id", "-p", "ActiveState", "-p", "MainPID", "-p", "ExecMainStartTimestampMonotonic"], capture_output=True, text=True, timeout=10)
    try:
        generation = (root / 'applied.json').stat().st_mtime_ns
    except FileNotFoundError:
        generation = None
    print(json.dumps({"applied_generation": generation, "api": api, "owner": text(root.parent / "owner.json"), "local_exists": root.exists(),
                      "context": {key: context.get(key) for key in ("frontend_address", "smb_port", "startup", "clustered", "ctdb_pnn")},
                      "member": text(root / "config/ctdb-identity"), "rank": text(root / "config/ctdb-rank"),
                      "services": services.stdout, "boot_id": text(Path("/proc/sys/kernel/random/boot_id"))}))


if __name__ == "__main__":
    os.umask(0o077)
    operation = sys.argv[1]
    if operation == "init":
        initialize(json.loads(sys.argv[2]))
    elif operation == "apply":
        sys.exit(apply_resource(sys.argv[2]))
    elif operation == "identity":
        retained_user_sid()
    elif operation == "snapshot":
        snapshot(sys.argv[2])
    elif operation == "prepare-fs":
        cephfs_operation(operation)
    elif operation == "read-data":
        cephfs_operation(operation, sys.argv[3], sys.argv[2])
    else:
        raise ValueError("unknown fixture operation")

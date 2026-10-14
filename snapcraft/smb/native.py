"""Native text-config adapter for the pinned Sambacc consumer.

This module does not generate shares, allocate identities or supervise apps.
"""
import copy
import io

from sambacc.config import GlobalConfig
from sambacc.netcmd_loader import template_config


def combine_sources(document, sources):
    """Combine ordered identity sources without changing positional defaults."""
    result = copy.deepcopy(document)
    for kind in ("users", "groups"):
        entries = list(result.get(kind, {}).get("all_entries", []))
        for source in sources:
            entries.extend(copy.deepcopy(source.get(kind, {}).get("all_entries", [])))
        names = set()
        for entry in entries:
            name = entry["name"]
            if name in names:
                raise ValueError("duplicate " + kind + " name")
            names.add(name)
        result[kind] = {"all_entries": entries}
    return result


def reject_public_addresses(document):
    """Reject excluded public-address requests, including effective overrides."""
    if isinstance(document, dict):
        for name, value in document.items():
            if name in ("public_addrs", "cluster_public_addrs", "public_addresses") and value:
                raise ValueError("public addresses require a separate HA/LB implementation")
            reject_public_addresses(value)
    elif isinstance(document, list):
        for value in document:
            reject_public_addresses(value)


def _overlay(options, required):
    for key, value in list(options.items()):
        normalized = " ".join(key.lower().split())
        if normalized in required:
            if str(value) != str(required[normalized]):
                raise ValueError("native option conflict: " + normalized)
            del options[key]
    options.update(required)


def _normalize_native_share_options(options):
    required = {"vfs objects"}
    for module in ("ceph", "ceph_new"):
        required.update(module + ":" + name for name in ("config_file", "filesystem", "user_id", "proxy"))
    native = {}
    for key, value in list(options.items()):
        normalized = " ".join(key.lower().split())
        if normalized not in required:
            continue
        if normalized in native and native[normalized] != value:
            raise ValueError("native option conflict: " + normalized)
        native[normalized] = value
        del options[key]
    options.update(native)


def render(document, cluster_id, root, context, sources):
    """Render upstream shares through Sambacc with explicit native path overlays."""
    reject_public_addresses(document)
    doc = combine_sources(document, sources)
    instance = doc["configs"][cluster_id]
    required = {
        "config backend": "file",
        "private dir": root + "/samba/private",
        "state directory": root + "/samba/state",
        "cache directory": root + "/samba/cache",
        "lock directory": root + "/samba/lock",
        "pid directory": root + "/samba/run",
        "ncalrpc dir": root + "/samba/ncalrpc",
        "log file": root + "/diagnostics/log.%m",
        "smb ports": str(context.get("smb_port", 445)),
    }
    for name in instance.get("globals", []):
        _overlay(doc["globals"][name].setdefault("options", {}), required)
    doc["globals"]["_native"] = {"options": required}
    instance.setdefault("globals", []).append("_native")
    for name in instance.get("shares", []):
        options = doc["shares"][name].setdefault("options", {})
        _normalize_native_share_options(options)
        for module in ("ceph", "ceph_new"):
            key = module + ":config_file"
            if key not in options:
                continue
            if options[key] != "/etc/ceph/ceph.conf":
                raise ValueError("native option conflict: " + key)
            mode = "proxy" if module == "ceph_new" and str(options.get("ceph_new:proxy", "no")).lower() == "yes" else "direct"
            options[key] = root + "/config/ceph-" + mode + ".conf"
    output = io.StringIO()
    template_config(output, GlobalConfig(initial_data=doc).get(cluster_id))
    return output.getvalue()


def compile_core(candidate, root):
    """Construct a complete private launch projection before any live writes."""
    spec = candidate["spec"]
    reject_public_addresses(candidate)
    cluster_id = spec["cluster_id"]
    document = combine_sources(candidate["config"], candidate.get("users") or [])
    instance = GlobalConfig(initial_data=document).get(cluster_id)
    globals_effective = {" ".join(k.lower().split()): str(v).lower() for k, v in instance.global_options()}
    if instance.with_ctdb or instance.with_addc or globals_effective.get("security") in ("ads", "domain"):
        raise ValueError("effective mode requires native cluster/domain preparation from the next feature stage")
    ports = spec.get("custom_ports") or {}
    context = {"proxy": False, "data_checks": [], "smb_port": ports.get("smb", 445), "metrics_port": ports.get("smbmetrics", 9922), "custom_dns": bool(spec.get("custom_dns"))}
    for share in instance.shares():
        options = dict(share.share_options())
        _normalize_native_share_options(options)
        for module in ("ceph", "ceph_new"):
            if module not in options.get("vfs objects", "").split():
                continue
            context["proxy"] |= module == "ceph_new" and options.get("ceph_new:proxy", "no").lower() == "yes"
            user = options.get(module + ":user_id")
            if user != "smb.fs.cluster." + cluster_id:
                raise ValueError("native option conflict: Ceph data identity")
            context["data_checks"].append({"filesystem": options[module + ":filesystem"], "path": options["path"], "user": user})
    files = {"smb.conf": render(document, cluster_id, root, context, [])}
    # Keep only the base service identities plus the complete managed projection.
    users = list(instance.users())
    groups = list(instance.groups())
    files["passwd"] = "root:x:0:0:root:/root:/bin/false\nnobody:x:65534:65534:nobody:/nonexistent:/bin/false\n"
    files["group"] = "root:x:0:\nnogroup:x:65534:\n"
    raw_users = {entry["name"]: entry for entry in document["users"]["all_entries"]}
    raw_groups = {entry["name"]: entry for entry in document["groups"]["all_entries"]}
    for user in users:
        fields = list(user.passwd_fields())
        # Sambacc 0.9 treats explicit zero as false. The native explicit-ID
        # contract preserves it; omitted IDs still use the pinned positions.
        fields[2] = str(raw_users[user.username].get("uid", user.uid))
        fields[3] = str(raw_users[user.username].get("gid", user.gid))
        for value in fields:
            if any(c in value for c in ":\n\r\0"):
                raise ValueError("invalid NSS user field")
        files["passwd"] += ":".join(fields) + "\n"
        if not (user.nt_passwd or user.plaintext_passwd):
            raise ValueError("managed user lacks a password or hash")
    for group in groups:
        fields = list(group.group_fields())
        fields[2] = str(raw_groups.get(group.groupname, {}).get("gid", group.gid))
        if any(any(c in value for c in ":\n\r\0") for value in fields):
            raise ValueError("invalid NSS group field")
        files["group"] += ":".join(fields) + "\n"
    files["nsswitch.conf"] = "passwd: files\ngroup: files\nhosts: files dns\nservices: files\n"
    if context["custom_dns"]:
        import ipaddress
        files["resolv.conf"] = "".join("nameserver " + str(ipaddress.ip_address(a)) + "\n" for a in spec["custom_dns"])
    return files, context


def role_command(role, root, context, snap):
    """Select a fixed foreground binary, never an executable from input data."""
    conf = root + "/config/smb.conf"
    if role == "smbd":
        return [snap + "/bin/smbd", "--foreground", "--no-process-group", "--configfile", conf]
    if role == "cephfs-proxy":
        return [snap + "/bin/libcephfsd", "--socket", root + "/proxy-work/libcephfsd.sock"]
    if role == "smbmetrics":
        return [snap + "/bin/smbmetrics", "--address", "0.0.0.0", "--port", str(context["metrics_port"])]
    raise ValueError("native role is not prepared: " + role)


def launch_mounts(role, root, context):
    """One non-propagating namespace and private resolver/NSS view per app."""
    operations = [(None, "/", (1 << 14) | (1 << 19))]  # MS_REC | MS_SLAVE
    if role == "smbd":
        for name in ("passwd", "group", "nsswitch.conf"):
            operations.append((root + "/config/" + name, "/etc/" + name, 4096))
    if context.get("custom_dns"):
        operations.append((root + "/config/resolv.conf", "/etc/resolv.conf", 4096))
    return operations


def data_config(text, cluster_id, root):
    """Adapt monitor/config context without exposing the admin keyring to VFS."""
    import configparser
    parser = configparser.ConfigParser(interpolation=None, strict=False)
    parser.read_string(text)
    for section in list(parser.sections()):
        if section != "global":
            parser.remove_section(section)
    if "global" not in parser:
        parser["global"] = {}
    parser["global"]["keyring"] = root + "/identity/data.keyring"
    parser["client.smb.fs.cluster." + cluster_id] = {"keyring": root + "/identity/data.keyring"}
    output = io.StringIO()
    parser.write(output)
    return output.getvalue()


def atomic_write(path, data):
    import os
    from pathlib import Path
    path = Path(path)
    tmp = path.with_name(path.name + ".tmp")
    try:
        with tmp.open("wb") as out:
            os.chmod(tmp, 0o600)
            out.write(data.encode() if isinstance(data, str) else data)
        os.replace(tmp, path)
    finally:
        tmp.unlink(missing_ok=True)


def publish(root):
    """Publish a validated projection only while Go has stopped all consumers."""
    from pathlib import Path
    root = Path(root)
    for file in sorted((root / "config/next").iterdir()):
        atomic_write(root / "config" / file.name, file.read_bytes())


def load_launch(root):
    import json
    from pathlib import Path
    root = Path(root)
    if (root / "stop-requested").exists():
        raise ValueError("SMB stop requested")
    return json.loads((root / "config/context.json").read_text())


def compile_files(root):
    """Render, target-validate and non-mutatingly prove data grants before stop."""
    import json
    import os
    import subprocess
    from pathlib import Path
    root = Path(root)
    candidate = json.loads((root / "config/candidate.next.json").read_text())
    files, context = compile_core(candidate, str(root))
    ceph_text = candidate["ceph_config"]
    ceph_conf = data_config(ceph_text, candidate["spec"]["cluster_id"], str(root))
    files["ceph-direct.conf"] = files["ceph-proxy.conf"] = ceph_conf
    files["context.json"] = json.dumps(context, sort_keys=True)
    files["candidate.json"] = json.dumps(candidate, sort_keys=True)
    next_dir = root / "config/next"
    next_dir.mkdir(mode=0o700, exist_ok=True)
    # Remove obsolete candidate-only files, never live state or shared data.
    for file in next_dir.iterdir():
        if file.name not in files:
            file.unlink()
    for name, data in files.items():
        atomic_write(next_dir / name, data)
    subprocess.run([os.environ["SNAP"] + "/bin/testparm", "--suppress-prompt", str(next_dir / "smb.conf")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if context["data_checks"]:
        import cephfs
        for check in context["data_checks"]:
            fs = cephfs.LibCephFS(conffile=str(next_dir / "ceph-direct.conf"), auth_id=check["user"])
            try:
                fs.conf_set("client_fs", check["filesystem"])
                fs.mount()
                fs.stat(check["path"])
            finally:
                fs.shutdown()


def enter_launch_context(role, root, context):
    """Enter the strictly confined app's own private mount context, then exec."""
    import ctypes
    import os
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.unshare(0x00020000) != 0:  # CLONE_NEWNS
        raise OSError(ctypes.get_errno(), "native mount namespace")
    libc.mount.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_ulong, ctypes.c_char_p]
    for source, target, flags in launch_mounts(role, str(root), context):
        if libc.mount(source.encode() if source else None, target.encode(), None, flags, None) != 0:
            raise OSError(ctypes.get_errno(), "native private bind")
    os.environ["LIBCEPHFSD_SOCKET"] = str(root) + "/proxy-work/libcephfsd.sock"
    os.environ["SMBMETRICS_SMBSTATUS"] = os.environ["SNAP"] + "/bin/smbstatus"
    os.environ["SMBMETRICS_SMBCONF"] = str(root) + "/config/smb.conf"


def prepare_local_accounts(root):
    """Use Sambacc's SAM-only importer in the smbd app's private NSS context."""
    import json
    from sambacc.passdb_loader import PassDBLoader
    candidate = json.loads((root / "config/candidate.json").read_text())
    doc = combine_sources(candidate["config"], candidate.get("users") or [])
    instance = GlobalConfig(initial_data=doc).get(candidate["spec"]["cluster_id"])
    loader = PassDBLoader(str(root / "config/smb.conf"))
    # This dedicated user-mode SAM belongs to the resource identity set.
    # Built-in/service accounts are not ACB_NORMAL logins and are left intact.
    users = list(instance.users())
    wanted = {user.username for user in users}
    for entry in loader._pdb.search_users(0x10):
        if entry["account_name"] not in wanted:
            loader._pdb.delete_sam_account(loader._pdb.getsampwnam(entry["account_name"]))
    for user in users:
        loader.add_user(user)


def ready(root, role):
    """Check actual daemon/control endpoints; snapctl acceptance is not ready."""
    import json
    import os
    import socket
    import subprocess
    import urllib.request
    context = json.loads((root / "config/context.json").read_text())
    if role == "cephfs-proxy":
        with socket.socket(socket.AF_UNIX) as client:
            client.settimeout(2)
            client.connect(str(root / "proxy-work/libcephfsd.sock"))
    elif role == "smbd":
        pid = int((root / "samba/run/smbd.pid").read_text().strip())
        os.kill(pid, 0)
        subprocess.run([os.environ["SNAP"] + "/bin/smbcontrol", "--configfile", str(root / "config/smb.conf"), str(pid), "ping"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3)
    elif role == "smbmetrics":
        with urllib.request.urlopen("http://127.0.0.1:" + str(context["metrics_port"]) + "/metrics", timeout=3) as response:
            if response.status != 200 or not response.read():
                raise ValueError("metrics not ready")
    else:
        raise ValueError("native readiness role is not prepared")


def active_root(base):
    import re
    cluster_id = (base / "owner.json").read_text()
    if not re.fullmatch(r"[a-zA-Z0-9]([a-zA-Z0-9-]{0,16}[a-zA-Z0-9])?", cluster_id):
        raise ValueError("invalid active SMB owner")
    return base / cluster_id


def main():
    import json
    import os
    import sys
    from pathlib import Path
    command, root_arg = sys.argv[1:3]
    root = Path(root_arg)
    if command == "compile":
        compile_files(root)
    elif command == "publish":
        publish(root)
    elif command == "ready":
        ready(root, sys.argv[3])
    elif command == "launch":
        root = active_root(root)
        role = sys.argv[3]
        context = load_launch(root)
        argv = role_command(role, str(root), context, os.environ["SNAP"])
        enter_launch_context(role, root, context)
        if role == "smbd":
            prepare_local_accounts(root)
        if role == "cephfs-proxy":
            os.chdir(root / "proxy-work")
        os.execv(argv[0], argv)
    else:
        raise ValueError("unknown native helper operation")


if __name__ == "__main__":
    import sys
    try:
        main()
    except Exception:
        # Preserve useful details only in the root-private cluster diagnostics.
        # Library exceptions can contain resource credentials, never expose
        # them through service status or the shared snap app journal.
        import traceback
        from pathlib import Path
        try:
            root = Path(sys.argv[2])
            if sys.argv[1] == "launch":
                root = active_root(root)
            atomic_write(root / "diagnostics/native-error", traceback.format_exc())
        except Exception:
            pass
        print("Native SMB operation failed; local configuration is not confirmed applied", file=sys.stderr)
        sys.exit(1)

"""Native text-config adapter for the pinned Sambacc consumer.

This module does not generate shares, allocate identities or supervise apps.
"""
import contextlib
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


def project_ctdb(document, cluster_id, root, *, cluster_meta_uri,
                 recovery_lock_uri, member, peers, context=None, sources=()):
    """Return (files, details) for private CTDB configuration, without I/O.

    document/sources/context use render's pinned Sambacc input shapes. root is
    a normalized absolute writable cluster root whose last component is
    cluster_id. Sources are canonical rados://POOL/CLUSTER_ID/OBJECT URIs in
    the same pool, with distinct objects (simple nonempty ._- name components,
    never leading '-'). Only the pinned writer's logging/legacy options and
    the explicitly overlaid CTDB keys are supported; other keys fail closed.
    No RADOS access is performed; the caller supplies the authoritative view.

    member and each peer are Sambacc metadata records: {identity: str, pnn:
    int, node: IP literal, state: new|ready|replaced|changed|gone}. peers excludes
    member. Assigned PNNs are retained, not allocated; 0..65535 is this
    projection's resource bound. Missing/changed/gone slots become '#'. The
    local member must be active. Identities and all addresses must be unique.

    files maps root-relative paths to text: config/smb.conf, config/ctdb.json,
    config/ctdb-{rank,identity,address}, ctdb/{ctdb.conf,nodes}. details contains
    the unchanged member, cluster_meta_uri, recovery_lock_argv and absolute
    directories for later preparation. Paths are checked lexically only:
    wiring must ensure the root and its descendants are not escaping symlinks.

    The recovery command uses a stable current-snap helper path and the shared
    client.smb.cluster.CLUSTER_ID identity, never a host, rank or snap revision.
    This does NOT prepare credentials, event scripts, sockets, namespaces or
    services, nor admit launch. The helper's package presence and its Ceph
    config/keyring view must be established by the subsequent wiring slice.
    """
    import ipaddress
    import json
    import re
    import shlex

    from sambacc import ctdb
    from sambacc.ceph.rados import parse_rados_uri

    if not isinstance(cluster_id, str) or not re.fullmatch(r"[a-zA-Z0-9]([a-zA-Z0-9-]{0,16}[a-zA-Z0-9])?", cluster_id):
        raise ValueError("invalid native cluster namespace")
    if (not isinstance(root, str) or not re.fullmatch(r"(/[a-zA-Z0-9_.-]+)+", root)
            or root.split("/")[-1] != cluster_id
            or any(part in (".", "..") for part in root.split("/"))):
        raise ValueError("invalid native cluster root")
    parsed = []
    for uri in (cluster_meta_uri, recovery_lock_uri):
        if not isinstance(uri, str) or not re.fullmatch(r"rados://[a-zA-Z0-9_.][a-zA-Z0-9_.-]*/[a-zA-Z0-9-]+/[a-zA-Z0-9_.][a-zA-Z0-9_.-]*", uri):
            raise ValueError("invalid CTDB RADOS object URI")
        info = parse_rados_uri(uri)
        if info["ns"] != cluster_id or info["pool"] in (".", "..") or info["key"] in (".", ".."):
            raise ValueError("CTDB source outside cluster namespace")
        parsed.append(info)
    meta, lock = parsed
    if meta["pool"] != lock["pool"] or meta["key"] == lock["key"]:
        raise ValueError("CTDB metadata and lock require distinct objects in one pool")

    entries, identities, addresses = {}, set(), set()
    for entry in [member, *peers]:
        rank = entry.get("pnn")
        identity = entry.get("identity")
        address = entry.get("node")
        if type(rank) is not int or not 0 <= rank <= 65535 or rank in entries:
            raise ValueError("invalid or duplicate CTDB PNN")
        if not isinstance(identity, str) or not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]*", identity) or identity in identities:
            raise ValueError("invalid or duplicate CTDB identity")
        if not isinstance(address, str) or "%" in address:
            raise ValueError("invalid CTDB node address")
        ip = ipaddress.ip_address(address)
        if ip in addresses or ip.is_unspecified or ip.is_multicast:
            raise ValueError("invalid or duplicate CTDB node address")
        ctdb.NodeState(entry.get("state"))
        entries[rank] = entry
        identities.add(identity)
        addresses.add(ip)
    if member["state"] in ("changed", "gone"):
        raise ValueError("local CTDB member is not active")

    reject_public_addresses(document)
    doc = combine_sources(document, sources)
    instance = GlobalConfig(initial_data=doc).get(cluster_id)
    if not instance.with_ctdb or instance.with_addc:
        raise ValueError("private CTDB projection requires the CTDB-only mode")
    required = {key: value for key, value in instance.ctdb_smb_config().global_options() if key != "include"}
    required.update({
        "config backend": "file", "registry shares": "no",
        "ctdbd socket": root + "/ctdb/run/ctdbd.socket",
        "bind interfaces only": "yes", "interfaces": member["node"],
        "winbindd socket directory": root + "/samba/winbindd",
    })
    for name in doc["configs"][cluster_id].get("globals", []):
        options = doc["globals"][name].setdefault("options", {})
        for key, value in options.items():
            normalized = " ".join(key.lower().split())
            if normalized == "include" or (normalized == "security" and str(value).strip().lower() in ("ads", "domain")):
                raise ValueError("native option conflict: " + normalized)
        _overlay(options, required)
    doc["globals"]["_native_ctdb"] = {"options": required}
    doc["configs"][cluster_id].setdefault("globals", []).append("_native_ctdb")

    # Same positional arguments as Sambacc's ctdb-rados-mutex command; -R
    # disables helper service registration (there is no external supervisor).
    recovery_argv = ["/snap/microceph/current/libexec/ctdb/ctdb_mutex_ceph_rados_helper",
                     "ceph", "client.smb.cluster." + cluster_id,
                     lock["pool"], lock["key"], "-n", lock["ns"], "-R"]
    # Reject legacy aliases such as nodes_json: the pinned node consumer gives
    # those precedence over cluster_meta_uri, escaping the authoritative source.
    supported = {"cluster_meta_uri", "nodes_path", "recovery_lock", "nodes_cmd",
                 "conf_file_includes", "ctdb_port", "public_addresses",
                 "log_level", "script_log_level", "realtime_scheduling"}
    if set(doc.get("ctdb", {})) - supported:
        raise ValueError("unsupported native CTDB option")
    # Check explicit overrides before applying upstream's container defaults.
    _overlay(doc.setdefault("ctdb", {}), {
        "cluster_meta_uri": cluster_meta_uri, "nodes_path": root + "/ctdb/nodes",
        "recovery_lock": "!" + shlex.join(recovery_argv), "nodes_cmd": "",
        "conf_file_includes": [], "ctdb_port": 0,
    })
    params = instance.ctdb_config()
    for key in ("log_level", "script_log_level", "realtime_scheduling"):
        if not isinstance(params[key], str) or not re.fullmatch(r"[a-zA-Z0-9]+", params[key]):
            raise ValueError("invalid CTDB option: " + key)
    conf = io.StringIO()
    ctdb.write_ctdb_conf(conf, params)
    # The pinned writer covers shared lock/logging/legacy settings, but not
    # native paths. Add those parameters to its existing sections.
    text = conf.getvalue().replace("[logging]\n", "[logging]\nlocation = file:" + root + "/diagnostics/ctdb.log\n")
    text = text.replace("[cluster]\n", "[cluster]\nnode address = " + member["node"] + "\nnodes list = " + params["nodes_path"] + "\n")
    text += "[database]\n"
    for kind in ("volatile", "persistent", "state"):
        text += kind + " database directory = " + root + "/ctdb/" + kind + "\n"
    text += "\n[event]\nscript directory = " + root + "/ctdb/events\n"
    slots = ["#"] * (max(entries) + 1)
    for rank, entry in entries.items():
        if entry["state"] not in ("changed", "gone"):
            slots[rank] = entry["node"]
    nodes = io.StringIO()
    ctdb.write_nodes_file(nodes, slots)
    files = {
        "config/smb.conf": render(doc, cluster_id, root, context or {}, []),
        "config/ctdb.json": json.dumps({"samba-container-config": "v0", "ctdb": params}, sort_keys=True) + "\n",
        "config/ctdb-rank": str(member["pnn"]) + "\n",
        "config/ctdb-identity": member["identity"] + "\n",
        "config/ctdb-address": member["node"] + "\n",
        "ctdb/ctdb.conf": text, "ctdb/nodes": nodes.getvalue(),
    }
    directories = [root + "/" + name for name in (
        "config", "diagnostics", "ctdb/run", "ctdb/events/legacy",
        "ctdb/volatile", "ctdb/persistent", "ctdb/state",
        "samba/private", "samba/state", "samba/cache", "samba/lock",
        "samba/run", "samba/ncalrpc", "samba/winbindd",
    )]
    return files, {"member": copy.deepcopy(member), "cluster_meta_uri": cluster_meta_uri,
                   "recovery_lock_argv": recovery_argv, "directories": directories}


def compile_core(candidate, root):
    """Construct a complete private launch projection before any live writes."""
    spec = candidate["spec"]
    reject_public_addresses(candidate)
    cluster_id = spec["cluster_id"]
    document = combine_sources(candidate["config"], candidate.get("users") or [])
    instance = GlobalConfig(initial_data=document).get(cluster_id)
    globals_effective = {" ".join(k.lower().split()): str(v).lower() for k, v in instance.global_options()}
    if instance.with_addc or globals_effective.get("security", "").strip() in ("ads", "domain"):
        raise ValueError("effective mode requires native cluster/domain preparation from the next feature stage")
    clustered = "clustered" in (spec.get("features") or [])
    if instance.with_ctdb != clustered or (clustered and not candidate.get("ctdb")):
        raise ValueError("effective CTDB mode requires the authoritative native reservation view")
    if not clustered and candidate.get("ctdb"):
        raise ValueError("CTDB view requires clustered mode")
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
    context["clustered"] = clustered
    if clustered:
        view = candidate["ctdb"]
        files, details = project_ctdb(document, cluster_id, root,
                                     cluster_meta_uri=spec["cluster_meta_uri"],
                                     recovery_lock_uri=spec["cluster_lock_uri"],
                                     member=view["member"], peers=view["peers"], context=context)
        # Ranks of retired/deleted owners need no invented IP/identity. Retain
        # trailing slots as well as internal holes using SQL's monotonic bound.
        nodes = files["ctdb/nodes"].splitlines()
        slots = view.get("slot_count", len(nodes))
        if type(slots) is not int or not len(nodes) <= slots <= 65536:
            raise ValueError("invalid authoritative CTDB slot count")
        files["ctdb/nodes"] += "#\n" * (slots - len(nodes))
        context["ctdb_pnn"] = details["member"]["pnn"]
    else:
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
    if clustered:
        files = {name if "/" in name else "config/" + name: data for name, data in files.items()}
    return files, context


def role_command(role, root, context, snap):
    """Select a fixed foreground binary, never an executable from input data."""
    conf = root + "/config/smb.conf"
    if role in ("ctdbd", "ctdb-nodes"):
        if not context.get("clustered"):
            raise ValueError("CTDB role requires prepared clustered context")
        if role == "ctdbd":
            # The pinned binary has no config/socket CLI options. Private binds
            # supply its supported production paths (not test-mode overrides).
            return [snap + "/bin/ctdbd", "--interactive"]
        return [snap + "/bin/python3", "-B", snap + "/smb/native.py", "nodes", root]
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
    if role in ("ctdbd", "ctdb-nodes", "ctdb-control") or (role == "smbd" and context.get("clustered")):
        if not context.get("clustered"):
            raise ValueError("CTDB mounts require prepared clustered context")
        for source, target in (("ctdb", "/etc/ctdb"), ("ctdb/run", "/run/ctdb"), ("ctdb", "/var/lib/ctdb")):
            operations.append((root + "/" + source, target, 4096))
        if role == "ctdbd":
            operations.append((root + "/config/ctdb-recovery.conf", "/etc/ceph/ceph.conf", 4096))
    if context.get("custom_dns"):
        operations.append((root + "/config/resolv.conf", "/etc/resolv.conf", 4096))
    return operations


def data_config(text, cluster_id, root, *, recovery=False):
    """Adapt monitor/config context without exposing the admin keyring to VFS."""
    import configparser
    parser = configparser.ConfigParser(interpolation=None, strict=False)
    parser.read_string(text)
    for section in list(parser.sections()):
        if section != "global":
            parser.remove_section(section)
    if "global" not in parser:
        parser["global"] = {}
    keyring = root + "/identity/" + ("recovery" if recovery else "data") + ".keyring"
    identity = "client.smb." + ("cluster." if recovery else "fs.cluster.") + cluster_id
    parser["global"]["keyring"] = keyring
    parser[identity] = {"keyring": keyring}
    output = io.StringIO()
    parser.write(output)
    return output.getvalue()


def atomic_write(path, data):
    import os
    from pathlib import Path
    path = Path(path)
    private_directory(path.parent)
    tmp = path.with_name(path.name + ".tmp")
    if path.is_symlink() or tmp.is_symlink():
        raise ValueError("native file is an escaping symlink")
    try:
        with tmp.open("wb") as out:
            os.chmod(tmp, 0o600)
            out.write(data.encode() if isinstance(data, str) else data)
        os.replace(tmp, path)
    finally:
        tmp.unlink(missing_ok=True)


def private_directory(path):
    """Create real private directories, refusing traversal through symlinks."""
    from pathlib import Path
    path = Path(path).absolute()
    if path.parent != path:
        private_directory(path.parent)
    if path.is_symlink():
        raise ValueError("native directory is an escaping symlink")
    path.mkdir(mode=0o700, exist_ok=True)


def publish(root, *, config_only=False):
    """Publish prepared files; config-only updates never write CTDB runtime files."""
    from pathlib import Path
    root = Path(root)
    if config_only and (root / "stop-requested").exists():
        raise ValueError("SMB stop requested")
    private_directory(root)
    next_dir = root / "config/next"
    private_directory(next_dir)
    publication = []
    # Validate the whole prepared set before any live write. Initial placement
    # still publishes CTDB startup files while all consumers are stopped.
    for file in sorted(next_dir.rglob("*")):
        if file.is_symlink():
            raise ValueError("native candidate is an escaping symlink")
        if not file.is_file():
            continue
        relative = file.relative_to(next_dir)
        if len(relative.parts) == 1:
            relative = Path("config") / relative
        if config_only:
            protected = (relative == Path("ctdb/ctdb.conf") or
                         (relative.parts[0] == "config" and relative.name.startswith("ctdb")))
            if protected:
                target = root / relative
                if target.is_symlink() or target.read_bytes() != file.read_bytes():
                    raise ValueError("CTDB configuration changed; explicit maintenance required")
                continue
            if relative.parts[0] == "ctdb":
                # The node monitor publishes/reloads nodes itself after seeing
                # candidate.json. Pre-writing nodes here would mask its reload.
                continue
            if relative.parts[0] != "config":
                raise ValueError("configuration refresh cannot publish runtime state")
        publication.append((root / relative, file.read_bytes()))
    for target, data in publication:
        if config_only and (root / "stop-requested").exists():
            raise ValueError("SMB stop requested")
        atomic_write(target, data)


def publish_config(root):
    """Apply only prepared Samba configuration; retain CTDB-owned state."""
    publish(root, config_only=True)


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
    files = {name if "/" in name else "config/" + name: data for name, data in files.items()}
    files["config/ceph-direct.conf"] = files["config/ceph-proxy.conf"] = ceph_conf
    if context["clustered"]:
        files["config/ctdb-recovery.conf"] = data_config(ceph_text, candidate["spec"]["cluster_id"], str(root), recovery=True)
        for directory in ("ctdb/run", "ctdb/events/legacy", "ctdb/volatile", "ctdb/persistent", "ctdb/state", "ctdb/scripts", "samba/winbindd"):
            private_directory(root / directory)
    files["config/context.json"] = json.dumps(context, sort_keys=True)
    files["config/candidate.json"] = json.dumps(candidate, sort_keys=True)
    next_dir = root / "config/next"
    private_directory(next_dir)
    # Remove obsolete candidate-only files, never live state or shared data.
    for file in sorted(next_dir.rglob("*"), reverse=True):
        if file.is_symlink():
            raise ValueError("native candidate is an escaping symlink")
        if file.is_file() and str(file.relative_to(next_dir)) not in files:
            file.unlink()
        elif file.is_dir() and not any(file.iterdir()):
            file.rmdir()
    for name, data in files.items():
        atomic_write(next_dir / name, data)
    subprocess.run([os.environ["SNAP"] + "/bin/testparm", "--suppress-prompt", str(next_dir / "config/smb.conf")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if context["data_checks"]:
        import cephfs
        for check in context["data_checks"]:
            fs = cephfs.LibCephFS(conffile=str(next_dir / "config/ceph-direct.conf"), auth_id=check["user"])
            try:
                fs.conf_set("client_fs", check["filesystem"])
                fs.mount()
                fs.stat(check["path"])
            finally:
                fs.shutdown()


def _prepare_ctdb_mountpoint(path):
    """Create only an empty runtime bind target; never alter host CTDB state."""
    import os
    import stat
    from pathlib import Path
    path = Path(path)
    try:
        path.mkdir(mode=0o755)
    except FileExistsError:
        pass
    info = path.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or
            info.st_mode & 0o022):
        raise ValueError("unsafe CTDB runtime mountpoint")
    if any(path.iterdir()):
        raise ValueError("CTDB runtime mountpoint contains unrelated state")


def enter_launch_context(role, root, context):
    """Enter the strictly confined app's own private mount context, then exec."""
    import ctypes
    import os
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.unshare(0x00020000) != 0:  # CLONE_NEWNS
        raise OSError(ctypes.get_errno(), "native mount namespace")
    libc.mount.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_ulong, ctypes.c_char_p]
    for source, target, flags in launch_mounts(role, str(root), context):
        if target == "/run/ctdb":
            # The system-files plug permits this target, not a /run layout.
            # MS_SLAVE above keeps the subsequent private bind off the host.
            _prepare_ctdb_mountpoint(target)
        if libc.mount(source.encode() if source else None, target.encode(), None, flags, None) != 0:
            raise OSError(ctypes.get_errno(), "native private bind")
    if context.get("clustered"):
        os.environ["CTDB_BASE"] = str(root / "ctdb")
        os.environ["CTDB"] = os.environ["SNAP"] + "/bin/ctdb"
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


def prepare_ctdb(root, snap):
    """Use the pinned minimal event-resource setup, never stock service scripts."""
    import os
    from pathlib import Path
    from sambacc import ctdb
    root = Path(root)
    for helper in ("ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper", "ctdb_takeover_helper", "ctdb_lock_helper"):
        if not os.access(Path(snap) / "libexec/ctdb" / helper, os.X_OK):
            raise ValueError("required CTDB helper is not executable")
    for name in ("functions", "notify.sh", "events/legacy/00.ctdb.script"):
        if not (Path(snap) / "share/ctdb" / name).is_file():
            raise ValueError("required CTDB event resource is absent")
    for name in ("ctdb", "ctdb/run", "ctdb/events/legacy", "ctdb/scripts"):
        private_directory(root / name)
    # An unexpected enabled script could invoke a service manager or public-IP
    # operations. Do not silently delete it or claim the cluster is prepared.
    if any(file.name != "00.ctdb.script" for file in (root / "ctdb/events/legacy").iterdir()):
        raise ValueError("unexpected enabled CTDB event script")
    (root / "ctdb/run/ctdb-nodes.pid").unlink(missing_ok=True)
    ctdb.ensure_ctdbd_etc_files(etc_path=str(root / "ctdb"), src_path=snap + "/share/ctdb")


def ctdb_control(root, operation, *args):
    """Fixed diagnostic primitives, run only in the CTDB app's private context."""
    import os
    import subprocess
    if operation not in ("pnn", "nodestatus", "status", "reloadnodes"):
        raise ValueError("unsupported native CTDB control command")
    if args and (operation != "nodestatus" or len(args) != 1 or not str(args[0]).isdigit()):
        raise ValueError("invalid native CTDB control argument")
    return subprocess.check_output([os.environ["SNAP"] + "/bin/ctdb", operation, *args], stderr=subprocess.DEVNULL, timeout=5)


def ctdb_membership(status):
    """Parse pinned text status, including the total bound of deleted PNN slots."""
    import ipaddress
    import re
    lines = status.decode().splitlines()
    counts = [re.fullmatch(r"Number of nodes:(\d+)(?: \(including \d+ deleted nodes\))?", line)
              for line in lines if line.startswith("Number of nodes:")]
    if len(counts) != 1 or counts[0] is None:
        raise ValueError("invalid CTDB membership status")
    slots = int(counts[0][1])
    if not 0 < slots <= 65536:
        raise ValueError("invalid CTDB membership slot count")
    nodes = {}
    for line in lines:
        if not line.startswith("pnn:"):
            continue
        entry = re.fullmatch(r"pnn:(\d+)\s+(\S+)\s+(.+)", line)
        if entry is None:
            raise ValueError("invalid CTDB membership node")
        pnn = int(entry[1])
        if pnn >= slots or pnn in nodes:
            raise ValueError("invalid CTDB membership rank")
        nodes[pnn] = (str(ipaddress.ip_address(entry[2])), entry[3].removesuffix(" (THIS NODE)"))
    if "Recovery mode:NORMAL (0)" not in lines:
        raise ValueError("CTDB recovery is not normal")
    return slots, nodes


def prepared_ctdb_nodes(root):
    """Consume the published SQL view through the real pinned projection."""
    import json
    candidate = json.loads((root / "config/candidate.json").read_text())
    files, context = compile_core(candidate, str(root))
    if not context["clustered"]:
        raise ValueError("node monitor requires CTDB preparation")
    expected = files["ctdb/nodes"]
    desired = {pnn: line for pnn, line in enumerate(expected.splitlines()) if not line.startswith("#")}
    return expected, desired


def observe_ctdb_nodes(root, expected, desired):
    slots, nodes = ctdb_membership(ctdb_control(root, "status"))
    matches = slots == len(expected.splitlines()) and {pnn: node[0] for pnn, node in nodes.items()} == desired
    return matches, nodes


def update_ctdb_nodes(root):
    """Publish all peer files before reload; never allocate or renumber peers."""
    if (root / "stop-requested").exists():
        raise ValueError("SMB stop requested")
    expected, desired = prepared_ctdb_nodes(root)
    path = root / "ctdb/nodes"
    if path.read_text() != expected:
        atomic_write(path, expected)
    matches, nodes = observe_ctdb_nodes(root, expected, desired)
    if matches:
        return
    for pnn, (address, flags) in nodes.items():
        if pnn in desired:
            if address != desired[pnn] or flags != "OK":
                raise ValueError("CTDB surviving membership is unhealthy or changed address")
        elif "DISCONNECTED" not in flags.split("|"):
            raise ValueError("CTDB retired membership is still connected")
    # Pinned reloadnodes compares nodes files on ALL connected hosts before
    # applying them. Keep the target on disk even on failure, so asynchronous
    # monitors can converge on identical files. Exit zero can mask partial
    # daemon reload failures: always observe the map, also after a restart.
    ctdb_control(root, "reloadnodes")
    matches, _ = observe_ctdb_nodes(root, expected, desired)
    if not matches:
        raise ValueError("CTDB daemon membership does not match published slots")


def check_ctdb_ready_owner(root):
    """Readiness is only meaningful for the exact active, unstopped owner."""
    if active_root(root.parent) != root:
        raise ValueError("CTDB readiness owner mismatch")
    if not load_launch(root).get("clustered"):
        raise ValueError("CTDB readiness requires prepared clustered context")


def probe_ctdb_ready(root):
    """Ask the confined monitor for fresh health, without re-entering snap run.

    The daemon cannot mount the CTDB production paths or select another app's
    profile. This endpoint accepts no commands/data; a connection triggers the
    monitor's existing PNN, nodestatus, recovery, membership and PID checks.
    Neither a socket path nor a previous successful check is readiness.
    """
    import socket
    check_ctdb_ready_owner(root)
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(25)
        client.connect(str(root / "ctdb/run/native-ready.socket"))
        if client.recv(1) != b"1":
            raise ValueError("CTDB monitor did not confirm readiness")
    check_ctdb_ready_owner(root)


@contextlib.contextmanager
def ctdb_readiness_listener(root):
    """One fixed monitor endpoint; a crash may leave a socket, never health."""
    import fcntl
    import os
    import socket
    import stat
    path = root / "ctdb/run/native-ready.socket"
    lock = root / "ctdb/run/native-ready.lock"
    # Keep a duplicate monitor from unlinking a live endpoint. O_NOFOLLOW and
    # the type check also refuse unexpected objects in retained runtime state.
    with os.fdopen(os.open(lock, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600), "r+") as guard:
        info = os.fstat(guard.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
            raise ValueError("unsafe CTDB readiness lock")
        fcntl.flock(guard, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            info = path.lstat()
        except FileNotFoundError:
            pass
        else:
            if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.geteuid():
                raise ValueError("unsafe CTDB readiness socket")
            path.unlink()
        with socket.socket(socket.AF_UNIX) as listener:
            listener.bind(str(path))
            os.chmod(path, 0o600)
            listener.listen(4)
            listener.settimeout(2)
            try:
                yield listener
            finally:
                path.unlink(missing_ok=True)


def monitor_ctdb_nodes(root):
    """Retry membership updates and answer only fresh, fixed readiness checks."""
    import os
    import socket
    pidfile = root / "ctdb/run/ctdb-nodes.pid"
    diagnostic = root / "diagnostics/nodes-error"
    with ctdb_readiness_listener(root) as listener:
        try:
            while True:
                try:
                    update_ctdb_nodes(root)
                    ready(root, "ctdbd")
                    atomic_write(pidfile, str(os.getpid()) + "\n")
                    diagnostic.unlink(missing_ok=True)
                except Exception:
                    pidfile.unlink(missing_ok=True)
                    # Never expose candidate secrets or claim reload completion.
                    try:
                        atomic_write(diagnostic, "CTDB membership update is not confirmed.\n")
                    except OSError:
                        pass
                try:
                    client, _ = listener.accept()
                except socket.timeout:
                    continue
                with client:
                    try:
                        client.settimeout(1)
                        check_ctdb_ready_owner(root)
                        ready(root, "ctdb-nodes")
                        check_ctdb_ready_owner(root)
                        client.sendall(b"1")
                    except Exception:
                        # EOF is failure. No cached success, commands, error
                        # details or CTDB protocol pass through this endpoint.
                        pass
        finally:
            pidfile.unlink(missing_ok=True)


def ready(root, role):
    """Check actual daemon/control endpoints; snapctl acceptance is not ready."""
    import json
    import os
    import socket
    import subprocess
    import urllib.request
    context = json.loads((root / "config/context.json").read_text())
    if role in ("ctdbd", "ctdb-nodes"):
        if not context.get("clustered"):
            raise ValueError("CTDB readiness requires prepared clustered context")
        assigned = context["ctdb_pnn"]
        if int(ctdb_control(root, "pnn").strip()) != assigned:
            raise ValueError("CTDB assigned PNN mismatch")
        ctdb_control(root, "nodestatus", str(assigned))
        status = ctdb_control(root, "status").decode()
        if "Recovery mode:NORMAL (0)" not in status.splitlines():
            raise ValueError("CTDB recovery is not normal")
        if role == "ctdb-nodes":
            expected, desired = prepared_ctdb_nodes(root)
            matches, _ = observe_ctdb_nodes(root, expected, desired)
            if (root / "ctdb/nodes").read_text() != expected or not matches:
                raise ValueError("CTDB daemon membership is not confirmed")
            pid = int((root / "ctdb/run/ctdb-nodes.pid").read_text().strip())
            os.kill(pid, 0)
    elif role == "cephfs-proxy":
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
    elif command == "publish-config":
        publish_config(root)
    elif command == "ready":
        if sys.argv[3] in ("ctdbd", "ctdb-nodes"):
            probe_ctdb_ready(root)
        else:
            ready(root, sys.argv[3])
    elif command == "prepare-ctdb":
        prepare_ctdb(root, os.environ["SNAP"])
    elif command == "nodes":
        monitor_ctdb_nodes(root)
    elif command == "control":
        root = active_root(root)
        context = load_launch(root)
        enter_launch_context("ctdb-control", root, context)
        if sys.argv[3] == "ready" and len(sys.argv) == 5 and sys.argv[4] in ("ctdbd", "ctdb-nodes"):
            ready(root, sys.argv[4])
        else:
            sys.stdout.buffer.write(ctdb_control(root, sys.argv[3], *sys.argv[4:]))
    elif command == "launch":
        root = active_root(root)
        role = sys.argv[3]
        context = load_launch(root)
        argv = role_command(role, str(root), context, os.environ["SNAP"])
        enter_launch_context(role, root, context)
        if role == "smbd":
            if context.get("clustered"):
                # Also gate snapd boot/restart launches, not just Go's initial
                # service ordering: clustered SAM access requires CTDB first.
                ready(root, "ctdbd")
            prepare_local_accounts(root)
        if role == "ctdbd":
            # --interactive deliberately forces stderr logging in the pinned
            # binary. Keep CTDB diagnostics in the root-private directory.
            fd = os.open(root / "diagnostics/ctdbd.log", os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
            os.dup2(fd, 1)
            os.dup2(fd, 2)
            os.close(fd)
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
            if sys.argv[1] in ("launch", "control"):
                root = active_root(root)
            atomic_write(root / "diagnostics/native-error", traceback.format_exc())
        except Exception:
            pass
        print("Native SMB operation failed; local configuration is not confirmed applied", file=sys.stderr)
        sys.exit(1)

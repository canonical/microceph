#!/usr/bin/env python3
"""Disposable external-directory/client fixture, never a native runtime helper."""
import hashlib
import json
import logging
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys

from smb_guest import BASE, PAYLOAD, safe_apply_result

REALM = "ROBOT.TEST"
DOMAIN = "ROBOT"
SERVICE = "robot-ad.robot.test"
DC_ROOT = Path("/root/robot-ad-dc")


def resource_documents(addresses, dc, password):
    """Use the real mgr resource shape and an explicit positive group allowlist."""
    def document(cluster, credential, secret, path):
        resources = [
            {"resource_type": "ceph.smb.join.auth", "auth_id": credential,
             "auth": {"username": "Administrator", "password": secret}},
            {"resource_type": "ceph.smb.cluster", "cluster_id": cluster,
             "auth_mode": "active-directory", "clustering": "always",
             "domain_settings": {"realm": REALM, "join_sources": [{"source_type": "resource", "ref": credential}]},
             "bind_addrs": [{"address": address} for address in addresses], "custom_dns": [dc],
             "custom_ports": {"smb": 1445, "smbmetrics": 19922}}]
        for share, group, readonly in (("data", "writers", False), ("read", "writers", True), ("auth", "readers", True)):
            resources.append({"resource_type": "ceph.smb.share", "cluster_id": cluster, "share_id": share,
                              "cephfs": {"volume": "robotsmb", "path": path, "provider": "samba-vfs/new"},
                              "readonly": readonly, "restrict_access": True,
                              "login_control": [{"name": DOMAIN + "\\" + group, "category": "group", "access": "read" if readonly else "read-write"}]})
        return {"resources": resources}
    # Placement retirement retains mgr resources and their path earmarks.
    # Keep the failed-join cluster isolated without deleting its retained state.
    return {"ad": document("robot-ad", "robot-join", password, "/ad"),
            "ad-wrong": document("robot-bad", "robot-bad-join", password + "-incorrect", "/ad-wrong"),
            "ad-refresh": {"resource_type": "ceph.smb.join.auth", "auth_id": "robot-join",
                           "auth": {"username": "bob", "password": "deliberately-not-a-join-credential"}}}


def client_argv(address, share, user, protocol, command):
    """Force each mechanism; a Kerberos failure must never fall back to NTLM."""
    if share not in ("data", "read", "auth") or user not in ("alice", "bob", "wrong", "unknown"):
        raise ValueError("unknown AD fixture share/user")
    argv = ["smbclient", "//" + SERVICE + "/" + share, "-I", address, "-p", "1445", "-m", "SMB3"]
    if protocol == "kerberos":
        # -N invalidates the explicit cache during Samba's option finalization.
        # A valid named cache already suppresses the password callback.
        argv += ["--use-kerberos=required", "--use-krb5-ccache=FILE:" + str(BASE / "client.ccache")]
    elif protocol == "ntlm":
        argv += ["--use-kerberos=off", "--option=client ntlmv2 auth=yes", "-A", str(BASE / (user + ".ad-auth"))]
    else:
        raise ValueError("protocol must be kerberos or ntlm, never desired")
    return argv + ["-c", command]


def parse_mappings(raw):
    """Parse actual wbinfo/getent values; never invent a missing identity."""
    result = {}
    for name in ("alice", "bob", "writers"):
        sid, entry = raw[name]
        if not re.fullmatch(r"S-1-5-21-(?:[0-9]+-){3}[0-9]+ " + (r"SID_DOM_GROUP \(2\)" if name == "writers" else r"SID_USER \(1\)") + r"\s*", sid):
            raise ValueError("missing domain SID")
        fields = entry.strip().split(":")
        if len(fields) != (4 if name == "writers" else 7):
            raise ValueError("missing NSS identity")
        result[name] = {"sid": sid.split()[0], "gid": int(fields[2 if name == 'writers' else 3])}
        if name != "writers":
            result[name]["uid"] = int(fields[2])
    return result


def run(argv, **kwargs):
    return subprocess.run(argv, capture_output=True, text=True, timeout=65, **kwargs)


def provision_dc(address, gateway):
    # Only called inside a newly owned disposable DC VM. No passwords in argv.
    from samba.auth import system_session
    from samba.provision import provision
    from samba.provision.sambadns import add_host_record
    if DC_ROOT.exists():
        raise ValueError("refusing to reprovision existing DC state")
    credentials = {name: "Aa1!" + secrets.token_urlsafe(24) for name in ("Administrator", "alice", "bob")}
    BASE.mkdir(mode=0o700, parents=True, exist_ok=True)
    (BASE / "ad-secrets.json").write_text(json.dumps(credentials))
    result = provision(logging.getLogger("robot-ad"), system_session(), targetdir=str(DC_ROOT),
                       realm=REALM, domain=DOMAIN, hostname="robot-dc", hostip=address,
                       adminpass=credentials["Administrator"], serverrole="active directory domain controller",
                       dns_backend="SAMBA_INTERNAL", use_rfc2307=True)
    for user in ("alice", "bob"):
        result.samdb.newuser(user, credentials[user])
    for group, members in (("writers", ["alice"]), ("readers", ["alice", "bob"])):
        result.samdb.newgroup(group)
        result.samdb.add_remove_group_members(group, members)
    zone = "DC=robot.test,CN=MicrosoftDNS,DC=DomainDnsZones," + str(result.samdb.domain_dn())
    for name in ("robot-ad", "robot-bad"):
        add_host_record(result.samdb, zone, "DC=" + name, gateway, None)
    unit = ("[Unit]\nAfter=network-online.target\n[Service]\n"
            "ExecStart=/usr/sbin/samba --foreground --no-process-group --configfile=" + str(DC_ROOT / "etc/smb.conf") +
            "\n[Install]\nWantedBy=multi-user.target\n")
    Path("/etc/systemd/system/robot-ad-dc.service").write_text(unit)


def initialize(addresses, dc):
    credentials = json.loads((BASE / "ad-secrets.json").read_text())
    for kind, document in resource_documents(addresses, dc, credentials["Administrator"]).items():
        (BASE / (kind + ".json")).write_text(json.dumps(document))
    for selector, user, password in (("alice", "alice", credentials["alice"]), ("bob", "bob", credentials["bob"]),
                                     ("wrong", "alice", credentials["bob"]), ("unknown", "no-such-robot-user", credentials["bob"])):
        (BASE / (selector + ".ad-auth")).write_text(f"username = {user}\npassword = {password}\ndomain = {DOMAIN}\n")
    (BASE / "krb5.conf").write_text(
        f"[libdefaults]\n default_realm = {REALM}\n dns_lookup_kdc = false\n rdns = false\n"
        f"[realms]\n {REALM} = {{\n  kdc = {dc}\n }}\n[domain_realm]\n .robot.test = {REALM}\n robot.test = {REALM}\n")


def probe_directory():
    credentials = json.loads((BASE / "ad-secrets.json").read_text())
    env = dict(os.environ, KRB5_CONFIG=str(BASE / "krb5.conf"), KRB5CCNAME="FILE:" + str(BASE / "probe.ccache"))
    result = run(["kinit", "alice@" + REALM], input=credentials["alice"] + "\n", env=env)
    return result.returncode


def apply(kind):
    if kind not in ("ad", "ad-wrong", "ad-refresh"):
        raise ValueError("unknown AD resource")
    result = run(["/snap/bin/microceph.ceph", "smb", "apply", "-i", str(BASE / (kind + ".json")), "-f", "json"])
    (BASE / (kind + ".result")).write_text(result.stdout + result.stderr)
    credentials = json.loads((BASE / "ad-secrets.json").read_text())
    safe = safe_apply_result(result.returncode, result.stdout, result.stderr,
                             list(credentials.values()) + [credentials["Administrator"] + "-incorrect"])
    print(json.dumps(safe))
    return 0 if safe["success"] else 1


def client(address, share, user, protocol, action, filename):
    if action not in ("put", "get") or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", filename):
        raise ValueError("invalid AD fixture I/O")
    cache = BASE / "client.ccache"
    cache.unlink(missing_ok=True)
    env = dict(os.environ, KRB5_CONFIG=str(BASE / "krb5.conf"), KRB5CCNAME="FILE:" + str(cache))
    credentials = json.loads((BASE / "ad-secrets.json").read_text())
    if protocol == "kerberos":
        if user not in ("alice", "bob"):
            raise ValueError("negative password probes use explicit domain NTLMv2")
        ticket = run(["kinit", user + "@" + REALM], input=credentials[user] + "\n", env=env)
        if ticket.returncode:
            print(json.dumps({"rc": ticket.returncode, "message": "fresh kinit failed", "ticket": ""}))
            return
    readback = BASE / "ad-readback.bin"
    readback.unlink(missing_ok=True)
    command = f"put {BASE}/payload.bin {filename}" if action == "put" else f"get {filename} {readback}"
    try:
        result = run(client_argv(address, share, user, protocol, command), env=env)
    except subprocess.TimeoutExpired:
        print(json.dumps({"rc": 124, "message": "SMB request reached the 65-second deadline", "ticket": "", "digest": ""}))
        return
    ticket = run(["klist", "-c", str(cache)], env=env).stdout if protocol == "kerberos" else ""
    message = result.stdout + result.stderr
    for secret in credentials.values():
        message = message.replace(secret, "<redacted>")
    print(json.dumps({"rc": result.returncode, "message": message, "ticket": ticket,
                      "digest": hashlib.sha256(readback.read_bytes()).hexdigest() if readback.exists() else ""}))


def account_values(row):
    """Keep only non-secret identity/change evidence; refuse absent computer data."""
    keys = ("objectSid", "pwdLastSet", "msDS-KeyVersionNumber")
    if any(not row.get(key) for key in keys):
        raise ValueError("missing AD computer identity/change evidence")
    return {key: str(row[key][0]) for key in keys}


def account():
    import ldb
    from samba.auth import system_session
    from samba.dcerpc import security
    from samba.ndr import ndr_unpack
    from samba.param import LoadParm
    from samba.samdb import SamDB
    lp = LoadParm()
    lp.load(str(DC_ROOT / "etc/smb.conf"))
    db = SamDB(url=str(DC_ROOT / "private/sam.ldb"), session_info=system_session(), lp=lp)
    rows = db.search(base=db.domain_dn(), scope=ldb.SCOPE_SUBTREE,
                     expression="(sAMAccountName=ROBOT-AD$)", attrs=["objectSid", "pwdLastSet", "msDS-KeyVersionNumber"])
    if len(rows) != 1:
        raise ValueError("missing unique AD service computer")
    row = {key: list(rows[0][key]) for key in ("objectSid", "pwdLastSet", "msDS-KeyVersionNumber")}
    row["objectSid"] = [str(ndr_unpack(security.dom_sid, row["objectSid"][0]))]
    row["pwdLastSet"] = [row["pwdLastSet"][0].decode()]
    row["msDS-KeyVersionNumber"] = [row["msDS-KeyVersionNumber"][0].decode()]
    print(json.dumps(account_values(row)))


def join_error():
    text = Path("/var/snap/microceph/common/smb/robot-bad/diagnostics/ad-initial-join-error").read_text()[-16384:]
    for secret in json.loads((BASE / "ad-secrets.json").read_text()).values():
        text = text.replace(secret, "<redacted>")
    print(text)


def diagnostics():
    credentials = json.loads((BASE / "ad-secrets.json").read_text()) if (BASE / "ad-secrets.json").exists() else {}
    for file in sorted(BASE.glob("*.first-error")) + [BASE / "dc-provision.log", Path("/var/snap/microceph/common/smb/robot-bad/diagnostics/ad-initial-join-error")]:
        if file.is_file():
            text = file.read_text()[-16384:]
            for secret in credentials.values():
                text = text.replace(secret, "<redacted>")
            print(file.name + ":\n" + text)


def mappings():
    # Observe the real smbd app's mount namespace and inherited library closure.
    pid = run(["systemctl", "show", "snap.microceph.smbd", "-p", "MainPID", "--value"]).stdout.strip()
    if not pid.isdigit() or int(pid) <= 0:
        raise ValueError("smbd is not running")
    env = dict(item.split("=", 1) for item in Path("/proc", pid, "environ").read_bytes().decode().strip("\0").split("\0") if "=" in item)
    def observe(binary, *args):
        result = run(["nsenter", "--mount=/proc/" + pid + "/ns/mnt", "--", env["SNAP"] + "/bin/" + binary, *args], env=env)
        if result.returncode:
            raise ValueError("app-context identity observation failed")
        return result.stdout.strip()
    raw = {}
    for name in ("alice", "bob", "writers"):
        login = DOMAIN + "\\" + name
        raw[name] = [observe("wbinfo", "--name-to-sid=" + login), observe("getent", "group" if name == "writers" else "passwd", login)]
    print(json.dumps(parse_mappings(raw)))


def prepare_data(uid, gid):
    import cephfs
    config = Path(os.environ["SNAP_DATA"]) / "conf"
    fs = cephfs.LibCephFS(conffile=str(config / "ceph.conf"), auth_id="admin")
    try:
        fs.conf_set("keyring", str(config / "ceph.keyring"))
        fs.conf_set("client_fs", "robotsmb")
        fs.mount()
        for path in ("/ad", "/ad-wrong"):
            fs.mkdirs(path, 0o755)
            fs.chown(path, int(uid), int(gid))
            fs.chmod(path, 0o755)
    finally:
        fs.shutdown()


def data_stat(filename):
    import cephfs
    config = Path(os.environ["SNAP_DATA"]) / "conf"
    fs = cephfs.LibCephFS(conffile=str(config / "ceph.conf"), auth_id="admin")
    try:
        fs.conf_set("keyring", str(config / "ceph.keyring"))
        fs.conf_set("client_fs", "robotsmb")
        fs.mount()
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", filename):
            raise ValueError("invalid backing file")
        stat = fs.stat("/ad/" + filename)
        fd = fs.open("/ad/" + filename, "r")
        try:
            content = fs.read(fd, 0, len(PAYLOAD) + 1)
        finally:
            fs.close(fd)
        print(json.dumps({"uid": stat["st_uid"], "gid": stat["st_gid"], "mode": stat["st_mode"] & 0o777,
                          "digest": hashlib.sha256(content).hexdigest()}))
    finally:
        fs.shutdown()


if __name__ == "__main__":
    os.umask(0o077)
    operation, *args = sys.argv[1:]
    # Preserve raw first failures privately; never expose a provision traceback containing secrets.
    try:
        if operation == "dc":
            provision_dc(*args)
        elif operation == "init":
            initialize(json.loads(args[0]), args[1])
        elif operation == "apply":
            sys.exit(apply(*args))
        elif operation == "client":
            client(*args)
        elif operation == "probe-directory":
            sys.exit(probe_directory())
        elif operation == "join-error":
            join_error()
        elif operation == "account":
            account()
        elif operation == "diagnostics":
            diagnostics()
        elif operation == "mappings":
            mappings()
        elif operation == "prepare-data":
            prepare_data(*args)
        elif operation == "stat":
            data_stat(*args)
        else:
            raise ValueError("unknown AD fixture operation")
    except Exception:
        import traceback
        error = BASE / (operation + ".first-error")
        if not error.exists():
            error.write_text(traceback.format_exc())
        print("AD fixture failed; inspect private guest first-error", file=sys.stderr)
        sys.exit(1)

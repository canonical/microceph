"""Pinned upstream bindings for the Go-owned native SMB implementation.

Go owns native policy, files, namespaces, readiness and lifecycle. This adapter
only resolves/renders upstream configuration and invokes Samba/CephFS bindings.
Requests and stderr are private subprocess data captured by the Go caller.
"""
import io
import json
from pathlib import Path
import sys

from sambacc.config import GlobalConfig
from sambacc.netcmd_loader import template_config


def bridge(command, request):
    """Call pinned upstream bindings without publishing native configuration."""
    if command == "check-data":
        import cephfs
        for check in request["checks"]:
            handle = cephfs.LibCephFS(conffile=request["config_path"], auth_id=check["user"])
            try:
                handle.conf_set("client_fs", check["filesystem"])
                handle.mount()
                handle.stat(check["path"])
            finally:
                handle.shutdown()
        return {}
    instance = GlobalConfig(initial_data=request["config"]).get(request["cluster_id"])
    if command == "accounts":
        # with_addc is a controller flag, not external ADS member detection.
        effective = {"".join(key.lower().split()): str(value).strip().lower()
                     for key, value in instance.global_options()}
        if instance.with_addc or "".join(effective.get("security", "").split()) in ("ads", "domain"):
            raise ValueError("AD member/controller must not import local SAM accounts")
        from sambacc.passdb_loader import PassDBLoader
        loader = PassDBLoader(request["config_path"])
        users = list(instance.users())
        wanted = {user.username for user in users}
        # The dedicated user-mode SAM owns normal logins, not built-in accounts.
        for entry in loader._pdb.search_users(0x10):
            if entry["account_name"] not in wanted:
                loader._pdb.delete_sam_account(loader._pdb.getsampwnam(entry["account_name"]))
        for user in users:
            loader.add_user(user)
        return {}
    if command == "inspect":
        return {
            "clustered": instance.with_ctdb,
            "domain_controller": instance.with_addc,
            "globals": [(key, str(value)) for key, value in instance.global_options()],
            "shares": [{"name": share.name, "options": [(key, str(value)) for key, value in share.share_options()]}
                       for share in instance.shares()],
            "users": [{"name": user.username, "fields": list(user.passwd_fields()),
                       "has_password": bool(user.nt_passwd or user.plaintext_passwd)}
                      for user in instance.users()],
            "groups": [{"name": group.groupname, "fields": list(group.group_fields())}
                       for group in instance.groups()],
        }
    if command == "render":
        output = io.StringIO()
        template_config(output, instance)
        return {"smb_conf": output.getvalue()}
    raise ValueError("unsupported upstream binding operation")


if __name__ == "__main__":
    command, request_path = sys.argv[1:]
    request = json.load(sys.stdin) if request_path == "-" else json.loads(Path(request_path).read_text())
    json.dump(bridge(command, request), sys.stdout)
    sys.stdout.write("\n")

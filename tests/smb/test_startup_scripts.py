"""Exercise real startup scripts with fixed-tool fixtures, not snap confinement."""
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[2]


class NativeStartupTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="smb-startup-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.snap = self.directory / "snap"
        self.common = self.directory / "common"
        self.base = self.common / "smb"
        self.root = self.base / "alpha"
        (self.snap / "bin").mkdir(parents=True)
        (self.snap / "commands").mkdir()
        (self.root / "config").mkdir(parents=True)
        (self.root / "proxy-work").mkdir()
        for directory in (self.common, self.base, self.root, self.root / "config", self.root / "proxy-work"):
            directory.chmod(0o700)
        (self.base / "owner.json").write_text("alpha")
        (self.base / "owner.json").chmod(0o600)
        self.context = {"custom_dns": False, "metrics_port": 9922,
                        "clustered": False, "proxy": True}
        self.publish_context()
        (self.root / "config/smb.conf").write_text("[global]\n")
        self.events = self.directory / "events.jsonl"
        self.env = {**os.environ, "SNAP": str(self.snap), "SNAP_COMMON": str(self.common),
                    "STARTUP_EVENTS": str(self.events), "REAL_PYTHON": sys.executable}
        for name in ("smb-startup", "cephfs-proxy.start", "smbmetrics.start",
                     "smbd.start", "ctdbd.start", "winbindd.start", "ctdb.start"):
            shutil.copy2(ROOT / "snapcraft/commands" / name, self.snap / "commands" / name)
        self.tool("python3", '''
if sys.argv[1:3] == ["-B", "-c"]:
    print(os.environ.get("RESOLVER_TARGET", "/etc/resolv.conf"))
elif (sys.argv[1:3] == ["-B", "-"] and len(sys.argv) == 5
      and (sys.argv[3].startswith(str(Path(os.environ["SNAP_COMMON"])))
           or sys.argv[3] in ("/run/ctdb", "/run/samba/winbindd"))):
    # Simulate kernel-only mountpoint/propagation primitives without touching
    # host /run or mounts. All context/record/layout parsing remains real.
    assert os.environ.get("MOCK_PRIVATE_NAMESPACE") == "1"
    if sys.argv[3].startswith(str(Path(os.environ["SNAP_COMMON"]))):
        source = Path(sys.argv[3])
        assert not any(path.is_symlink() for path in (source, *source.parents))
        assert source.stat().st_uid == os.geteuid()
        assert not source.stat().st_mode & 0o022
else:
    os.execv(os.environ["REAL_PYTHON"], [os.environ["REAL_PYTHON"], *sys.argv[1:]])
''')
        self.tool("unshare", '''
record("unshare", sys.argv[1:5])
assert sys.argv[1:5] == ["--mount", "--propagation", "slave", "--"]
os.environ["MOCK_PRIVATE_NAMESPACE"] = "1"
os.execv(sys.argv[5], sys.argv[5:])
''')
        self.tool("mount", '''
record("mount", sys.argv[1:], force_mount2=os.environ.get("LIBMOUNT_FORCE_MOUNT2"))
root = Path(os.environ["SNAP_COMMON"]) / "smb/alpha"
if os.environ.get("STOP_DURING_MOUNT"):
    (root / "stop-requested").write_text("stop requested\\n")
if os.environ.get("CHANGE_OWNER_DURING_MOUNT"):
    (root.parent / "owner.json").write_text("beta")
''')
        native = '''
record(Path(sys.argv[0]).name, sys.argv[1:])
print(json.dumps({"program": Path(sys.argv[0]).name, "args": sys.argv[1:],
                  "pid": os.getpid(), "cwd": os.getcwd(),
                  "socket": os.environ.get("LIBCEPHFSD_SOCKET"),
                  "smbconf": os.environ.get("SMBMETRICS_SMBCONF"),
                  "smbstatus": os.environ.get("SMBMETRICS_SMBSTATUS"),
                  "testparm": os.environ.get("SMBMETRICS_TESTPARM"),
                  "ctdb": os.environ.get("SMBMETRICS_CTDB"),
                  "ctdb_base": os.environ.get("CTDB_BASE")}))
'''
        for program in ("libcephfsd", "smbmetrics", "smbd", "ctdbd", "winbindd"):
            self.tool(program, native)
        self.tool("ctdb", '''
args = sys.argv[1:]
record("ctdb", args)
assert args[:2] == ["-t", "5"]
operation = args[2]
if operation == "pnn":
    print("2")
elif operation == "nodestatus":
    assert args[3:] == ["2"]
elif operation == "status":
    print("Number of nodes:3\\npnn:2 192.0.2.3 OK (THIS NODE)\\nRecovery mode:NORMAL (0)")
elif operation == "getdbmap":
    print("dbid:0x1 name:secrets.tdb path:retained PERSISTENT")
    if not os.environ.get("MISSING_DATABASE"):
        print("dbid:0x2 name:autorid.tdb path:retained PERSISTENT")
elif operation == "pfetch":
    if args[3] != "autorid.tdb" or not os.environ.get("MISSING_RECORD"):
        print("retained")
    if os.environ.get("STOP_DURING_FETCH"):
        (Path(os.environ["SNAP_COMMON"]) / "smb/alpha/stop-requested").write_text("stop")
else:
    sys.exit(9)
''')

    def publish_context(self):
        temporary = self.root / "config/context.next"
        temporary.write_text(json.dumps(self.context))
        temporary.chmod(0o600)
        temporary.replace(self.root / "config/context.json")

    def publish_resolver(self):
        (self.root / "config/resolv.conf").write_text("nameserver 192.0.2.53\n")
        (self.root / "config/resolv.conf").chmod(0o600)

    def tool(self, name, body):
        prelude = f"#!{sys.executable}\n" + '''import json
import os
from pathlib import Path
import sys

def record(program, args, **extra):
    with open(os.environ["STARTUP_EVENTS"], "a") as stream:
        stream.write(json.dumps({"program": program, "args": args, **extra}) + "\\n")

'''
        path = self.snap / "bin" / name
        path.write_text(prelude + body)
        path.chmod(0o755)

    def launch(self, role):
        process = subprocess.Popen([str(self.snap / "commands" / (role + ".start"))],
                                   env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, start_new_session=True)
        try:
            stdout, stderr = process.communicate(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.communicate()
            raise
        return process.pid, process.returncode, stdout, stderr

    def recorded(self):
        if not self.events.exists():
            return []
        return [json.loads(line) for line in self.events.read_text().splitlines()]

    def test_prepared_services_exec_fixed_native_program_in_same_pid(self):
        for role, program in (("cephfs-proxy", "libcephfsd"), ("smbmetrics", "smbmetrics")):
            with self.subTest(role=role):
                pid, rc, stdout, stderr = self.launch(role)
                self.assertEqual(rc, 0, stderr)
                result = json.loads(stdout)
                self.assertEqual(result["pid"], pid, "no resident startup supervisor")
                self.assertEqual(result["program"], program)
                self.assertEqual(result["socket"], str(self.root / "proxy-work/libcephfsd.sock"))
                if role == "cephfs-proxy":
                    self.assertEqual(result["cwd"], str(self.root / "proxy-work"))
                    self.assertEqual(result["args"], ["--socket", result["socket"]])
                else:
                    self.assertEqual(result["args"], ["--address", "0.0.0.0", "--port", "9922"])
                    self.assertEqual(result["smbconf"], str(self.root / "config/smb.conf"))
                    self.assertEqual(result["smbstatus"], str(self.snap / "bin/smbstatus"))
                    self.assertEqual(result["testparm"], str(self.snap / "bin/testparm"))
                    self.assertEqual(result["ctdb"], str(self.snap / "bin/ctdb"))
        self.assertEqual([event["program"] for event in self.recorded()], ["libcephfsd", "smbmetrics"])

    def test_inherited_dns_never_creates_namespace_or_uses_stale_resolver(self):
        (self.root / "config/resolv.conf").write_text("stale custom DNS\n")
        for role in ("cephfs-proxy", "smbmetrics"):
            _, rc, _, stderr = self.launch(role)
            self.assertEqual(rc, 0, stderr)
        self.assertFalse(any(event["program"] in ("mount", "unshare") for event in self.recorded()))

    def test_custom_dns_enters_namespace_before_the_only_bind_and_native_exec(self):
        self.context["custom_dns"] = True
        self.publish_context()
        self.publish_resolver()
        for role, program in (("cephfs-proxy", "libcephfsd"), ("smbmetrics", "smbmetrics")):
            with self.subTest(role=role):
                self.events.unlink(missing_ok=True)
                pid, rc, stdout, stderr = self.launch(role)
                self.assertEqual(rc, 0, stderr)
                self.assertEqual(json.loads(stdout)["pid"], pid)
                events = self.recorded()
                self.assertEqual([event["program"] for event in events], ["unshare", "mount", program])
                self.assertEqual(events[1]["args"], ["--no-mtab", "--internal-only", "--bind",
                                                     str(self.root / "config/resolv.conf"), "/etc/resolv.conf"])
                self.assertEqual(events[1]["force_mount2"], "always")

    def test_systemd_stub_is_an_approved_canonical_resolver_target(self):
        self.context["custom_dns"] = True
        self.publish_context()
        self.publish_resolver()
        self.env["RESOLVER_TARGET"] = "/run/systemd/resolve/stub-resolv.conf"
        _, rc, _, stderr = self.launch("smbmetrics")
        if Path(self.env["RESOLVER_TARGET"]).is_file():
            self.assertEqual(rc, 0, stderr)
            self.assertEqual(self.recorded()[1]["args"][-1], self.env["RESOLVER_TARGET"])
        else:
            self.assertNotEqual(rc, 0, "missing approved targets still cannot be mounted")
            self.assertNotIn("not approved", stderr)
            self.assertEqual([event["program"] for event in self.recorded()], ["unshare"])

    def test_unapproved_resolver_is_refused_without_mount_or_native_exec(self):
        self.context["custom_dns"] = True
        self.publish_context()
        self.publish_resolver()
        self.env["RESOLVER_TARGET"] = "/run/unapproved-resolver"
        _, rc, _, _ = self.launch("smbmetrics")
        self.assertNotEqual(rc, 0)
        self.assertEqual([event["program"] for event in self.recorded()], ["unshare"])

    def test_owner_change_or_stop_after_mount_cannot_reach_native_exec(self):
        self.context["custom_dns"] = True
        self.publish_context()
        self.publish_resolver()
        for change in ("STOP_DURING_MOUNT", "CHANGE_OWNER_DURING_MOUNT"):
            with self.subTest(change=change):
                self.events.unlink(missing_ok=True)
                (self.root / "stop-requested").unlink(missing_ok=True)
                (self.base / "owner.json").write_text("alpha")
                self.env.pop("STOP_DURING_MOUNT", None)
                self.env.pop("CHANGE_OWNER_DURING_MOUNT", None)
                self.env[change] = "1"
                _, rc, _, _ = self.launch("cephfs-proxy")
                self.assertNotEqual(rc, 0)
                self.assertEqual([event["program"] for event in self.recorded()], ["unshare", "mount"])

    def test_bad_context_or_intent_is_refused_before_tools_run(self):
        cases = ("missing-owner", "invalid-owner", "missing-context", "invalid-json",
                 "non-object-context", "unsafe-mode", "symlink-context", "stop",
                 "invalid-port", "invalid-dns", "missing-pnn", "invalid-pnn", "ad-without-cluster")
        original = json.dumps(self.context)
        for case in cases:
            with self.subTest(case=case):
                owner = self.base / "owner.json"
                context = self.root / "config/context.json"
                owner.write_text("alpha")
                owner.chmod(0o600)
                context.unlink(missing_ok=True)
                context.write_text(original)
                context.chmod(0o600)
                (self.root / "stop-requested").unlink(missing_ok=True)
                if case == "missing-owner":
                    owner.unlink()
                elif case == "invalid-owner":
                    owner.write_text("../alpha")
                elif case == "missing-context":
                    context.unlink()
                elif case == "invalid-json":
                    context.write_text("{")
                elif case == "non-object-context":
                    context.write_text("[]")
                elif case == "unsafe-mode":
                    context.chmod(0o666)
                elif case == "symlink-context":
                    context.unlink()
                    context.symlink_to(self.root / "config/smb.conf")
                elif case == "stop":
                    (self.root / "stop-requested").write_text("stop\n")
                else:
                    values = json.loads(original)
                    if case == "invalid-port":
                        values["metrics_port"] = "9922; unexpected-command"
                    elif case == "invalid-dns":
                        values["custom_dns"] = "false"
                    elif case in ("missing-pnn", "invalid-pnn"):
                        values["clustered"] = True
                        if case == "invalid-pnn":
                            values["ctdb_pnn"] = -1
                    else:
                        values["ad_member"] = {"realm": "EXAMPLE.TEST"}
                    context.write_text(json.dumps(values))
                _, rc, _, _ = self.launch("smbmetrics")
                self.assertNotEqual(rc, 0)
                self.assertEqual(self.recorded(), [])

    def test_clustered_environment_uses_existing_owner_local_ctdb_paths(self):
        self.context.update(clustered=True, ctdb_pnn=2)
        self.publish_context()
        _, rc, stdout, stderr = self.launch("smbmetrics")
        self.assertEqual(rc, 0, stderr)
        self.assertEqual(json.loads(stdout)["ctdb_base"], str(self.root / "ctdb"))
        self.assertEqual([event["program"] for event in self.recorded()], ["smbmetrics"])

    def test_proxy_refuses_a_symlink_work_directory(self):
        (self.root / "proxy-work").rmdir()
        (self.root / "proxy-work").symlink_to(self.directory)
        _, rc, _, _ = self.launch("cephfs-proxy")
        self.assertNotEqual(rc, 0)
        self.assertEqual(self.recorded(), [])

    def prepare_clustered(self, *, ad=False):
        for name in ("ctdb/run", "samba/winbindd", "diagnostics"):
            (self.root / name).mkdir(parents=True, exist_ok=True, mode=0o700)
        for name in ("passwd", "group", "nsswitch.conf"):
            (self.root / "config" / name).write_text("prepared fixture\n")
            (self.root / "config" / name).chmod(0o600)
        configuration = "[database]\n" + "".join(
            f"{kind} database directory = {self.root}/ctdb/{kind}\n"
            for kind in ("volatile", "persistent", "state"))
        (self.root / "ctdb/ctdb.conf").write_text(configuration)
        for name in ("functions", "notify.sh", "events/legacy/00.ctdb.script"):
            path = self.root / "ctdb" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("packaged resource fixture\n")
        for name in ("ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper",
                     "ctdb_takeover_helper", "ctdb_lock_helper"):
            path = self.snap / "libexec/ctdb" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("#!/bin/sh\nexit 0\n")
            path.chmod(0o755)
        fixed = self.base / ".conf/ctdb"
        fixed.mkdir(parents=True, mode=0o700)
        (fixed / "ctdb.conf").write_text(configuration)
        (fixed / "owner").write_text("alpha")
        for name in ("events", "functions", "notify.sh"):
            (fixed / name).symlink_to(self.root / "ctdb" / name)
        self.context.update(clustered=True, ctdb_pnn=2,
                            startup={"smbd": {"prepared": True}})
        if ad:
            records = [{"database": "secrets.tdb", "key": "SECRETS/MACHINE_PASSWORD/EXAMPLE"},
                       {"database": "secrets.tdb", "key": "SECRETS/SID/EXAMPLE"},
                       {"database": "autorid.tdb", "key": "0x434f4e46494700"}]
            self.context["ad_member"] = {"workgroup": "EXAMPLE"}
            self.context["startup"] = {
                role: {"prepared": True, "initialized": True, "records": records}
                for role in ("smbd", "winbindd")}
        self.publish_context()

    def test_remaining_services_exec_fixed_native_binary_without_go_supervisor(self):
        self.prepare_clustered(ad=True)
        for role in ("smbd", "ctdbd", "winbindd"):
            with self.subTest(role=role):
                self.events.unlink(missing_ok=True)
                pid, rc, stdout, stderr = self.launch(role)
                self.assertEqual(rc, 0, stderr)
                if role == "winbindd":
                    log = self.root / "diagnostics/winbindd-startup.log"
                    stdout = log.read_text()
                    self.assertEqual(stat.S_IMODE(log.stat().st_mode), 0o600)
                result = json.loads(stdout)
                self.assertEqual(result["program"], role)
                self.assertEqual(result["pid"], pid)
                self.assertEqual(result["ctdb_base"], str(self.root / "ctdb"))
                expected = {
                    "smbd": ["--foreground", "--no-process-group", "--configfile",
                             str(self.root / "config/smb.conf")],
                    # Pinned CTDB accepts only --interactive; its config and
                    # PID paths come from the recipe layout/private runtime bind.
                    "ctdbd": ["--interactive"],
                    "winbindd": ["-F", "--no-process-group", "--configfile",
                                 str(self.root / "config/smb.conf")],
                }
                self.assertEqual(result["args"], expected[role])
                events = self.recorded()
                self.assertEqual(events[0]["program"], "unshare")
                targets = [event["args"][-1] for event in events if event["program"] == "mount"]
                expected_targets = ["/run/ctdb"]
                if role != "ctdbd":
                    expected_targets = ["/etc/passwd", "/etc/group", "/etc/nsswitch.conf",
                                        "/run/ctdb", "/run/samba/winbindd"]
                self.assertEqual(targets, expected_targets)
                self.assertTrue({event["program"] for event in events}.issubset(
                    {"unshare", "mount", "ctdb", role}))

    def test_retained_start_refuses_missing_record_or_database_without_reconstruction(self):
        self.prepare_clustered(ad=True)
        for failure in ("MISSING_RECORD", "MISSING_DATABASE"):
            self.env.pop("MISSING_RECORD", None)
            self.env.pop("MISSING_DATABASE", None)
            self.env[failure] = "1"
            for role in ("smbd", "winbindd"):
                with self.subTest(failure=failure, role=role):
                    self.events.unlink(missing_ok=True)
                    _, rc, _, _ = self.launch(role)
                    self.assertNotEqual(rc, 0)
                    events = self.recorded()
                    self.assertNotIn(role, [event["program"] for event in events])
                    queries = [event["args"][2:] for event in events if event["program"] == "ctdb"]
                    self.assertIn(["getdbmap"], queries)
                    if failure == "MISSING_DATABASE":
                        self.assertNotIn(["pfetch", "autorid.tdb", "0x434f4e46494700"], queries)

    def test_only_go_selected_initial_winbind_requirements_allow_missing_idmap(self):
        self.prepare_clustered(ad=True)
        self.context["startup"]["winbindd"]["records"] = self.context["startup"]["winbindd"]["records"][:2]
        self.context["startup"]["winbindd"]["initialized"] = False
        self.publish_context()
        self.env["MISSING_DATABASE"] = "1"
        _, rc, _, stderr = self.launch("winbindd")
        self.assertEqual(rc, 0, stderr)
        self.events.unlink()
        _, rc, _, _ = self.launch("smbd")
        self.assertNotEqual(rc, 0)
        self.assertNotIn("smbd", [event["program"] for event in self.recorded()])

    def test_ctdb_startup_refuses_missing_helpers_and_foreign_events(self):
        self.prepare_clustered()
        event = self.root / "ctdb/events/legacy/.unexpected"
        event.write_text("retain")
        _, rc, _, _ = self.launch("ctdbd")
        self.assertNotEqual(rc, 0)
        self.assertNotIn("ctdbd", [item["program"] for item in self.recorded()])
        self.assertEqual(event.read_text(), "retain")
        event.unlink()
        (self.snap / "libexec/ctdb/ctdb_lock_helper").unlink()
        self.events.unlink()
        _, rc, _, _ = self.launch("ctdbd")
        self.assertNotEqual(rc, 0)
        self.assertNotIn("ctdbd", [item["program"] for item in self.recorded()])

    def test_fixed_ctdb_configuration_must_match_the_prepared_owner(self):
        self.prepare_clustered()
        (self.base / ".conf/ctdb/owner").write_text("other")
        _, rc, _, _ = self.launch("ctdbd")
        self.assertNotEqual(rc, 0)
        self.assertEqual([event["program"] for event in self.recorded()], ["unshare"])

    def test_fixed_ctdb_resources_must_match_the_prepared_owner(self):
        self.prepare_clustered()
        for name in ("events", "functions", "notify.sh"):
            alias = self.base / ".conf/ctdb" / name
            for failure in ("missing", "foreign"):
                with self.subTest(name=name, failure=failure):
                    alias.unlink()
                    if failure == "foreign":
                        alias.symlink_to(self.directory / "foreign")
                    self.events.unlink(missing_ok=True)
                    try:
                        _, rc, _, _ = self.launch("ctdbd")
                        self.assertNotEqual(rc, 0)
                        self.assertEqual([event["program"] for event in self.recorded()], ["unshare"])
                    finally:
                        alias.unlink(missing_ok=True)
                        alias.symlink_to(self.root / "ctdb" / name)

    def test_stop_during_record_check_refuses_native_exec(self):
        self.prepare_clustered(ad=True)
        self.env["STOP_DURING_FETCH"] = "1"
        _, rc, _, _ = self.launch("winbindd")
        self.assertNotEqual(rc, 0)
        self.assertNotIn("winbindd", [event["program"] for event in self.recorded()])

    def test_unprepared_service_waits_without_serving_and_stop_cancels_wait(self):
        self.prepare_clustered()
        self.context["startup"]["smbd"]["prepared"] = False
        self.publish_context()
        process = subprocess.Popen([str(self.snap / "commands/smbd.start")], env=self.env,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, start_new_session=True)
        try:
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                if any(event["program"] == "ctdb" and event["args"][2:] == ["status"]
                       for event in self.recorded()):
                    break
                time.sleep(0.02)
            else:
                self.fail("startup did not reach its prepared-state guard")
            self.assertIsNone(process.poll())
            self.assertNotIn("smbd", [event["program"] for event in self.recorded()])
            (self.root / "stop-requested").write_text("stop")
            stdout, stderr = process.communicate(timeout=5)
            self.assertNotEqual(process.returncode, 0, stdout + stderr)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()

    def test_winbind_diagnostics_refuse_symlinks_and_fifos(self):
        self.prepare_clustered(ad=True)
        log = self.root / "diagnostics/winbindd-startup.log"
        retained = self.directory / "retain"
        retained.write_text("retain")
        for kind in ("symlink", "fifo"):
            with self.subTest(kind=kind):
                log.unlink(missing_ok=True)
                if kind == "symlink":
                    log.symlink_to(retained)
                else:
                    os.mkfifo(log, mode=0o600)
                self.events.unlink(missing_ok=True)
                _, rc, _, _ = self.launch("winbindd")
                self.assertNotEqual(rc, 0)
                self.assertNotIn("winbindd", [event["program"] for event in self.recorded()])
                self.assertEqual(retained.read_text(), "retain")


if __name__ == "__main__":
    unittest.main()

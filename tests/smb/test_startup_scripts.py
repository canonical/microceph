"""Exercise smbd's real startup scripts; fixtures do not prove confinement."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class NativeStartupTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="smb-startup-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.snap = self.directory / "snap"
        self.common = self.directory / "common"
        self.base = self.common / "smb"
        self.root = self.base / "alpha"
        (self.snap / "bin").mkdir(parents=True)
        (self.snap / "commands").mkdir()
        (self.root / "config").mkdir(parents=True)
        for directory in (self.common, self.base, self.root, self.root / "config"):
            directory.chmod(0o700)
        (self.base / "owner.json").write_text("alpha")
        (self.base / "owner.json").chmod(0o600)
        self.context = {"startup": {"smbd": {"prepared": True}}, "smb_port": 445}
        self.publish_context()
        for name in ("smb.conf", "passwd", "group", "nsswitch.conf"):
            path = self.root / "config" / name
            path.write_text("prepared\n")
            path.chmod(0o600)
        (self.root / "identity").mkdir(mode=0o700)
        (self.root / "samba/private").mkdir(parents=True, mode=0o700)
        for name, data in (("identity/local-sam", "local-sam-v1\n"),
                           ("samba/private/passdb.tdb", "retained-native-identity"),
                           ("samba/private/secrets.tdb", "retained-native-identity")):
            path = self.root / name
            path.write_text(data)
            path.chmod(0o600)
        self.events = self.directory / "events"
        self.env = {**os.environ, "SNAP": str(self.snap), "SNAP_COMMON": str(self.common),
                    "STARTUP_EVENTS": str(self.events), "REAL_PYTHON": sys.executable}
        for name in ("smb-startup", "smbd.start"):
            shutil.copy2(ROOT / "snapcraft/commands" / name, self.snap / "commands" / name)
        self.tool("python3", '''
if sys.argv[1:3] == ["-B", "-"] and len(sys.argv) == 5:
    # Only the mount propagation check is substituted, never context parsing.
    assert os.environ["PRIVATE_NAMESPACE"] == "1"
    source = Path(sys.argv[3])
    assert not any(p.is_symlink() for p in (source, *source.parents))
    assert source.stat().st_uid == os.geteuid()
    assert not source.stat().st_mode & 0o022
else:
    os.execv(os.environ["REAL_PYTHON"], [os.environ["REAL_PYTHON"], *sys.argv[1:]])
''')
        self.tool("unshare", '''
record("unshare", sys.argv[1:5])
assert sys.argv[1:5] == ["--mount", "--propagation", "slave", "--"]
os.environ["PRIVATE_NAMESPACE"] = "1"
os.execv(sys.argv[5], sys.argv[5:])
''')
        self.tool("mount", '''
record("mount", sys.argv[1:])
assert os.environ["LIBMOUNT_FORCE_MOUNT2"] == "always"
root = Path(os.environ["SNAP_COMMON"]) / "smb/alpha"
if os.environ.get("STOP_DURING_MOUNT"):
    (root / "stop-requested").write_text("stop")
if os.environ.get("CHANGE_OWNER_DURING_MOUNT"):
    (root.parent / "owner.json").write_text("beta")
''')
        self.tool("smbd", '''
record("smbd", sys.argv[1:])
print(json.dumps({"pid": os.getpid(), "args": sys.argv[1:]}))
''')

    def tool(self, name, body):
        prelude = f"#!{sys.executable}\n" + '''import json
import os
from pathlib import Path
import sys

def record(program, args):
    with open(os.environ["STARTUP_EVENTS"], "a") as stream:
        stream.write(json.dumps({"program": program, "args": args}) + "\\n")
'''
        path = self.snap / "bin" / name
        path.write_text(prelude + body)
        path.chmod(0o755)

    def publish_context(self):
        path = self.root / "config/context.json"
        path.write_text(json.dumps(self.context))
        path.chmod(0o600)

    def launch(self):
        process = subprocess.Popen([str(self.snap / "commands/smbd.start")], env=self.env,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        stdout, stderr = process.communicate(timeout=10)
        return process.pid, process.returncode, stdout, stderr

    def recorded(self):
        return [json.loads(line) for line in self.events.read_text().splitlines()] if self.events.exists() else []

    def test_smbd_execs_in_same_pid_after_private_nss_binds(self):
        pid, rc, stdout, stderr = self.launch()
        self.assertEqual(rc, 0, stderr)
        self.assertEqual(json.loads(stdout), {"pid": pid, "args": ["--foreground", "--no-process-group", "--configfile", str(self.root / "config/smb.conf")]})
        self.assertEqual([event["program"] for event in self.recorded()], ["unshare", "mount", "mount", "mount", "smbd"])
        self.assertEqual([event["args"][-1] for event in self.recorded() if event["program"] == "mount"], ["/etc/passwd", "/etc/group", "/etc/nsswitch.conf"])

    def test_stop_or_owner_change_during_mount_prevents_native_exec(self):
        for flag in ("STOP_DURING_MOUNT", "CHANGE_OWNER_DURING_MOUNT"):
            with self.subTest(flag=flag):
                (self.base / "owner.json").write_text("alpha")
                (self.root / "stop-requested").unlink(missing_ok=True)
                self.env.pop("STOP_DURING_MOUNT", None)
                self.env[flag] = "1"
                _, rc, _, _ = self.launch()
                self.assertNotEqual(rc, 0)
        self.assertNotIn("smbd", [event["program"] for event in self.recorded()])

    def test_dangling_stop_intent_prevents_binds(self):
        (self.root / "stop-requested").symlink_to(self.root / "missing")
        _, rc, _, _ = self.launch()
        self.assertNotEqual(rc, 0)
        self.assertEqual([event["program"] for event in self.recorded()], ["unshare"])

    def test_clustered_context_requires_assigned_rank(self):
        self.context.update(clustered=True, ctdb_pnn=2)
        self.publish_context()
        command = ['bash', '-euc', '. "$SNAP/commands/smb-startup"; smb_startup_context']
        result = subprocess.run(command, env=self.env, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.split(b'\0')[-3:-1], [b'1', b'2'])
        del self.context['ctdb_pnn']
        self.publish_context()
        result = subprocess.run(command, env=self.env, capture_output=True)
        self.assertNotEqual(result.returncode, 0)

    def test_future_context_rejects_before_binds(self):
        for field in ("clustered", "ad_member", "proxy", "metrics_port"):
            with self.subTest(field=field):
                self.context[field] = True
                self.publish_context()
                _, rc, _, _ = self.launch()
                self.assertNotEqual(rc, 0)
                del self.context[field]
        self.assertTrue(all(event["program"] == "unshare" for event in self.recorded()))

    def test_retained_local_sam_loss_refuses_independent_start_without_recreation(self):
        # Model a retained prepared gateway, not first Go-owned initialization.
        private = self.root / "samba/private"
        identity = self.root / "identity"
        fence = identity / "local-sam"
        fence.write_text("local-sam-v1\n")
        fence.chmod(0o600)
        for lost in ("passdb.tdb", "secrets.tdb", "both", "fence", "empty", "symlink"):
            with self.subTest(lost=lost):
                fence.write_text("local-sam-v1\n")
                for name in ("passdb.tdb", "secrets.tdb"):
                    path = private / name
                    path.unlink(missing_ok=True)
                    path.write_text("retained-native-identity")
                    path.chmod(0o600)
                if lost == "both":
                    for path in private.iterdir():
                        path.unlink()
                elif lost == "fence":
                    fence.unlink()
                elif lost == "empty":
                    (private / "secrets.tdb").write_text("")
                elif lost == "symlink":
                    (private / "secrets.tdb").unlink()
                    (private / "secrets.tdb").symlink_to(private / "passdb.tdb")
                else:
                    (private / lost).unlink()
                before = {str(p): p.read_bytes() for p in private.iterdir()}
                self.assertNotEqual(self.launch()[1], 0)
                self.assertEqual({str(p): p.read_bytes() for p in private.iterdir()}, before)
        self.assertNotIn("smbd", [event["program"] for event in self.recorded()])

    def test_context_symlink_and_writable_mode_refused(self):
        path = self.root / "config/context.json"
        path.chmod(0o666)
        self.assertNotEqual(self.launch()[1], 0)
        path.chmod(0o600)
        target = self.directory / "outside"
        path.replace(target)
        path.symlink_to(target)
        self.assertNotEqual(self.launch()[1], 0)
        self.assertNotIn("smbd", [event["program"] for event in self.recorded()])


if __name__ == "__main__":
    unittest.main()

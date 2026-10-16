"""Readiness-only monitor transport; real sockets/processes, no confinement claim."""
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import unittest

import test_ctdb_startup as startup


class CTDBReadinessTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        startup.CTDBStartupTests.setUpClass()
        cls.native = startup.CTDBStartupTests.native

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="smb-ready-")
        self.addCleanup(self.directory.cleanup)
        base = Path(self.directory.name)
        self.root = base / "alpha"
        (self.root / "config").mkdir(parents=True)
        (self.root / "ctdb/run").mkdir(parents=True)
        (base / "owner.json").write_text("alpha")
        candidate = startup.CTDBStartupTests.candidate(self)
        files, context = self.native.compile_core(candidate, str(self.root))
        (self.root / "config/candidate.json").write_text(json.dumps(candidate))
        (self.root / "config/context.json").write_text(json.dumps(context))
        (self.root / "ctdb/nodes").write_text(files["ctdb/nodes"])
        (self.root / "ctdb/run/ctdb-nodes.pid").write_text(str(os.getpid()))
        self.snap = base / "snap"
        (self.snap / "bin").mkdir(parents=True)
        self.mode = base / "mode"
        self.mode.write_text("healthy")
        self.log = base / "commands"
        # Only the production CTDB CLI operations/argv are emulated. No custom
        # socket flags, test mode, private protocol or permission translation.
        ctdb = self.snap / "bin/ctdb"
        ctdb.write_text("#!" + sys.executable + "\n" + '''
import os
from pathlib import Path
import sys
import time
base = Path(os.environ["SNAP"]).parent
mode = (base / "mode").read_text()
with (base / "commands").open("a") as out:
    out.write(" ".join(sys.argv[1:]) + "\\n")
args = sys.argv[1:]
if args == ["pnn"]:
    if mode == "timeout":
        time.sleep(10)
    if mode == "pause":
        (base / "in-control").write_text("pnn")
        until = time.monotonic() + 4
        while not (base / "release-control").exists() and time.monotonic() < until:
            time.sleep(0.01)
    print("1" if mode == "pnn" else "2")
elif args == ["nodestatus", "2"]:
    sys.exit(1 if mode == "node" else 0)
elif args == ["status"]:
    print("Number of nodes:3")
    if mode != "membership":
        print("pnn:0 192.0.2.1 OK")
    print("pnn:2 192.0.2.3 OK")
    print("Recovery mode:RECOVERY (1)" if mode == "recovery" else "Recovery mode:NORMAL (0)")
elif args == ["reloadnodes"]:
    pass  # Exit zero must not mask a still-wrong membership map.
else:
    sys.exit(9)
''')
        ctdb.chmod(0o700)
        self.env = dict(os.environ, SNAP=str(self.snap), PYTHONDONTWRITEBYTECODE="1")

    def command(self, role="ctdbd"):
        return subprocess.run([sys.executable, "-B", str(startup.MODULE), "ready", str(self.root), role],
                              env=self.env, capture_output=True, timeout=28)

    def monitor(self):
        process = subprocess.Popen([sys.executable, "-B", str(startup.MODULE), "nodes", str(self.root)],
                                   env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(self.stop_monitor, process)
        return process

    def stop_monitor(self, process):
        if process.poll() is None:
            process.terminate()
        process.communicate(timeout=8)

    def wait_ready(self):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if self.command().returncode == 0:
                return
            time.sleep(0.05)
        self.fail("monitor did not confirm real control readiness")

    def test_daemon_command_refuses_stale_files_even_with_healthy_ctdb_cli(self):
        # The denied snap invocation cannot be replaced by a PID/file check or
        # an unnamespaced CTDB client. This process must require a live monitor.
        self.assertNotEqual(self.command().returncode, 0)
        self.assertFalse(self.log.exists(), "daemon-side helper must not execute CTDB")

    def test_fresh_controls_fail_closed_and_recover_without_restart(self):
        self.monitor()
        self.wait_ready()
        for mode in ("pnn", "node", "recovery", "membership", "healthy"):
            with self.subTest(mode=mode):
                self.native.atomic_write(self.mode, mode)
                result = self.command("ctdb-nodes")
                self.assertEqual(result.returncode == 0, mode == "healthy", result.stderr)
        commands = self.log.read_text().splitlines()
        self.assertIn("pnn", commands)
        self.assertIn("nodestatus 2", commands)
        self.assertIn("status", commands)
        # A request can see transient bad membership after accept even if no
        # updater iteration sees it. Reload issuance/map verification belongs
        # to CTDBMembershipTests, not this fresh-health transport test.

    def test_stop_owner_and_nonclustered_checks_reject_even_live_endpoint(self):
        self.monitor()
        self.wait_ready()
        (self.root / "stop-requested").write_text("stop")
        self.assertNotEqual(self.command().returncode, 0)
        (self.root / "stop-requested").unlink()
        (self.root.parent / "owner.json").write_text("other")
        self.assertNotEqual(self.command().returncode, 0)
        (self.root.parent / "owner.json").write_text("alpha")
        context_path = self.root / "config/context.json"
        context = json.loads(context_path.read_text())
        context["clustered"] = False
        self.native.atomic_write(context_path, json.dumps(context))
        self.assertNotEqual(self.command().returncode, 0)

    def test_crash_stale_socket_and_pid_cannot_be_ready_and_restart_recovers(self):
        process = self.monitor()
        self.wait_ready()
        process.kill()
        process.communicate(timeout=8)
        self.assertTrue((self.root / "ctdb/run/native-ready.socket").exists())
        self.assertNotEqual(self.command().returncode, 0)
        self.monitor()
        self.wait_ready()

    def test_listener_rejects_duplicate_and_symlink_without_stealing_endpoint(self):
        with self.native.ctdb_readiness_listener(self.root):
            with self.assertRaises(BlockingIOError):
                with self.native.ctdb_readiness_listener(self.root):
                    self.fail("second monitor stole listener")
        target = self.root / "ctdb/run/native-ready.socket"
        outside = self.root.parent / "outside"
        outside.write_text("retain")
        target.symlink_to(outside)
        with self.assertRaises(ValueError):
            with self.native.ctdb_readiness_listener(self.root):
                self.fail("symlink replaced")
        self.assertEqual(outside.read_text(), "retain")

    def test_endpoint_rechecks_health_after_accept_and_honors_control_timeout(self):
        self.monitor()
        self.wait_ready()
        self.native.atomic_write(self.mode, "timeout")
        started = time.monotonic()
        self.assertNotEqual(self.command().returncode, 0)
        self.assertLess(time.monotonic() - started, 12)
        self.native.atomic_write(self.mode, "healthy")
        self.wait_ready()

    def test_stop_during_control_cannot_acknowledge_old_health(self):
        self.monitor()
        self.wait_ready()
        self.native.atomic_write(self.mode, "pause")
        with socket.socket(socket.AF_UNIX) as client:
            client.settimeout(7)
            client.connect(str(self.root / "ctdb/run/native-ready.socket"))
            deadline = time.monotonic() + 3
            while not (self.root.parent / "in-control").exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue((self.root.parent / "in-control").exists())
            (self.root / "stop-requested").write_text("stop")
            (self.root.parent / "release-control").write_text("release")
            self.assertEqual(client.recv(1), b"")

    def test_server_checks_stop_and_owner_not_only_the_client(self):
        # Connect directly: a caller cannot bypass server-side launch intent.
        self.monitor()
        self.wait_ready()
        for change in ("stop", "owner"):
            with self.subTest(change=change):
                if change == "stop":
                    (self.root / "stop-requested").write_text("stop")
                else:
                    (self.root.parent / "owner.json").write_text("other")
                with socket.socket(socket.AF_UNIX) as client:
                    client.settimeout(7)
                    client.connect(str(self.root / "ctdb/run/native-ready.socket"))
                    self.assertEqual(client.recv(1), b"")
                (self.root / "stop-requested").unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()

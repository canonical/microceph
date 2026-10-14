"""Native clustered wiring contracts; no daemons or namespaces are launched."""
import importlib.util
import json
import os
import sys
from pathlib import Path
import tempfile
import unittest
import yaml
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[2] / "snapcraft/smb/native.py"


class CTDBStartupTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("native_startup", MODULE)
        cls.native = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.native)

    def candidate(self):
        return {"spec": {"cluster_id": "alpha", "features": ["clustered"],
                         "cluster_meta_uri": "rados://.smb/alpha/cluster.meta",
                         "cluster_lock_uri": "rados://.smb/alpha/cluster.lock"},
                "config": {"samba-container-config": "v0", "globals": {}, "shares": {}, "configs": {"alpha": {
                    "instance_name": "alpha", "instance_features": ["ctdb"], "shares": [], "globals": []}}},
                "ctdb": {"member": {"identity": "3", "pnn": 2, "node": "192.0.2.3", "state": "new"},
                         "peers": [{"identity": "1", "pnn": 0, "node": "192.0.2.1", "state": "new"}]},
                "ceph_config": "[global]\nmon host = 192.0.2.5\n[client.admin]\nkeyring = /admin\n"}

    def test_clustered_compile_calls_projection_with_authoritative_view(self):
        candidate = self.candidate()
        with patch.object(self.native, "project_ctdb", wraps=self.native.project_ctdb) as projection:
            files, context = self.native.compile_core(candidate, "/native/alpha")
        self.assertEqual(projection.call_args.kwargs["member"], candidate["ctdb"]["member"])
        self.assertEqual(projection.call_args.kwargs["peers"], candidate["ctdb"]["peers"])
        self.assertEqual(files["ctdb/nodes"], "192.0.2.1\n#\n192.0.2.3\n")
        self.assertIn("config/smb.conf", files)
        self.assertEqual(context["ctdb_pnn"], 2)
        self.assertTrue(context["clustered"])
        for modification in ("view", "feature", "effective"):
            bad = self.candidate()
            if modification == "view":
                del bad["ctdb"]
            elif modification == "feature":
                bad["spec"]["features"] = []
            else:
                bad["config"]["configs"]["alpha"]["instance_features"] = []
            with self.subTest(modification=modification), self.assertRaises(ValueError):
                self.native.compile_core(bad, "/native/alpha")

    def test_sql_rank_history_retains_trailing_retired_slots(self):
        candidate = self.candidate()
        candidate["ctdb"]["slot_count"] = 5
        files, _ = self.native.compile_core(candidate, "/native/alpha")
        self.assertEqual(files["ctdb/nodes"], "192.0.2.1\n#\n192.0.2.3\n#\n#\n")
        for invalid in (2, 65537, True):
            candidate["ctdb"]["slot_count"] = invalid
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                self.native.compile_core(candidate, "/native/alpha")

    def test_nested_compile_publication_and_private_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "config/candidate.next.json").write_text(json.dumps(self.candidate()))
            with patch.dict(os.environ, SNAP="/snap/microceph/current"), patch("subprocess.run") as run:
                self.native.compile_files(root)
                self.native.publish(root)
            self.assertEqual((root / "ctdb/nodes").read_text(), "192.0.2.1\n#\n192.0.2.3\n")
            self.assertEqual(run.call_args.args[0][-1], str(root / "config/next/config/smb.conf"))
            recovery = (root / "config/ctdb-recovery.conf").read_text()
            self.assertIn("client.smb.cluster.alpha", recovery)
            self.assertIn(str(root / "identity/recovery.keyring"), recovery)
            self.assertNotIn("client.admin", recovery)
            self.assertNotIn("data.keyring", recovery)
            for file in (root / "config/next").rglob("*"):
                if file.is_file():
                    self.assertEqual(file.stat().st_mode & 0o777, 0o600)
            self.assertTrue((root / "ctdb/persistent").is_dir())
            self.assertTrue((root / "samba/winbindd").is_dir())
            # Publication must not follow a pre-existing escaping target.
            (root / "ctdb/nodes").unlink()
            (root / "ctdb/nodes").symlink_to(Path(directory) / "outside")
            with self.assertRaises(ValueError):
                self.native.publish(root)
            self.assertFalse((Path(directory) / "outside").exists())

    def test_config_only_publication_preserves_ctdb_and_monitor_reload(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            candidate = self.candidate()
            (root / "config/candidate.next.json").write_text(json.dumps(candidate))
            with patch.dict(os.environ, SNAP="/snap/microceph/current"), patch("subprocess.run"):
                self.native.compile_files(root)
                self.native.publish(root)
                # Simulate runtime-owned state that must not be overwritten.
                (root / "ctdb/nodes").write_text("192.0.2.1\n#\n#\n")
                (root / "ctdb/state/retained").write_text("runtime")
                candidate["users"] = [{"users": {"all_entries": [{"name": "alice", "password": "changed", "uid": 2000, "gid": 2000}]}}]
                (root / "config/candidate.next.json").write_text(json.dumps(candidate))
                self.native.compile_files(root)
                self.native.publish_config(root)
            self.assertIn("alice:x:2000:2000:", (root / "config/passwd").read_text())
            self.assertEqual((root / "ctdb/nodes").read_text(), "192.0.2.1\n#\n#\n")
            self.assertEqual((root / "ctdb/state/retained").read_text(), "runtime")
            with patch.object(self.native, "ctdb_control", side_effect=[
                    b"Number of nodes:3\npnn:0 192.0.2.1 OK\nRecovery mode:NORMAL (0)\n", b"",
                    b"Number of nodes:3\npnn:0 192.0.2.1 OK\npnn:2 192.0.2.3 OK\nRecovery mode:NORMAL (0)\n"]) as control:
                self.native.update_ctdb_nodes(root)
            self.assertEqual([call.args[1] for call in control.call_args_list], ["status", "reloadnodes", "status"])
            self.assertEqual((root / "ctdb/nodes").read_text(), "192.0.2.1\n#\n192.0.2.3\n")
            (root / "stop-requested").write_text("stop")
            with self.assertRaisesRegex(ValueError, "stop requested"):
                self.native.publish_config(root)

    def test_config_only_rejects_ctdb_reconfiguration_before_any_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            candidate = self.candidate()
            (root / "config/candidate.next.json").write_text(json.dumps(candidate))
            with patch.dict(os.environ, SNAP="/snap/microceph/current"), patch("subprocess.run"):
                self.native.compile_files(root)
                self.native.publish(root)
                original = (root / "config/candidate.json").read_bytes()
                candidate["spec"]["cluster_lock_uri"] = "rados://.smb/alpha/new.lock"
                (root / "config/candidate.next.json").write_text(json.dumps(candidate))
                self.native.compile_files(root)
                with self.assertRaisesRegex(ValueError, "CTDB.*maintenance"):
                    self.native.publish_config(root)
            self.assertEqual((root / "config/candidate.json").read_bytes(), original)

    def test_fixed_commands_mounts_and_pinned_event_preparation(self):
        context = {"clustered": True, "ctdb_pnn": 2}
        snap = "/snap/microceph/current"
        self.assertEqual(self.native.role_command("ctdbd", "/native/alpha", context, snap), [snap + "/bin/ctdbd", "--interactive"])
        self.assertEqual(self.native.role_command("ctdb-nodes", "/native/alpha", context, snap),
                         [snap + "/bin/python3", "-B", snap + "/smb/native.py", "nodes", "/native/alpha"])
        mounts = self.native.launch_mounts("ctdbd", "/native/alpha", context)
        for source, target in (("ctdb", "/etc/ctdb"), ("ctdb/run", "/run/ctdb"),
                               ("ctdb", "/var/lib/ctdb"), ("config/ctdb-recovery.conf", "/etc/ceph/ceph.conf")):
            self.assertIn(("/native/alpha/" + source, target, 4096), mounts)
        self.assertNotIn("CTDB_TEST_MODE", MODULE.read_text())
        with tempfile.TemporaryDirectory() as directory:
            root, snap = Path(directory) / "alpha", Path(directory) / "snap"
            for name in ("functions", "notify.sh", "events/legacy/00.ctdb.script"):
                resource = snap / "share/ctdb" / name
                resource.parent.mkdir(parents=True, exist_ok=True)
                resource.write_text("#!/bin/sh\nexit 0\n")
                resource.chmod(0o755)
            for name in ("ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper", "ctdb_takeover_helper", "ctdb_lock_helper"):
                resource = snap / "libexec/ctdb" / name
                resource.parent.mkdir(parents=True, exist_ok=True)
                resource.write_text("helper")
                resource.chmod(0o755)
            (root / "ctdb/run").mkdir(parents=True)
            (root / "ctdb/run/ctdb-nodes.pid").write_text("stale")
            self.native.prepare_ctdb(root, str(snap))
            self.assertFalse((root / "ctdb/run/ctdb-nodes.pid").exists())
            self.assertEqual((root / "ctdb/functions").read_text(), "#!/bin/sh\nexit 0\n")
            self.assertEqual([p.name for p in (root / "ctdb/events/legacy").iterdir()], ["00.ctdb.script"])
            (snap / "libexec/ctdb/ctdb-eventd").unlink()
            with self.assertRaises(ValueError):
                self.native.prepare_ctdb(root, str(snap))

    def test_recipe_primes_real_resource_paths_and_fixed_control(self):
        recipe = yaml.safe_load((MODULE.parents[2] / "snap/snapcraft.yaml").read_text())
        part = recipe["parts"]["native-smb"]
        self.assertIn("libexec/ctdb/**", part["prime"])
        self.assertIn("share/ctdb/**", part["prime"])
        self.assertEqual(part["organize"]["etc/ctdb/functions"], "share/ctdb/functions")
        self.assertEqual(part["organize"]["etc/ctdb/notify.sh"], "share/ctdb/notify.sh")
        for target in ("/etc/ctdb", "/run/ctdb", "/var/lib/ctdb"):
            self.assertEqual(recipe["layout"][target], {"type": "tmpfs"})
        self.assertEqual(recipe["layout"]["/usr/libexec/ctdb"]["symlink"], "$SNAP/libexec/ctdb")
        self.assertEqual(recipe["apps"]["ctdb"]["command"], "commands/ctdb.start")
        self.assertIn("smb-mounts", recipe["apps"]["ctdb"]["plugs"])
        with patch.dict(os.environ, SNAP="/snap/microceph/current"), patch("subprocess.check_output", return_value=b"2\n") as run:
            self.assertEqual(self.native.ctdb_control(Path("/native/alpha"), "pnn"), b"2\n")
            self.assertEqual(run.call_args.args[0], ["/snap/microceph/current/bin/ctdb", "pnn"])
            self.native.ctdb_control(Path("/native/alpha"), "nodestatus", "2")
            self.assertEqual(run.call_args.args[0][-2:], ["nodestatus", "2"])
            for command, args in (("shell", []), ("pnn", ["--help"]), ("nodestatus", ["-1"])):
                with self.subTest(command=command), self.assertRaises(ValueError):
                    self.native.ctdb_control(Path("/native/alpha"), command, *args)

    def test_readiness_requires_assigned_pnn_healthy_node_and_normal_recovery(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "config").mkdir()
            (root / "config/context.json").write_text(json.dumps({"clustered": True, "ctdb_pnn": 2}))
            for output, failure in (([b"2\n", b"", b"Recovery mode:NORMAL (0)\n"], False), ([b"1\n"], True),
                                    ([b"2\n", b"", b"Recovery mode:RECOVERY (1)\n"], True)):
                with patch.object(self.native, "ctdb_control", side_effect=output) as control:
                    if failure:
                        with self.assertRaises(ValueError):
                            self.native.ready(root, "ctdbd")
                    else:
                        self.native.ready(root, "ctdbd")
                        self.assertEqual([call.args[1:] for call in control.call_args_list], [("pnn",), ("nodestatus", "2"), ("status",)])
            with patch.object(self.native, "ctdb_control", side_effect=RuntimeError("unhealthy")):
                with self.assertRaises(RuntimeError):
                    self.native.ready(root, "ctdbd")

    def test_app_launch_gates_sam_access_even_without_go_ordering(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            root = base / "alpha"
            (root / "config").mkdir(parents=True)
            (base / "owner.json").write_text("alpha")
            (root / "config/context.json").write_text(json.dumps({"clustered": True, "ctdb_pnn": 2}))
            with patch.object(sys, "argv", ["native.py", "launch", str(base), "smbd"]), patch.dict(os.environ, SNAP="/snap/microceph/current"), patch.object(self.native, "enter_launch_context"), patch.object(self.native, "prepare_local_accounts") as sam, patch("os.execv") as execute:
                with patch.object(self.native, "ready", side_effect=ValueError("CTDB is recovering")):
                    with self.assertRaisesRegex(ValueError, "recovering"):
                        self.native.main()
                    sam.assert_not_called()
                    execute.assert_not_called()
                events = []
                sam.side_effect = lambda *_: events.append("sam")
                execute.side_effect = lambda *_: events.append("exec")
                with patch.object(self.native, "ready", side_effect=lambda *args: events.append("ctdb ready")):
                    self.native.main()
                self.assertEqual(events, ["ctdb ready", "sam", "exec"])
            for source, target in (("ctdb", "/etc/ctdb"), ("ctdb/run", "/run/ctdb")):
                self.assertIn((str(root / source), target, 4096), self.native.launch_mounts("smbd", str(root), {"clustered": True}))

    def test_nodes_step_observes_daemon_even_when_startup_file_matches(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "ctdb").mkdir()
            (root / "config/candidate.json").write_text(json.dumps(self.candidate()))
            (root / "ctdb/nodes").write_text("192.0.2.1\n#\n192.0.2.3\n")
            with patch.object(self.native, "ctdb_control", return_value=b"Number of nodes:3\npnn:0 192.0.2.1 OK\npnn:2 192.0.2.3 OK\nRecovery mode:NORMAL (0)\n") as control:
                self.native.update_ctdb_nodes(root)
                control.assert_called_once_with(root, "status")
            (root / "stop-requested").write_text("stop")
            with self.assertRaises(ValueError):
                self.native.update_ctdb_nodes(root)


if __name__ == "__main__":
    unittest.main()

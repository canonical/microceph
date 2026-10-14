"""Pinned CTDB control contracts with real compilation/files, not daemon tests."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import test_ctdb_startup as startup


class CTDBMembershipTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        startup.CTDBStartupTests.setUpClass()
        cls.native = startup.CTDBStartupTests.native

    def candidate(self):
        return startup.CTDBStartupTests.candidate(self)

    def status(self, peers, slots=3):
        return (f"Number of nodes:{slots}\n" + "".join(
            f"pnn:{pnn} {ip} {flags}\n" for pnn, ip, flags in peers
        ) + "Recovery mode:NORMAL (0)\n").encode()

    def test_reload_waits_for_peer_files_without_rolling_back_and_verifies_daemon(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "ctdb").mkdir()
            (root / "config/candidate.json").write_text(json.dumps(self.candidate()))
            (root / "ctdb/nodes").write_text("192.0.2.1\n#\n#\n")
            old = self.status([(0, "192.0.2.1", "OK")])
            new = self.status([(0, "192.0.2.1", "OK"), (2, "192.0.2.3", "OK")])
            with patch.object(self.native, "ctdb_control", side_effect=[old, RuntimeError("peer files differ")]):
                with self.assertRaises(RuntimeError):
                    self.native.update_ctdb_nodes(root)
            expected = "192.0.2.1\n#\n192.0.2.3\n"
            self.assertEqual((root / "ctdb/nodes").read_text(), expected,
                             "all nodes must retain the target file for reloadnodes' identical-file check")
            # Even exit zero can hide a partial reload in the pinned tools/ctdb.c.
            with patch.object(self.native, "ctdb_control", side_effect=[old, b"", old]):
                with self.assertRaisesRegex(ValueError, "membership"):
                    self.native.update_ctdb_nodes(root)
            with patch.object(self.native, "ctdb_control", side_effect=[old, b"", new]) as control:
                self.native.update_ctdb_nodes(root)
                self.assertEqual([c.args[1] for c in control.call_args_list], ["status", "reloadnodes", "status"])
            with patch.object(self.native, "ctdb_control", return_value=new) as control:
                self.native.update_ctdb_nodes(root)
                control.assert_called_once_with(root, "status")

    def test_reload_rejects_unhealthy_survivors_and_changed_active_address(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "ctdb").mkdir()
            (root / "config/candidate.json").write_text(json.dumps(self.candidate()))
            (root / "ctdb/nodes").write_text("old\n")
            for peers in ([(0, "192.0.2.1", "UNHEALTHY")], [(0, "192.0.2.9", "OK")]):
                with self.subTest(peers=peers), patch.object(self.native, "ctdb_control", return_value=self.status(peers)) as control:
                    with self.assertRaises(ValueError):
                        self.native.update_ctdb_nodes(root)
                    control.assert_called_once_with(root, "status")

    def test_monitor_retries_read_and_reload_failures(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "ctdb/run").mkdir(parents=True)
            with patch.object(self.native, "update_ctdb_nodes", side_effect=[OSError("read failed"), RuntimeError("reload failed"), None]) as update, patch.object(self.native, "ready"), patch("time.sleep", side_effect=[None, None, KeyboardInterrupt]):
                with self.assertRaises(KeyboardInterrupt):
                    self.native.monitor_ctdb_nodes(root)
            self.assertEqual(update.call_count, 3)
            self.assertFalse((root / "ctdb/run/ctdb-nodes.pid").exists())

    def test_retirement_and_same_address_reuse_preserve_slot_bound(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "ctdb").mkdir()
            candidate = self.candidate()
            candidate["ctdb"]["peers"] = [{"identity": "4", "pnn": 3, "node": "192.0.2.1", "state": "new"}]
            candidate["ctdb"]["slot_count"] = 5
            (root / "config/candidate.json").write_text(json.dumps(candidate))
            (root / "ctdb/nodes").write_text("192.0.2.1\n#\n192.0.2.3\n#\n#\n")
            old = self.status([(0, "192.0.2.1", "DISCONNECTED"), (2, "192.0.2.3", "OK")], 5)
            new = self.status([(2, "192.0.2.3", "OK"), (3, "192.0.2.1", "OK")], 5)
            with patch.object(self.native, "ctdb_control", side_effect=[old, b"", new]):
                self.native.update_ctdb_nodes(root)
            self.assertEqual((root / "ctdb/nodes").read_text().splitlines(), ["#", "#", "192.0.2.3", "192.0.2.1", "#"])

    def test_nodes_readiness_requires_daemon_map_not_just_pid_or_reload_exit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "ctdb/run").mkdir(parents=True)
            (root / "config/candidate.json").write_text(json.dumps(self.candidate()))
            (root / "config/context.json").write_text(json.dumps({"clustered": True, "ctdb_pnn": 2}))
            (root / "ctdb/run/ctdb-nodes.pid").write_text(str(os.getpid()))
            (root / "ctdb/nodes").write_text("192.0.2.1\n#\n192.0.2.3\n")
            for slots, peers, failure in ((3, [(0, "192.0.2.1", "OK"), (2, "192.0.2.3", "OK")], False),
                                           (4, [(0, "192.0.2.1", "OK"), (2, "192.0.2.3", "OK")], True),
                                           (3, [(2, "192.0.2.3", "OK")], True)):
                status = self.status(peers, slots)
                with self.subTest(slots=slots, peers=peers), patch.object(self.native, "ctdb_control", side_effect=[b"2\n", b"", status, status]):
                    if failure:
                        with self.assertRaisesRegex(ValueError, "membership"):
                            self.native.ready(root, "ctdb-nodes")
                    else:
                        self.native.ready(root, "ctdb-nodes")

    def test_atomic_node_publication_failure_does_not_issue_reload(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "alpha"
            (root / "config").mkdir(parents=True)
            (root / "ctdb").mkdir()
            (root / "config/candidate.json").write_text(json.dumps(self.candidate()))
            (root / "ctdb/nodes").write_text("retained\n")
            with patch.object(self.native, "atomic_write", side_effect=OSError("publication failed")), patch.object(self.native, "ctdb_control") as control:
                with self.assertRaises(OSError):
                    self.native.update_ctdb_nodes(root)
                control.assert_not_called()
            self.assertEqual((root / "ctdb/nodes").read_text(), "retained\n")


if __name__ == "__main__":
    unittest.main()

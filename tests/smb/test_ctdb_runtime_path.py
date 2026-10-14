"""CTDB mountpoint safety and shared consumer contracts, not live confinement."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import yaml

ROOT = Path(__file__).resolve().parents[2]


class CTDBRuntimePathTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("native_runtime", ROOT / "snapcraft/smb/native.py")
        cls.native = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.native)

    def test_mountpoint_creation_and_reuse_do_not_modify_existing_data(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "ctdb"
            self.native._prepare_ctdb_mountpoint(target)
            self.assertTrue(target.is_dir())
            self.assertFalse(target.stat().st_mode & 0o022)
            target.chmod(0o700)
            self.native._prepare_ctdb_mountpoint(target)
            self.assertEqual(target.stat().st_mode & 0o777, 0o700)
            retained = target / "unrelated.socket"
            retained.write_text("host state")
            with self.assertRaisesRegex(ValueError, "mountpoint"):
                self.native._prepare_ctdb_mountpoint(target)
            self.assertEqual(retained.read_text(), "host state")
            self.assertEqual(target.stat().st_mode & 0o777, 0o700)

    def test_mountpoint_rejects_symlinks_files_unsafe_modes_and_foreign_owner(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            outside = base / "outside"
            outside.mkdir()
            link = base / "link"
            link.symlink_to(outside)
            regular = base / "file"
            regular.write_text("keep")
            unsafe = base / "unsafe"
            unsafe.mkdir()
            unsafe.chmod(0o777)
            for target in (link, regular, unsafe):
                with self.subTest(target=target), self.assertRaisesRegex(ValueError, "mountpoint"):
                    self.native._prepare_ctdb_mountpoint(target)
            with patch("os.geteuid", return_value=outside.stat().st_uid + 1):
                with self.assertRaisesRegex(ValueError, "mountpoint"):
                    self.native._prepare_ctdb_mountpoint(outside)
            self.assertTrue(link.is_symlink())
            self.assertEqual(regular.read_text(), "keep")
            self.assertEqual(unsafe.stat().st_mode & 0o777, 0o777)
            self.assertEqual(list(outside.iterdir()), [])

    def test_all_socket_consumers_share_private_bind_and_permission(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        self.assertNotIn("/run/ctdb", recipe["layout"])
        self.assertNotIn("/var/run/ctdb", recipe["layout"])
        self.assertEqual(recipe["plugs"]["ctdb-run"], {"interface": "system-files", "write": ["/run/ctdb"]})
        consumers = {"ctdbd": "ctdbd", "ctdb-nodes": "ctdb-nodes", "ctdb": "ctdb-control", "smbd": "smbd"}
        granted = {name for name, app in recipe["apps"].items() if "ctdb-run" in app.get("plugs", [])}
        self.assertEqual(granted, set(consumers))
        for app, role in consumers.items():
            with self.subTest(app=app):
                mounts = self.native.launch_mounts(role, "/native/alpha", {"clustered": True})
                self.assertEqual(mounts[0], (None, "/", (1 << 14) | (1 << 19)))
                self.assertIn(("/native/alpha/ctdb/run", "/run/ctdb", 4096), mounts)
                self.assertIn("smb-mounts", recipe["apps"][app]["plugs"])
                self.assertIn("network-control", recipe["apps"][app]["plugs"])
        self.assertNotIn(("/native/alpha/ctdb/run", "/run/ctdb", 4096),
                         self.native.launch_mounts("smbd", "/native/alpha", {}))

    def test_launch_prepares_target_after_nonpropagation_before_runtime_bind(self):
        import ctypes
        events = []
        class LibC:
            def unshare(self, flags):
                events.append(("unshare", flags))
                return 0
            class Mount:
                def __call__(self, source, target, kind, flags, data):
                    events.append(("mount", target, flags))
                    return 0
            mount = Mount()
        with patch.object(ctypes, "CDLL", return_value=LibC()), patch.dict(os.environ, SNAP="/snap/microceph/current"), patch.object(self.native, "_prepare_ctdb_mountpoint", side_effect=lambda p: events.append(("prepare", str(p)))):
            self.native.enter_launch_context("ctdbd", Path("/native/alpha"), {"clustered": True})
        self.assertEqual(events[0], ("unshare", 0x00020000))
        self.assertEqual(events[1], ("mount", b"/", (1 << 14) | (1 << 19)))
        index = events.index(("prepare", "/run/ctdb"))
        self.assertEqual(events[index + 1], ("mount", b"/run/ctdb", 4096))

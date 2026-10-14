"""Recipe-level closure checks; these are not installed-package qualification."""
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]


class PackagingTests(unittest.TestCase):
    def test_fixed_disabled_apps_and_narrow_permissions(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        self.assertIn("snapd2.78", recipe["assumes"])
        for role in ("smbd", "ctdbd", "ctdb-nodes", "winbindd", "cephfs-proxy", "smb-remote", "smbmetrics"):
            app = recipe["apps"][role]
            self.assertEqual(app["install-mode"], "disable")
            wrapper = ROOT / "snapcraft" / app["command"]
            self.assertTrue(wrapper.exists())
            self.assertIn("launch", wrapper.read_text())
            self.assertIn(role, wrapper.read_text())
            self.assertNotIn("snapctl", wrapper.read_text())
            self.assertNotIn("smb-identity", app["plugs"] if role in ("ctdbd", "ctdb-nodes") else [])
        self.assertNotIn("firewall-control", recipe["plugs"])
        self.assertEqual(recipe["plugs"]["smb-identity"]["user-identity-switching"], True)
        self.assertIn("dirty worktree detected", recipe["parts"]["microceph"]["override-build"])

    def test_pinned_consumer_and_native_provider_closure(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        packages = recipe["parts"]["native-smb"]["stage-packages"]
        for package in ("samba", "samba-vfs-ceph", "ctdb", "winbind", "libnss-winbind", "python3-samba", "libcephfs-proxy2", "libcephfs-daemon", "python3-cephfs"):
            self.assertTrue(any(p.split("=")[0] == package for p in packages), package)
        self.assertIn("--require-hashes", recipe["parts"]["sambacc"]["override-build"])
        self.assertEqual(recipe["parts"]["smbmetrics"]["source-commit"], "172d34cabea2f72815de022e17e1c31135942c85")
        self.assertIn("smbmetrics-native.patch", recipe["parts"]["smbmetrics"]["override-build"])


if __name__ == "__main__":
    unittest.main()

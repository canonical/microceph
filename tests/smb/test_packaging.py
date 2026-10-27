"""PR1's direct-only package/startup contract (not an installed-snap proof)."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]


class PackagingTests(unittest.TestCase):
    def setUp(self):
        self.recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())

    def test_only_smbd_is_declared(self):
        apps = self.recipe["apps"]
        self.assertEqual(apps["smbd"]["command"], "commands/smbd.start")
        self.assertEqual(apps["smbd"]["install-mode"], "disable")
        self.assertEqual(apps["smbd"]["stop-mode"], "sigterm-all")
        self.assertNotIn("after", apps["smbd"], "prepared smbd must survive daemon restart independently")
        for role in ("ctdbd", "ctdb", "winbindd", "smbmetrics", "cephfs-proxy"):
            self.assertNotIn(role, apps)
            self.assertFalse((ROOT / "snapcraft/commands" / (role + ".start")).exists())

    def test_direct_dependency_closure(self):
        native = self.recipe["parts"]["native-smb"]
        packages = native["stage-packages"]
        self.assertIn("samba=2:4.23.6+dfsg-1ubuntu2.2", packages)
        self.assertIn("samba-vfs-ceph=2:4.23.6+dfsg-1ubuntu2.2", packages)
        self.assertTrue(any(package.startswith("python3-cephfs=") for package in packages))
        for package in packages:
            self.assertFalse(package.split("=")[0] in ("ctdb", "winbind", "libnss-winbind", "libcephfs-daemon", "libcephfs-proxy2"))
        for executable in ("smbd", "smbcontrol", "testparm"):
            self.assertIn("bin/" + executable, native["prime"])
        self.assertIn("--require-hashes", self.recipe["parts"]["sambacc"]["override-build"])
        self.assertIn("sambacc==0.9", (ROOT / "snapcraft/smb/requirements.txt").read_text())
        self.assertNotIn("smbmetrics", self.recipe["parts"])

    def test_mount_grants_are_only_private_nss(self):
        for name in ("smb-mounts", "smb-control-mounts"):
            mounts = self.recipe["plugs"][name]["mount"]
            self.assertEqual({mount["where"] for mount in mounts}, {"/etc/passwd", "/etc/group", "/etc/nsswitch.conf"})
            self.assertTrue(all(mount["options"] == ["bind"] for mount in mounts))
        self.assertTrue(self.recipe["plugs"]["smb-identity"]["user-identity-switching"])
        for name in ("ctdb-run", "winbind-run", "ctdb-socket", "smb-control-access"):
            self.assertNotIn(name, self.recipe["plugs"])


@unittest.skipUnless(shutil.which("snap"), "requires the real snap metadata validator")
class SkeletonValidationTests(unittest.TestCase):
    def validate(self, recipe, *, command_stubs=True):
        keys = ("name", "base", "grade", "summary", "description", "license", "confinement", "assumes", "plugs", "slots", "environment", "layout", "apps")
        metadata = {key: recipe[key] for key in keys if key in recipe}
        metadata["version"] = "0-test"
        with tempfile.TemporaryDirectory(prefix="smb-skeleton-") as directory:
            skeleton = Path(directory) / "prime"
            skeleton.mkdir(mode=0o755)
            (skeleton / "meta").mkdir()
            (skeleton / "meta/snap.yaml").write_text(yaml.safe_dump(metadata))
            for layout in metadata["layout"].values():
                source = layout.get("bind", "")
                if source.startswith("$SNAP/"):
                    (skeleton / source.removeprefix("$SNAP/")).mkdir(parents=True, exist_ok=True)
            if command_stubs:
                for app in recipe["apps"].values():
                    for key in ("command", "reload-command", "stop-command"):
                        if key in app:
                            command = skeleton / app[key].split()[0]
                            command.parent.mkdir(parents=True, exist_ok=True)
                            command.write_text("#!/bin/sh\nexit 0\n")
                            command.chmod(0o755)
            return subprocess.run(["snap", "pack", "--check-skeleton", str(skeleton)], capture_output=True, text=True, timeout=30)

    def recipe(self):
        return yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text().replace("$CRAFT_ARCH_TRIPLET_BUILD_FOR", "x86_64-linux-gnu"))

    def test_actual_recipe_passes_real_metadata_validator(self):
        result = self.validate(self.recipe())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_real_validator_rejects_forbidden_runtime_layout(self):
        recipe = self.recipe()
        recipe["layout"]["/run/smb"] = {"type": "tmpfs"}
        result = self.validate(recipe)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('defines a new top-level directory "/run"', result.stdout + result.stderr)

    def test_missing_commands_are_reported_as_skeleton_diagnostics(self):
        result = self.validate(self.recipe(), command_stubs=False)
        self.assertIn("commands/", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

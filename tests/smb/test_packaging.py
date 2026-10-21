"""Recipe-level closure checks; these are not installed-package qualification."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
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
            self.assertIn(f'exec "$SNAP/bin/microceph-smb-runtime" {role} "$SNAP_COMMON/smb"', wrapper.read_text())
            self.assertNotIn("native.py", wrapper.read_text())
            self.assertIn(role, wrapper.read_text())
            self.assertNotIn("snapctl", wrapper.read_text())
            self.assertNotIn("smb-identity", app["plugs"] if role in ("ctdbd", "ctdb-nodes") else [])
        self.assertNotIn("firewall-control", recipe["plugs"])
        self.assertEqual(recipe["plugs"]["smb-identity"]["user-identity-switching"], True)
        self.assertIn("dirty worktree detected", recipe["parts"]["microceph"]["override-build"])
        part = recipe["parts"]["microceph"]
        self.assertIn('./cmd/microceph-smb-runtime', part["override-build"])
        self.assertIn('strip -s "${CRAFT_PART_INSTALL}/bin/microceph-smb-runtime"', part["override-build"])
        self.assertIn("bin/microceph-smb-runtime", part["prime"])
        self.assertIn("./cmd/microceph-smb-runtime", (ROOT / "microceph/Makefile").read_text())
        self.assertNotIn("microceph-smb-runtime", recipe["apps"])

    def test_smb_boot_retries_do_not_exhaust_the_default_start_limit(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        for role in ("smbd", "winbindd"):
            self.assertEqual(recipe["apps"][role].get("restart-delay"), "10s")
        self.assertEqual(recipe["apps"]["smbd"].get("restart-condition", "on-failure"), "on-failure")

    def test_ctdb_socket_grant_is_exact_and_only_for_daemon_and_metrics(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        self.assertEqual(recipe["plugs"].get("ctdb-socket"), {
            "interface": "system-files", "write": ["/run/ctdb/ctdbd.socket"],
        })
        consumers = {name for name, app in recipe["apps"].items()
                     if "ctdb-socket" in app.get("plugs", [])}
        self.assertEqual(consumers, {"daemon", "smbmetrics"})
        for name in consumers:
            self.assertNotIn("ctdb-run", recipe["apps"][name]["plugs"])

    def test_ctdb_runtime_bind_rules_and_pinned_event_resources(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        self.assertNotIn("/run/ctdb", recipe["layout"])
        self.assertNotIn("/var/run/ctdb", recipe["layout"])
        self.assertEqual(recipe["plugs"]["ctdb-run"], {"interface": "system-files", "write": ["/run/ctdb"]})
        consumers = {name for name, app in recipe["apps"].items() if "ctdb-run" in app.get("plugs", [])}
        self.assertEqual(consumers, {"ctdbd", "ctdb-nodes", "ctdb", "smbd", "winbindd"})
        for name in consumers:
            self.assertIn("smb-mounts", recipe["apps"][name]["plugs"])
            self.assertIn("network-control", recipe["apps"][name]["plugs"])
        expected = {
            "/etc/resolv.conf": "config/resolv.conf", "/run/systemd/resolve/stub-resolv.conf": "config/resolv.conf",
            "/etc/passwd": "config/passwd", "/etc/group": "config/group", "/etc/nsswitch.conf": "config/nsswitch.conf",
            "/etc/ctdb": "ctdb{,/}", "/run/ctdb": "ctdb/run{,/}", "/var/lib/ctdb": "ctdb{,/}",
            "/run/samba/winbindd": "samba/winbindd{,/}",
        }
        rules = recipe["plugs"]["smb-mounts"]["mount"]
        self.assertEqual(len(rules), len(expected))
        for rule in rules:
            self.assertEqual(rule["what"], "/var/snap/microceph/common/smb/*/" + expected[rule["where"]])
            self.assertIn(set(rule["options"]), ({"bind"}, {"rw", "bind"}))
            self.assertNotIn("type", rule)
            self.assertNotIn("persistent", rule)
        part = recipe["parts"]["native-smb"]
        self.assertIn("libexec/ctdb/**", part["prime"])
        self.assertIn("share/ctdb/**", part["prime"])
        for name in ("functions", "notify.sh"):
            self.assertEqual(part["organize"]["etc/ctdb/" + name], "share/ctdb/" + name)
        self.assertEqual(recipe["layout"]["/usr/libexec/ctdb"]["symlink"], "$SNAP/libexec/ctdb")
        for target in ("/etc/ctdb", "/var/lib/ctdb"):
            self.assertEqual(recipe["layout"][target], {"type": "tmpfs"})
        control = (ROOT / "snapcraft/commands/ctdb.start").read_text()
        self.assertIn('microceph-smb-runtime" ctdb-control', control)

    def test_ad_socket_binding_is_app_scoped_and_public_only(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        self.assertEqual(recipe["plugs"].get("winbind-run"), {
            "interface": "system-files", "write": ["/run/samba"],
        })
        self.assertEqual({name for name, app in recipe["apps"].items()
                          if "winbind-run" in app.get("plugs", [])}, {"smbd", "winbindd"})
        self.assertIn("ctdb-run", recipe["apps"]["winbindd"]["plugs"])
        rule = {"what": "/var/snap/microceph/common/smb/*/samba/winbindd{,/}",
                "where": "/run/samba/winbindd", "options": ["bind"]}
        self.assertIn(rule, recipe["plugs"]["smb-mounts"]["mount"])
        self.assertNotIn("/run/samba", recipe["layout"])
        self.assertNotIn("/run/samba/winbindd", recipe["layout"])

    def test_pinned_consumer_and_native_provider_closure(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        packages = recipe["parts"]["native-smb"]["stage-packages"]
        for package in ("samba", "samba-vfs-ceph", "ctdb", "winbind", "libnss-winbind", "python3-samba", "libcephfs-proxy2", "libcephfs-daemon", "python3-cephfs"):
            self.assertTrue(any(p.split("=")[0] == package for p in packages), package)
        self.assertIn("--require-hashes", recipe["parts"]["sambacc"]["override-build"])
        self.assertEqual(recipe["parts"]["smbmetrics"]["source-commit"], "172d34cabea2f72815de022e17e1c31135942c85")
        self.assertIn("smbmetrics-native.patch", recipe["parts"]["smbmetrics"]["override-build"])
        self.assertIn("native-smb", recipe["parts"]["strip"]["after"])

    @unittest.skipUnless(all(shutil.which(cmd) for cmd in ("cc", "readelf", "strip")),
                         "requires a native compiler and binutils")
    def test_strip_preserves_data_and_strips_elf(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        with tempfile.TemporaryDirectory(prefix="smb prime ") as directory:
            prime = Path(directory)
            library = prime / "lib/native library.so"
            library.parent.mkdir()
            source = prime / "fixture.c"
            source.write_text("int native_fixture(void) { return 42; }\n")
            subprocess.run(["cc", "-g", "-shared", "-fPIC", str(source), "-o", str(library)], check=True)
            before = subprocess.check_output(["readelf", "-S", str(library)], text=True)
            self.assertIn(".debug_info", before)
            payloads = {
                "lib/systemd/system/smbd.service": b"[Service]\nExecStart=/usr/sbin/smbd\n",
                "lib/x86_64-linux-gnu/perl-base/Exporter.pm": b"package Exporter;\n1;\n",
                "lib/binfmt.d/python3.14.conf": b"python metadata\n",
                "lib/valgrind/python3.supp": b"suppression data\n",
                "lib/python3.14/site-packages/untouched.so": b"\x7fELFintentionally excluded Python fixture",
            }
            for name, content in payloads.items():
                path = prime / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(content)
            result = subprocess.run(["bash", "-ec", recipe["parts"]["strip"]["override-prime"]],
                                    env={**os.environ, "CRAFT_PRIME": str(prime)}, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            for name, content in payloads.items():
                self.assertEqual((prime / name).read_bytes(), content, name)
            after = subprocess.check_output(["readelf", "-S", str(library)], text=True)
            self.assertNotIn(".debug_info", after)
            symbols = subprocess.check_output(["readelf", "--dyn-syms", str(library)], text=True)
            self.assertIn("native_fixture", symbols)

    @unittest.skipUnless(shutil.which("strip"), "requires binutils")
    def test_strip_failure_for_elf_remains_fatal(self):
        recipe = yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text())
        with tempfile.TemporaryDirectory() as directory:
            prime = Path(directory)
            (prime / "lib").mkdir()
            (prime / "lib/broken.so").write_bytes(b"\x7fELFnot a valid ELF object")
            (prime / "lib/metadata.txt").write_text("later non-ELF data must not mask failure\n")
            result = subprocess.run(["bash", "-ec", recipe["parts"]["strip"]["override-prime"]],
                                    env={**os.environ, "CRAFT_PRIME": str(prime)}, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)


@unittest.skipUnless(shutil.which("snap"), "requires the real snap metadata validator")
class SkeletonValidationTests(unittest.TestCase):
    def validate(self, recipe, *, command_stubs=True):
        """Project actual metadata; only normal build expansion/test version differ."""
        keys = ("name", "base", "grade", "summary", "description", "license",
                "confinement", "assumes", "plugs", "slots", "environment", "layout", "apps")
        metadata = {key: recipe[key] for key in keys if key in recipe}
        metadata["version"] = "0-test"
        with tempfile.TemporaryDirectory(prefix="ctdb-skeleton-") as directory:
            skeleton = Path(directory) / "prime"
            skeleton.mkdir(mode=0o755)
            (skeleton / "meta").mkdir()
            (skeleton / "meta/snap.yaml").write_text(yaml.safe_dump(metadata))
            # Normal prime directories required by snap's bind-source checks.
            for layout in metadata["layout"].values():
                source = layout.get("bind", "")
                if source.startswith("$SNAP/"):
                    (skeleton / source.removeprefix("$SNAP/")).mkdir(parents=True, exist_ok=True)
            if command_stubs:
                # Stubs satisfy skeleton file checks, not installed-binary qualification.
                for app in recipe["apps"].values():
                    for key in ("command", "reload-command", "stop-command"):
                        if key in app:
                            command = skeleton / app[key].split()[0]
                            command.parent.mkdir(parents=True, exist_ok=True)
                            command.write_text("#!/bin/sh\nexit 0\n")
                            command.chmod(0o755)
            return subprocess.run(["snap", "pack", "--check-skeleton", str(skeleton)],
                                  capture_output=True, text=True, timeout=30)

    def recipe(self):
        return yaml.safe_load((ROOT / "snap/snapcraft.yaml").read_text().replace(
            "$CRAFT_ARCH_TRIPLET_BUILD_FOR", "x86_64-linux-gnu"))

    def test_actual_recipe_passes_real_metadata_validator(self):
        result = self.validate(self.recipe())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_real_validator_rejects_forbidden_runtime_layout(self):
        recipe = self.recipe()
        recipe["layout"]["/run/ctdb"] = {"type": "tmpfs"}
        result = self.validate(recipe)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('layout "/run/ctdb" defines a new top-level directory "/run"', result.stdout + result.stderr)

    def test_missing_commands_are_reported_as_skeleton_diagnostics(self):
        result = self.validate(self.recipe(), command_stubs=False)
        self.assertIn("commands/", result.stdout + result.stderr)
        self.assertNotIn('layout "/run/ctdb"', result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

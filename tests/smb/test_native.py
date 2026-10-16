"""Consumer contracts: run with the pinned Sambacc 0.9 on PYTHONPATH."""
import configparser
import copy
import importlib.util
import io
import tempfile
import json
import os
import subprocess
from pathlib import Path
import unittest
from unittest.mock import patch

from sambacc.config import GlobalConfig
from sambacc.netcmd_loader import template_config

MODULE = Path(__file__).resolve().parents[2] / "snapcraft/smb/native.py"


class NativeConsumerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("native", MODULE)
        cls.native = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.native)

    def config(self):
        return {
            "samba-container-config": "v0",
            "configs": {"alpha": {"instance_name": "alpha", "globals": ["default"], "shares": []}},
            "globals": {"default": {"options": {"load printers": "No"}}},
            "shares": {},
        }

    def test_sources_combine_in_order_and_preserve_fields(self):
        sources = [
            {"users": {"all_entries": [{"name": "alice", "password": "one", "uid": 2345, "gid": 3000}]}},
            {"users": {"all_entries": [{"name": "bob", "nt_hash": "ab" * 16, "extra": "retained"}]},
             "groups": {"all_entries": [{"name": "team", "gid": 3000, "ou": "retained"}]}},
        ]
        before = copy.deepcopy(sources)
        merged = self.native.combine_sources(self.config(), sources)
        self.assertEqual(merged["users"]["all_entries"], sources[0]["users"]["all_entries"] + sources[1]["users"]["all_entries"])
        self.assertEqual(sources, before)
        instance = GlobalConfig(initial_data=merged).get("alpha")
        users = list(instance.users())
        self.assertEqual([(u.uid, u.gid) for u in users], [(2345, 3000), (1001, 1001)])
        self.assertEqual(users[1].nt_passwd, bytes.fromhex("ab" * 16))
        self.assertEqual(list(instance.groups())[0].groupname, "team")

    def test_duplicate_users_and_groups_are_distinct_errors(self):
        for kind in ("users", "groups"):
            sources = [{kind: {"all_entries": [{"name": "duplicate"}]}}] * 2
            with self.subTest(kind=kind), self.assertRaisesRegex(ValueError, "duplicate " + kind):
                self.native.combine_sources(self.config(), sources)

    def test_shareless_uses_pinned_renderer_and_private_native_paths(self):
        result = self.native.render(self.config(), "alpha", "/var/snap/microceph/common/smb/alpha", {}, [])
        self.assertIn("[global]", result)
        self.assertIn("private dir = /var/snap/microceph/common/smb/alpha/samba/private", result)
        self.assertNotIn("config backend = registry", result)
        self.assertNotIn("client.admin", result)
        self.assertNotIn("[homes]", result)

    def test_provider_aliases_do_not_duplicate_generation(self):
        doc = self.config()
        doc["configs"]["alpha"]["shares"] = ["direct", "proxy"]
        for name, mode in (("direct", "no"), ("proxy", "yes")):
            doc["shares"][name] = {"options": {
                "path": "/", "vfs objects": "ceph_new", "ceph_new:proxy": mode,
                "ceph_new:config_file": "/etc/ceph/ceph.conf", "ceph_new:user_id": "smb.fs.cluster.alpha",
            }}
        result = self.native.render(doc, "alpha", "/native/alpha", {}, [])
        self.assertIn("ceph_new:config_file = /native/alpha/config/ceph-direct.conf", result)
        self.assertIn("ceph_new:config_file = /native/alpha/config/ceph-proxy.conf", result)
        self.assertIn("ceph_new:proxy = yes", result)
        self.assertEqual(doc["shares"]["proxy"]["options"]["ceph_new:config_file"], "/etc/ceph/ceph.conf")

    def test_conflicting_custom_paths_rejected(self):
        doc = self.config()
        doc["globals"]["default"]["options"]["PRIVATE DIR"] = "/unrelated"
        with self.assertRaisesRegex(ValueError, "native option conflict"):
            self.native.render(doc, "alpha", "/native/alpha", {}, [])

    def test_public_addresses_rejected_in_effective_documents(self):
        for field in ("public_addrs", "cluster_public_addrs"):
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "separate HA/LB"):
                self.native.reject_public_addresses({field: [{"address": "192.0.2.1"}]})
        doc = self.config()
        doc["ctdb"] = {"public_addresses": [{"address": "192.0.2.1"}]}
        with self.assertRaisesRegex(ValueError, "separate HA/LB"):
            self.native.render(doc, "alpha", "/native/alpha", {}, [])
        self.native.reject_public_addresses({"public_addrs": []})

    def test_compile_core_uses_effective_providers_and_complete_nss(self):
        doc = self.config()
        doc["configs"]["alpha"]["shares"] = ["data"]
        doc["shares"]["data"] = {"options": {"path": "/data", "vfs objects": "ceph_new", "ceph_new:proxy": "yes", "ceph_new:filesystem": "fs", "ceph_new:config_file": "/etc/ceph/ceph.conf", "ceph_new:user_id": "smb.fs.cluster.alpha"}}
        candidate = {"config": doc, "spec": {"cluster_id": "alpha"}, "users": [{"users": {"all_entries": [{"name": "alice", "password": "one", "uid": 2345, "gid": 3000}]}}, {"users": {"all_entries": [{"name": "bob", "password": "two"}]}}]}
        files, context = self.native.compile_core(candidate, "/native/alpha")
        self.assertTrue(context["proxy"])
        self.assertIn("alice:x:2345:3000:", files["passwd"])
        self.assertIn("bob:x:1001:1001:", files["passwd"])
        self.assertNotIn(":one:", files["passwd"])
        self.assertEqual(context["data_checks"], [{"filesystem": "fs", "path": "/data", "user": "smb.fs.cluster.alpha"}])
        self.assertEqual(self.native.role_command("cephfs-proxy", "/native/alpha", context, "/snap/microceph/current")[0], "/snap/microceph/current/bin/libcephfsd")
        self.assertIn("/native/alpha/config/smb.conf", self.native.role_command("smbd", "/native/alpha", context, "/snap/microceph/current"))
        with self.assertRaises(ValueError):
            self.native.role_command("/bin/sh", "/native/alpha", context, "/snap/microceph/current")

    def test_launch_context_uses_slave_and_only_private_binds(self):
        operations = self.native.launch_mounts("smbd", "/native/alpha", {"custom_dns": True})
        self.assertEqual(operations[0], (None, "/", (1 << 14) | (1 << 19)))
        self.assertIn(("/native/alpha/config/passwd", "/etc/passwd", 4096), operations)
        self.assertIn(("/native/alpha/config/resolv.conf", "/etc/resolv.conf", 4096), operations)
        self.assertNotIn((None, "/", (1 << 14) | (1 << 18)), operations)

    def test_publish_private_files_and_launch_stop_guard(self):
        with tempfile.TemporaryDirectory() as path:
            root = Path(path)
            (root / "config/next").mkdir(parents=True)
            (root / "config/next/smb.conf").write_text("[global]\n")
            self.native.publish(root)
            self.assertEqual((root / "config/smb.conf").read_text(), "[global]\n")
            self.assertEqual((root / "config/smb.conf").stat().st_mode & 0o777, 0o600)
            (root / "stop-requested").write_text("stop")
            with self.assertRaisesRegex(ValueError, "stop requested"):
                self.native.load_launch(root)

    def test_native_ceph_config_has_only_stable_data_key(self):
        result = self.native.data_config("[global]\nmon host = 192.0.2.1\n[client]\nkeyring = /etc/ceph/ceph.keyring\n[client.admin]\nkeyring = /admin\n", "alpha", "/native/alpha")
        self.assertNotIn("client.admin", result)
        self.assertNotIn("/admin", result)
        self.assertIn("/native/alpha/identity/data.keyring", result)
        self.assertIn("192.0.2.1", result)

    def test_active_owner_resolves_only_own_cluster_directory(self):
        with tempfile.TemporaryDirectory() as path:
            base = Path(path)
            (base / "owner.json").write_text("alpha")
            self.assertEqual(self.native.active_root(base), base / "alpha")
            (base / "owner.json").write_text("../unrelated")
            with self.assertRaises(ValueError):
                self.native.active_root(base)

    def test_explicit_zero_ids_are_not_omitted(self):
        candidate = {"config": self.config(), "spec": {"cluster_id": "alpha"}, "users": [{"users": {"all_entries": [{"name": "alice", "password": "pass", "uid": 0, "gid": 0}]}}]}
        files, _ = self.native.compile_core(candidate, "/native/alpha")
        self.assertIn("alice:x:0:0:", files["passwd"])

    @unittest.skipUnless(os.environ.get("SMB_NATIVE_PACKAGE_TESTS"), "requires the exact extracted Samba package")
    def test_real_package_sam_password_update_and_removal_keep_sid(self):
        from sambacc.passdb_loader import PassDBLoader
        with tempfile.TemporaryDirectory() as path:
            root = Path(path)
            for directory in ("config", "samba/private", "samba/state", "samba/cache", "samba/lock", "samba/run", "samba/ncalrpc", "diagnostics"):
                (root / directory).mkdir(parents=True, exist_ok=True)
            candidate = {"config": self.config(), "spec": {"cluster_id": "alpha"}, "users": [{"users": {"all_entries": [{"name": "nobody", "nt_hash": "ab" * 16, "uid": 65534, "gid": 65534}]}}]}
            (root / "config/smb.conf").write_text(self.native.render(candidate["config"], "alpha", str(root), {}, candidate["users"]))
            (root / "config/candidate.json").write_text(json.dumps(candidate))
            self.native.prepare_local_accounts(root)
            loader = PassDBLoader(str(root / "config/smb.conf"))
            initial_sid = str(loader._pdb.getsampwnam("nobody").user_sid)
            candidate["users"][0]["users"]["all_entries"][0]["nt_hash"] = "cd" * 16
            (root / "config/candidate.json").write_text(json.dumps(candidate))
            self.native.prepare_local_accounts(root)
            loader = PassDBLoader(str(root / "config/smb.conf"))
            self.assertEqual(str(loader._pdb.getsampwnam("nobody").user_sid), initial_sid)
            self.assertEqual(loader._pdb.getsampwnam("nobody").nt_passwd, bytes.fromhex("cd" * 16))
            candidate["users"] = []
            (root / "config/candidate.json").write_text(json.dumps(candidate))
            self.native.prepare_local_accounts(root)
            self.assertEqual(loader._pdb.search_users(0x10), [])

    def test_effective_override_cannot_switch_backend_or_unprepared_mode(self):
        for key, value in (("config backend", "registry"), ("PRIVATE   DIR", "/unrelated")):
            doc = self.config()
            doc["globals"]["default"]["options"][key] = value
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, "native option conflict"):
                self.native.render(doc, "alpha", "/native/alpha", {}, [])
        for features in (["ctdb"], ["addc"]):
            doc = self.config()
            doc["configs"]["alpha"]["instance_features"] = features
            with self.assertRaises(ValueError):
                self.native.compile_core({"config": doc, "spec": {"cluster_id": "alpha"}}, "/native/alpha")

    def test_case_alias_cannot_defeat_provider_config_isolation(self):
        doc = self.config()
        doc["configs"]["alpha"]["shares"] = ["data"]
        doc["shares"]["data"] = {"options": {"vfs objects": "ceph_new", "ceph_new:proxy": "yes", "ceph_new:config_file": "/etc/ceph/ceph.conf", "CEPH_NEW:CONFIG_FILE": "/unrelated"}}
        with self.assertRaisesRegex(ValueError, "native option conflict"):
            self.native.render(doc, "alpha", "/native/alpha", {}, [])

    def test_go_null_optionals_keep_shareless_port_defaults(self):
        files, context = self.native.compile_core({"config": self.config(), "spec": {"cluster_id": "alpha", "custom_ports": None, "custom_dns": None}, "users": None}, "/native/alpha")
        self.assertEqual(context["smb_port"], 445)
        self.assertEqual(context["metrics_port"], 9922)
        self.assertFalse(context["proxy"])
        self.assertIn("smb ports = 445", files["smb.conf"])
        endpoint = {"frontend_address": "198.51.100.7", "smb_port": 1445, "metrics_port": 19922}
        candidate = {"config": self.config(), "spec": {"cluster_id": "alpha"}, "endpoint": endpoint}
        files, context = self.native.compile_core(candidate, "/native/alpha")
        parsed = configparser.ConfigParser(interpolation=None, strict=False, delimiters=("=",))
        parsed.read_string(files["smb.conf"])
        self.assertEqual(parsed["global"]["interfaces"], endpoint["frontend_address"])
        self.assertEqual(parsed["global"]["bind interfaces only"], "yes")
        self.assertEqual(parsed["global"]["smb ports"], "1445")
        self.assertEqual(self.native.role_command("smbmetrics", "/native/alpha", context, "/snap"),
                         ["/snap/bin/smbmetrics", "--address", "0.0.0.0", "--port", "19922"])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "config").mkdir()
            (root / "config/context.json").write_text(json.dumps(context))
            with patch("urllib.request.urlopen") as request:
                request.return_value.__enter__.return_value.status = 200
                self.native.ready(root, "smbmetrics")
                request.assert_called_once_with("http://127.0.0.1:19922/metrics", timeout=3)
        for key, value in (("interfaces", "192.0.2.1"), ("bind interfaces only", "no"), ("smb ports", "445")):
            doc = self.config()
            doc["globals"]["default"]["options"][key] = value
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, "native option conflict"):
                self.native.compile_core(dict(candidate, config=doc), "/native/alpha")

    def test_generated_user_share_options_survive_native_adapter(self):
        # Pinned mgr/smb handler._generate_config/_generate_share output shape.
        # This tests passthrough, not a second resource validator/share generator.
        doc = self.config()
        doc["globals"]["default"]["options"]["server string"] = "User gateway"
        doc["configs"]["alpha"]["shares"] = ["Friendly Data"]
        options = {"path": "/resolved/subvolume/data", "vfs objects": "acl_xattr ceph_snapshots ceph_new",
                   "ceph_new:proxy": "yes", "ceph_new:config_file": "/etc/ceph/ceph.conf",
                   "ceph_new:filesystem": "fs", "ceph_new:user_id": "smb.fs.cluster.alpha",
                   "read only": "yes", "browseable": "no", "comment": "Team archive",
                   "max connections": "12", "valid users": "alice @team", "read list": "alice",
                   "write list": "@team", "invalid users": "blocked", "admin users": "admin",
                   "create mask": "0640", "x:ceph:id": "alpha.data"}
        doc["shares"]["Friendly Data"] = {"options": options}
        candidate = {"config": doc, "spec": {"cluster_id": "alpha"}, "users": [
            {"users": {"all_entries": [{"name": "alice", "password": "secret", "gid": 3000}]}},
            {"groups": {"all_entries": [{"name": "team", "gid": 3000}]}}]}
        files, context = self.native.compile_core(candidate, "/native/alpha")
        parsed = configparser.ConfigParser(interpolation=None, strict=False, delimiters=("=",))
        parsed.read_string(files["smb.conf"])
        expected = dict(options, **{"ceph_new:config_file": "/native/alpha/config/ceph-proxy.conf"})
        for key, value in expected.items():
            self.assertEqual(parsed["Friendly Data"][key], value)
        self.assertEqual(parsed["global"]["server string"], "User gateway")
        self.assertIn("alice:x:1000:3000:", files["passwd"])
        self.assertIn("team:x:3000:", files["group"])
        self.assertTrue(context["proxy"])
        self.assertNotIn("secret", files["smb.conf"])

    @unittest.skipUnless(os.environ.get("SMB_NATIVE_PACKAGE_TESTS"), "requires the exact extracted native packages")
    def test_real_package_text_config_and_cephfs_constructor(self):
        import cephfs
        with tempfile.TemporaryDirectory() as path:
            root = Path(path)
            for directory in ("samba/private", "samba/state", "samba/cache", "samba/lock", "samba/run", "samba/ncalrpc", "diagnostics"):
                (root / directory).mkdir(parents=True, exist_ok=True)
            conf = root / "smb.conf"
            conf.write_text(self.native.render(self.config(), "alpha", str(root), {}, []))
            subprocess.run([os.environ["SMB_NATIVE_TESTPARM"], "--suppress-prompt", str(conf)], check=True, stdout=subprocess.DEVNULL)
            ceph_conf = root / "ceph.conf"
            ceph_conf.write_text(self.native.data_config("[global]\nmon host = 127.0.0.1\n", "alpha", str(root)))
            fs = cephfs.LibCephFS(conffile=str(ceph_conf), auth_id="smb.fs.cluster.alpha")
            try:
                fs.conf_set("client_fs", "contract-test")
                self.assertEqual(fs.conf_get("keyring"), str(root / "identity/data.keyring"))
            finally:
                fs.shutdown()

    def test_launch_probe_covers_supported_operations_only(self):
        script = (MODULE.parents[1].parent / "tests/scripts/test_smb_native_permissions.sh").read_text()
        self.assertIn("MS_REC | MS_SLAVE", script)
        self.assertIn("setgroups(2,", script)
        self.assertNotIn("NETLINK_NETFILTER", script)
        self.assertNotIn("probe netfilter", script)


if __name__ == "__main__":
    unittest.main()

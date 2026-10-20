"""Only pinned upstream binding contracts; native policy belongs to Go."""
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest
from unittest.mock import Mock, patch

from sambacc.config import GlobalConfig
from sambacc.netcmd_loader import template_config


class NativeBridgeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        path = Path(__file__).resolve().parents[2] / "snapcraft/smb/native.py"
        spec = importlib.util.spec_from_file_location("native_bridge", path)
        cls.native = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.native)

    def request(self):
        return {"cluster_id": "alpha", "config": {
            "samba-container-config": "v0",
            "configs": {"alpha": {"instance_name": "alpha", "globals": ["native"], "shares": ["data"]}},
            "globals": {"native": {"options": {"config backend": "file", "private dir": "/native/alpha/samba/private"}}},
            "shares": {"data": {"options": {"path": "/data", "read only": "yes", "comment": "literal {{.value}}"}}},
            "users": {"all_entries": [{"name": "alice", "password": "private-password", "uid": 2345, "gid": 3000}, {"name": "bob", "nt_hash": "ab" * 16}]},
            "groups": {"all_entries": [{"name": "team", "gid": 3000}]},
        }}

    def call(self, command, request):
        self.assertTrue(callable(getattr(self.native, "bridge", None)), "missing thin upstream-binding entry point")
        return self.native.bridge(command, request)

    def test_inspection_keeps_upstream_identity_defaults_without_secret_output(self):
        result = self.call("inspect", self.request())
        self.assertEqual(result["users"][0]["fields"][2:4], ["2345", "3000"])
        self.assertEqual(result["users"][1]["fields"][2:4], ["1001", "1001"])
        self.assertTrue(all(user["has_password"] for user in result["users"]))
        self.assertIn({"name": "team", "fields": ["team", "x", "3000", ""]}, result["groups"])
        self.assertNotIn("private-password", repr(result))
        self.assertNotIn("abababab", repr(result))
        self.assertFalse(result["clustered"])
        self.assertFalse(result["domain_controller"])

    def test_member_is_not_a_controller_and_cannot_import_sam(self):
        request = self.request()
        request["config"]["globals"]["native"]["options"].update({"security": "ads", "realm": "EXAMPLE.TEST"})
        result = self.call("inspect", request)
        self.assertFalse(result["domain_controller"])
        with self.assertRaisesRegex(ValueError, "member.*SAM"):
            self.call("accounts", request)
        request["config"]["configs"]["alpha"]["instance_features"] = ["addc"]
        self.assertTrue(self.call("inspect", request)["domain_controller"])

    def test_join_uses_actual_pinned_joiner_stdin_and_stops_on_uncertainty(self):
        # Patch only Popen: execute the real pinned Joiner and command builder.
        request = dict(self.request(), config_path="/private/alpha/config/smb.conf",
                       joins=[{"username": "first", "password": "first-private"},
                              {"username": "second", "password": "second-private"}])
        process = Mock()
        process.wait.return_value = 0
        with patch("sambacc.join.subprocess.Popen", return_value=process) as popen:
            self.assertEqual(self.call("join", request), {})
        argv = popen.call_args.args[0]
        self.assertIn("/private/alpha/config/smb.conf", argv)
        self.assertIn("--no-dns-updates", argv)
        self.assertIn("first", argv)
        self.assertNotIn("first-private", repr(argv))
        self.assertNotIn("second", repr(argv))
        self.assertEqual(popen.call_args.kwargs["stdin"], subprocess.PIPE)
        self.assertEqual(process.stdin.write.call_args_list[0].args, (b"first-private",))
        process.wait.return_value = 1
        with patch("sambacc.join.subprocess.Popen", return_value=process) as popen:
            with self.assertRaises(Exception):
                self.call("join", request)
        self.assertEqual(popen.call_count, 1, "nonzero upstream exit is not proof of no mutation")

    def test_render_is_the_pinned_renderer_and_does_not_publish_files(self):
        request = self.request()
        with tempfile.TemporaryDirectory() as directory:
            request["config"]["globals"]["native"]["options"]["private dir"] = directory + "/private"
            expected = io.StringIO()
            template_config(expected, GlobalConfig(initial_data=request["config"]).get("alpha"))
            result = self.call("render", request)
            self.assertEqual(result["smb_conf"], expected.getvalue())
            self.assertIn("literal {{.value}}", result["smb_conf"])
            self.assertEqual(list(Path(directory).iterdir()), [])

    def test_ctdb_description_uses_pinned_defaults(self):
        request = self.request()
        request["config"]["configs"]["alpha"]["instance_features"] = ["ctdb"]
        result = self.call("inspect", request)
        instance = GlobalConfig(initial_data=request["config"]).get("alpha")
        self.assertTrue(result["clustered"])
        self.assertEqual(result["ctdb"], instance.ctdb_config())
        self.assertEqual(result["ctdb_globals"], list(instance.ctdb_smb_config().global_options()))

    def test_account_binding_uses_prepared_config_and_upstream_users(self):
        request = self.request()
        request["config_path"] = "/native/alpha/config/smb.conf"
        loader = Mock()
        loader._pdb.search_users.return_value = [{"account_name": "obsolete"}, {"account_name": "alice"}]
        constructor = Mock(return_value=loader)
        with patch.dict("sys.modules", {"sambacc.passdb_loader": types.SimpleNamespace(PassDBLoader=constructor)}):
            self.assertEqual(self.call("accounts", request), {})
        constructor.assert_called_once_with(request["config_path"])
        loader._pdb.search_users.assert_called_once_with(0x10)
        loader._pdb.getsampwnam.assert_called_once_with("obsolete")
        loader._pdb.delete_sam_account.assert_called_once_with(loader._pdb.getsampwnam.return_value)
        users = [call.args[0] for call in loader.add_user.call_args_list]
        self.assertEqual([user.username for user in users], ["alice", "bob"])
        self.assertEqual(users[1].nt_passwd, bytes.fromhex("ab" * 16))

    def test_cephfs_binding_closes_the_upstream_handle_on_stat_failure(self):
        request = dict(self.request(), config_path="/native/alpha/config/ceph-direct.conf",
                       checks=[{"filesystem": "fs", "path": "/data", "user": "smb.fs.cluster.alpha"}])
        handle = Mock()
        constructor = Mock(return_value=handle)
        with patch.dict("sys.modules", {"cephfs": types.SimpleNamespace(LibCephFS=constructor)}):
            self.assertEqual(self.call("check-data", request), {})
            handle.stat.side_effect = OSError("data access refused")
            with self.assertRaisesRegex(OSError, "data access refused"):
                self.call("check-data", request)
        self.assertEqual(constructor.call_count, 2)
        constructor.assert_called_with(conffile=request["config_path"], auth_id="smb.fs.cluster.alpha")
        handle.conf_set.assert_called_with("client_fs", "fs")
        handle.stat.assert_called_with("/data")
        self.assertEqual(handle.shutdown.call_count, 2)

    @unittest.skipUnless(os.environ.get("SMB_NATIVE_PACKAGE_TESTS"), "requires the exact extracted Samba package")
    def test_real_package_sam_update_and_removal_keep_sid(self):
        from sambacc.passdb_loader import PassDBLoader
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            request = self.request()
            request["config_path"] = str(root / "smb.conf")
            options = request["config"]["globals"]["native"]["options"]
            for key, name in (("private dir", "private"), ("state directory", "state"),
                              ("cache directory", "cache"), ("lock directory", "lock"),
                              ("pid directory", "run"), ("ncalrpc dir", "ncalrpc")):
                (root / name).mkdir()
                options[key] = str(root / name)
            request["config"]["users"] = {"all_entries": [{"name": "nobody", "nt_hash": "ab" * 16, "uid": 65534, "gid": 65534}]}
            request["config"]["groups"] = {"all_entries": []}
            Path(request["config_path"]).write_text(self.call("render", request)["smb_conf"])
            self.call("accounts", request)
            loader = PassDBLoader(request["config_path"])
            sid = str(loader._pdb.getsampwnam("nobody").user_sid)
            request["config"]["users"]["all_entries"][0]["nt_hash"] = "cd" * 16
            self.call("accounts", request)
            loader = PassDBLoader(request["config_path"])
            self.assertEqual(str(loader._pdb.getsampwnam("nobody").user_sid), sid)
            self.assertEqual(loader._pdb.getsampwnam("nobody").nt_passwd, bytes.fromhex("cd" * 16))
            request["config"]["users"]["all_entries"] = []
            self.call("accounts", request)
            self.assertEqual(loader._pdb.search_users(0x10), [])

    @unittest.skipUnless(os.environ.get("SMB_NATIVE_PACKAGE_TESTS"), "requires the exact extracted native packages")
    def test_real_package_renderer_and_cephfs_constructor(self):
        import cephfs
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            request = self.request()
            request["config"]["globals"]["native"]["options"]["private dir"] = str(root)
            conf = root / "smb.conf"
            conf.write_text(self.call("render", request)["smb_conf"])
            subprocess.run([os.environ["SMB_NATIVE_TESTPARM"], "--suppress-prompt", str(conf)], check=True, stdout=subprocess.DEVNULL)
            ceph_conf = root / "ceph.conf"
            keyring = str(root / "data.keyring")
            ceph_conf.write_text("[global]\nmon host = 127.0.0.1\nkeyring = " + keyring + "\n")
            handle = cephfs.LibCephFS(conffile=str(ceph_conf), auth_id="smb.fs.cluster.alpha")
            try:
                handle.conf_set("client_fs", "contract-test")
                self.assertEqual(handle.conf_get("keyring"), keyring)
            finally:
                handle.shutdown()

    def test_bridge_has_no_runtime_or_publication_commands(self):
        self.assertTrue(callable(getattr(self.native, "bridge", None)), "missing thin upstream-binding entry point")
        for command in ("launch", "publish", "ready", "nodes", "control", "arbitrary"):
            with self.subTest(command=command), self.assertRaises(ValueError):
                self.native.bridge(command, self.request())


if __name__ == "__main__":
    unittest.main()

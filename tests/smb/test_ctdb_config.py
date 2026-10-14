"""Pure configuration coverage with pinned Sambacc; not native qualification."""
import configparser
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from sambacc import ctdb
from sambacc.ceph.rados import parse_rados_uri
from sambacc.commands.ctdb import ctdb_rados_mutex
from sambacc.config import GlobalConfig

MODULE = Path(__file__).resolve().parents[2] / "snapcraft/smb/native.py"


class CTDBProjectionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("native_ctdb", MODULE)
        cls.native = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.native)

    def inputs(self):
        return {
            "document": {
                "samba-container-config": "v0",
                "configs": {"alpha": {"instance_name": "alpha", "instance_features": ["ctdb"], "globals": ["default"], "shares": ["data"]}},
                "globals": {"default": {"options": {"load printers": "No"}}},
                "shares": {"data": {"options": {"path": "/data", "vfs objects": "ceph_new", "ceph_new:proxy": "yes", "ceph_new:config_file": "/etc/ceph/ceph.conf", "ceph_new:filesystem": "fs", "ceph_new:user_id": "smb.fs.cluster.alpha"}}},
            },
            "cluster_id": "alpha", "root": "/native/alpha",
            "cluster_meta_uri": "rados://.smb/alpha/cluster.meta",
            "recovery_lock_uri": "rados://.smb/alpha/cluster.lock",
            "member": {"identity": "host-c", "pnn": 3, "node": "192.0.2.3", "state": "ready"},
            "peers": [
                {"identity": "host-a", "pnn": 0, "node": "192.0.2.1", "state": "new"},
                {"identity": "retired", "pnn": 2, "node": "192.0.2.2", "state": "gone"},
                {"identity": "changing", "pnn": 4, "node": "192.0.2.4", "state": "changed"},
            ],
        }

    def project(self, inputs):
        self.assertTrue(callable(getattr(self.native, "project_ctdb", None)), "missing pure CTDB configuration projection")
        return self.native.project_ctdb(**inputs)

    def test_pinned_consumer_semantics_and_text_backend(self):
        inputs = self.inputs()
        files, details = self.project(inputs)
        model_doc = copy.deepcopy(inputs["document"])
        model_doc["ctdb"] = json.loads(files["config/ctdb.json"])["ctdb"]
        instance = GlobalConfig(initial_data=model_doc).get("alpha")
        params = instance.ctdb_config()
        parser = configparser.ConfigParser(interpolation=None)
        parser.read_string(files["ctdb/ctdb.conf"])
        self.assertEqual(parser["cluster"]["recovery lock"], params["recovery_lock"])
        self.assertEqual(parser["cluster"]["nodes list"], "/native/alpha/ctdb/nodes")
        self.assertEqual(parser["cluster"]["node address"], "192.0.2.3")
        self.assertEqual(parser["logging"]["location"], "file:/native/alpha/diagnostics/ctdb.log")
        for kind in ("volatile", "persistent", "state"):
            self.assertEqual(parser["database"][kind + " database directory"], "/native/alpha/ctdb/" + kind)
        self.assertEqual(parser["event"]["script directory"], "/native/alpha/ctdb/events")
        self.assertEqual(params["cluster_meta_uri"], inputs["cluster_meta_uri"])
        self.assertEqual(params["nodes_path"], "/native/alpha/ctdb/nodes")
        self.assertFalse(params["public_addresses"])
        self.assertFalse(params["conf_file_includes"])
        # Samba permits ordered repeated globals; ':' is part of VFS keys.
        samba = configparser.ConfigParser(interpolation=None, strict=False, delimiters=("=",))
        samba.read_string(files["config/smb.conf"])
        pinned = dict(instance.ctdb_smb_config().global_options())
        for key in ("clustering", "ctdb:registry.tdb"):
            self.assertEqual(samba["global"][key], pinned[key])
        self.assertEqual(samba["global"]["config backend"], "file")
        self.assertEqual(samba["global"]["registry shares"], "no")
        self.assertNotIn("include", samba["global"])
        self.assertEqual(samba["global"]["ctdbd socket"], "/native/alpha/ctdb/run/ctdbd.socket")
        self.assertEqual(samba["global"]["interfaces"], "192.0.2.3")
        self.assertEqual(samba["global"]["winbindd socket directory"], "/native/alpha/samba/winbindd")
        self.assertEqual(samba["data"]["ceph_new:config_file"], "/native/alpha/config/ceph-proxy.conf")
        self.assertEqual(details["member"], inputs["member"])
        self.assertEqual(files["config/ctdb-rank"], "3\n")
        self.assertEqual(files["config/ctdb-identity"], "host-c\n")
        self.assertEqual(files["config/ctdb-address"], "192.0.2.3\n")
        self.assertNotIn("10.interface", "".join(files.values()))
        self.assertNotIn("public_addresses", files)

    def test_slots_are_comments_and_peer_order_is_not_rank_allocation(self):
        inputs = self.inputs()
        files, _ = self.project(inputs)
        self.assertEqual(ctdb.read_nodes_file(io.StringIO(files["ctdb/nodes"])), ["192.0.2.1", "#", "#", "192.0.2.3", "#"])
        inputs["peers"].reverse()
        self.assertEqual(self.project(inputs)[0], files)
        inputs["peers"] = []
        inputs["member"]["pnn"] = 0
        inputs["member"]["node"] = "2001:db8::3"
        self.assertEqual(self.project(inputs)[0]["ctdb/nodes"], "2001:db8::3\n")

    def test_recovery_lock_is_shared_stable_and_matches_pinned_helper_arguments(self):
        inputs = self.inputs()
        files, details = self.project(inputs)
        rinfo = parse_rados_uri(inputs["recovery_lock_uri"])
        self.assertEqual(details["recovery_lock_argv"], ["/snap/microceph/current/libexec/ctdb/ctdb_mutex_ceph_rados_helper", "ceph", "client.smb.cluster.alpha", rinfo["pool"], rinfo["key"], "-n", rinfo["ns"], "-R"])
        ctx = SimpleNamespace(cli=SimpleNamespace(mutex_uri=inputs["recovery_lock_uri"], cluster_name="ceph", ceph_id={"client_name": "client.smb.cluster.alpha", "full_name": True}))
        with patch.dict("os.environ", {"SAMBA_SPECIFICS": "ctdb_rados_mutex_skip_reg"}), patch("sambacc.samba_cmds.execute") as execute:
            ctdb_rados_mutex(ctx)
        upstream_argv = list(execute.call_args.args[0])
        self.assertEqual(upstream_argv[0], "/usr/libexec/ctdb/ctdb_mutex_ceph_rados_helper")
        self.assertEqual(details["recovery_lock_argv"][1:], upstream_argv[1:])
        first_lock = json.loads(files["config/ctdb.json"])["ctdb"]["recovery_lock"]
        local = inputs["member"]
        inputs["member"] = inputs["peers"].pop(0)
        inputs["peers"].append(local)
        inputs["root"] = "/other-native-root/alpha"
        second, _ = self.project(inputs)
        self.assertEqual(json.loads(second["config/ctdb.json"])["ctdb"]["recovery_lock"], first_lock)
        self.assertNotIn("host-", first_lock)
        self.assertNotIn("/native/", first_lock)

    def test_invalid_placement_sources_and_escape_fail_before_projection(self):
        cases = [
            ("member", "pnn", -1), ("member", "pnn", True), ("member", "pnn", "3"),
            ("member", "pnn", 65536), ("member", "pnn", 0),
            ("member", "node", "192.0.2.1"), ("member", "node", "host.invalid"),
            ("member", "node", "192.0.2.3\n[cluster]"), ("member", "node", "fe80::1%eth0"),
            ("member", "identity", "host-a"), ("member", "identity", "bad\nidentity"),
            ("member", "state", "gone"), ("member", "state", "unknown"),
            (None, "cluster_meta_uri", "file:/outside/meta"),
            (None, "cluster_meta_uri", "rados://.smb/other/cluster.meta"),
            (None, "cluster_meta_uri", "rados://.smb/alpha/"),
            (None, "recovery_lock_uri", "rados:mon-config-key:lock"),
            (None, "recovery_lock_uri", "rados://.smb/alpha/cluster.lock;touch"),
            (None, "recovery_lock_uri", "rados://.smb/alpha/cluster%0alock"),
            (None, "recovery_lock_uri", "rados://.smb/alpha/cluster.lock?x=y"),
            (None, "recovery_lock_uri", "rados://.smb/alpha/cluster.lock#fragment"),
            (None, "recovery_lock_uri", "rados://.smb/alpha/cluster.meta"),
            (None, "recovery_lock_uri", "rados://other/alpha/cluster.lock"),
            (None, "cluster_meta_uri", "rados://-pool/alpha/cluster.meta"),
            (None, "recovery_lock_uri", "rados://.smb/alpha/-R"),
            (None, "root", "/native/alpha/../beta"), (None, "root", "relative/alpha"),
            (None, "root", "/native/beta"), (None, "cluster_id", "../alpha"),
        ]
        for section, key, value in cases:
            inputs = self.inputs()
            target = inputs if section is None else inputs[section]
            target[key] = value
            with self.subTest(section=section, key=key, value=value), self.assertRaises(ValueError):
                self.project(inputs)
        inputs = self.inputs()
        inputs["peers"][1]["node"] = inputs["member"]["node"]
        with self.assertRaises(ValueError):
            self.project(inputs)

    def test_pure_input_preservation_conflicts_and_unprepared_runtime(self):
        inputs = self.inputs()
        inputs["sources"] = [{"users": {"all_entries": [{"name": "alice", "password": "one"}]}}]
        with tempfile.TemporaryDirectory() as directory:
            inputs["root"] = str(Path(directory) / "alpha")
            before = copy.deepcopy(inputs)
            files, details = self.project(inputs)
            self.assertEqual(inputs, before)
            self.assertFalse(Path(inputs["root"]).exists())
            for name in files:
                self.assertFalse(Path(name).is_absolute())
                self.assertNotIn("..", Path(name).parts)
            for name in details["directories"]:
                self.assertTrue(name.startswith(inputs["root"] + "/"))
        for key, value in (
            ("CLUSTERING", "no"), ("interfaces", "192.0.2.99"), ("include", "registry"), ("ctdbd socket", "/outside/socket"),
            ("security", "ads"), ("security", "domain"), ("security", "ADS"), ("security", "DoMaIn"),
            ("security", " ads "), ("security", "domain "), ("security", "\tAdS\t"), ("security", " DoMaIn "),
        ):
            inputs = self.inputs()
            inputs["document"]["globals"]["default"]["options"][key] = value
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                self.project(inputs)
        for options in ({"public_addresses": [{"address": "192.0.2.99"}]}, {"nodes_json": "/outside/meta"}, {"nodes_cmd": "arbitrary-command"}, {"conf_file_includes": ["/outside/smb.conf"]}, {"nodes_path": "/outside/nodes"}):
            inputs = self.inputs()
            inputs["document"]["ctdb"] = options
            with self.subTest(options=options), self.assertRaises(ValueError):
                self.project(inputs)
        with self.assertRaises(ValueError):
            self.native.compile_core({"config": self.inputs()["document"], "spec": {"cluster_id": "alpha"}}, "/native/alpha")
        for role in ("ctdbd", "ctdb-nodes"):
            with self.assertRaises(ValueError):
                self.native.role_command(role, "/native/alpha", {}, "/snap/microceph/current")


if __name__ == "__main__":
    unittest.main()

"""Frozen local-mode safety assertions against the singleton's real consumers.

Host/guest I/O is stubbed; no VMs, native services or installed acceptance.
"""
import copy
import importlib.util
import json
import os
from pathlib import Path
from types import SimpleNamespace

import pytest
import smb_ops as m


FIXTURES = Path(__file__).with_name("fixtures")


def _sample(name="smb_ceph_readiness.json"):
    return json.loads((FIXTURES / name).read_text())


def _result(stdout="", rc=0):
    return SimpleNamespace(rc=rc, stdout=stdout, stderr="")


def _runner():
    spec = importlib.util.spec_from_file_location("retained_singleton_runner", Path(__file__).parents[1] / "smb_runner.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _owned_osd_inventory():
    instance = {"name": "node", "expanded_config": {m.OWNER_KEY: "owner"},
                "expanded_devices": {"root": {"type": "disk", "path": "/", "pool": "default"},
                                     "test-data": {"type": "disk", "pool": "default", "source": "node-osd"}}}
    volume = {"name": "node-osd", "content_type": "block", "config": {m.OWNER_KEY: "owner"}}
    blocks = {"blockdevices": [
        {"name": "/dev/sdb", "type": "disk", "size": 20 * 1024**3, "serial": "lxd_root",
         "pkname": None, "mountpoints": [], "children": [
             {"name": "/dev/sdb1", "type": "part", "pkname": "/dev/sdb", "serial": None, "mountpoints": ["/"]}]},
        {"name": "/dev/sdc", "type": "disk", "size": 4 * 1024**3, "serial": "lxd_test--data",
         "pkname": None, "mountpoints": []}]}
    return instance, volume, blocks


def test_smb_osd_selector_uses_owned_attachment_serial_not_letter_or_size():
    instance, volume, blocks = _owned_osd_inventory()
    blocks["blockdevices"].append({"name": "/dev/sda", "type": "disk", "size": 4 * 1024**3,
                                   "serial": "unrelated", "pkname": None, "mountpoints": []})
    assert m.select_owned_osd(instance, volume, blocks, "node", "node-osd", "default", "owner") == "/dev/sdc"


@pytest.mark.parametrize("mutation", [
    lambda i, v, b: i["expanded_config"].update({m.OWNER_KEY: "foreign"}),
    lambda i, v, b: v["config"].update({m.OWNER_KEY: "foreign"}),
    lambda i, v, b: i["expanded_devices"]["test-data"].update(source="other-volume"),
    lambda i, v, b: i["expanded_devices"]["test-data"].update(pool="other-pool"),
    lambda i, v, b: i["expanded_devices"]["test-data"].update(path="/"),
    lambda i, v, b: v.update(content_type="filesystem"),
    lambda i, v, b: b["blockdevices"][1].update(serial=None),
    lambda i, v, b: b["blockdevices"][1].update(size=20 * 1024**3),
    lambda i, v, b: b["blockdevices"][1].update(type="part", pkname="/dev/sdb"),
    lambda i, v, b: b["blockdevices"][1].update(mountpoints=["/mnt"]),
    lambda i, v, b: b["blockdevices"][1].update(children=[{"name": "/dev/sdc1", "mountpoints": ["/"]}]),
    lambda i, v, b: b["blockdevices"].append(copy.deepcopy(b["blockdevices"][1])),
    lambda i, v, b: b["blockdevices"][0]["children"][0].update(mountpoints=[]),
    lambda i, v, b: b["blockdevices"][1].pop("mountpoints"),
    lambda i, v, b: b["blockdevices"][1].pop("pkname"),
    lambda i, v, b: b["blockdevices"][0]["children"].append(b["blockdevices"].pop()),
    lambda i, v, b: b["blockdevices"][1].update(name="/dev/../sdc"),
], ids=["foreign-vm", "foreign-volume", "wrong-volume", "wrong-pool", "root-attachment", "filesystem-volume",
        "missing-serial", "wrong-size", "partition", "mounted", "children", "ambiguous", "invisible-root",
        "missing-mountpoints", "missing-parent", "nested-disk", "escaping-path"])
def test_smb_osd_selector_refuses_unsafe_or_unproven_identity(mutation):
    instance, volume, blocks = _owned_osd_inventory()
    mutation(instance, volume, blocks)
    with pytest.raises(AssertionError):
        m.select_owned_osd(instance, volume, blocks, "node", "node-osd", "default", "owner")


def test_smb_osd_path_revalidates_host_attachment_and_guest_inventory():
    library = m.smb_ops()
    instance, volume, blocks = _owned_osd_inventory()
    calls = []
    def command(*args, **kwargs):
        calls.append(args)
        return _result(json.dumps(blocks))
    library.owner, library.volumes = "owner", ["node-osd"]
    library.nodes = [SimpleNamespace(name="node", command=command)]
    library._host = lambda *args: json.dumps(instance if "/instances/" in args[-1] else volume)
    assert library.smb_osd_path(0) == "/dev/sdc"
    assert calls[0][0] == "lsblk" and "--json" in calls[0] and "SERIAL" in calls[0][-1]
    blocks["blockdevices"][1]["mountpoints"] = ["/mounted-since-first-probe"]
    with pytest.raises(AssertionError):
        library.smb_osd_path(0)
    blocks["blockdevices"][1]["mountpoints"] = []
    instance["expanded_config"][m.OWNER_KEY] = "foreign"
    with pytest.raises(AssertionError):
        library.smb_osd_path(0)
    assert len(calls) == 3


def test_smb_ceph_readiness_accepts_captured_clock_warning():
    sample, warning = _sample(), _sample("smb_ceph_clock_warning.json")
    assert m.smb_ceph_readiness_signature(warning["health"], warning["clock"], sample["settled_map"], sample["pgs"], sample["settled_map"]) is not None


@pytest.mark.parametrize("index,mutation", [
    (0, lambda d: d.update(status="HEALTH_ERR")),
    (0, lambda d: d["checks"]["MON_CLOCK_SKEW"].update(severity="HEALTH_ERR")),
    (0, lambda d: d["checks"].update(MON_DOWN={"severity": "HEALTH_WARN"})),
    (0, lambda d: d["checks"].update(PG_AVAILABILITY={"severity": "HEALTH_WARN"})),
    (0, lambda d: d.update(checks={"OSD_DOWN": {"severity": "HEALTH_WARN"}})),
    (0, lambda d: d.update(checks={"MON_CLOCK_SKEW": None})),
    (0, lambda d: d.update(checks={"MON_CLOCK_SKEW": {}})),
    (1, lambda d: d["timechecks"].update(round_status="ongoing")),
    (1, lambda d: d.update(time_skew_status={})),
    (1, lambda d: next(iter(d["time_skew_status"].values())).update(health="HEALTH_ERR")),
    (1, lambda d: next(iter(d["time_skew_status"].values())).update(health="UNKNOWN")),
    (2, lambda d: d["osds"][0].update(up=0)),
    (2, lambda d: d["pools"][0].update(pg_num_target=999)),
    (3, lambda d: d["pg_stats"][0].update(state="peering")),
    (4, lambda d: d.update(epoch=d["epoch"] + 1)),
], ids=["health-error", "clock-error", "mon-down", "pg-unavailable", "osd-down", "null-warning", "incomplete-warning",
        "ongoing-clock", "missing-clock", "monitor-error", "unknown-monitor", "down-osd", "placement-target", "peering", "epoch-race"])
def test_smb_ceph_clock_warning_does_not_hide_other_readiness_failures(index, mutation):
    sample, warning = _sample(), _sample("smb_ceph_clock_warning.json")
    args = [warning["health"], warning["clock"], sample["settled_map"], sample["pgs"], copy.deepcopy(sample["settled_map"])]
    mutation(args[index])
    if index == 2:
        args[4] = copy.deepcopy(args[2])
    assert m.smb_ceph_readiness_signature(*args) is None


def test_smb_ceph_readiness_rejects_premature_health_ok_during_placement():
    s = _sample()
    ready = m.smb_ceph_readiness_signature
    assert ready(s["health_ok"], s["clock"], s["unsettled_map"], s["pgs"], s["unsettled_map"]) is None
    assert ready(s["health_warn"], s["clock"], s["settled_map"], s["pgs"], s["settled_map"]) is None
    assert ready(s["health_ok"], s["clock"], s["settled_map"], s["pgs"], s["settled_map"]) is not None


@pytest.mark.parametrize("index,value", [(i, v) for i in range(5) for v in (None, [], {}, "invalid")])
def test_smb_ceph_readiness_fails_closed_for_missing_documents(index, value):
    s = _sample()
    args = [s["health_ok"], s["clock"], s["settled_map"], s["pgs"], copy.deepcopy(s["settled_map"])]
    args[index] = value
    assert m.smb_ceph_readiness_signature(*args) is None


@pytest.mark.parametrize("index,mutation", [
    (0, lambda d: d.update(checks={"MON_CLOCK_SKEW": {}})),
    (1, lambda d: d.update(time_skew_status={})),
    (1, lambda d: d.pop("timechecks")),
    (1, lambda d: next(iter(d["time_skew_status"].values())).pop("health")),
    (1, lambda d: next(iter(d["time_skew_status"].values())).update(health="HEALTH_WARN")),
    (2, lambda d: d.update(pools=[])),
    (2, lambda d: d.update(osds=[])),
    (2, lambda d: d["osds"][0].update(up=0)),
    (2, lambda d: d["pools"][0].pop("pg_placement_num_target")),
    (2, lambda d: d["pools"][0].update(pg_num_target=2)),
    (2, lambda d: d["pools"][0].update(pg_num_pending=2)),
    (2, lambda d: d["pools"][0].update(pg_num=True)),
    (2, lambda d: d["pools"].append(d["pools"][0])),
    (3, lambda d: d.update(pg_ready=False)),
    (3, lambda d: d.update(pg_stats=[])),
    (3, lambda d: d["pg_stats"].pop()),
    (3, lambda d: d["pg_stats"].append(d["pg_stats"][0])),
    (3, lambda d: d["pg_stats"][0].update(state="peering")),
    (3, lambda d: d["pg_stats"][0].update(pgid="99.0")),
    (3, lambda d: d["pg_stats"][0].update(acting=[])),
    (3, lambda d: d["pg_stats"][0].update(up=[999, 2, 3])),
    (4, lambda d: d.update(epoch=d["epoch"] + 1)),
    (4, lambda d: d.update(fsid="different-cluster")),
], ids=["inconsistent-health", "empty-clock", "missing-timechecks", "missing-monitor-health", "monitor-warn",
        "empty-pools", "empty-osds", "down-osd", "missing-target", "target-mismatch", "pending-mismatch", "boolean-count", "duplicate-pool",
        "pg-not-ready", "empty-pgs", "missing-pg", "duplicate-pg", "peering", "foreign-pool", "empty-acting", "up-acting-mismatch", "epoch-race", "fsid-race"])
def test_smb_ceph_readiness_fails_closed_for_incomplete_or_inconsistent_data(index, mutation):
    s = _sample()
    args = [s["health_ok"], s["clock"], s["settled_map"], s["pgs"], copy.deepcopy(s["settled_map"])]
    mutation(args[index])
    if index == 2:
        args[4] = copy.deepcopy(args[2])
    assert m.smb_ceph_readiness_signature(*args) is None


def _readiness_node(sample, observations, clock, calls, seconds_per_read=1):
    state = {"index": 0, "maps": 0}
    def command(*argv, timeout, **kwargs):
        assert 0 < timeout <= min(30, 300 - clock[0])
        calls.append((argv, timeout))
        clock[0] += min(seconds_per_read, timeout)
        observation = observations[min(state["index"], len(observations) - 1)]
        if argv[1:] == ("osd", "dump", "-f", "json"):
            value = observation["map"]
            state["maps"] += 1
            if state["maps"] % 2 == 0:
                state["index"] += 1
        elif argv[1:] == ("pg", "dump", "pgs_brief", "-f", "json"):
            value = observation.get("pgs", sample["pgs"])
        elif argv[1:] == ("health", "detail", "-f", "json"):
            value = observation["health"]
        elif argv[1:] == ("time-sync-status", "-f", "json"):
            value = sample["clock"]
        else:
            raise AssertionError(f"Unexpected readiness command: {argv}")
        return _result(json.dumps(value))
    return SimpleNamespace(command=command)


@pytest.mark.parametrize("mode", ["settle", "clock-warning", "timeout"])
def test_smb_ceph_wait_requires_two_coherent_samples_with_one_original_budget(monkeypatch, mode):
    sample, calls, clock, warnings = _sample(), [], [0], []
    settled = {"health": sample["health_ok"], "map": sample["settled_map"]}
    observations = [
        {"health": sample["health_ok"], "map": sample["unsettled_map"]},
        {"health": sample["health_warn"], "map": sample["settled_map"]},
        settled, dict(settled, pgs={"pg_ready": True, "pg_stats": []}), settled, settled]
    if mode == "clock-warning":
        warning = _sample("smb_ceph_clock_warning.json")
        sample["clock"] = warning["clock"]
        observations = [{"health": warning["health"], "map": sample["settled_map"]}] * 2
    monkeypatch.setattr(m.time, "monotonic", lambda: clock[0])
    monkeypatch.setattr(m.time, "sleep", lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    monkeypatch.setattr(m.logger, "warn", warnings.append)
    library = m.smb_ops()
    library.nodes = [_readiness_node(sample, observations, clock, calls, 30 if mode == "timeout" else 1)]
    if mode == "timeout":
        with pytest.raises(AssertionError, match="SMB Ceph readiness"):
            library.wait_for_smb_ceph_health()
        assert clock[0] <= 300 and calls[-1][1] < 30
    else:
        assert library.wait_for_smb_ceph_health() == observations[-1]["health"]
        assert len(calls) == 5 * len(observations)
        if mode == "clock-warning":
            assert len(warnings) == 1 and "MON_CLOCK_SKEW" in warnings[0]
            assert "0.0663787" in warnings[0] and "0.19567" in warnings[0]
    assert not any(argv == ("microceph.ceph", "health") for argv, _ in calls)


def test_smb_ceph_readiness_json_rejects_malformed_read(monkeypatch):
    monkeypatch.setattr(m.time, "monotonic", lambda: 0)
    library = m.smb_ops()
    library.nodes = [SimpleNamespace(command=lambda *args, **kwargs: _result("{invalid"))]
    with pytest.raises(AssertionError, match="SMB Ceph readiness received malformed JSON"):
        library.wait_for_smb_ceph_health()


def test_smb_ceph_robot_uses_the_owning_gate_without_rejecting_clock_warnings_again():
    from robot.api import get_model
    model = get_model(Path(__file__).with_name("smb_suite.resource"))
    setup = next(block for section in model.sections for block in section.body
                 if getattr(block, "name", "") == "SMB Suite Setup" and hasattr(block, "body"))
    calls = [statement.keyword for statement in setup.body if hasattr(statement, "keyword")]
    assert calls.count("Wait For SMB Ceph Health") == 2
    assert not any("Health OK" in keyword for keyword in calls)
    model = get_model(Path(__file__).parents[1] / "smb-singleton-tests/smb_singleton_tests.robot")
    calls = [statement.keyword for section in model.sections for block in section.body
             for statement in getattr(block, "body", []) if hasattr(statement, "keyword")]
    reboot = calls.index("Reboot SMB VM")
    assert "Wait For SMB Ceph Health" in calls[reboot:]


@pytest.mark.parametrize("mutation", [
    lambda d: d.update(owner="invalid"),
    lambda d: d["nodes"].__setitem__(0, "foreign-vm"),
    lambda d: d["nodes"].append(d["nodes"][0]),
    lambda d: d["volumes"].__setitem__(0, "foreign-volume"),
    lambda d: d.update(pool="../foreign"),
], ids=["owner", "foreign-vm", "duplicate-vm", "foreign-volume", "pool"])
def test_smb_shared_manifest_binds_only_owned_names_without_robot_context(tmp_path, mutation):
    owner = "a" * 32
    document = {"owner": owner, "pool": "default", "nodes": [f"smb-ci-{owner[:8]}-n{i}" for i in range(3)],
                "volumes": [f"smb-ci-{owner[:8]}-n{i}-osd" for i in range(3)]}
    path = tmp_path / "lab.json"
    path.write_text(json.dumps(document))
    library = m.smb_ops()
    library.use_smb_lab(str(path))
    assert [node.name for node in library.nodes] == document["nodes"]
    assert library.volumes == document["volumes"]
    mutation(document)
    path.write_text(json.dumps(document))
    with pytest.raises(AssertionError):
        library.use_smb_lab(str(path))


@pytest.mark.parametrize("owner", ["robot-never", "robot-other"])
def test_smb_scenario_retirement_refuses_foreign_owners_and_never_purges(monkeypatch, owner):
    library, calls = m.smb_ops(), []
    library.nodes = [SimpleNamespace(command=lambda *args, **kwargs: calls.append(args))]
    monkeypatch.setattr(library, "smb_snapshot", lambda *args: {"owner": "foreign"})
    with pytest.raises(AssertionError, match="foreign"):
        library.retire_smb_scenario()
    assert not calls
    snapshots = iter([{"owner": owner}, {"owner": "", "roles": {"smbd": {"ActiveState": "inactive"}, "ctdbd": {"ActiveState": "inactive"}}}])
    monkeypatch.setattr(library, "smb_snapshot", lambda *args: next(snapshots))
    library.retire_smb_scenario()
    assert calls == [("microceph", "disable", "smb", "--cluster-id", owner)]


@pytest.mark.parametrize("disk,memory,headroom,reserve", [(9, 64, 10, 0), (20, 3, 10, 0), (20, 64, 0, 0), (20, 64, 10, -1)])
def test_smb_setup_capacity_refusal_precedes_allocation(tmp_path, monkeypatch, disk, memory, headroom, reserve):
    library, calls = m.smb_ops(), []
    snap = tmp_path / "candidate.snap"
    snap.write_bytes(b"test-only")
    monkeypatch.setattr(library, "_variable", lambda key, default: {
        "SNAP_PATH": str(snap), "SMB_LAB_MANIFEST": str(tmp_path / "lab.json"),
        "SMB_DISK_HEADROOM_GIB": headroom, "SMB_DISK_RESERVE_PER_VM_GIB": reserve,
    }.get(key, default))
    def host(*args):
        calls.append(args)
        if args[:3] == ("lxc", "remote", "list"):
            return '{"local":{}}'
        if args[:3] == ("lxc", "remote", "get-default"):
            return "local"
        assert args[:2] == ("lxc", "query") and args[2].endswith("/resources")
        return json.dumps({"space": {"total": disk * 1024**3, "used": 0}})
    monkeypatch.setattr(library, "_host", host)
    read_text = Path.read_text
    monkeypatch.setattr(Path, "read_text", lambda path, *a, **k:
                        f"MemAvailable: {memory * 1024**2} kB\n" if str(path) == "/proc/meminfo" else read_text(path, *a, **k))
    with pytest.raises((AssertionError, ValueError), match="capacity"):
        library.provision_smb_lab()
    assert not (tmp_path / "lab.json").exists()
    assert not library.nodes and not library.volumes
    assert all(call[1] in ("remote", "query") for call in calls)


@pytest.mark.parametrize("invalid", [False, True])
def test_smb_runner_forwards_real_robot_allocation_budget_precedence(tmp_path, monkeypatch, invalid):
    """Real runner -> Robot CLI/variable file -> real allocation-time capacity."""
    runner = _runner()
    here = tmp_path / "project/tests/robot"
    suite = here / "smb-singleton-tests"
    suite.mkdir(parents=True)
    observation = tmp_path / "budget.json"
    library = tmp_path / "BudgetProbe.py"
    library.write_text(
        "import json,sys\nfrom pathlib import Path\nfrom unittest.mock import patch\n"
        + "sys.path.insert(0, " + repr(str(Path(m.__file__).parent)) + ")\n"
        + "from smb_ops import smb_ops\n"
        + "class BudgetProbe(smb_ops):\n"
        + "    def record_budget(self):\n"
        + "        data = {key: self._variable(key, default) for key, default in "
          "(('SMB_DISK_HEADROOM_GIB', 50), ('SMB_DISK_RESERVE_PER_VM_GIB', 24))}\n"
        + "        data['queries'] = []\n"
        + "        def host(*args):\n"
        + "            data['queries'].append(args)\n"
        + "            return json.dumps({'space': {'total': 1000 * 1024**3, 'used': 0}})\n"
        + "        self._host = host\n"
        + "        read_text = Path.read_text\n"
        + "        try:\n"
        + "            with patch.object(Path, 'read_text', lambda path, *a, **k: "
          "'MemAvailable: 67108864 kB\\n' if str(path) == '/proc/meminfo' else read_text(path, *a, **k)):\n"
        + "                self._capacity(3)\n"
        + "        finally:\n"
        + "            Path(" + repr(str(observation)) + ").write_text(json.dumps(data))\n"
    )
    (suite / "budget.robot").write_text(
        "*** Settings ***\nLibrary    " + str(library)
        + "\n*** Test Cases ***\nBudget\n    Record Budget\nSecond\n    No Operation\nThird\n    No Operation\n"
    )
    variable_file = tmp_path / "limits.yaml"
    variable_file.write_text("SMB_DISK_HEADROOM_GIB: 80\nSMB_DISK_RESERVE_PER_VM_GIB: 5\n")
    monkeypatch.setattr(runner, "HERE", here)
    previous = os.umask(0o022)
    try:
        rc = runner.main(["--outputdir", str(tmp_path / "output"), "--variablefile", str(variable_file),
                          "-v", "smb disk headroom gib:" + ("0" if invalid else "10"),
                          "--variable=SMB_DISK_RESERVE_PER_VM_GIB:0"])
    finally:
        os.umask(previous)
    assert rc == int(invalid)
    data = json.loads(observation.read_text())
    assert data["SMB_DISK_HEADROOM_GIB"] == ("0" if invalid else "10")
    assert data["SMB_DISK_RESERVE_PER_VM_GIB"] == "0"
    if invalid:
        assert data["queries"] == [], "invalid resolved budget must reject before allocation I/O"
    else:
        assert data["queries"] == [["lxc", "query", "local:/1.0/storage-pools/default/resources"]]


@pytest.mark.parametrize("mode", ["pass", "robot-failure", "missing-output", "setup-failure", "foreign-vm", "foreign-volume", "attached-volume", "uncertain-idle"])
def test_smb_coordinator_real_setup_and_standalone_owned_cleanup(tmp_path, monkeypatch, mode):
    """Exercise main, real provision/manifest/rebind/idle/delete; stub only I/O."""
    runner, library = _runner(), m.smb_ops()
    owner = "a" * 32
    inventory, volumes, calls = [], [], []
    snap = tmp_path / "candidate.snap"
    snap.write_bytes(b"test-only")
    monkeypatch.setattr(m.uuid, "uuid4", lambda: SimpleNamespace(hex=owner))
    monkeypatch.setattr(m.smb_ops, "_variable", lambda self, key, default: {
        "SNAP_PATH": str(snap), "SMB_LAB_MANIFEST": str(tmp_path / "smb-results/private/lab.json"),
        "SMB_DISK_HEADROOM_GIB": 10, "SMB_DISK_RESERVE_PER_VM_GIB": 0,
    }.get(key, default))
    read_text = Path.read_text
    monkeypatch.setattr(Path, "read_text", lambda path, *a, **k:
                        "MemAvailable: 67108864 kB\n" if str(path) == "/proc/meminfo" else read_text(path, *a, **k))

    def host(self, *args, **kwargs):
        calls.append(args)
        if args[:3] == ("lxc", "remote", "list"):
            return '{"local":{}}'
        if args[:3] == ("lxc", "remote", "get-default"):
            return "local"
        if args[:2] == ("lxc", "query"):
            if args[2].endswith("/resources"):
                return json.dumps({"space": {"total": 20 * 1024**3, "used": 0}})
            return json.dumps(volumes if "/volumes/custom?" in args[2] else inventory)
        if args[:4] == ("lxc", "storage", "volume", "create"):
            # Allocation intent is already durably private before host mutation.
            manifest = Path(library.manifest)
            document = json.loads(manifest.read_text())
            assert args[5] in document["volumes"]
            assert manifest.stat().st_mode & 0o777 == 0o600
            volumes.append({"name": args[5], "config": {m.OWNER_KEY: owner}, "used_by": []})
            return ""
        if args[:2] == ("lxc", "delete"):
            name = args[2].removeprefix("local:")
            assert next(row for row in inventory if row["name"] == name)["config"][m.OWNER_KEY] == owner
            inventory[:] = [row for row in inventory if row["name"] != name]
            return ""
        if args[:4] == ("lxc", "storage", "volume", "delete"):
            row = next(row for row in volumes if row["name"] == args[5])
            assert row["config"][m.OWNER_KEY] == owner and not row["used_by"]
            volumes.remove(row)
            return ""
        raise AssertionError(f"Unexpected host I/O: {args}")

    def launch(self, name, tag, **kwargs):
        assert tag == owner
        assert name in json.loads(Path(library.manifest).read_text())["nodes"]
        inventory.append({"name": name, "status": "Running", "config": {m.OWNER_KEY: tag}})
        if mode == "setup-failure":
            raise AssertionError("injected launch uncertainty")

    def command(vm, *args, **kwargs):
        calls.append((vm.name, *args))
        if args[0] == "sha256sum":
            import hashlib
            return _result(hashlib.sha256(snap.read_bytes()).hexdigest())
        if args[:3] == ("snap", "debug", "confinement"):
            return _result("strict")
        if args[0] == "ip":
            return _result('[{"prefsrc":"192.0.2.1"}]')
        if args[:3] == ("python3", library._base + "/smb_guest.py", "snapshot"):
            return _result(json.dumps({"owner": "alpha" if mode == "uncertain-idle" else "", "api": '{"metadata":{}}',
                "services": "Id=snap.microceph.smbd.service\nActiveState=inactive\n\nId=snap.microceph.ctdbd.service\nActiveState=inactive"}))
        return _result()

    monkeypatch.setattr(m.smb_ops, "_host", host)
    monkeypatch.setattr(m._VM, "command", command)
    monkeypatch.setattr(m.Harness, "launch_owned_test_vm", launch)
    monkeypatch.setattr(m.Harness, "_lxc_file_push", lambda *a, **k: None)
    for method in ("probe_instance_network", "copy_snap_to_vm", "install_microceph_from_local_snap", "wait_for_microceph_control_socket", "apt_update", "apt_install"):
        monkeypatch.setattr(m._VM, method, lambda *a, **k: None)

    manifests = []
    def execute(directory, variables, options, dryrun):
        assert not dryrun
        manifests.extend(v.split(":", 1)[1] for v in variables if v.startswith("SMB_LAB_MANIFEST:"))
        library.provision_smb_lab()
        library.verify_smb_lab()
        assert len(library.nodes) == len(library.volumes) == 3
        if mode == "foreign-vm":
            inventory[0]["config"][m.OWNER_KEY] = "foreign"
        if mode == "foreign-volume":
            volumes[0]["config"][m.OWNER_KEY] = "foreign"
        if mode == "attached-volume":
            volumes[0]["used_by"] = ["/instances/foreign"]
        inventory.append({"name": "unrelated", "config": {m.OWNER_KEY: owner}})
        volumes.append({"name": "unrelated", "config": {m.OWNER_KEY: owner}})
        directory.mkdir(parents=True)
        if mode != "missing-output":
            (directory / "output.xml").write_text('<robot><suite>' + '<test><status status="PASS"/></test>' * 3 + '</suite></robot>')
        return int(mode == "robot-failure")

    monkeypatch.setattr(runner, "_robot", execute)
    previous = os.umask(0o022)
    try:
        assert runner.main(["--snap-path", str(snap), "--outputdir", str(tmp_path)]) == (0 if mode == "pass" else 1)
        summary = json.loads((tmp_path / "smb-summary.json").read_text())
        assert summary["dryrun"] is False
        assert (tmp_path / "smb-results/private").stat().st_mode & 0o777 == 0o700
        assert (tmp_path / "smb-summary.json").stat().st_mode & 0o777 == 0o600
        if mode != "setup-failure":
            assert manifests == [library.manifest]
            assert any(row["name"] == "unrelated" for row in inventory)
            assert any(row["name"] == "unrelated" for row in volumes)
        if mode == "foreign-vm":
            assert not any(call[:2] == ("lxc", "delete") for call in calls)
            assert "Refusing foreign resource" in summary["errors"][-1]
        elif mode in ("foreign-volume", "attached-volume"):
            assert not any(call[:4] == ("lxc", "storage", "volume", "delete") for call in calls)
            assert summary["errors"]
        else:
            assert not any(row["name"].startswith("smb-ci-") for row in inventory + volumes)
        if mode == "uncertain-idle":
            assert "did not release" in summary["errors"][-1]
        # Cleanup observes only: never retry disable after an uncertain suite stop.
        assert not any("disable" in call for call in calls)
        before = len(calls)
        with pytest.raises(FileExistsError):
            runner.main(["--outputdir", str(tmp_path)])
        assert len(calls) == before
    finally:
        os.umask(previous)
    restored = os.umask(previous)
    assert restored == previous

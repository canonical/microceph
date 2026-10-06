"""Ceph 20 import compatibility and MicroCeph SMB orchestration contracts."""

import importlib
import json
import sys
import types
from pathlib import Path
from typing import Generic, TypeVar

import pytest

SOURCE_ROOT = Path(__file__).parents[1] / "src"
FIXTURES_ROOT = Path(__file__).parent / "fixtures"


class _OrchResult(Generic[TypeVar("T")]):
    pass


class _Attrs:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


class _DescriptionSpec(_Attrs):
    @classmethod
    def from_json(cls, data):
        spec = data.get("spec", {})
        return cls(service_id=data.get("service_id"), cluster_id=spec.get("cluster_id"),
                   config_uri=spec.get("config_uri"), placement=_Attrs(**data.get("placement", {})), source_json=data)

    def to_json(self):
        return self.source_json


def _install_ceph20_stubs(monkeypatch):
    """Stub external bindings, not MicroCeph's orchestration implementation."""
    names = ("ceph", "ceph.deployment", "ceph.deployment.inventory", "ceph.deployment.service_spec",
             "mgr_module", "orchestrator", "microceph.client", "microceph.client.client", "microceph.client.service")
    modules = {name: types.ModuleType(name) for name in names}
    for name in ("ceph", "ceph.deployment", "microceph.client"):
        modules[name].__path__ = []
    inventory = modules["ceph.deployment.inventory"]
    inventory.Device = type("Device", (), {})
    inventory.Devices = type("Devices", (), {})
    service_spec = modules["ceph.deployment.service_spec"]
    for name in ("ServiceSpec", "PlacementSpec", "RGWSpec", "MONSpec", "MDSSpec", "NFSServiceSpec"):
        setattr(service_spec, name, type(name, (_Attrs,), {}))
    service_spec.SMBSpec = _DescriptionSpec
    modules["ceph"].deployment = modules["ceph.deployment"]
    modules["ceph.deployment"].inventory = inventory
    modules["ceph.deployment"].service_spec = service_spec
    modules["mgr_module"].MgrModule = type("MgrModule", (), {})
    modules["mgr_module"].NotifyType = str

    class OrchestratorCLICommandBase:
        @classmethod
        def make_registry_subtype(cls, name):
            return type(name, (cls,), {"COMMANDS": {}})

        @classmethod
        def dump_cmd_list(cls):
            return list(cls.COMMANDS.values())

    class HostSpec:
        def __init__(self, hostname, addr=None, **kwargs):
            self.hostname, self.addr, self.status = hostname, addr, kwargs.get("status")

    orch = modules["orchestrator"]
    orch.Orchestrator = type("Orchestrator", (), {})
    orch.OrchestratorCLICommandBase = OrchestratorCLICommandBase
    orch.HostSpec = HostSpec
    for name in ("InventoryFilter", "InventoryHost", "ServiceDescription", "DaemonDescription"):
        setattr(orch, name, type(name, (_Attrs,), {}))
    orch.DaemonDescriptionStatus = types.SimpleNamespace(**{name: name for name in ("unknown", "error", "stopped", "running", "starting")})
    orch.handle_orch_error = lambda function: function
    orch.OrchResult = _OrchResult
    modules["microceph.client.client"].Client = type("Client", (), {})
    modules["microceph.client.service"].RemoteException = Exception
    modules["microceph.client"].client = modules["microceph.client.client"]
    modules["microceph.client"].service = modules["microceph.client.service"]
    for name, module in modules.items():
        monkeypatch.setitem(sys.modules, name, module)


def _load_module(monkeypatch):
    _install_ceph20_stubs(monkeypatch)
    monkeypatch.syspath_prepend(str(SOURCE_ROOT))
    for name in ("microceph", "microceph.module"):
        monkeypatch.delitem(sys.modules, name, raising=False)
    return importlib.import_module("microceph.module")


def test_manager_module_imports_with_ceph20_orchestrator(monkeypatch):
    """Ceph 20 no longer exports CLICommandMeta; use its registry interface."""
    module = _load_module(monkeypatch)
    assert module.MicroCephOrchestrator.__name__ == "MicroCephOrchestrator"
    assert module.MicroCephOrchestrator.CLICommand.__name__ == "MicroCephOrchestratorCLICommand"
    assert module.MicroCephOrchestrator.CLICommand.dump_cmd_list() == []
    assert module.daemon_spec_map["smb"] is module.SMBSpec


class _HostPlacement:
    def __init__(self, hostname, network="", name=""):
        self.hostname, self.network, self.name = hostname, network, name


class _SMBPlacement:
    def __init__(self, *, hosts=None, count=1, count_per_host=None, label=None, host_pattern=None):
        self.hosts, self.count, self.count_per_host = hosts or [], count, count_per_host
        self.label, self.host_pattern = label, host_pattern

    def filter_matching_hostspecs(self, hosts):
        available = [host.hostname for host in hosts]
        return [host.hostname for host in self.hosts if host.hostname in available] if self.hosts else available

    def get_target_count(self, hosts):
        return self.count if self.count is not None else len(self.filter_matching_hostspecs(hosts)) * (self.count_per_host or 1)


class _SMBSpec:
    service_id = cluster_id = "files"
    config_uri = "rados://.smb/files/config.smb"
    features = ["clustered"]
    cluster_meta_uri = "rados://.smb/files/cluster.meta.json"
    cluster_lock_uri = "rados://.smb/files/cluster.meta.lock"
    join_sources = []
    user_sources = ["rados:mon-config-key:smb/config/files/users-groups.0.json"]
    include_ceph_users = ["client.smb.fs.cluster.files"]
    placement = _SMBPlacement()

    def to_json(self):
        return json.loads((FIXTURES_ROOT / "smb-spec-user-direct.json").read_text())


class _SMBServices:
    def __init__(self, records):
        self.records = records
        self.group_configs = {}
        self.applied, self.removed, self.events = [], [], []
        self.apply_failures, self.remove_failures = {}, {}

    def list_services(self):
        return self.records

    def apply_smb(self, target, payload):
        self.applied.append((target, payload))
        self.events.append(("apply", target))
        if target in self.apply_failures:
            raise self.apply_failures[target]

    def remove_smb(self, target, cluster_id):
        self.removed.append((target, cluster_id))
        self.events.append(("remove", target))
        if target in self.remove_failures:
            raise self.remove_failures[target]

    def get_smb_group(self, cluster_id):
        config = next((r.get("group_config", "") for r in self.records
                       if r["service"] == "smb" and r["group_id"] == cluster_id), "")
        return {"cluster_id": cluster_id, "group_config": self.group_configs.get(cluster_id, config)}

    def finalize_smb(self, cluster_id, group_config):
        self.events.append(("finalize", cluster_id))


def _manager(monkeypatch, records=(), members=("node-a", "node-b"), offline=()):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    hosts = [{"name": name, "address": f"10.0.0.{i}:7443", "status": "UNREACHABLE" if name in offline else "ONLINE"}
             for i, name in enumerate(members, 1)]
    manager.microceph = types.SimpleNamespace(
        cluster=types.SimpleNamespace(get_cluster_members=lambda: hosts), services=_SMBServices(list(records)))
    manager.remote = lambda _module, _method, _resources: {"resources": []}
    return manager


def _smb_record(location, desired_spec=None, group="files", **config):
    record = {"service": "smb", "group_id": group, "location": location}
    if desired_spec is not None:
        record.update(info='{"config_uri":"rados://.smb/files/config.smb"}',
                      group_config=json.dumps(dict(desired_spec=desired_spec, **config)))
    return record


def _clustered_payload(spec, host, ranks, next_rank, cluster="files"):
    return {"service_spec": spec, "microceph": {
        "ctdb": {"rank": ranks[f"smb.{cluster}.{host}"], "identity": f"smb.{cluster}.{host}"},
        "ctdb_ranks": ranks, "next_ctdb_rank": next_rank}}


def _nonclustered_spec():
    spec = _SMBSpec()
    spec.features = []
    desired = spec.to_json()
    for field in ("features", "cluster_meta_uri", "cluster_lock_uri"):
        desired["spec"].pop(field, None)
    spec.to_json = lambda: desired
    return spec


def test_describe_smb_service_uses_grouped_service_state(monkeypatch):
    desired = _SMBSpec().to_json()
    manager = _manager(monkeypatch, [_smb_record(name, desired) for name in ("node-a", "node-b")])
    descriptions = manager.describe_service(service_type="smb", service_name="smb.files")
    assert len(descriptions) == 1
    assert descriptions[0].spec.to_json() == desired
    assert descriptions[0].running == 0


def test_describe_services_skips_malformed_smb_group(monkeypatch):
    manager = _manager(monkeypatch, [
        dict(_smb_record("node-a", group="broken"), group_config="not-json", info="{}"),
        {"service": "mon", "group_id": "", "location": "node-a", "info": "{}"},
    ])
    assert manager.describe_service(service_type="smb") == []
    # A malformed SMB record must not hide unrelated service descriptions.
    descriptions = manager.describe_service()
    assert len(descriptions) == 1 and descriptions[0].spec.service_type == "mon"


def test_list_smb_daemons_uses_grouped_members_and_reports_unknown(monkeypatch):
    desired = _SMBSpec().to_json()
    manager = _manager(monkeypatch, [_smb_record(name, desired) for name in ("node-a", "node-b")])
    descriptions = manager.list_daemons(service_name="smb.files", daemon_type="smb")
    assert [d.hostname for d in descriptions] == ["node-a", "node-b"]
    assert all(d.status == "unknown" and d.is_active is False for d in descriptions)
    filtered = manager.list_daemons(service_name="smb.files", daemon_type="smb", daemon_id="node-a", host="node-a")
    assert [d.hostname for d in filtered] == ["node-a"]


@pytest.mark.parametrize(("placement", "message"), [
    (_SMBPlacement(label="smb"), "labels"),
    (_SMBPlacement(host_pattern="node-*"), "host patterns"),
    (_SMBPlacement(hosts=[_HostPlacement("node-a", network="10.0.0.1")]), "host network overrides"),
    (_SMBPlacement(hosts=[_HostPlacement("node-a", name="smb.0")]), "daemon names"),
    (_SMBPlacement(hosts=[_HostPlacement("node-missing")]), "unknown MicroCeph members: node-missing"),
    (_SMBPlacement(count=3), "requests 3 daemons but only 2 hosts"),
])
def test_smb_target_hosts_rejects_invalid_placement(monkeypatch, placement, message):
    manager = _manager(monkeypatch)
    spec = _SMBSpec()
    spec.placement = placement
    with pytest.raises(ValueError, match=message):
        manager._smb_target_hosts(spec)


def test_smb_target_hosts_preserves_explicit_host_order(monkeypatch):
    manager = _manager(monkeypatch)
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-b"), _HostPlacement("node-a")], count=None)
    assert manager._smb_target_hosts(spec) == ["node-b", "node-a"]


@pytest.mark.parametrize(("attribute", "value"), [
    ("service_id", "other"), ("features", ["cephfs-proxy"]),
    ("custom_ports", {"ctdb": 14379}), ("custom_ports", {"smbmetrics": 19009}),
    ("custom_dns", ["192.0.2.53"]), ("include_ceph_users", ["client.smb.fs.cluster.files", "client.extra"]),
    ("remote_control_ssl_cert", "rados:mon-config-key:smb/cert"), ("unmanaged", True),
])
def test_apply_smb_rejects_unsupported_native_features(monkeypatch, attribute, value):
    manager = _manager(monkeypatch)
    spec = _SMBSpec()
    setattr(spec, attribute, value)
    with pytest.raises(ValueError, match="native SMB does not support"):
        manager.apply_smb(spec)
    assert manager.microceph.services.events == []


@pytest.mark.parametrize("count", [1, 2])
def test_apply_smb_nonclustered_member_limit(monkeypatch, count):
    manager = _manager(monkeypatch)
    spec = _nonclustered_spec()
    spec.placement = _SMBPlacement(count=count)
    if count == 1:
        manager.apply_smb(spec)
        assert manager.microceph.services.applied == [("node-a", spec.to_json())]
    else:
        with pytest.raises(ValueError, match="exactly one"):
            manager.apply_smb(spec)
        assert manager.microceph.services.events == []


@pytest.mark.parametrize("to_clustered", [True, False])
def test_apply_smb_rejects_clustering_mode_transition(monkeypatch, to_clustered):
    old = _nonclustered_spec() if to_clustered else _SMBSpec()
    new = _SMBSpec() if to_clustered else _nonclustered_spec()
    manager = _manager(monkeypatch, [_smb_record("node-a", old.to_json())])
    with pytest.raises(ValueError, match="cannot be changed"):
        manager.apply_smb(new)
    assert manager.microceph.services.events == []


def test_apply_smb_accepts_custom_smb_port_and_bind_network(monkeypatch):
    manager = _manager(monkeypatch)
    spec = _SMBSpec()
    spec.custom_ports, spec.bind_addrs = {"smb": 1445}, [{"network": "10.0.0.0/24"}]
    assert manager.apply_smb(spec) == "Applied SMB service 'files'"


def test_apply_smb_reconciles_members_and_sends_the_upstream_spec(monkeypatch):
    manager = _manager(monkeypatch, [_smb_record("node-old")])
    spec = _SMBSpec()
    assert manager.apply_smb(spec) == "Applied SMB service 'files'"
    ranks = {"smb.files.node-old": 0, "smb.files.node-a": 1}
    assert manager.microceph.services.applied == [("node-a", _clustered_payload(spec.to_json(), "node-a", ranks, 2))]
    assert manager.microceph.services.events == [("apply", "node-a"), ("remove", "node-old")]
    assert manager.microceph.services.removed == [("node-old", "files")]


def test_apply_smb_reconciles_clustered_members_and_ranks(monkeypatch):
    manager = _manager(monkeypatch, [_smb_record(n) for n in ("node-a", "node-b")], members=("node-a", "node-b", "node-c"))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-c"), _HostPlacement("node-b")], count=2)
    desired = spec.to_json()
    desired.update(features=["clustered"], cluster_meta_uri=spec.cluster_meta_uri, cluster_lock_uri=spec.cluster_lock_uri)
    spec.to_json = lambda: desired
    assert manager.apply_smb(spec) == "Applied SMB service 'files'"
    ranks = {"smb.files.node-a": 0, "smb.files.node-b": 1, "smb.files.node-c": 2}
    assert manager.microceph.services.applied == [(n, _clustered_payload(desired, n, ranks, 3)) for n in ("node-b", "node-c")]
    assert manager.microceph.services.events == [("apply", "node-b"), ("apply", "node-c"), ("remove", "node-a")]


def test_apply_smb_rejects_a_second_cluster_on_an_occupied_host(monkeypatch):
    manager = _manager(monkeypatch, [_smb_record("node-a", group="other")])
    with pytest.raises(ValueError, match="at most one SMB cluster"):
        manager.apply_smb(_SMBSpec())


def test_apply_smb_permits_a_second_cluster_on_disjoint_hosts(monkeypatch):
    manager = _manager(monkeypatch, [_smb_record("node-a")])
    spec = _SMBSpec()
    spec.service_id = spec.cluster_id = "second"
    spec.include_ceph_users = ["client.smb.fs.cluster.second"]
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-b")], count=None)
    assert manager.apply_smb(spec) == "Applied SMB service 'second'"
    assert manager.microceph.services.applied == [("node-b", _clustered_payload(spec.to_json(), "node-b", {"smb.second.node-b": 0}, 1, "second"))]
    assert manager.microceph.services.removed == []


@pytest.mark.parametrize("recorded_spec", [False, True])
def test_apply_smb_reapplies_unchanged_placement(monkeypatch, recorded_spec):
    """Stable URIs may contain changed RADOS data; the node decides whether to restart."""
    spec = _SMBSpec()
    manager = _manager(monkeypatch, [_smb_record("node-a", spec.to_json() if recorded_spec else None)])
    assert manager.apply_smb(spec) == manager.apply_smb(spec) == "Applied SMB service 'files'"
    expected = _clustered_payload(spec.to_json(), "node-a", {"smb.files.node-a": 0}, 1)
    assert manager.microceph.services.applied == [("node-a", expected)] * 2
    assert manager.microceph.services.removed == []


def test_smb_ranks_survive_remove_add_and_rejoin(monkeypatch):
    desired = _SMBSpec().to_json()
    ranks = {"smb.files.node-a": 0, "smb.files.node-b": 1}
    manager = _manager(monkeypatch,
                       [_smb_record(n, desired, ctdb_ranks=ranks, next_ctdb_rank=2) for n in ("node-a", "node-b")],
                       members=("node-a", "node-b", "node-c"))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-b"), _HostPlacement("node-c")], count=2)
    manager.apply_smb(spec)
    payloads = dict(manager.microceph.services.applied)
    assert payloads["node-b"]["microceph"]["ctdb"]["rank"] == 1
    assert payloads["node-c"]["microceph"]["ctdb"]["rank"] == 2
    ranks = {**ranks, "smb.files.node-c": 2}
    assert payloads["node-c"]["microceph"]["ctdb_ranks"] == ranks
    assert payloads["node-c"]["microceph"]["next_ctdb_rank"] == 3
    manager.microceph.services.records = [_smb_record(n, desired, ctdb_ranks=ranks, next_ctdb_rank=3) for n in ("node-b", "node-c")]
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-a")], count=1)
    manager.microceph.services.applied.clear()
    manager.apply_smb(spec)
    assert manager.microceph.services.applied[0][1]["microceph"]["ctdb"] == {"rank": 0, "identity": "smb.files.node-a"}


@pytest.mark.parametrize(("members", "count", "expected", "excess"), [
    (("node-a", "node-b", "node-c"), 2, ["node-b", "node-c"], 3),
    (("node-a", "node-b"), None, ["node-b"], 2),
])
def test_count_and_default_placement_skip_offline_hosts(monkeypatch, members, count, expected, excess):
    manager = _manager(monkeypatch, members=members, offline=("node-a",))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(count=count)
    assert manager._smb_target_hosts(spec) == expected
    manager.apply_smb(spec)
    assert [n for n, _ in manager.microceph.services.applied] == expected
    spec.placement = _SMBPlacement(count=excess)
    with pytest.raises(ValueError, match=f"only {len(expected)} hosts"):
        manager.apply_smb(spec)


def test_smb_rank_state_recovers_partial_member_metadata(monkeypatch):
    record = _smb_record("node-b", _SMBSpec().to_json())
    record["info"] = json.dumps({"ctdb_rank": 4, "ctdb_identity": "smb.files.node-b"})
    manager = _manager(monkeypatch, [record], members=("node-b", "node-c"))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(count=2)
    manager.apply_smb(spec)
    payloads = dict(manager.microceph.services.applied)
    assert payloads["node-b"]["microceph"]["ctdb"]["rank"] == 4
    assert payloads["node-c"]["microceph"]["ctdb"]["rank"] == 5


def test_apply_smb_attempts_every_member_but_preserves_sources_after_failure(monkeypatch):
    manager = _manager(monkeypatch, [_smb_record("node-old", _SMBSpec().to_json())], members=("node-a", "node-b", "node-old"))
    manager.microceph.services.apply_failures["node-a"] = RuntimeError("node-a unavailable")
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-a"), _HostPlacement("node-b")], count=2)
    with pytest.raises(RuntimeError, match="node-a unavailable"):
        manager.apply_smb(spec)
    assert manager.microceph.services.events == [("apply", "node-a"), ("apply", "node-b")]
    assert manager.microceph.services.removed == []
    manager.microceph.services.apply_failures.clear()
    manager.apply_smb(spec)
    assert manager.microceph.services.removed == [("node-old", "files")]


def test_remove_absent_smb_service_is_idempotent_and_accepts_force(monkeypatch):
    manager = _manager(monkeypatch)
    # Exercise the upstream keyword argument, rather than just inspecting the signature.
    first = manager.remove_service("smb.files", force=True, force_delete_data=True)
    assert first == manager.remove_service("smb.files") == "Removed SMB service 'files'"
    assert manager.microceph.services.removed == []


@pytest.mark.parametrize(("failure", "resource_survives"), [(False, False), (True, False), (False, True)])
def test_remove_smb_service_attempts_every_member(monkeypatch, failure, resource_survives):
    manager = _manager(monkeypatch, [_smb_record(n) for n in ("node-a", "node-b")])
    if resource_survives:
        manager.remote = lambda *_args: {"resource_type": "ceph.smb.cluster", "cluster_id": "files"}
    if failure:
        manager.microceph.services.remove_failures["node-a"] = RuntimeError("node-a unavailable")
        with pytest.raises(RuntimeError, match="node-a unavailable"):
            manager.remove_service("smb.files")
    else:
        assert manager.remove_service("smb.files") == "Removed SMB service 'files'"
    assert manager.microceph.services.removed == [("node-a", "files"), ("node-b", "files")]
    expected = [("remove", "node-a"), ("remove", "node-b")]
    if not failure and not resource_survives:
        expected.append(("finalize", "files"))
    assert manager.microceph.services.events == expected


@pytest.mark.parametrize("offline", [(), ("node-pending",)])
def test_remove_smb_service_drains_zero_receipt_reservations(monkeypatch, offline):
    manager = _manager(monkeypatch, members=("node-a", "node-b", "node-pending"), offline=offline)
    manager.microceph.services.group_configs["files"] = json.dumps(
        {"ctdb_ranks": {"smb.files.node-pending": 0}, "next_ctdb_rank": 1})
    manager.remove_service("smb.files")
    assert manager.microceph.services.removed == [("node-pending", "files")]
    assert manager.microceph.services.events == [("remove", "node-pending"), ("finalize", "files")]


@pytest.mark.parametrize("retired_state", ["removed_member", "different_cluster"])
def test_remove_smb_service_skips_confirmed_retired_rank_owner(monkeypatch, retired_state):
    records = [_smb_record("node-a")]
    members = ("node-a",)
    if retired_state == "different_cluster":
        members = ("node-a", "node-retired")
        records.append(_smb_record("node-retired", group="other"))
    manager = _manager(monkeypatch, records, members=members)
    manager.microceph.services.group_configs["files"] = json.dumps(
        {"ctdb_ranks": {"smb.files.node-retired": 0, "smb.files.node-a": 1}, "next_ctdb_rank": 2})
    manager.microceph.services.remove_failures["node-retired"] = RuntimeError(
        "target is no longer a cluster member" if retired_state == "removed_member"
        else "SMB service already manages cluster 'other' on this host"
    )

    assert manager.remove_service("smb.files") == "Removed SMB service 'files'"
    assert manager.microceph.services.removed == [("node-a", "files")]

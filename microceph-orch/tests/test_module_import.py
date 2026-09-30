"""Compatibility tests for loading the MicroCeph manager module."""

import importlib
import inspect
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


def _install_ceph20_stubs(monkeypatch):
    """Install the Ceph 20 import surface needed while loading the module."""
    ceph = types.ModuleType("ceph")
    ceph.__path__ = []
    deployment = types.ModuleType("ceph.deployment")
    deployment.__path__ = []
    inventory = types.ModuleType("ceph.deployment.inventory")
    service_spec = types.ModuleType("ceph.deployment.service_spec")

    class Device:
        pass

    class Devices:
        pass

    class ServiceSpec:
        pass

    class PlacementSpec:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class RGWSpec:
        pass

    class MONSpec:
        pass

    class MDSSpec:
        pass

    class NFSServiceSpec:
        pass

    class SMBSpec:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

        @classmethod
        def from_json(cls, data):
            spec = data.get("spec", {})
            return cls(
                service_id=data.get("service_id"),
                cluster_id=spec.get("cluster_id"),
                config_uri=spec.get("config_uri"),
                placement=PlacementSpec(**data.get("placement", {})),
                source_json=data,
            )

        def to_json(self):
            if hasattr(self, "source_json"):
                return self.source_json
            return {
                "service_id": self.service_id,
                "spec": {
                    "cluster_id": self.cluster_id,
                    "config_uri": self.config_uri,
                },
            }

    inventory.Device = Device
    inventory.Devices = Devices
    service_spec.ServiceSpec = ServiceSpec
    service_spec.PlacementSpec = PlacementSpec
    service_spec.RGWSpec = RGWSpec
    service_spec.MONSpec = MONSpec
    service_spec.MDSSpec = MDSSpec
    service_spec.NFSServiceSpec = NFSServiceSpec
    service_spec.SMBSpec = SMBSpec
    ceph.deployment = deployment
    deployment.inventory = inventory
    deployment.service_spec = service_spec

    mgr_module = types.ModuleType("mgr_module")

    class MgrModule:
        pass

    mgr_module.MgrModule = MgrModule
    mgr_module.NotifyType = str

    orchestrator = types.ModuleType("orchestrator")

    class Orchestrator:
        pass

    class OrchestratorCLICommandBase:
        @classmethod
        def make_registry_subtype(cls, name):
            return type(name, (cls,), {"COMMANDS": {}})

        @classmethod
        def dump_cmd_list(cls):
            return list(cls.COMMANDS.values())

    class HostSpec:
        def __init__(self, hostname, addr=None, **kwargs):
            self.hostname = hostname
            self.addr = addr
            self.status = kwargs.get("status")

    class InventoryFilter:
        pass

    class InventoryHost:
        pass

    class ServiceDescription:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class DaemonDescriptionStatus:
        unknown = "unknown"
        error = "error"
        stopped = "stopped"
        running = "running"
        starting = "starting"

    class DaemonDescription:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    def handle_orch_error(function):
        return function

    orchestrator.Orchestrator = Orchestrator
    orchestrator.OrchestratorCLICommandBase = OrchestratorCLICommandBase
    orchestrator.HostSpec = HostSpec
    orchestrator.InventoryFilter = InventoryFilter
    orchestrator.InventoryHost = InventoryHost
    orchestrator.ServiceDescription = ServiceDescription
    orchestrator.DaemonDescription = DaemonDescription
    orchestrator.DaemonDescriptionStatus = DaemonDescriptionStatus
    orchestrator.handle_orch_error = handle_orch_error
    orchestrator.OrchResult = _OrchResult

    client_package = types.ModuleType("microceph.client")
    client_package.__path__ = []
    client = types.ModuleType("microceph.client.client")

    class Client:
        pass

    client.Client = Client
    service = types.ModuleType("microceph.client.service")
    service.RemoteException = Exception
    client_package.client = client
    client_package.service = service

    for name, module in {
        "ceph": ceph,
        "ceph.deployment": deployment,
        "ceph.deployment.inventory": inventory,
        "ceph.deployment.service_spec": service_spec,
        "mgr_module": mgr_module,
        "orchestrator": orchestrator,
        "microceph.client": client_package,
        "microceph.client.client": client,
        "microceph.client.service": service,
    }.items():
        monkeypatch.setitem(sys.modules, name, module)


def test_manager_module_imports_with_ceph20_orchestrator(monkeypatch):
    """Ceph 20 no longer exports the legacy CLICommandMeta symbol."""
    _install_ceph20_stubs(monkeypatch)
    monkeypatch.syspath_prepend(str(SOURCE_ROOT))

    for module_name in ("microceph", "microceph.module"):
        monkeypatch.delitem(sys.modules, module_name, raising=False)

    module = importlib.import_module("microceph.module")

    assert module.MicroCephOrchestrator.__name__ == "MicroCephOrchestrator"
    assert module.MicroCephOrchestrator.CLICommand.__name__ == "MicroCephOrchestratorCLICommand"
    assert module.MicroCephOrchestrator.CLICommand.dump_cmd_list() == []


def test_manager_registers_smb_for_service_description(monkeypatch):
    module = _load_module(monkeypatch)

    assert module.daemon_spec_map["smb"] is module.SMBSpec


class _HostPlacement:
    def __init__(self, hostname, network="", name=""):
        self.hostname = hostname
        self.network = network
        self.name = name


class _SMBPlacement:
    def __init__(
        self,
        *,
        hosts=None,
        count=1,
        count_per_host=None,
        label=None,
        host_pattern=None,
    ):
        self.hosts = hosts or []
        self.count = count
        self.count_per_host = count_per_host
        self.label = label
        self.host_pattern = host_pattern

    def filter_matching_hostspecs(self, hosts):
        available = [host.hostname for host in hosts]
        if self.hosts:
            return [host.hostname for host in self.hosts if host.hostname in available]
        return available

    def get_target_count(self, hosts):
        if self.count is not None:
            return self.count
        return len(self.filter_matching_hostspecs(hosts)) * (self.count_per_host or 1)


class _SMBSpec:
    service_id = "files"
    cluster_id = "files"
    config_uri = "rados://.smb/files/config.smb"
    features = ["clustered"]
    cluster_meta_uri = "rados://.smb/files/cluster.meta.json"
    cluster_lock_uri = "rados://.smb/files/cluster.meta.lock"
    join_sources = []
    user_sources = ["rados:mon-config-key:smb/config/files/users-groups.0.json"]
    include_ceph_users = ["client.smb.fs.cluster.files"]
    placement = _SMBPlacement()

    def to_json(self):
        return json.loads(
            (FIXTURES_ROOT / "smb-spec-user-direct.json").read_text()
        )


class _SMBServices:
    def __init__(self, records):
        self.records = records
        self.applied = []
        self.removed = []
        self.events = []
        self.apply_failures = {}
        self.remove_failures = {}

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


class _SMBCluster:
    def __init__(self, members, offline=()):
        self.members = members
        self.offline = offline

    def get_cluster_members(self):
        return [
            {
                "name": name,
                "address": f"10.0.0.{index}:7443",
                "status": "UNREACHABLE" if name in self.offline else "ONLINE",
            }
            for index, name in enumerate(self.members, start=1)
        ]


class _SMBClient:
    def __init__(self, records, members=("node-a", "node-b"), offline=()):
        self.cluster = _SMBCluster(members, offline)
        self.services = _SMBServices(records)


def _smb_record(location, desired_spec):
    return {
        "service": "smb",
        "group_id": "files",
        "location": location,
        "info": '{"config_uri":"rados://.smb/files/config.smb"}',
        "group_config": json.dumps({"desired_spec": desired_spec}),
    }


def test_describe_smb_service_uses_grouped_service_state(monkeypatch):
    module = _load_module(monkeypatch)
    desired_spec = json.loads(
        (FIXTURES_ROOT / "smb-spec-user-direct.json").read_text()
    )
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([
        _smb_record("node-a", desired_spec),
        _smb_record("node-b", desired_spec),
    ])

    descriptions = manager.describe_service(service_type="smb", service_name="smb.files")

    assert len(descriptions) == 1
    assert descriptions[0].spec.to_json() == desired_spec
    assert descriptions[0].running == 0


def test_describe_services_skips_malformed_smb_group(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([
        {
            "service": "smb",
            "group_id": "broken",
            "location": "node-a",
            "group_config": "not-json",
            "info": "{}",
        },
        {
            "service": "mon",
            "group_id": "",
            "location": "node-a",
            "info": "{}",
        },
    ])

    descriptions = manager.describe_service(service_type="smb")

    assert descriptions == []


def test_list_smb_daemons_uses_grouped_members_and_reports_unknown(monkeypatch):
    module = _load_module(monkeypatch)
    desired_spec = _SMBSpec().to_json()
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([
        _smb_record("node-a", desired_spec),
        _smb_record("node-b", desired_spec),
    ])

    descriptions = manager.list_daemons(service_name="smb.files", daemon_type="smb")

    assert [description.hostname for description in descriptions] == ["node-a", "node-b"]
    assert all(
        description.status == module.DaemonDescriptionStatus.unknown
        for description in descriptions
    )
    assert all(description.is_active is False for description in descriptions)

    filtered = manager.list_daemons(
        service_name="smb.files",
        daemon_type="smb",
        daemon_id="node-a",
        host="node-a",
    )
    assert [description.hostname for description in filtered] == ["node-a"]


def _load_module(monkeypatch):
    _install_ceph20_stubs(monkeypatch)
    monkeypatch.syspath_prepend(str(SOURCE_ROOT))

    for module_name in ("microceph", "microceph.module"):
        monkeypatch.delitem(sys.modules, module_name, raising=False)

    return importlib.import_module("microceph.module")


@pytest.mark.parametrize(
    ("placement", "description"),
    [
        (_SMBPlacement(label="smb"), "labels"),
        (_SMBPlacement(host_pattern="node-*"), "host patterns"),
        (
            _SMBPlacement(hosts=[_HostPlacement("node-a", network="10.0.0.1")]),
            "host network overrides",
        ),
        (
            _SMBPlacement(hosts=[_HostPlacement("node-a", name="smb.0")]),
            "daemon names",
        ),
    ],
)
def test_smb_target_hosts_rejects_unsupported_placement_expressions(
    monkeypatch, placement, description
):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _SMBSpec()
    spec.placement = placement

    with pytest.raises(ValueError, match=description):
        manager._smb_target_hosts(spec)


def test_smb_target_hosts_rejects_unknown_explicit_hostname(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-missing")])

    with pytest.raises(ValueError, match="unknown MicroCeph members: node-missing"):
        manager._smb_target_hosts(spec)


def test_smb_target_hosts_preserves_explicit_host_order(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(
        hosts=[_HostPlacement("node-b"), _HostPlacement("node-a")],
        count=None,
    )

    assert manager._smb_target_hosts(spec) == ["node-b", "node-a"]


def test_smb_target_hosts_rejects_count_above_available_members(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(count=3)

    with pytest.raises(ValueError, match="requests 3 daemons but only 2 hosts"):
        manager._smb_target_hosts(spec)


def test_apply_smb_reconciles_members_and_sends_the_upstream_spec(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [{"service": "smb", "group_id": "files", "location": "node-old"}]
    )

    result = manager.apply_smb(_SMBSpec())

    assert result == "Applied SMB service 'files'"
    desired_spec = json.loads(
        (FIXTURES_ROOT / "smb-spec-user-direct.json").read_text()
    )
    assert manager.microceph.services.applied == [
        ("node-a", {
            "service_spec": desired_spec,
            "microceph": {
                "ctdb": {"rank": 1, "identity": "smb.files.node-a"},
                "ctdb_ranks": {"smb.files.node-old": 0, "smb.files.node-a": 1},
                "next_ctdb_rank": 2,
            },
        })
    ]
    assert manager.microceph.services.events == [
        ("apply", "node-a"),
        ("remove", "node-old"),
    ]
    assert manager.microceph.services.removed == [("node-old", "files")]


@pytest.mark.parametrize(
    ("attribute", "value"),
    [
        ("service_id", "other"),
        ("features", ["cephfs-proxy"]),
        ("custom_ports", {"ctdb": 14379}),
        ("custom_ports", {"smbmetrics": 19009}),
        ("custom_dns", ["192.0.2.53"]),
        ("include_ceph_users", ["client.smb.fs.cluster.files", "client.extra"]),
        ("remote_control_ssl_cert", "rados:mon-config-key:smb/cert"),
        ("unmanaged", True),
    ],
)
def test_apply_smb_rejects_unsupported_native_features(monkeypatch, attribute, value):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _SMBSpec()
    setattr(spec, attribute, value)

    with pytest.raises(ValueError, match="native SMB does not support"):
        manager.apply_smb(spec)


def _nonclustered_spec():
    spec = _SMBSpec()
    spec.features = []
    desired = spec.to_json()
    for field in ("features", "cluster_meta_uri", "cluster_lock_uri"):
        desired["spec"].pop(field, None)
    spec.to_json = lambda: desired
    return spec


def test_apply_smb_accepts_one_nonclustered_member(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _nonclustered_spec()
    manager.apply_smb(spec)
    assert manager.microceph.services.applied == [("node-a", spec.to_json())]


def test_apply_smb_rejects_multiple_nonclustered_members(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _nonclustered_spec()
    spec.placement = _SMBPlacement(count=2)
    with pytest.raises(ValueError, match="exactly one"):
        manager.apply_smb(spec)
    assert manager.microceph.services.events == []


@pytest.mark.parametrize("to_clustered", [True, False])
def test_apply_smb_rejects_clustering_mode_transition(monkeypatch, to_clustered):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    old = _nonclustered_spec() if to_clustered else _SMBSpec()
    new = _SMBSpec() if to_clustered else _nonclustered_spec()
    manager.microceph = _SMBClient([_smb_record("node-a", old.to_json())])
    with pytest.raises(ValueError, match="cannot be changed"):
        manager.apply_smb(new)
    assert manager.microceph.services.events == []


def test_apply_smb_accepts_custom_smb_port_and_bind_network(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])
    spec = _SMBSpec()
    spec.custom_ports = {"smb": 1445}
    spec.bind_addrs = [{"network": "10.0.0.0/24"}]

    result = manager.apply_smb(spec)

    assert result == "Applied SMB service 'files'"


def test_apply_smb_reconciles_clustered_members_and_ranks(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [
            {"service": "smb", "group_id": "files", "location": "node-a"},
            {"service": "smb", "group_id": "files", "location": "node-b"},
        ],
        members=("node-a", "node-b", "node-c"),
    )
    spec = _SMBSpec()
    spec.features = ["clustered"]
    spec.cluster_meta_uri = "rados://.smb/files/cluster.meta.json"
    spec.cluster_lock_uri = "rados://.smb/files/cluster.meta.lock"
    spec.placement = _SMBPlacement(
        hosts=[_HostPlacement("node-c"), _HostPlacement("node-b")],
        count=2,
    )
    desired_spec = _SMBSpec().to_json()
    desired_spec.update(
        {
            "features": ["clustered"],
            "cluster_meta_uri": spec.cluster_meta_uri,
            "cluster_lock_uri": spec.cluster_lock_uri,
        }
    )
    spec.to_json = lambda: desired_spec

    result = manager.apply_smb(spec)

    assert result == "Applied SMB service 'files'"
    assert manager.microceph.services.applied == [
        (
            "node-b",
            {
                "service_spec": desired_spec,
                "microceph": {
                    "ctdb": {"rank": 1, "identity": "smb.files.node-b"},
                    "ctdb_ranks": {
                        "smb.files.node-a": 0,
                        "smb.files.node-b": 1,
                        "smb.files.node-c": 2,
                    },
                    "next_ctdb_rank": 3,
                },
            },
        ),
        (
            "node-c",
            {
                "service_spec": desired_spec,
                "microceph": {
                    "ctdb": {"rank": 2, "identity": "smb.files.node-c"},
                    "ctdb_ranks": {
                        "smb.files.node-a": 0,
                        "smb.files.node-b": 1,
                        "smb.files.node-c": 2,
                    },
                    "next_ctdb_rank": 3,
                },
            },
        ),
    ]
    assert manager.microceph.services.events == [
        ("apply", "node-b"),
        ("apply", "node-c"),
        ("remove", "node-a"),
    ]


def test_apply_smb_rejects_a_second_cluster_on_an_occupied_host(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [{"service": "smb", "group_id": "other", "location": "node-a"}]
    )

    with pytest.raises(ValueError, match="at most one SMB cluster"):
        manager.apply_smb(_SMBSpec())


def test_apply_smb_permits_a_second_cluster_on_disjoint_hosts(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [{"service": "smb", "group_id": "files", "location": "node-a"}]
    )
    spec = _SMBSpec()
    spec.service_id = "second"
    spec.cluster_id = "second"
    spec.include_ceph_users = ["client.smb.fs.cluster.second"]
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-b")], count=None)

    result = manager.apply_smb(spec)

    assert result == "Applied SMB service 'second'"
    desired_spec = _SMBSpec().to_json()
    assert manager.microceph.services.applied == [
        ("node-b", {
            "service_spec": desired_spec,
            "microceph": {
                            "ctdb": {"rank": 0, "identity": "smb.second.node-b"},
                            "ctdb_ranks": {"smb.second.node-b": 0},
                            "next_ctdb_rank": 1,
                        },
        })
    ]
    assert manager.microceph.services.removed == []


def test_apply_smb_is_idempotent_for_an_unchanged_placement(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [{"service": "smb", "group_id": "files", "location": "node-a"}]
    )
    spec = _SMBSpec()

    first = manager.apply_smb(spec)
    second = manager.apply_smb(spec)

    assert first == second == "Applied SMB service 'files'"
    payload = {
        "service_spec": spec.to_json(),
        "microceph": {
            "ctdb": {"rank": 0, "identity": "smb.files.node-a"},
            "ctdb_ranks": {"smb.files.node-a": 0},
            "next_ctdb_rank": 1,
        },
    }
    assert manager.microceph.services.applied == [
        ("node-a", payload),
        ("node-a", payload),
    ]
    assert manager.microceph.services.removed == []


def test_smb_ranks_survive_remove_add_and_rejoin(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    desired = _SMBSpec().to_json()
    old_config = {
        "desired_spec": desired,
        "ctdb_ranks": {"smb.files.node-a": 0, "smb.files.node-b": 1},
        "next_ctdb_rank": 2,
    }
    manager.microceph = _SMBClient(
        [_smb_record(name, desired) for name in ("node-a", "node-b")],
        members=("node-a", "node-b", "node-c"),
    )
    for record in manager.microceph.services.records:
        record["group_config"] = json.dumps(old_config)
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(
        hosts=[_HostPlacement("node-b"), _HostPlacement("node-c")], count=2
    )

    manager.apply_smb(spec)
    payloads = dict(manager.microceph.services.applied)
    assert payloads["node-b"]["microceph"]["ctdb"]["rank"] == 1
    assert payloads["node-c"]["microceph"]["ctdb"]["rank"] == 2
    assert payloads["node-c"]["microceph"]["ctdb_ranks"] == {
        "smb.files.node-a": 0, "smb.files.node-b": 1, "smb.files.node-c": 2
    }
    assert payloads["node-c"]["microceph"]["next_ctdb_rank"] == 3

    # The retired rank remains in shared state after node-a is removed.
    new_config = {
        "desired_spec": desired,
        "ctdb_ranks": payloads["node-c"]["microceph"]["ctdb_ranks"],
        "next_ctdb_rank": 3,
    }
    manager.microceph.services.records = [
        _smb_record(name, desired) for name in ("node-b", "node-c")
    ]
    for record in manager.microceph.services.records:
        record["group_config"] = json.dumps(new_config)
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-a")], count=1)
    manager.microceph.services.applied.clear()
    manager.apply_smb(spec)
    assert manager.microceph.services.applied[0][1]["microceph"]["ctdb"] == {
        "rank": 0, "identity": "smb.files.node-a"
    }


def test_count_placement_skips_offline_hosts(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([], members=("node-a", "node-b", "node-c"), offline=("node-a",))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(count=2)
    assert manager._smb_target_hosts(spec) == ["node-b", "node-c"]
    manager.apply_smb(spec)
    assert [name for name, _ in manager.microceph.services.applied] == ["node-b", "node-c"]

    spec.placement = _SMBPlacement(count=3)
    with pytest.raises(ValueError, match="only 2 hosts are available"):
        manager.apply_smb(spec)


def test_default_placement_counts_only_online_candidates(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([], members=("node-a", "node-b"), offline=("node-a",))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(count=None)
    assert manager._smb_target_hosts(spec) == ["node-b"]
    spec.placement = _SMBPlacement(count=2)
    with pytest.raises(ValueError, match="only 1 hosts"):
        manager._smb_target_hosts(spec)


def test_smb_rank_state_recovers_partial_member_metadata(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    record = _smb_record("node-b", _SMBSpec().to_json())
    record["info"] = json.dumps({"ctdb_rank": 4, "ctdb_identity": "smb.files.node-b"})
    manager.microceph = _SMBClient([record], members=("node-b", "node-c"))
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(count=2)
    manager.apply_smb(spec)
    payloads = dict(manager.microceph.services.applied)
    assert payloads["node-b"]["microceph"]["ctdb"]["rank"] == 4
    assert payloads["node-c"]["microceph"]["ctdb"]["rank"] == 5


def test_apply_smb_attempts_every_member_but_preserves_sources_after_failure(monkeypatch):
    module = _load_module(monkeypatch)
    desired = _SMBSpec().to_json()
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [_smb_record("node-old", desired)],
        members=("node-a", "node-b", "node-old"),
    )
    manager.microceph.services.apply_failures["node-a"] = RuntimeError("node-a unavailable")
    spec = _SMBSpec()
    spec.placement = _SMBPlacement(hosts=[_HostPlacement("node-a"), _HostPlacement("node-b")], count=2)

    with pytest.raises(RuntimeError, match="node-a unavailable"):
        manager.apply_smb(spec)

    assert manager.microceph.services.events == [
        ("apply", "node-a"),
        ("apply", "node-b"),
    ]
    assert manager.microceph.services.removed == []

    manager.microceph.services.apply_failures.clear()
    manager.apply_smb(spec)
    assert manager.microceph.services.removed == [("node-old", "files")]


def test_apply_smb_reapplies_unchanged_spec_to_refresh_rados_config(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([_smb_record("node-a", _SMBSpec().to_json())])
    spec = _SMBSpec()
    manager.apply_smb(spec)
    manager.apply_smb(spec)
    assert len(manager.microceph.services.applied) == 2


def test_remove_service_accepts_force_delete_data_for_ceph_compatibility(monkeypatch):
    module = _load_module(monkeypatch)
    parameters = inspect.signature(module.MicroCephOrchestrator.remove_service).parameters
    assert "force_delete_data" in parameters


def test_remove_absent_smb_service_is_idempotent_and_accepts_force(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])

    first = manager.remove_service("smb.files", force=True)
    second = manager.remove_service("smb.files")

    assert first == second == "Removed SMB service 'files'"
    assert manager.microceph.services.removed == []


def test_remove_smb_service_attempts_every_member_after_failure(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([
        {"service": "smb", "group_id": "files", "location": "node-a"},
        {"service": "smb", "group_id": "files", "location": "node-b"},
    ])
    manager.microceph.services.remove_failures["node-a"] = RuntimeError("node-a unavailable")

    with pytest.raises(RuntimeError, match="node-a unavailable"):
        manager.remove_service("smb.files")

    assert manager.microceph.services.removed == [
        ("node-a", "files"),
        ("node-b", "files"),
    ]


def test_remove_smb_service_removes_every_placed_member(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [
            {"service": "smb", "group_id": "files", "location": "node-a"},
            {"service": "smb", "group_id": "files", "location": "node-b"},
        ]
    )

    result = manager.remove_service("smb.files")

    assert result == "Removed SMB service 'files'"
    assert manager.microceph.services.removed == [
        ("node-a", "files"),
        ("node-b", "files"),
    ]

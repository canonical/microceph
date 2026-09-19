"""Compatibility tests for loading the MicroCeph manager module."""

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
        def __init__(self, hostname, *_args, **_kwargs):
            self.hostname = hostname

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
    features = []
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

    def list_services(self):
        return self.records

    def apply_smb(self, target, payload):
        self.applied.append((target, payload))
        self.events.append(("apply", target))

    def remove_smb(self, target, cluster_id):
        self.removed.append((target, cluster_id))
        self.events.append(("remove", target))


class _SMBCluster:
    def get_cluster_members(self):
        return [
            {"name": "node-a", "address": "10.0.0.1:7443", "status": "online"},
            {"name": "node-b", "address": "10.0.0.2:7443", "status": "online"},
        ]


class _SMBClient:
    def __init__(self, records):
        self.cluster = _SMBCluster()
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
    assert manager.microceph.services.applied == [("node-a", desired_spec)]
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
        ("custom_ports", {"smb": 1445}),
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
    assert manager.microceph.services.applied == [("node-b", desired_spec)]
    assert manager.microceph.services.removed == []


def test_remove_smb_service_accepts_the_orchestrator_force_argument(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient([])

    result = manager.remove_service("smb.files", force=True)

    assert result == "Removed SMB service 'files'"


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

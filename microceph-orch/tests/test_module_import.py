"""Compatibility tests for loading the MicroCeph manager module."""

import importlib
import sys
import types
from pathlib import Path
from typing import Generic, TypeVar

import pytest


SOURCE_ROOT = Path(__file__).parents[1] / "src"


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

    class DaemonDescription:
        pass

    def handle_orch_error(function):
        return function

    orchestrator.Orchestrator = Orchestrator
    orchestrator.OrchestratorCLICommandBase = OrchestratorCLICommandBase
    orchestrator.HostSpec = HostSpec
    orchestrator.InventoryFilter = InventoryFilter
    orchestrator.InventoryHost = InventoryHost
    orchestrator.ServiceDescription = ServiceDescription
    orchestrator.DaemonDescription = DaemonDescription
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


class _SMBPlacement:
    count = 1
    count_per_host = None

    def filter_matching_hostspecs(self, hosts):
        return [host.hostname for host in hosts]

    def get_target_count(self, _hosts):
        return self.count


class _SMBSpec:
    service_id = "files"
    cluster_id = "files"
    config_uri = "rados://.smb/files/config.smb"
    features = []
    join_sources = []
    user_sources = ["rados:mon-config-key:smb/config/files/users-groups.0.json"]
    include_ceph_users = ["client.smb.fs.cluster.files"]
    placement = _SMBPlacement()


class _SMBServices:
    def __init__(self, records):
        self.records = records
        self.applied = []
        self.removed = []

    def list_services(self):
        return self.records

    def apply_smb(self, target, payload):
        self.applied.append((target, payload))

    def remove_smb(self, target, cluster_id):
        self.removed.append((target, cluster_id))


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


def test_describe_smb_service_reconstructs_a_valid_smb_spec(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [
            {
                "service": "smb",
                "group_id": "files",
                "location": "node-a",
                "info": '{"config_uri":"rados://.smb/files/config.smb"}',
            }
        ]
    )

    descriptions = manager.describe_service(service_type="smb")

    assert len(descriptions) == 1
    spec = descriptions[0].spec
    assert isinstance(spec, module.SMBSpec)
    assert spec.cluster_id == "files"
    assert spec.config_uri == "rados://.smb/files/config.smb"
    assert spec.placement.hosts == ["node-a"]
    assert spec.placement.count == 1


def _load_module(monkeypatch):
    _install_ceph20_stubs(monkeypatch)
    monkeypatch.syspath_prepend(str(SOURCE_ROOT))

    for module_name in ("microceph", "microceph.module"):
        monkeypatch.delitem(sys.modules, module_name, raising=False)

    return importlib.import_module("microceph.module")


def test_apply_smb_reconciles_members_and_sends_the_upstream_spec(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [{"service": "smb", "group_id": "files", "location": "node-old"}]
    )

    result = manager.apply_smb(_SMBSpec())

    assert result == "Applied SMB service 'files'"
    assert manager.microceph.services.applied == [
        (
            "node-a",
            {
                "cluster_id": "files",
                "config_uri": "rados://.smb/files/config.smb",
                "features": [],
                "join_sources": [],
                "user_sources": [
                    "rados:mon-config-key:smb/config/files/users-groups.0.json"
                ],
            },
        )
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


def test_apply_smb_rejects_a_second_cluster(monkeypatch):
    module = _load_module(monkeypatch)
    manager = module.MicroCephOrchestrator.__new__(module.MicroCephOrchestrator)
    manager.microceph = _SMBClient(
        [{"service": "smb", "group_id": "other", "location": "node-a"}]
    )

    with pytest.raises(ValueError, match="only one SMB cluster"):
        manager.apply_smb(_SMBSpec())


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

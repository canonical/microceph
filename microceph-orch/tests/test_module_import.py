"""Compatibility tests for loading the MicroCeph manager module."""

import importlib
import json
import sys
import types
from pathlib import Path
from typing import Generic, TypeVar


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
        def __new__(cls, **kwargs):
            # Real ServiceSpec dispatches SMB to SMBSpec, which requires a full
            # cluster_id/config_uri. Partial observed rows cannot construct it.
            if cls is ServiceSpec and kwargs.get("service_type") == "smb":
                raise ValueError("a valid cluster_id is required")
            return object.__new__(cls)

        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class PlacementSpec(types.SimpleNamespace):
        pass

    class RGWSpec:
        pass

    class MONSpec:
        pass

    class MDSSpec:
        pass

    class NFSServiceSpec(ServiceSpec):
        pass

    class SMBSpec(ServiceSpec):
        def __init__(self, **kwargs):
            if not kwargs.get("cluster_id") or not kwargs.get("config_uri"):
                raise ValueError("SMB inventory requires observed cluster_id/config_uri")
            super().__init__(**kwargs)

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
        pass

    class InventoryFilter:
        pass

    class InventoryHost:
        pass

    class ServiceDescription(types.SimpleNamespace):
        pass

    class DaemonDescription(types.SimpleNamespace):
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


def test_smb_inventory_uses_observed_endpoints(monkeypatch):
    _install_ceph20_stubs(monkeypatch)
    monkeypatch.syspath_prepend(str(SOURCE_ROOT))
    for name in ("microceph", "microceph.module"):
        monkeypatch.delitem(sys.modules, name, raising=False)
    module = importlib.import_module("microceph.module")
    # New complete, legacy partial, and missing observations: no resource/config
    # fetches and no invented default frontend endpoint for older rows.
    rows = [
        {"service": "smb", "group_id": "alpha", "location": "node1", "info": json.dumps({
            "config_uri": "rados://.smb/alpha/observed-config.json",
            "frontend_address": "2001:db8::7", "smb_port": 1445,
            "config": {"password": "must-not-escape"}})},
        {"service": "smb", "group_id": "alpha", "location": "node2", "info": json.dumps({
            "config_uri": "rados://.smb/alpha/observed-config.json", "smb_port": 445})},
        {"service": "smb", "group_id": "beta", "location": "node3", "info": ""},
    ]
    adapter = object.__new__(module.MicroCephOrchestrator)
    adapter.microceph = types.SimpleNamespace(services=types.SimpleNamespace(list_services=lambda: rows))
    daemons = adapter.list_daemons()
    assert [(d.service_name, d.hostname, d.ip, d.ports) for d in daemons] == [
        ("smb.alpha", "node1", "2001:db8::7", [1445]),
        ("smb.alpha", "node2", None, [445]), ("smb.beta", "node3", None, None)]
    assert daemons[0].status_desc is None
    assert daemons[1].status_desc is None
    rows[2]['info'] = '{malformed legacy observation'
    assert adapter.list_daemons()[2].ip is None
    descriptions = adapter.describe_service()
    assert [(d.spec.service_id, d.running, d.spec.unmanaged) for d in descriptions] == [
        ("alpha", 2, True)]
    assert descriptions[0].spec.cluster_id == "alpha"
    assert descriptions[0].spec.config_uri == "rados://.smb/alpha/observed-config.json"
    assert descriptions[0].spec.placement.hosts == ["node1", "node2"]
    assert "must-not-escape" not in repr([vars(d) for d in daemons + descriptions])
    assert not hasattr(adapter, "apply_smb")

    # Never guess a source for legacy rows or select one host's source when
    # applied references disagree. Other services and all daemon rows survive.
    rows.append({"service": "nfs", "group_id": "files", "location": "node4",
                 "info": '{"bind_address":"0.0.0.0","bind_port":2049}'})
    for info in ("", "{malformed", "null", "[]", '{"smb_port":445}',
                 '{"config_uri":42}', '{"config_uri":"https://user:secret@host/config"}',
                 '{"config_uri":"rados://.smb/other/config.smb"}',
                 '{"config_uri":"rados://.smb/alpha/other-config.json"}'):
        rows[1]["info"] = info
        assert [d.spec.service_id for d in adapter.describe_service()] == ["files"]
        assert len(adapter.list_daemons()) == 4

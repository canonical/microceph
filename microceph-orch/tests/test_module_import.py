"""Compatibility tests for loading the MicroCeph manager module."""

import importlib
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
        pass

    class PlacementSpec:
        pass

    class RGWSpec:
        pass

    class MONSpec:
        pass

    class MDSSpec:
        pass

    class NFSServiceSpec:
        pass

    inventory.Device = Device
    inventory.Devices = Devices
    service_spec.ServiceSpec = ServiceSpec
    service_spec.PlacementSpec = PlacementSpec
    service_spec.RGWSpec = RGWSpec
    service_spec.MONSpec = MONSpec
    service_spec.MDSSpec = MDSSpec
    service_spec.NFSServiceSpec = NFSServiceSpec
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

    class ServiceDescription:
        pass

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

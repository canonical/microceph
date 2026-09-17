"""Unit tests for the MicroCeph manager's extended API client."""

import importlib.util
import json
import sys
import types
from pathlib import Path


SOURCE_ROOT = Path(__file__).parents[1] / "src" / "microceph" / "client"


def _load_cluster_module(monkeypatch):
    """Load the client modules without importing the Ceph manager package."""
    package_name = "test_microceph_client"
    package = types.ModuleType(package_name)
    package.__path__ = [str(SOURCE_ROOT)]
    monkeypatch.setitem(sys.modules, package_name, package)

    service_name = f"{package_name}.service"
    service = types.ModuleType(service_name)

    class BaseService:
        pass

    service.BaseService = BaseService
    monkeypatch.setitem(sys.modules, service_name, service)

    module_name = f"{package_name}.cluster"
    spec = importlib.util.spec_from_file_location(module_name, SOURCE_ROOT / "cluster.py")
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, module_name, module)
    spec.loader.exec_module(module)

    return module


class _RequestRecorder:
    def __init__(self):
        self.request = None

    def _put(self, path, **kwargs):
        self.request = (path, kwargs)
        return {"metadata": {"result": "ok"}}

    def _delete(self, path, **kwargs):
        self.request = (path, kwargs)
        return {"metadata": {"result": "ok"}}


def test_apply_smb_waits_for_targeted_services_api_result(monkeypatch):
    cluster = _load_cluster_module(monkeypatch)

    class Service(_RequestRecorder, cluster.ExtendedAPIService):
        pass

    service = Service()
    payload = {
        "service_type": "smb",
        "service_id": "files",
        "spec": {
            "cluster_id": "files",
            "config_uri": "rados://.smb/files/config.smb",
        },
    }

    result = service.apply_smb("node a", payload)

    assert result == {"result": "ok"}
    assert service.request[0] == "/1.0/services/smb?target=node+a"
    assert service.request[1]["json"] == {
        "name": "smb",
        "bool": True,
        "payload": json.dumps(payload),
    }


def test_remove_smb_waits_for_targeted_services_api_result(monkeypatch):
    cluster = _load_cluster_module(monkeypatch)

    class Service(_RequestRecorder, cluster.ExtendedAPIService):
        pass

    service = Service()

    result = service.remove_smb("node-a", "files")

    assert result == {"result": "ok"}
    assert service.request[0] == "/1.0/services/smb?target=node-a"
    assert service.request[1]["json"] == {"cluster_id": "files"}

"""Unit tests for the MicroCeph manager's extended API client."""

import importlib.util
import json
import sys
import types
from pathlib import Path

import pytest


SOURCE_ROOT = Path(__file__).parents[1] / "src" / "microceph" / "client"


def _load_client_module(monkeypatch):
    """Load the top-level client with lightweight dependency stubs."""
    package_name = "test_microceph_top_client"
    package = types.ModuleType(package_name)
    package.__path__ = [str(SOURCE_ROOT)]
    monkeypatch.setitem(sys.modules, package_name, package)

    class Session:
        def mount(self, *_args):
            pass

    requests = types.ModuleType("requests")
    requests.sessions = types.SimpleNamespace(Session=Session)
    unixsocket = types.ModuleType("requests_unixsocket")
    unixsocket.DEFAULT_SCHEME = "http+unix://"
    unixsocket.UnixAdapter = object
    snaphelpers = types.ModuleType("snaphelpers")
    snaphelpers.Snap = object
    monkeypatch.setitem(sys.modules, "requests", requests)
    monkeypatch.setitem(sys.modules, "requests_unixsocket", unixsocket)
    monkeypatch.setitem(sys.modules, "snaphelpers", snaphelpers)

    cluster_name = f"{package_name}.cluster"
    cluster = types.ModuleType(cluster_name)

    class Service:
        def __init__(self, session, endpoint, certs, timeout=None):
            self.timeout = timeout

    cluster.StatusService = Service
    cluster.ExtendedAPIService = Service
    cluster.MicroClusterService = Service
    monkeypatch.setitem(sys.modules, cluster_name, cluster)

    module_name = f"{package_name}.client"
    spec = importlib.util.spec_from_file_location(module_name, SOURCE_ROOT / "client.py")
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, module_name, module)
    spec.loader.exec_module(module)
    return module


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


def test_client_sets_bounded_extended_api_timeout(monkeypatch):
    client_module = _load_client_module(monkeypatch)

    client = client_module.Client("http+unix://socket")

    assert client.services.timeout == 300


class _RequestRecorder:
    def __init__(self):
        self.request = None
        self.metadata = {"result": "ok"}

    def _put(self, path, **kwargs):
        self.request = (path, kwargs)
        return {"metadata": self.metadata}

    _get = _delete = _put


@pytest.fixture
def service(monkeypatch):
    cluster = _load_cluster_module(monkeypatch)

    class Service(_RequestRecorder, cluster.ExtendedAPIService):
        pass

    return Service()


def test_apply_smb_waits_for_targeted_services_api_result(service):
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


def test_remove_smb_waits_for_targeted_services_api_result(service):
    result = service.remove_smb("node-a", "files")

    assert result == {"result": "ok"}
    assert service.request[0] == "/1.0/services/smb?target=node-a"
    assert service.request[1]["json"] == {"cluster_id": "files"}


def test_get_smb_group_uses_internal_reservation_endpoint(service):
    service.metadata = {"group_config": "{}"}
    assert service.get_smb_group("files") == {"group_config": "{}"}
    assert service.request[0] == "/1.0/services/smb?cluster_id=files"


def test_finalize_smb_uses_internal_finalization_signal(service):
    config = '{"ctdb_ranks":{}}'
    assert service.finalize_smb("files", config) == {"result": "ok"}
    assert service.request == ("/1.0/services/smb", {
        "json": {"cluster_id": "files", "finalize": True, "group_config": config}})


def test_get_smb_local_state_uses_targeted_internal_observation_endpoint(service):
    service.metadata = {"cluster_id": "files", "local_cluster_id": "files"}

    assert service.get_smb_local_state("node a", "files") == service.metadata
    assert service.request == (
        "/1.0/services/smb?cluster_id=files&local=true&target=node+a", {})

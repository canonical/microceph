"""Focused tests of SMB orch request translation, not native service lifecycle."""
import importlib
import json
import sys
import types
import pytest

from test_module_import import SOURCE_ROOT, _install_ceph20_stubs


@pytest.fixture
def orch_request(monkeypatch):
    _install_ceph20_stubs(monkeypatch)
    monkeypatch.syspath_prepend(str(SOURCE_ROOT))
    for name in ("microceph", "microceph.module"):
        monkeypatch.delitem(sys.modules, name, raising=False)
    module = importlib.import_module("microceph.module")
    adapter = object.__new__(module.MicroCephOrchestrator)
    calls = []
    services = types.SimpleNamespace(
        apply_smb=lambda *args: calls.append(args),
        remove_smb=lambda *args: calls.append(args),
        list_services=lambda: [],
    )
    adapter.microceph = types.SimpleNamespace(
        services=services,
        cluster=types.SimpleNamespace(get_cluster_members=lambda: [{"name": "node1"}, {"name": "node2"}]),
    )
    document = {"service_type": "smb", "service_id": "alpha", "spec": {
        "cluster_id": "alpha", "config_uri": "rados://.smb/alpha/config.smb"}}
    spec = types.SimpleNamespace(
        service_type="smb", service_id="alpha", cluster_id="alpha", unmanaged=False,
        preview_only=False, networks=[], config={}, extra_container_args=[],
        extra_entrypoint_args=[], custom_configs=[], to_json=lambda: document,
        placement=types.SimpleNamespace(hosts=[types.SimpleNamespace(hostname="node2", network="", name="")],
                                        count=None, count_per_host=None, label=None, host_pattern=None),
    )
    return adapter, spec, calls


def test_apply_only_requests_explicit_targets(orch_request):
    adapter, spec, calls = orch_request
    assert "requested" in adapter.apply_smb(spec).lower()
    assert calls == [("node2", "alpha", spec.to_json())]


@pytest.mark.parametrize("invalid", ["identity", "selector", "member", "shrink"])
def test_rejects_invalid_request_before_any_placement(orch_request, invalid):
    adapter, spec, calls = orch_request
    if invalid == "identity":
        spec.service_id = "different"
    elif invalid == "selector":
        spec.placement.label = "storage"
    elif invalid == "member":
        spec.placement.hosts[0].hostname = "unknown"
    else:
        adapter.microceph.services.list_services = lambda: [{"service": "smb", "group_id": "alpha", "location": "node1"}]
    with pytest.raises(ValueError):
        adapter.apply_smb(spec)
    assert calls == []


def test_immediate_placement_failure_is_not_success(orch_request):
    adapter, spec, calls = orch_request
    def fail(*args):
        raise RuntimeError("API unavailable")
    adapter.microceph.services.apply_smb = fail
    with pytest.raises(RuntimeError, match="API unavailable"):
        adapter.apply_smb(spec)
    assert calls == []


def test_removal_finds_unpublished_owner_and_never_purges(orch_request):
    adapter, spec, calls = orch_request
    # Fence even not-yet-published requests; Go retires only exact owners and
    # cannot stop another group's shared apps when this group is absent.
    adapter.remove_service("smb.alpha")
    assert calls == [("node1", "alpha"), ("node2", "alpha")]
    with pytest.raises(ValueError):
        adapter.remove_service("smb.alpha", force=True)
    assert calls == [("node1", "alpha"), ("node2", "alpha")]


def test_service_client_uses_async_wire_flag_and_nonpurging_delete(monkeypatch):
    package = types.ModuleType("orch_http_client")
    package.__path__ = [str(SOURCE_ROOT / "microceph" / "client")]
    monkeypatch.setitem(sys.modules, "orch_http_client", package)
    cluster = importlib.import_module("orch_http_client.cluster")
    calls = []
    response = types.SimpleNamespace(text="{}", raise_for_status=lambda: None, json=lambda: {"metadata": {}})
    def send(**kwargs):
        calls.append(kwargs)
        return response
    client = cluster.ExtendedAPIService(types.SimpleNamespace(request=send), "http+unix://socket")
    spec = {"service_type": "smb", "service_id": "alpha", "spec": {"cluster_id": "alpha"}}
    client.apply_smb("node2", "alpha", spec)
    put = calls[-1]
    assert put["method"] == "put"
    assert put["params"] == {"target": "node2"}
    assert put["timeout"] == 30
    assert put["json"]["name"] == "smb" and put["json"]["bool"] is False
    assert json.loads(put["json"]["payload"]) == {"cluster_id": "alpha", "orchestrator_spec": spec}
    client.remove_smb("node2", "alpha")
    assert calls[-1]["method"] == "delete"
    assert calls[-1]["json"] == {"cluster_id": "alpha", "purge": False, "orchestrated": True}

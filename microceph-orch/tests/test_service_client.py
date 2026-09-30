"""Response handling tests independent of the Ceph manager environment."""

import importlib.util
import json
import sys
import types
from pathlib import Path

import pytest


@pytest.fixture
def service_module(monkeypatch):
    # Only the transport is stubbed; load the real response handling code.
    class HTTPError(Exception):
        def __init__(self, message, response=None):
            super().__init__(message)
            self.response = response

    exceptions = types.ModuleType("requests.exceptions")
    exceptions.HTTPError = HTTPError
    exceptions.ConnectionError = type("ConnectionError", (Exception,), {})
    sessions = types.ModuleType("requests.sessions")
    sessions.Session = object
    monkeypatch.setitem(sys.modules, "requests.exceptions", exceptions)
    monkeypatch.setitem(sys.modules, "requests.sessions", sessions)
    source = Path(__file__).parents[1] / "src/microceph/client/service.py"
    spec = importlib.util.spec_from_file_location("test_service_client_module", source)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def client_response(module, payload, http_status=200):
    class Response:
        text = json.dumps(payload)

        def json(self):
            return payload

        def raise_for_status(self):
            if http_status >= 400:
                raise module.HTTPError("HTTP request failed", response=self)

    response = Response()
    session = types.SimpleNamespace(request=lambda **kwargs: response)
    return module.BaseService(session, "http+unix://socket"), response


@pytest.mark.parametrize("http_status", [200, 500])
def test_error_envelope_is_not_success(service_module, http_status):
    client, response = client_response(service_module, {
        "type": "error", "error_code": 500,
        "error": "SMB placement failed", "metadata": None,
    }, http_status)
    with pytest.raises(service_module.HTTPError) as error:
        client._put("/1.0/services/smb", json={"name": "smb"})
    assert error.value.response is response


def test_failed_sync_response_is_not_success(service_module):
    client, _ = client_response(service_module, {
        "type": "sync", "status": "Failure", "status_code": 400,
        "error": "", "metadata": None,
    })
    with pytest.raises(service_module.HTTPError):
        client._put("/1.0/services/smb")


@pytest.mark.parametrize("http_status", [200, 503])
def test_error_envelope_preserves_exception_translation(service_module, http_status):
    client, _ = client_response(service_module, {
        "type": "error", "error_code": 503,
        "error": "Database is not yet initialized",
    }, http_status)
    with pytest.raises(service_module.ClusterServiceUnavailableException):
        client._get("/1.0/services")


def test_success_envelope_is_unchanged(service_module):
    payload = {"type": "sync", "status": "Success", "status_code": 200, "metadata": None}
    client, _ = client_response(service_module, payload)
    assert client._put("/1.0/services/smb") == payload

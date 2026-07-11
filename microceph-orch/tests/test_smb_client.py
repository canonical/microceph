# SPDX-FileCopyrightText: 2026 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import json

import pytest

from microceph.client.cluster import ExtendedAPIService


class FakeResponse:
    def __init__(self, payload=None, status=200):
        self._payload = payload if payload is not None else {}
        self.status = status
        self.text = json.dumps(self._payload)

    def raise_for_status(self):
        if self.status >= 400:
            from requests.exceptions import HTTPError

            raise HTTPError(response=self)

    def json(self):
        return self._payload


class FakeSession:
    """Records requests and replays canned responses."""

    def __init__(self, response=None):
        self.calls = []
        self.response = response or FakeResponse()

    def request(self, method, url, **kwargs):
        self.calls.append({"method": method, "url": url, **kwargs})
        return self.response


ENDPOINT = "http+unix://%2Fpath%2Fcontrol.socket"


@pytest.fixture
def session():
    return FakeSession()


@pytest.fixture
def service(session):
    return ExtendedAPIService(session, ENDPOINT, None)


def test_apply_smb_puts_spec_json(service, session):
    spec_json = '{"service_type": "smb", "cluster_id": "dev"}'

    service.apply_smb(spec_json)

    assert len(session.calls) == 1
    call = session.calls[0]
    assert call["method"] == "put"
    assert call["url"] == f"{ENDPOINT}/1.0/services/smb"
    assert call["data"] == spec_json


def test_remove_smb_deletes_with_cluster_id(service, session):
    service.remove_smb("dev")

    assert len(session.calls) == 1
    call = session.calls[0]
    assert call["method"] == "delete"
    assert call["url"] == f"{ENDPOINT}/1.0/services/smb"
    assert call["json"] == {"cluster_id": "dev"}


def test_list_smb_returns_metadata(session):
    session.response = FakeResponse(
        {"metadata": [{"cluster_id": "dev", "placed_on": ["m1"]}]}
    )
    service = ExtendedAPIService(session, ENDPOINT, None)

    statuses = service.list_smb()

    assert statuses == [{"cluster_id": "dev", "placed_on": ["m1"]}]
    assert session.calls[0]["method"] == "get"
    assert session.calls[0]["url"] == f"{ENDPOINT}/1.0/services/smb"


def test_apply_smb_surfaces_api_errors(session):
    from requests.exceptions import HTTPError

    session.response = FakeResponse({"error": "field 'bind_addrs' is not supported in Phase 1"}, status=400)
    service = ExtendedAPIService(session, ENDPOINT, None)

    with pytest.raises(HTTPError):
        service.apply_smb("{}")

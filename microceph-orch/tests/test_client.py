"""
Tests for the MicroCeph client layer (cluster.py).
"""

import json
import pytest
from unittest.mock import MagicMock, patch

from microceph.client.cluster import (
    MicroClusterService,
    StatusService,
    ExtendedAPIService,
)
from microceph.client.service import RemoteException


@pytest.fixture
def mock_session():
    return MagicMock()


@pytest.fixture
def endpoint():
    return "http+unix://%2Ftmp%2Ftest.socket"


# ===================================================================
# MicroClusterService
# ===================================================================

class TestMicroClusterService:
    def test_get_cluster_members(self, mock_session, endpoint):
        svc = MicroClusterService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {
                "metadata": [
                    {"name": "n1", "address": "10.0.0.1:7443", "status": "ONLINE", "extra": "ignored"},
                    {"name": "n2", "address": "10.0.0.2:7443", "status": "ONLINE"},
                ]
            },
        )
        members = svc.get_cluster_members()
        assert len(members) == 2
        assert members[0] == {"name": "n1", "address": "10.0.0.1:7443", "status": "ONLINE"}
        # "extra" key should be filtered out
        assert "extra" not in members[0]

    def test_get_cluster_members_null_metadata(self, mock_session, endpoint):
        svc = MicroClusterService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        members = svc.get_cluster_members()
        assert members == []

    def test_get_cluster_members_missing_metadata(self, mock_session, endpoint):
        svc = MicroClusterService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {},
        )
        members = svc.get_cluster_members()
        assert members == []


# ===================================================================
# ExtendedAPIService
# ===================================================================

class TestExtendedAPIService:
    def test_list_services_returns_list(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": [{"service": "mon", "location": "n1"}]},
        )
        result = svc.list_services()
        assert isinstance(result, list)
        assert len(result) == 1

    def test_list_services_null_metadata(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        result = svc.list_services()
        assert result == []

    def test_list_disks_null_metadata(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        result = svc.list_disks()
        assert result == []

    def test_list_resources_null_metadata(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        result = svc.list_resources()
        assert result == []

    def test_enable_service_payload(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        svc.enable_service(name="rgw", payload='{"Port": 8080}', wait=True)

        call_args = mock_session.request.call_args
        body = call_args.kwargs.get("json") or call_args[1].get("json")
        assert body["name"] == "rgw"
        assert body["bool"] is True
        assert body["payload"] == '{"Port": 8080}'

    def test_enable_service_wait_false(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        svc.enable_service(name="mon", wait=False)

        call_args = mock_session.request.call_args
        body = call_args.kwargs.get("json") or call_args[1].get("json")
        assert body["bool"] is False

    def test_delete_service(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {},
        )
        svc.delete_service("rgw")
        call_args = mock_session.request.call_args
        assert call_args.kwargs.get("method") == "delete" or call_args[0][0] == "delete"

    def test_delete_nfs_service(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {},
        )
        svc.delete_nfs_service("mycluster")
        call_args = mock_session.request.call_args
        body = call_args.kwargs.get("json") or call_args[1].get("json")
        assert body == {"cluster_id": "mycluster"}

    def test_restart_services(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {},
        )
        svc.restart_services(["mon", "rgw"])
        call_args = mock_session.request.call_args
        body = call_args.kwargs.get("json") or call_args[1].get("json")
        assert body == [{"service": "mon"}, {"service": "rgw"}]

    def test_get_status_null_metadata(self, mock_session, endpoint):
        svc = ExtendedAPIService(mock_session, endpoint)
        mock_session.request.return_value = MagicMock(
            status_code=200,
            text='{}',
            json=lambda: {"metadata": None},
        )
        result = svc.get_status()
        assert result == {}

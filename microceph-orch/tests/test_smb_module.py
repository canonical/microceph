# SPDX-FileCopyrightText: 2026 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import json
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

from microceph.module import MicroCephOrchestrator


class FakeSMBSpec:
    service_id = "dev"
    cluster_id = "dev"

    def to_json(self):
        # Mirrors the real ServiceSpec.to_json(): subclass fields nested
        # under "spec", not flat (verified against the packed mgr tree).
        return {
            "service_type": "smb",
            "service_id": "dev",
            "placement": {"hosts": ["m1"]},
            "spec": {
                "cluster_id": "dev",
                "config_uri": "rados://.smb/dev/scc.dev.json",
            },
        }


@pytest.fixture
def orch():
    # Bypass __init__: it dials the microcephd unix socket.
    instance = object.__new__(MicroCephOrchestrator)
    instance.microceph = SimpleNamespace(services=MagicMock())
    return instance


def test_apply_smb_serializes_spec(orch):
    result = orch.apply_smb(FakeSMBSpec())

    orch.microceph.services.apply_smb.assert_called_once()
    payload = json.loads(orch.microceph.services.apply_smb.call_args[0][0])
    assert payload["cluster_id"] == "dev"
    assert payload["config_uri"] == "rados://.smb/dev/scc.dev.json"
    assert "spec" not in payload
    assert "smb.dev" in result


def test_remove_service_routes_smb(orch):
    result = orch.remove_service("smb.dev")

    orch.microceph.services.remove_smb.assert_called_once_with("dev")
    assert "smb.dev" in result


def test_remove_service_rejects_other_services(orch):
    with pytest.raises(NotImplementedError):
        orch.remove_service("nfs.foo")

    with pytest.raises(NotImplementedError):
        orch.remove_service("smb")

    orch.microceph.services.remove_smb.assert_not_called()

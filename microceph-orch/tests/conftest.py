"""
Test fixtures and mock setup for microceph-orch tests.

Installs mock modules into sys.modules so that Ceph-internal and
snap-only imports work outside the snap environment.
"""

import sys
from unittest.mock import MagicMock, patch

import pytest

from stubs import (
    OrchResult,
    handle_orch_error,
    CLICommandMeta,
    HostSpec,
    PlacementSpec,
    ServiceSpec,
    RGWSpec,
    MONSpec,
    MDSSpec,
    NFSServiceSpec,
    Device,
    Devices,
    InventoryFilter,
    InventoryHost,
    ServiceDescription,
    DaemonDescription,
    Orchestrator,
    MgrModule,
    NotifyType,
)


# ---------------------------------------------------------------------------
# Install mocks into sys.modules BEFORE any microceph imports
# ---------------------------------------------------------------------------

def _install_mocks():
    """Inject mock modules so `from ceph.deployment...` etc. work.

    Also mocks snap-specific dependencies (requests_unixsocket, snaphelpers)
    that are only available inside the snap environment.
    """

    # ceph.deployment.inventory
    inv_mod = MagicMock()
    inv_mod.Device = Device
    inv_mod.Devices = Devices

    # ceph.deployment.service_spec
    spec_mod = MagicMock()
    spec_mod.ServiceSpec = ServiceSpec
    spec_mod.PlacementSpec = PlacementSpec
    spec_mod.RGWSpec = RGWSpec
    spec_mod.MONSpec = MONSpec
    spec_mod.MDSSpec = MDSSpec
    spec_mod.NFSServiceSpec = NFSServiceSpec

    # ceph.deployment
    deployment_mod = MagicMock()
    deployment_mod.inventory = inv_mod
    deployment_mod.service_spec = spec_mod

    # ceph
    ceph_mod = MagicMock()
    ceph_mod.deployment = deployment_mod

    # mgr_module
    mgr_mod = MagicMock()
    mgr_mod.MgrModule = MgrModule
    mgr_mod.NotifyType = NotifyType

    # orchestrator
    orch_mod = MagicMock()
    orch_mod.Orchestrator = Orchestrator
    orch_mod.HostSpec = HostSpec
    orch_mod.InventoryFilter = InventoryFilter
    orch_mod.InventoryHost = InventoryHost
    orch_mod.ServiceDescription = ServiceDescription
    orch_mod.DaemonDescription = DaemonDescription
    orch_mod.CLICommandMeta = CLICommandMeta
    orch_mod.handle_orch_error = handle_orch_error
    orch_mod.OrchResult = OrchResult

    # snap-only deps
    requests_unixsocket_mod = MagicMock()
    requests_unixsocket_mod.DEFAULT_SCHEME = "http+unix://"
    snaphelpers_mod = MagicMock()

    sys.modules.update({
        "ceph": ceph_mod,
        "ceph.deployment": deployment_mod,
        "ceph.deployment.inventory": inv_mod,
        "ceph.deployment.service_spec": spec_mod,
        "mgr_module": mgr_mod,
        "orchestrator": orch_mod,
        "requests_unixsocket": requests_unixsocket_mod,
        "snaphelpers": snaphelpers_mod,
    })


# Install mocks before pytest collects test modules
_install_mocks()


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def mock_client():
    """Return a mock Client with pre-wired service stubs."""
    client = MagicMock()

    # Default: cluster with 3 members
    client.cluster.get_cluster_members.return_value = [
        {"name": "node1", "address": "10.0.0.1:7443", "status": "ONLINE"},
        {"name": "node2", "address": "10.0.0.2:7443", "status": "ONLINE"},
        {"name": "node3", "address": "10.0.0.3:7443", "status": "ONLINE"},
    ]

    # Default: no services running
    client.services.list_services.return_value = []

    # Default: no disks
    client.services.list_disks.return_value = []

    # Default: no resources
    client.services.list_resources.return_value = []

    # Default: status available
    client.status.is_available.return_value = None

    return client


@pytest.fixture
def orchestrator(mock_client):
    """Return a MicroCephOrchestrator with a mocked client."""
    from microceph.module import MicroCephOrchestrator

    with patch.object(MicroCephOrchestrator, "__init__", lambda self, *a, **kw: None):
        orch = MicroCephOrchestrator.__new__(MicroCephOrchestrator)
        orch.microceph = mock_client
        orch.run = True
    return orch

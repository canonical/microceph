"""
Tests for MicroCephOrchestrator methods.
"""

import json
import pytest
from unittest.mock import MagicMock

from stubs import (
    HostSpec,
    PlacementSpec,
    ServiceSpec,
    RGWSpec,
    NFSServiceSpec,
    InventoryFilter,
    OrchestratorError,
    OrchestratorValidationError,
)

from microceph.client.service import RemoteException


def _svc(service, location, group_id="", info=""):
    """Build a MicroCeph service record as returned by list_services()."""
    return {"service": service, "location": location,
            "group_id": group_id, "info": info}


# ===================================================================
# available()
# ===================================================================

class TestAvailable:
    def test_available_success(self, orchestrator, mock_client):
        ok, msg, info = orchestrator.available()
        assert ok is True
        assert msg == ""
        mock_client.status.is_available.assert_called_once()

    def test_available_remote_error(self, orchestrator, mock_client):
        mock_client.status.is_available.side_effect = RemoteException("socket gone")
        ok, msg, _ = orchestrator.available()
        assert ok is False
        assert "Cannot reach" in msg

    def test_available_unexpected_error(self, orchestrator, mock_client):
        mock_client.status.is_available.side_effect = OSError("permission denied")
        ok, msg, _ = orchestrator.available()
        assert ok is False
        assert "Unexpected error" in msg


# ===================================================================
# get_hosts()
# ===================================================================

class TestGetHosts:
    def test_get_hosts_basic(self, orchestrator, mock_client):
        result = orchestrator.get_hosts()
        hosts = result.result
        assert len(hosts) == 3
        assert hosts[0].hostname == "node1"
        assert hosts[0].addr == "10.0.0.1"
        assert hosts[0].status == "ONLINE"

    def test_get_hosts_strips_port(self, orchestrator, mock_client):
        mock_client.cluster.get_cluster_members.return_value = [
            {"name": "h1", "address": "192.168.1.100:9443", "status": "ONLINE"},
        ]
        result = orchestrator.get_hosts()
        assert result.result[0].addr == "192.168.1.100"

    def test_get_hosts_no_port_in_address(self, orchestrator, mock_client):
        mock_client.cluster.get_cluster_members.return_value = [
            {"name": "h1", "address": "192.168.1.100", "status": "ONLINE"},
        ]
        result = orchestrator.get_hosts()
        # Should fall back to raw address when no ":" present
        assert result.result[0].addr == "192.168.1.100"

    def test_get_hosts_ipv6_address(self, orchestrator, mock_client):
        mock_client.cluster.get_cluster_members.return_value = [
            # bracketed IPv6 with port
            {"name": "h1", "address": "[fe80::1]:7443", "status": "ONLINE"},
            # bare IPv6 literal, no port
            {"name": "h2", "address": "fe80::2", "status": "ONLINE"},
        ]
        result = orchestrator.get_hosts()
        assert result.result[0].addr == "fe80::1"
        assert result.result[1].addr == "fe80::2"

    def test_get_hosts_missing_address(self, orchestrator, mock_client):
        mock_client.cluster.get_cluster_members.return_value = [
            {"name": "h1", "status": "ONLINE"},
        ]
        result = orchestrator.get_hosts()
        # Missing address should not crash
        assert result.result[0].hostname == "h1"

    def test_get_hosts_empty_cluster(self, orchestrator, mock_client):
        mock_client.cluster.get_cluster_members.return_value = []
        result = orchestrator.get_hosts()
        assert result.result == []

    def test_get_hosts_api_error(self, orchestrator, mock_client):
        mock_client.cluster.get_cluster_members.side_effect = RemoteException("fail")
        result = orchestrator.get_hosts()
        assert result.exception is not None


# ===================================================================
# describe_service()
# ===================================================================

class TestDescribeService:
    def test_describe_service_all(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "mon", "location": "node2", "group_id": "", "info": ""},
            {"service": "rgw", "location": "node1", "group_id": "", "info": ""},
        ]
        result = orchestrator.describe_service()
        descs = result.result
        assert len(descs) == 2  # mon and rgw grouped
        types = {d.spec.service_type for d in descs}
        assert types == {"mon", "rgw"}

    def test_describe_service_filter_type(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "rgw", "location": "node1", "group_id": "", "info": ""},
        ]
        result = orchestrator.describe_service(service_type="rgw")
        descs = result.result
        assert len(descs) == 1
        assert descs[0].spec.service_type == "rgw"

    def test_describe_service_with_group_id(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "nfs", "location": "node1", "group_id": "mycluster", "info": "{}"},
        ]
        result = orchestrator.describe_service()
        descs = result.result
        assert len(descs) == 1
        assert descs[0].spec.service_id == "mycluster"

    def test_describe_service_empty(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = []
        result = orchestrator.describe_service()
        assert result.result == []

    def test_describe_service_running_count(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "mon", "location": "node2", "group_id": "", "info": ""},
            {"service": "mon", "location": "node3", "group_id": "", "info": ""},
        ]
        result = orchestrator.describe_service()
        assert result.result[0].running == 3


# ===================================================================
# list_daemons()
# ===================================================================

class TestListDaemons:
    def test_list_daemons_basic(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "rgw", "location": "node2", "group_id": "", "info": ""},
        ]
        result = orchestrator.list_daemons()
        daemons = result.result
        assert len(daemons) == 2

    def test_list_daemons_filter_daemon_type(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "rgw", "location": "node2", "group_id": "", "info": ""},
        ]
        result = orchestrator.list_daemons(daemon_type="mon")
        assert len(result.result) == 1
        assert result.result[0].daemon_type == "mon"

    def test_list_daemons_filter_host(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "mon", "location": "node2", "group_id": "", "info": ""},
        ]
        result = orchestrator.list_daemons(host="node1")
        assert len(result.result) == 1
        assert result.result[0].hostname == "node1"

    def test_list_daemons_filter_service_name(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "nfs", "location": "node1", "group_id": "cluster1", "info": "{}"},
            {"service": "nfs", "location": "node1", "group_id": "cluster2", "info": "{}"},
        ]
        result = orchestrator.list_daemons(service_name="nfs.cluster1")
        assert len(result.result) == 1

    def test_list_daemons_filter_daemon_id(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "node1", "group_id": "", "info": ""},
            {"service": "mon", "location": "node2", "group_id": "", "info": ""},
        ]
        result = orchestrator.list_daemons(daemon_id="node2")
        assert len(result.result) == 1
        assert result.result[0].hostname == "node2"

    def test_list_daemons_nfs_with_info(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {
                "service": "nfs",
                "location": "node1",
                "group_id": "mycluster",
                "info": json.dumps({"bind_address": "10.0.0.5", "bind_port": 2049}),
            },
        ]
        result = orchestrator.list_daemons()
        daemon = result.result[0]
        assert daemon.ip == "10.0.0.5"
        assert daemon.ports == [2049]

    def test_list_daemons_nfs_wildcard_address(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {
                "service": "nfs",
                "location": "node1",
                "group_id": "mycluster",
                "info": json.dumps({"bind_address": "0.0.0.0", "bind_port": 2049}),
            },
        ]
        result = orchestrator.list_daemons()
        assert result.result[0].ip is None  # 0.0.0.0 should be None

    def test_list_daemons_nfs_malformed_info(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "nfs", "location": "node1", "group_id": "c1", "info": "not-json"},
        ]
        # Should not crash, just skip the NFS info parsing
        result = orchestrator.list_daemons()
        assert len(result.result) == 1
        assert result.result[0].ip is None
        assert result.result[0].ports is None

    def test_list_daemons_nfs_empty_info(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "nfs", "location": "node1", "group_id": "c1", "info": ""},
        ]
        result = orchestrator.list_daemons()
        assert len(result.result) == 1

    def test_list_daemons_nfs_null_info(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "nfs", "location": "node1", "group_id": "c1", "info": None},
        ]
        result = orchestrator.list_daemons()
        assert len(result.result) == 1

    def test_list_daemons_metadata_missing_optional_keys(self, orchestrator, mock_client):
        # ExtendedAPIService.list_services() may omit group_id (and other
        # optional keys); list_daemons must not raise KeyError.
        mock_client.services.list_services.return_value = [
            {"service": "mon", "location": "n1"},
        ]
        result = orchestrator.list_daemons()
        assert result.exception is None
        assert len(result.result) == 1
        daemon = result.result[0]
        assert daemon.daemon_type == "mon"
        assert daemon.hostname == "n1"
        assert daemon.service_name == "mon"


# ===================================================================
# get_inventory()
# ===================================================================

class TestGetInventory:
    def test_get_inventory_shows_osd_disks(self, orchestrator, mock_client):
        mock_client.services.list_disks.return_value = [
            {"location": "node1", "path": "/dev/sdb"},
            {"location": "node1", "path": "/dev/sdc"},
        ]
        result = orchestrator.get_inventory()
        inv = result.result
        # 3 hosts (from cluster members), node1 has 2 OSD disks
        hosts = {h.name for h in inv}
        assert hosts == {"node1", "node2", "node3"}
        node1 = [h for h in inv if h.name == "node1"][0]
        assert len(node1.devices.devices) == 2
        assert all(d.available is False for d in node1.devices.devices)

    def test_get_inventory_multi_host(self, orchestrator, mock_client):
        mock_client.services.list_disks.return_value = [
            {"location": "node1", "path": "/dev/sda"},
            {"location": "node2", "path": "/dev/sda"},
        ]
        result = orchestrator.get_inventory()
        hosts = {h.name for h in result.result}
        assert hosts == {"node1", "node2", "node3"}

    def test_get_inventory_with_host_filter(self, orchestrator, mock_client):
        mock_client.services.list_disks.return_value = [
            {"location": "node1", "path": "/dev/sda"},
            {"location": "node2", "path": "/dev/sda"},
            {"location": "node3", "path": "/dev/sda"},
        ]
        filt = InventoryFilter(hosts=["node1", "node3"])
        result = orchestrator.get_inventory(host_filter=filt)
        hosts = {h.name for h in result.result}
        assert hosts == {"node1", "node3"}

    def test_get_inventory_label_filter_rejected(self, orchestrator, mock_client):
        filt = InventoryFilter(labels=["role=osd"])
        result = orchestrator.get_inventory(host_filter=filt)
        assert isinstance(result.exception, OrchestratorValidationError)
        mock_client.services.list_disks.assert_not_called()

    def test_get_inventory_empty(self, orchestrator, mock_client):
        mock_client.services.list_disks.return_value = []
        mock_client.cluster.get_cluster_members.return_value = []
        result = orchestrator.get_inventory()
        assert result.result == []

    def test_get_inventory_includes_members_without_disks(self, orchestrator, mock_client):
        mock_client.services.list_disks.return_value = []
        # Default mock has 3 cluster members
        result = orchestrator.get_inventory()
        hosts = {h.name for h in result.result}
        assert hosts == {"node1", "node2", "node3"}
        # No devices on any host
        for h in result.result:
            assert len(h.devices.devices) == 0


# ===================================================================
# apply_rbd_mirror()
# ===================================================================

class TestApplyRbdMirror:
    def test_apply_rbd_mirror_success(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="rbd-mirror",
            placement=PlacementSpec(hosts=["node1", "node2"]),
        )
        result = orchestrator.apply_rbd_mirror(spec)
        assert result.exception is None
        # Per-host fan-out via the proxyTarget middleware: one call per
        # requested host with target=<host>.
        assert "enabled on node1, node2" in result.result
        assert mock_client.services.enable_service.call_count == 2
        targets = sorted(
            c.kwargs["target"]
            for c in mock_client.services.enable_service.call_args_list
        )
        assert targets == ["node1", "node2"]

    def test_apply_rbd_mirror_no_placement(self, orchestrator, mock_client):
        spec = ServiceSpec(service_type="rbd-mirror")
        result = orchestrator.apply_rbd_mirror(spec)
        assert result.exception is not None
        assert "No placement hosts" in str(result.exception)

    def test_apply_rbd_mirror_service_id_rejected(self, orchestrator, mock_client):
        # Bare-name guard: rbd-mirror does not support dotted names.
        spec = ServiceSpec(
            service_type="rbd-mirror",
            service_id="zone1",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rbd_mirror(spec)
        assert isinstance(result.exception, ValueError)
        mock_client.services.enable_service.assert_not_called()

    def test_apply_rbd_mirror_skips_existing(self, orchestrator, mock_client):
        # node1 already runs the service; node2 is also requested.
        mock_client.services.list_services.return_value = [
            _svc("rbd-mirror", "node1"),
        ]
        spec = ServiceSpec(
            service_type="rbd-mirror",
            placement=PlacementSpec(hosts=["node1", "node2"]),
        )
        result = orchestrator.apply_rbd_mirror(spec)
        assert result.exception is None
        assert "already active on node1" in result.result
        assert "enabled on node2" in result.result
        # Only the not-yet-active host is targeted.
        mock_client.services.enable_service.assert_called_once()
        assert (
            mock_client.services.enable_service.call_args.kwargs["target"]
            == "node2"
        )

    def test_apply_list_services_failure_propagates(self, orchestrator, mock_client):
        # If list_services itself fails we must NOT silently treat the
        # set of existing hosts as empty and re-apply everywhere; the
        # error has to surface.
        mock_client.services.list_services.side_effect = RemoteException(
            "list failed"
        )
        spec = ServiceSpec(
            service_type="rbd-mirror",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rbd_mirror(spec)
        assert isinstance(result.exception, RemoteException)
        mock_client.services.enable_service.assert_not_called()

    def test_apply_rbd_mirror_partial_failure(self, orchestrator, mock_client):
        # node1 enables, node2 fails — any failure raises with
        # partial-success context in the message.
        spec = ServiceSpec(
            service_type="rbd-mirror",
            placement=PlacementSpec(hosts=["node1", "node2"]),
        )

        def side_effect(*args, **kwargs):
            if kwargs.get("target") == "node2":
                raise RemoteException("node2 boom")

        mock_client.services.enable_service.side_effect = side_effect
        result = orchestrator.apply_rbd_mirror(spec)
        assert isinstance(result.exception, OrchestratorError)
        msg = str(result.exception)
        assert "Failed to enable rbd-mirror" in msg
        assert "node2: node2 boom" in msg
        assert "enabled on node1" in msg

    def test_apply_rbd_mirror_all_existing(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("rbd-mirror", "node1"),
        ]
        spec = ServiceSpec(
            service_type="rbd-mirror",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rbd_mirror(spec)
        assert result.exception is None
        assert "already active on node1" in result.result
        mock_client.services.enable_service.assert_not_called()

    def test_apply_rbd_mirror_api_error(self, orchestrator, mock_client):
        mock_client.services.enable_service.side_effect = RemoteException("fail")
        spec = ServiceSpec(
            service_type="rbd-mirror",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rbd_mirror(spec)
        assert result.exception is not None


# ===================================================================
# apply_rgw()
# ===================================================================

class TestApplyRgw:
    def test_apply_rgw_basic(self, orchestrator, mock_client):
        spec = RGWSpec(
            service_type="rgw",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rgw(spec)
        assert result.exception is None
        assert "enabled on node1" in result.result

        call_kwargs = mock_client.services.enable_service.call_args
        assert call_kwargs.kwargs["name"] == "rgw"
        assert call_kwargs.kwargs["target"] == "node1"

    def test_apply_rgw_with_port(self, orchestrator, mock_client):
        spec = RGWSpec(
            service_type="rgw",
            rgw_frontend_port=8080,
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rgw(spec)
        assert result.exception is None

        call_kwargs = mock_client.services.enable_service.call_args
        payload = json.loads(call_kwargs.kwargs["payload"])
        assert payload["Port"] == 8080
        assert call_kwargs.kwargs["target"] == "node1"

    def test_apply_rgw_ssl_cert_without_key_warns(self, orchestrator, mock_client, caplog):
        """SSL cert is present but no key; should warn and not send cert."""
        spec = RGWSpec(
            service_type="rgw",
            rgw_frontend_ssl_certificate=["-----BEGIN CERT-----"],
            placement=PlacementSpec(hosts=["node1"]),
        )
        import logging
        with caplog.at_level(logging.WARNING):
            result = orchestrator.apply_rgw(spec)

        assert result.exception is None
        # Cert should NOT be in payload (useless without key)
        call_kwargs = mock_client.services.enable_service.call_args
        payload = json.loads(call_kwargs.kwargs["payload"])
        assert "SSLCertificate" not in payload
        # Warning should be logged
        assert any("private key" in r.message for r in caplog.records)

    def test_apply_rgw_no_placement(self, orchestrator, mock_client):
        spec = RGWSpec(service_type="rgw")
        result = orchestrator.apply_rgw(spec)
        assert result.exception is not None

    def test_apply_rgw_service_id_rejected(self, orchestrator, mock_client):
        # MicroCeph deploys a single bare 'rgw' service; per-realm
        # service_id is not supported and is rejected up front so the
        # operator does not believe a realm was provisioned.
        spec = RGWSpec(
            service_type="rgw",
            service_id="realm1",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_rgw(spec)
        assert isinstance(result.exception, ValueError)
        assert "does not support a service_id" in str(result.exception)
        mock_client.services.enable_service.assert_not_called()


# ===================================================================
# apply_nfs()
# ===================================================================

class TestApplyNfs:
    def test_apply_nfs_basic(self, orchestrator, mock_client):
        spec = NFSServiceSpec(
            service_type="nfs",
            service_id="mycluster",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_nfs(spec)
        assert result.exception is None
        assert "enabled on node1" in result.result

        call_kwargs = mock_client.services.enable_service.call_args
        payload = json.loads(call_kwargs.kwargs["payload"])
        assert payload["cluster_id"] == "mycluster"
        assert call_kwargs.kwargs["target"] == "node1"

    def test_apply_nfs_with_port(self, orchestrator, mock_client):
        spec = NFSServiceSpec(
            service_type="nfs",
            service_id="mycluster",
            port=12049,
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_nfs(spec)
        call_kwargs = mock_client.services.enable_service.call_args
        payload = json.loads(call_kwargs.kwargs["payload"])
        assert payload["bind_port"] == 12049

    def test_apply_nfs_with_virtual_ip(self, orchestrator, mock_client):
        spec = NFSServiceSpec(
            service_type="nfs",
            service_id="mycluster",
            virtual_ip="10.0.0.100",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_nfs(spec)
        call_kwargs = mock_client.services.enable_service.call_args
        payload = json.loads(call_kwargs.kwargs["payload"])
        assert payload["bind_address"] == "10.0.0.100"

    def test_apply_nfs_missing_service_id(self, orchestrator, mock_client):
        spec = NFSServiceSpec(
            service_type="nfs",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_nfs(spec)
        assert result.exception is not None
        assert "cluster_id" in str(result.exception)

    def test_apply_nfs_no_placement(self, orchestrator, mock_client):
        spec = NFSServiceSpec(
            service_type="nfs",
            service_id="mycluster",
        )
        result = orchestrator.apply_nfs(spec)
        assert result.exception is not None
        assert "No placement hosts" in str(result.exception)

    def test_apply_nfs_skips_existing(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("nfs", "node1", group_id="mycluster", info="{}"),
        ]
        spec = NFSServiceSpec(
            service_type="nfs",
            service_id="mycluster",
            placement=PlacementSpec(hosts=["node1", "node2"]),
        )
        result = orchestrator.apply_nfs(spec)
        assert "already active on node1" in result.result
        assert "enabled on node2" in result.result
        mock_client.services.enable_service.assert_called_once()
        # Only the host that did not yet run the cluster is targeted.
        assert (
            mock_client.services.enable_service.call_args.kwargs["target"]
            == "node2"
        )

    def test_apply_nfs_distinct_cluster_not_conflated(self, orchestrator, mock_client):
        """A host running nfs.other must not suppress enabling nfs.mycluster."""
        mock_client.services.list_services.return_value = [
            _svc("nfs", "node1", group_id="other", info="{}"),
        ]
        spec = NFSServiceSpec(
            service_type="nfs",
            service_id="mycluster",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_nfs(spec)
        assert result.exception is None
        # nfs.other on node1 must not be mistaken for nfs.mycluster
        assert "already active" not in result.result
        assert "enabled" in result.result
        mock_client.services.enable_service.assert_called_once()


# ===================================================================
# apply_mon() / apply_mgr() / apply_mds()
# ===================================================================

# ===================================================================
# apply_cephfs_mirror()
# ===================================================================

class TestApplyCephfsMirror:
    def test_apply_cephfs_mirror_success(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="cephfs-mirror",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_cephfs_mirror(spec)
        assert result.exception is None
        assert "enabled on node1" in result.result
        mock_client.services.enable_service.assert_called_once_with(
            name="cephfs-mirror", payload="{}", wait=True, target="node1",
        )

    def test_apply_cephfs_mirror_service_id_rejected(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="cephfs-mirror",
            service_id="x",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_cephfs_mirror(spec)
        assert isinstance(result.exception, ValueError)
        mock_client.services.enable_service.assert_not_called()


# ===================================================================
# _parse_service_name()
# ===================================================================

class TestParseServiceName:
    def test_simple_name(self, orchestrator):
        assert orchestrator._parse_service_name("rgw") == ("rgw", "")

    def test_dotted_name(self, orchestrator):
        assert orchestrator._parse_service_name("nfs.mycluster") == ("nfs", "mycluster")

    def test_multi_dot_name(self, orchestrator):
        """Ensure dotted names like nfs.my.cluster split on first dot only."""
        assert orchestrator._parse_service_name("nfs.my.cluster") == ("nfs", "my.cluster")

    def test_empty_string(self, orchestrator):
        assert orchestrator._parse_service_name("") == ("", "")


# ===================================================================
# describe_service() - service_name filter
# ===================================================================

class TestDescribeServiceNameFilter:
    def test_filter_by_service_name(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            {"service": "nfs", "location": "node1", "group_id": "c1", "info": "{}"},
            {"service": "nfs", "location": "node1", "group_id": "c2", "info": "{}"},
        ]
        result = orchestrator.describe_service(service_name="nfs.c1")
        assert len(result.result) == 1
        assert result.result[0].spec.service_id == "c1"


# ===================================================================
# remove_service() - dotted non-NFS
# ===================================================================

class TestRemoveServiceDotted:
    def test_remove_dotted_non_nfs_rejected(self, orchestrator, mock_client):
        # Removing 'mds.myfs' (or any dotted non-NFS name) must be
        # rejected: silently dropping the id would otherwise wipe the
        # bare MDS service across every host while the operator
        # believed only a specific filesystem was being removed.
        result = orchestrator.remove_service("mds.myfs")
        assert isinstance(result.exception, ValueError)
        assert "does not support a service_id" in str(result.exception)
        mock_client.services.delete_service.assert_not_called()


# ===================================================================
# apply_mon() / apply_mgr() / apply_mds() / apply_cephfs_mirror()
# ===================================================================

class TestApplyGenericServices:
    def test_apply_mon(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="mon",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_mon(spec)
        assert result.exception is None
        assert "enabled on node1" in result.result
        mock_client.services.enable_service.assert_called_once_with(
            name="mon", payload="{}", wait=True, target="node1",
        )

    def test_apply_mgr(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="mgr",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_mgr(spec)
        assert result.exception is None
        mock_client.services.enable_service.assert_called_once_with(
            name="mgr", payload="{}", wait=True, target="node1",
        )

    def test_apply_mon_service_id_rejected(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="mon",
            service_id="x",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_mon(spec)
        assert isinstance(result.exception, ValueError)
        mock_client.services.enable_service.assert_not_called()

    def test_apply_mgr_service_id_rejected(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="mgr",
            service_id="x",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_mgr(spec)
        assert isinstance(result.exception, ValueError)
        mock_client.services.enable_service.assert_not_called()

    def test_apply_mds(self, orchestrator, mock_client):
        spec = ServiceSpec(
            service_type="mds",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_mds(spec)
        assert result.exception is None
        mock_client.services.enable_service.assert_called_once_with(
            name="mds", payload="{}", wait=True, target="node1",
        )

    def test_apply_mds_service_id_rejected(self, orchestrator, mock_client):
        # Per-filesystem MDS placement is not supported by MicroCeph;
        # a service_id (filesystem name) is rejected up front.
        spec = ServiceSpec(
            service_type="mds",
            service_id="fs1",
            placement=PlacementSpec(hosts=["node1"]),
        )
        result = orchestrator.apply_mds(spec)
        assert isinstance(result.exception, ValueError)
        assert "does not support a service_id" in str(result.exception)
        mock_client.services.enable_service.assert_not_called()


# ===================================================================
# remove_service()
# ===================================================================

class TestRemoveService:
    def test_remove_service_basic(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("rgw", "node1"),
            _svc("rgw", "node2"),
        ]
        result = orchestrator.remove_service("rgw")
        assert result.exception is None
        assert "removed from node1, node2" in result.result
        assert mock_client.services.delete_service.call_count == 2

    def test_remove_service_no_hosts(self, orchestrator, mock_client):
        # list_services returns [] (default) — removing a service that
        # isn't deployed must surface as an error (cephadm parity), not
        # a green no-op.
        result = orchestrator.remove_service("rgw")
        assert isinstance(result.exception, OrchestratorError)
        assert "not running on any host" in str(result.exception)
        mock_client.services.delete_service.assert_not_called()

    def test_remove_service_nfs_with_cluster_id(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("nfs", "node1", group_id="mycluster"),
        ]
        result = orchestrator.remove_service("nfs.mycluster")
        assert result.exception is None
        assert "removed from node1" in result.result
        mock_client.services.delete_nfs_service.assert_called_once()
        c = mock_client.services.delete_nfs_service.call_args
        assert c.args == ("mycluster",)
        assert c.kwargs["target"] == "node1"

    def test_remove_service_nfs_without_cluster_id(self, orchestrator, mock_client):
        result = orchestrator.remove_service("nfs")
        assert result.exception is not None
        assert "cluster_id" in str(result.exception)

    def test_remove_service_nfs_group_id_no_match(self, orchestrator, mock_client):
        # NFS cluster 'other' exists on node1, but we're removing
        # 'mycluster' — there is nothing to do for this cluster_id;
        # surface as an error and do NOT cascade into deleting the
        # unrelated cluster.
        mock_client.services.list_services.return_value = [
            _svc("nfs", "node1", group_id="other"),
        ]
        result = orchestrator.remove_service("nfs.mycluster")
        assert isinstance(result.exception, OrchestratorError)
        assert "not running on any host" in str(result.exception)
        mock_client.services.delete_nfs_service.assert_not_called()

    def test_remove_service_api_error(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("rgw", "node1"),
        ]
        mock_client.services.delete_service.side_effect = RemoteException("fail")
        result = orchestrator.remove_service("rgw")
        assert result.exception is not None
        assert "Failed to remove" in str(result.exception)

    def test_remove_service_partial_failure(self, orchestrator, mock_client):
        # node1 succeeds, node2 fails — any failure raises so the
        # operator sees a visible error, and the partial-success
        # context is included in the message.
        mock_client.services.list_services.return_value = [
            _svc("rgw", "node1"),
            _svc("rgw", "node2"),
        ]

        def side_effect(*args, **kwargs):
            if kwargs.get("target") == "node2":
                raise RemoteException("node2 boom")

        mock_client.services.delete_service.side_effect = side_effect
        result = orchestrator.remove_service("rgw")
        assert isinstance(result.exception, OrchestratorError)
        msg = str(result.exception)
        assert "Failed to remove rgw" in msg
        assert "node2: node2 boom" in msg
        assert "removed from node1" in msg


# ===================================================================
# remove_host()
# ===================================================================

class TestRemoveHost:
    def test_remove_host_success(self, orchestrator, mock_client):
        result = orchestrator.remove_host("node2")
        assert result.exception is None
        assert "Removed host node2" in result.result
        mock_client.cluster.remove.assert_called_once_with("node2")

    def test_remove_host_api_error(self, orchestrator, mock_client):
        mock_client.cluster.remove.side_effect = RemoteException("node not found")
        result = orchestrator.remove_host("badhost")
        assert result.exception is not None


# ===================================================================
# service_action()
# ===================================================================

class TestServiceAction:
    def test_restart_service(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("mon", "node1"),
            _svc("mon", "node2"),
        ]
        result = orchestrator.service_action("restart", "mon")
        assert result.exception is None
        # One restart call per host carrying target=<host>.
        assert mock_client.services.restart_services.call_count == 2
        targets = sorted(
            c.kwargs["target"]
            for c in mock_client.services.restart_services.call_args_list
        )
        assert targets == ["node1", "node2"]
        assert any("Restarted mon on node1" in r for r in result.result)
        assert any("Restarted mon on node2" in r for r in result.result)

    def test_restart_no_hosts(self, orchestrator, mock_client):
        # Restarting a service that isn't deployed surfaces as an
        # error so the operator notices the typo or stale assumption
        # (cephadm parity).
        result = orchestrator.service_action("restart", "mon")
        assert isinstance(result.exception, OrchestratorError)
        assert "not running on any host" in str(result.exception)
        mock_client.services.restart_services.assert_not_called()

    def test_restart_dotted_non_nfs_rejected(self, orchestrator, mock_client):
        # Dotted non-NFS names are rejected: restarting 'rgw.realm1'
        # would have to silently drop the id and restart every rgw,
        # which is a surprising operation.
        result = orchestrator.service_action("restart", "rgw.realm1")
        assert isinstance(result.exception, ValueError)
        assert "does not support a service_id" in str(result.exception)
        mock_client.services.restart_services.assert_not_called()

    def test_restart_unsupported_service_type(self, orchestrator, mock_client):
        # MicroCeph's backend serviceWorkerTable only handles osd/mon/rgw.
        # The orchestrator must reject restart for other service types up
        # front rather than surfacing an opaque RemoteException.
        for svc in ["nfs.mycluster", "mds", "mgr", "rbd-mirror"]:
            mock_client.services.restart_services.reset_mock()
            result = orchestrator.service_action("restart", svc)
            assert isinstance(result.exception, OrchestratorValidationError), svc
            mock_client.services.restart_services.assert_not_called()

    def test_restart_partial_failure(self, orchestrator, mock_client):
        # node1 restarts, node2 fails — any failure raises with
        # partial-success context in the message.
        mock_client.services.list_services.return_value = [
            _svc("mon", "node1"),
            _svc("mon", "node2"),
        ]

        def side_effect(*args, **kwargs):
            if kwargs.get("target") == "node2":
                raise RemoteException("node2 boom")

        mock_client.services.restart_services.side_effect = side_effect
        result = orchestrator.service_action("restart", "mon")
        assert isinstance(result.exception, OrchestratorError)
        msg = str(result.exception)
        assert "Failed to restart mon" in msg
        assert "node2: node2 boom" in msg
        assert "restarted on node1" in msg

    def test_unsupported_action(self, orchestrator, mock_client):
        result = orchestrator.service_action("stop", "mon")
        assert result.exception is not None
        assert "not supported" in str(result.exception)

    def test_restart_api_error(self, orchestrator, mock_client):
        mock_client.services.list_services.return_value = [
            _svc("mon", "node1"),
        ]
        mock_client.services.restart_services.side_effect = RemoteException("fail")
        result = orchestrator.service_action("restart", "mon")
        assert result.exception is not None
        assert "Failed to restart" in str(result.exception)

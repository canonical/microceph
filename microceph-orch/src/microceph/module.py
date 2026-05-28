#
# Copyright 2025, Canonical Ltd.
#

import logging
from typing import Tuple, Dict, Any, List, Optional
from collections import defaultdict
import time
import json

from ceph.deployment.inventory import Device, Devices
from ceph.deployment.service_spec import (
    ServiceSpec,
    PlacementSpec,
    RGWSpec,
    MONSpec,
    MDSSpec,
    NFSServiceSpec,
)

from mgr_module import MgrModule
from mgr_module import NotifyType

from orchestrator import (
    Orchestrator,
    HostSpec,
    InventoryFilter,
    InventoryHost,
    ServiceDescription,
    DaemonDescription,
    CLICommandMeta,
    handle_orch_error,
    OrchResult,
    OrchestratorError,
    OrchestratorValidationError,
)

from .client.client import Client
from .client.service import RemoteException

logger = logging.getLogger(__name__)

daemon_spec_map = {
    'mon': MONSpec,
    'mds': MDSSpec,
    'rgw': RGWSpec,
    'nfs': NFSServiceSpec,
}

# Services for which MicroCeph's backend supports restart. The Go
# serviceWorkerTable (microceph/ceph/services.go) only registers handlers
# for osd, mon and rgw; restarting any other service raises
# "no handler defined for service X" at the backend.
RESTART_SUPPORTED_SERVICES = frozenset({"osd", "mon", "rgw"})

# Services for which MicroCeph distinguishes individual instances via a
# `service_id` (the `<type>.<id>` dotted form). Currently only NFS, where
# the id is the NFS cluster_id. Any other service is deployed as a single
# bare instance per node; using a dotted name on them is rejected up front
# so operators get a clear error instead of an apparently successful
# operation that silently targets the wrong scope.
DOTTED_NAME_SUPPORTED_SERVICES = frozenset({"nfs"})


def _require_bare_service_name(svc_type: str, svc_id: str) -> None:
    """Reject dotted service names for services that don't support them.

    Raises ValueError if `svc_id` is non-empty and `svc_type` is not in
    DOTTED_NAME_SUPPORTED_SERVICES.
    """
    if svc_id and svc_type not in DOTTED_NAME_SUPPORTED_SERVICES:
        supported = ", ".join(sorted(DOTTED_NAME_SUPPORTED_SERVICES))
        raise ValueError(
            f"Service type '{svc_type}' does not support a service_id; "
            f"MicroCeph deploys a single bare '{svc_type}' per node. "
            f"Use '{svc_type}' (without an id) instead of "
            f"'{svc_type}.{svc_id}'. Dotted names are only valid for: "
            f"{supported}."
        )

class MicroCephOrchestrator(Orchestrator,
                            MgrModule,
                            metaclass=CLICommandMeta):

    def __init__(self, *args: Any, **kwargs: Any):
        """

        :param args:
        :param kwargs:
        """
        super(MicroCephOrchestrator, self).__init__(*args, **kwargs)
        self.microceph = Client.from_socket()
        self.run = True

    def serve(self) -> None:
        """
        Called by the ceph-mgr service to start any server that
        is provided by this Python plugin.  The implementation
        of this function should block until ``shutdown`` is called.

        You *must* implement ``shutdown`` if you implement ``serve``

        :return:
        """
        while self.run:
            logger.debug("Running serve loop")
            time.sleep(30)

    def shutdown(self) -> None:
        """

        :return:
        """
        self.run = False

    def available(self) -> Tuple[bool, str, Dict[str, Any]]:
        """
        Report whether we can talk to the orchestrator.  This is the
        place to give the user a meaningful message if the orchestrator
        isn't running or can't be contacted.

        This method may be called frequently (e.g. every page load
        to conditionally display a warning banner), so make sure it's
        not too expensive.  It's okay to give a slightly stale status
        (e.g. based on a periodic background ping of the orchestrator)
        if that's necessary to make this method fast.

        .. note::
            `True` doesn't mean that the desired functionality
            is actually available in the orchestrator. I.e. this
            won't work as expected::

                >>> #doctest: +SKIP
                ... if OrchestratorClientMixin().available()[0]:  # wrong.
                ...     OrchestratorClientMixin().get_hosts()

        :return: boolean representing whether the module is available/usable
        :return: string describing any error
        :return: dict containing any module specific information
        """
        try:
            self.microceph.status.is_available()
        except RemoteException as e:
            return False, f"Cannot reach the MicroCeph API: {e}", {}
        except Exception as e:
            return False, f"Unexpected error reaching MicroCeph API: {e}", {}

        return True, "", {}

    def notify(self, notify_type: NotifyType, notify_id: str) -> None:
        """

        :param notify_type:
        :param notify_id:
        :return:
        """
        logger.info(f"noop: notify called with notify_type: {notify_type} and notify_id: {notify_id}")

    @handle_orch_error
    def get_hosts(self) -> List[HostSpec]:
        """
        Report the hosts in the cluster.

        :return: list of HostSpec
        """
        specs = []
        for m in self.microceph.cluster.get_cluster_members():
            # microcluster addresses are "host:port"; IPv6 uses the
            # bracketed "[addr]:port" form. Strip the port without
            # corrupting a bare IPv6 literal (which also contains ":").
            address = m.get('address', '')
            if address.startswith('['):
                addr = address[1:].partition(']')[0]
            elif address.count(':') == 1:
                addr = address.rsplit(':', 1)[0]
            else:
                # No port present (hostname-only or bare IPv6 literal).
                addr = address
            specs.append(HostSpec(
                m.get('name', ''),
                addr,
                status=m.get('status', 'unknown'),
            ))

        return specs

    def _get_service_hostlist(self, recorded_services: list) -> dict:
        """Get a dict describing the distribution of services"""
        service_hostlist = defaultdict(list)
        for record in recorded_services:
            service = record.get('service', '')
            group_id = record.get('group_id', '')
            service_name = service if not group_id else f"{service}.{group_id}"
            service_host = record.get('location', '')
            service_hostlist[service_name].append(service_host)
            logger.debug(
                f"microceph record service({service_name}) at "
                f"({service_host}) configured({record.get('info', '')})"
            )
        return service_hostlist

    @staticmethod
    def _parse_service_name(service_name: str) -> Tuple[str, str]:
        """Split a service name into (type, id).

        Handles dotted names like 'nfs.my.cluster' correctly by splitting
        on the first dot only.

        :return: (service_type, service_id); service_id is '' if no dot.
        """
        svc_type, _, svc_id = service_name.partition('.')
        return svc_type, svc_id

    @handle_orch_error
    def describe_service(self,
                service_type: Optional[str] = None,
                service_name: Optional[str] = None,
                refresh: bool = False
            ) -> List[ServiceDescription]:

        logger.info(f"describing service... service_type={service_type}, service_name={service_name}, "
                    f"refresh={refresh}")

        recorded_services = self.microceph.services.list_services()
        service_hostlist = self._get_service_hostlist(recorded_services)

        service_descs = []
        for svc_name, hostlist in service_hostlist.items():
            spec = None
            svc_type, svc_id = self._parse_service_name(svc_name)
            logger.debug(f"{svc_name} under description for filter {service_type}")

            # Apply filters
            if service_type and svc_type != service_type:
                continue
            if service_name and svc_name != service_name:
                continue

            if svc_type in daemon_spec_map:
                spec = daemon_spec_map[svc_type](
                    service_id=svc_id, service_type=svc_type, placement=PlacementSpec(hosts=hostlist, count=len(hostlist))
                )
            else:
                spec = ServiceSpec(
                    service_id=svc_id, service_type=svc_type, placement=PlacementSpec(hosts=hostlist, count=len(hostlist))
                )

            # `size` is the desired daemon count; `running` is the actual.
            # MicroCeph's desired state IS the current set of hosts running
            # the service (no separate spec store), so they match. Setting
            # `size` explicitly avoids `ceph orch ls` rendering `RUNNING/-`,
            # which looks like a degraded/incomplete deployment.
            service_descs.append(ServiceDescription(
                spec=spec,
                running=len(hostlist),
                size=len(hostlist),
            ))

        return service_descs

    @handle_orch_error
    def list_daemons(self,
                service_name: Optional[str] = None,
                daemon_type: Optional[str] = None,
                daemon_id: Optional[str] = None,
                host: Optional[str] = None,
                refresh: bool = False
            ) -> List[DaemonDescription]:

        logger.info(f"listing daemons... service_name={service_name}, daemon_type={daemon_type}, "
                    f"daemon_id={daemon_id}, host={host}, refresh={refresh}")

        services = self.microceph.services.list_services()
        descriptions = []
        for svc in services:
            svc_daemon_type = svc.get('service', '')
            svc_hostname = svc.get('location', '')
            svc_group_id = svc.get('group_id', '')
            svc_ip = None
            svc_ports = None
            svc_name = f"{svc_daemon_type}.{svc_group_id}" if svc_group_id else svc_daemon_type

            # Apply filters
            if daemon_type and svc_daemon_type != daemon_type:
                continue
            if host and svc_hostname != host:
                continue
            if daemon_id and svc_hostname != daemon_id:
                continue
            if service_name and svc_name != service_name:
                continue

            # Extract NFS-specific info (bind address and port)
            if svc_daemon_type == 'nfs' and svc.get('info'):
                try:
                    info = json.loads(svc['info'])
                    bind_addr = info.get('bind_address', '')
                    svc_ip = None if '0.0.0.0' in bind_addr else bind_addr or None
                    svc_ports = [info['bind_port']] if 'bind_port' in info else None
                except (json.JSONDecodeError, KeyError) as e:
                    logger.warning(f"Failed to parse NFS service info for {svc_hostname}: {e}")

            descriptions.append(DaemonDescription(
                service_name=svc_name,
                daemon_type=svc_daemon_type,
                daemon_id=svc_hostname,
                hostname=svc_hostname,
                ip=svc_ip,
                ports=svc_ports
            ))

        logger.debug(f"list_daemons returning {len(descriptions)} daemons")
        return descriptions

    @handle_orch_error
    def get_inventory(self,
                host_filter: Optional[InventoryFilter] = None,
                refresh: bool = False
            ) -> List[InventoryHost]:
        """Report storage device inventory per host.

        Devices are sourced from the cluster-wide OSD list (/1.0/disks), so
        every reported device backs an existing OSD and is marked
        unavailable. Discovery of free/unused disks is not exposed here:
        /1.0/resources reports only the local node and the socket client
        cannot proxy to peers (see the _apply_service note on targeting).
        Hosts with no OSD disks are still listed so callers see every member.

        Only `host_filter.hosts` is honored; `host_filter.labels` raises
        NotImplementedError because MicroCeph has no host-label concept.
        """

        # Resolve which hosts to include based on the filter.
        filter_hosts = None
        if host_filter:
            if host_filter.labels:
                raise OrchestratorValidationError(
                    "MicroCeph orchestrator does not support host labels"
                )
            if host_filter.hosts:
                filter_hosts = set(host_filter.hosts)

        osd_disks = self.microceph.services.list_disks()

        # Build inventory from OSD disk list (cluster-wide source of truth
        # for which hosts exist and what disks they have).
        devices_by_host: Dict[str, List[Device]] = defaultdict(list)
        for d in osd_disks:
            host = d.get('location', '')
            if filter_hosts and host not in filter_hosts:
                continue
            devices_by_host[host].append(
                Device(path=d.get('path', ''), available=False)
            )

        # Ensure all cluster members appear even if they have no OSD disks.
        try:
            for member in self.microceph.cluster.get_cluster_members():
                host = member.get('name', '')
                if filter_hosts and host not in filter_hosts:
                    continue
                if host not in devices_by_host:
                    devices_by_host[host] = []
        except RemoteException:
            pass

        inventory = []
        for host, devs in devices_by_host.items():
            inventory.append(InventoryHost(
                name=host,
                devices=Devices(devs)
            ))

        return inventory

    def _get_placement_hosts(self, spec: ServiceSpec) -> List[str]:
        """Extract target hosts from a ServiceSpec's placement.

        Returns the list of hostnames from the placement spec.
        Raises ValueError if no hosts are specified; callers must
        provide explicit placement.
        """
        hosts = []
        if spec.placement and spec.placement.hosts:
            hosts = [h.hostname if hasattr(h, 'hostname') else str(h) for h in spec.placement.hosts]

        if not hosts:
            raise ValueError(
                f"No placement hosts specified for {spec.service_type}. "
                "Explicit host placement is required."
            )

        return hosts

    def _get_existing_service_hosts(self, service_type: str,
                                    group_id: Optional[str] = None) -> set:
        """Return the set of hostnames that already run the given service.

        When group_id is provided (e.g. an NFS cluster id), only services
        matching both the type and that group are counted, so distinct NFS
        clusters are not conflated.

        A failure to list services from the backend is propagated to the
        caller rather than swallowed: returning an empty set on error
        would otherwise produce false "no-op" successes and re-apply
        services that are in fact already running.
        """
        services = self.microceph.services.list_services()
        return {
            svc.get('location', '') for svc in services
            if svc.get('service') == service_type
            and (group_id is None or svc.get('group_id', '') == group_id)
        }

    def _apply_service(self, service_type: str, spec: ServiceSpec,
                       payload: str, group_id: Optional[str] = None) -> str:
        """Common logic for applying (enabling) a service.

        Per-host targeting: MicroCeph's HTTP API endpoints for service
        enable/delete/restart all declare ProxyTarget=true in the Go
        rest layer. The microcluster proxyTarget middleware inspects the
        `?target=<member>` query parameter and forwards the request over
        mTLS to that member's HTTPS endpoint. The Python client uses the
        local unix socket; the server transparently proxies per-host
        calls from there, so no direct HTTPS connectivity from the orch
        module is required.

        We iterate requested placement hosts, skip any host that already
        runs the service, and call enable_service once per remaining
        host with `target=<host>`. Any per-host failure raises so the
        operator gets a visible error rather than a partial-success
        result that the Ceph orchestrator framework would render as
        green.

        Note: `service_type` is the bare service type (e.g. "rgw",
        "nfs"), never the dotted form. Callers that accept dotted names
        are expected to parse and pass `group_id` separately.

        Returns a summary string on success.
        Raises RuntimeError if any host fails to enable.
        """
        hosts = self._get_placement_hosts(spec)
        existing = self._get_existing_service_hosts(service_type, group_id)

        # If every requested host already runs the service, there is
        # nothing to do.
        if existing.issuperset(hosts):
            skipped_str = ', '.join(hosts)
            logger.info(f"Skipping {service_type}: already active on {skipped_str}")
            return f"{service_type}: already active on {skipped_str}"

        to_enable = [h for h in hosts if h not in existing]
        already_active = sorted(existing & set(hosts))

        enabled: List[str] = []
        failures: List[str] = []
        for host in to_enable:
            logger.info(f"Enabling {service_type} on {host}")
            try:
                self.microceph.services.enable_service(
                    name=service_type,
                    payload=payload,
                    wait=True,
                    target=host,
                )
                enabled.append(host)
            except Exception as e:
                # Tolerate the TOCTOU window between the snapshot taken
                # by _get_existing_service_hosts and the per-host enable
                # call. The backend's genericHospitalityCheck
                # (microceph/ceph/services_placement_generic.go) returns
                # "<svc> service already active on host" if the service
                # raced into existence between snapshot and enable; for
                # an apply that is the desired end-state, not a failure.
                if "already active on host" in str(e):
                    logger.info(
                        f"{service_type} became active on {host} after "
                        "snapshot; treating as no-op."
                    )
                    enabled.append(host)
                    continue
                logger.error(f"Failed to enable {service_type} on {host}: {e}")
                failures.append(f"{host}: {e}")

        # Any failure is surfaced as an exception so the orchestrator
        # framework reports the operation as failed. Partial-success
        # context is included in the message to aid debugging.
        if failures:
            ctx = []
            if enabled:
                ctx.append(f"enabled on {', '.join(enabled)}")
            if already_active:
                ctx.append(f"already active on {', '.join(already_active)}")
            ctx_str = f" ({'; '.join(ctx)})" if ctx else ""
            raise OrchestratorError(
                f"Failed to enable {service_type}: "
                + "; ".join(failures)
                + ctx_str
            )

        parts = []
        if enabled:
            parts.append(f"enabled on {', '.join(enabled)}")
        if already_active:
            parts.append(f"already active on {', '.join(already_active)}")

        summary = "; ".join(parts) if parts else "no-op"
        return f"{service_type}: {summary}"

    @handle_orch_error
    def apply_rbd_mirror(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the rbd-mirror service on the target hosts.

        rbd-mirror is a client-like service with no additional parameters.
        The Go API's ClientServicePlacement handler takes no payload.
        Dotted names are rejected (see `_require_bare_service_name`).
        """
        logger.debug("Applying rbd-mirror service")
        _require_bare_service_name("rbd-mirror", spec.service_id or "")
        return self._apply_service("rbd-mirror", spec, "{}")

    @handle_orch_error
    def apply_rgw(self, spec: RGWSpec) -> OrchResult[str]:
        """Enable the RGW service on the target hosts.

        Extracts port and SSL configuration from the RGWSpec and passes
        them as the JSON payload to the MicroCeph enable_service API.

        Note on SSL: The Ceph RGWSpec provides rgw_frontend_ssl_certificate
        (a list of PEM cert strings) but has no private key field. MicroCeph's
        Go API (RgwServicePlacement) requires both SSLCertificate and
        SSLPrivateKey to enable SSL. Until the spec is extended or a separate
        key source is added, SSL cannot be fully configured via the
        orchestrator interface.

        Note on service_id (realms/zones): MicroCeph deploys a single bare
        "rgw" service per node and does not support multiple RGW instances
        with distinct service_ids on the same cluster. Supplying a
        service_id raises ValueError so the operator is not surprised
        by a silently-misscoped subsequent remove_service call.
        """
        logger.debug("Applying RGW service")
        _require_bare_service_name("rgw", spec.service_id or "")

        # Build RGW-specific payload from the spec.
        # Go's RgwServicePlacement expects: Port, SSLPort, SSLCertificate, SSLPrivateKey
        # Field names match Go exported field names (no json tags defined,
        # so encoding/json matches case-insensitively).
        rgw_params: Dict[str, Any] = {}
        if spec.rgw_frontend_port:
            rgw_params['Port'] = spec.rgw_frontend_port

        # TODO: SSL support for RGW via orchestrator interface.
        #
        # SSL cannot be configured through this path. MicroCeph's Go API requires
        # both SSLCertificate and SSLPrivateKey as raw PEM material; if either is
        # missing, SSL is skipped and RGW falls back to plain HTTP (see rgw.go).
        # The Ceph RGWSpec only carries the cert chain (rgw_frontend_ssl_certificate)
        # with no private key field, so we cannot supply both.
        if spec.rgw_frontend_ssl_certificate:
            logger.warning(
                "RGWSpec provides SSL certificate but the Ceph orchestrator spec "
                "has no private key field. MicroCeph requires both certificate and "
                "private key for SSL. SSL will be skipped; RGW will be deployed "
                "in non-SSL mode."
            )

        payload = json.dumps(rgw_params) if rgw_params else "{}"
        return self._apply_service("rgw", spec, payload)

    @handle_orch_error
    def apply_nfs(self, spec: NFSServiceSpec) -> OrchResult[str]:
        """Enable the NFS service on the target hosts.

        Extracts the NFS cluster ID and optional port from the
        NFSServiceSpec and passes them as the JSON payload.
        """
        logger.debug("Applying NFS service")

        # Go's NFSServicePlacement expects: cluster_id, v4_min_version, bind_address, bind_port
        # The Ceph NFSServiceSpec uses service_id as the NFS cluster identifier.
        if not spec.service_id:
            raise ValueError("NFS service_id (cluster_id) is required")

        nfs_params: Dict[str, Any] = {
            'cluster_id': spec.service_id,
        }
        if spec.port:
            nfs_params['bind_port'] = spec.port
        if spec.virtual_ip:
            nfs_params['bind_address'] = spec.virtual_ip

        payload = json.dumps(nfs_params)
        return self._apply_service("nfs", spec, payload, group_id=spec.service_id)

    @handle_orch_error
    def apply_mon(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the MON service on the target hosts.

        Dotted names are rejected (see `_require_bare_service_name`).
        """
        logger.debug("Applying MON service")
        _require_bare_service_name("mon", spec.service_id or "")
        return self._apply_service("mon", spec, "{}")

    @handle_orch_error
    def apply_mgr(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the MGR service on the target hosts.

        Dotted names are rejected (see `_require_bare_service_name`).
        """
        logger.debug("Applying MGR service")
        _require_bare_service_name("mgr", spec.service_id or "")
        return self._apply_service("mgr", spec, "{}")

    @handle_orch_error
    def apply_mds(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the MDS service on the target hosts.

        Note on service_id (filesystem name): MicroCeph deploys a single
        bare "mds" service and does not support per-filesystem MDS
        placement. Supplying a service_id raises ValueError.
        """
        logger.debug("Applying MDS service")
        _require_bare_service_name("mds", spec.service_id or "")
        return self._apply_service("mds", spec, "{}")

    @handle_orch_error
    def apply_cephfs_mirror(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the cephfs-mirror service on the target hosts.

        cephfs-mirror is a client-like service (same as rbd-mirror)
        with no additional parameters. Dotted names are rejected
        (see `_require_bare_service_name`).
        """
        logger.debug("Applying cephfs-mirror service")
        _require_bare_service_name("cephfs-mirror", spec.service_id or "")
        return self._apply_service("cephfs-mirror", spec, "{}")

    @handle_orch_error
    def remove_service(self, service_name: str, force: bool = False) -> OrchResult[str]:
        """Remove a service cluster-wide.

        Discovers all hosts currently running the requested service via
        list_services() and issues a DELETE per host using the server-side
        proxyTarget middleware (see _apply_service for transport notes).
        Errors from individual hosts are collected so a partial failure
        does not mask successful removals on other nodes.

        :param service_name: service type or type.id (e.g. "rgw",
            "nfs.mycluster")
        :param force: unused, kept for interface compatibility
        """
        logger.info(f"Removing service: {service_name}, force={force}")

        svc_type, svc_id = self._parse_service_name(service_name)

        if svc_type == 'nfs' and not svc_id:
            raise ValueError(
                "NFS removal requires service name in 'nfs.<cluster_id>' format"
            )
        # For non-NFS services, a dotted name is rejected up front:
        # MicroCeph deploys a single bare instance per node and silently
        # dropping the id would let an operator believe they were
        # removing a specific realm/filesystem while actually wiping the
        # bare service from every host.
        _require_bare_service_name(svc_type, svc_id)

        # Discover hosts currently running this service. For non-nfs
        # services group_id is unused; for nfs it filters to the specific
        # cluster_id.
        group_id = svc_id if svc_type == 'nfs' else None
        hosts = sorted(self._get_existing_service_hosts(svc_type, group_id))

        if not hosts:
            # Match cephadm: removing a service that is not deployed is
            # surfaced as an error so the operator notices a typo or
            # stale state rather than seeing a green no-op.
            raise OrchestratorError(
                f"Service {service_name!r} is not running on any host"
            )

        removed: List[str] = []
        failures: List[str] = []
        for host in hosts:
            try:
                if svc_type == 'nfs':
                    self.microceph.services.delete_nfs_service(
                        svc_id, target=host,
                    )
                else:
                    self.microceph.services.delete_service(
                        svc_type, target=host,
                    )
                removed.append(host)
            except Exception as e:
                logger.error(f"Failed to remove {service_name} from {host}: {e}")
                failures.append(f"{host}: {e}")

        # Any failure raises so the operator gets a visible error;
        # partial-success context is included in the message.
        if failures:
            ctx = f" (removed from {', '.join(removed)})" if removed else ""
            raise OrchestratorError(
                f"Failed to remove {service_name}: "
                + "; ".join(failures)
                + ctx
            )

        return f"{service_name}: removed from {', '.join(removed)}"

    @handle_orch_error
    def remove_host(self, host: str, force: bool = False,
                    offline: bool = False, rm_crush_entry: bool = False) -> OrchResult[str]:
        """Remove a host from the MicroCeph cluster.

        :param host: hostname to remove
        :param force: unused, kept for interface compatibility
        :param offline: unused, kept for interface compatibility
        :param rm_crush_entry: unused, kept for interface compatibility
        """
        logger.info(f"Removing host: {host}, force={force}")
        self.microceph.cluster.remove(host)
        return f"Removed host {host}"

    @handle_orch_error
    def service_action(self, action: str, service_name: str) -> OrchResult[List[str]]:
        """Perform an action (restart) on a service.

        Currently only 'restart' is supported via the MicroCeph API. The
        restart is fanned out per host running the service, using the
        server-side proxyTarget middleware (see _apply_service).

        :param action: one of "start", "stop", "restart", "redeploy", "reconfig"
        :param service_name: service type (e.g. "mon", "rgw")
        """
        logger.info(f"Service action: {action} on {service_name}")

        if action != "restart":
            raise OrchestratorValidationError(
                f"Service action '{action}' is not supported by MicroCeph. "
                "Only 'restart' is currently available."
            )

        svc_type, svc_id = self._parse_service_name(service_name)

        # All currently-supported restart services are bare; reject
        # dotted names so an operator does not silently restart all
        # NFS clusters (etc.) when intending to target one.
        _require_bare_service_name(svc_type, svc_id)

        if svc_type not in RESTART_SUPPORTED_SERVICES:
            raise OrchestratorValidationError(
                f"Restart of service type {svc_type!r} is not supported by "
                f"MicroCeph. Supported services: "
                f"{sorted(RESTART_SUPPORTED_SERVICES)}."
            )

        # group_id is threaded through for future-proofing: if NFS
        # were ever added to RESTART_SUPPORTED_SERVICES, _require_bare
        # above would already have rejected the dotted-name path, so
        # svc_id is always empty here; keeping the call shape future-
        # compatible avoids a silent fan-out across all clusters.
        group_id = svc_id if svc_type in DOTTED_NAME_SUPPORTED_SERVICES else None
        hosts = sorted(self._get_existing_service_hosts(svc_type, group_id))
        if not hosts:
            # Restarting a service that is not deployed is an error: the
            # operator either targeted the wrong name or expected the
            # service to be running. Match cephadm semantics.
            raise OrchestratorError(
                f"Service {svc_type!r} is not running on any host"
            )

        restarted: List[str] = []
        failures: List[str] = []
        for host in hosts:
            try:
                self.microceph.services.restart_services(
                    [svc_type], target=host,
                )
                restarted.append(host)
            except Exception as e:
                logger.error(f"Failed to restart {svc_type} on {host}: {e}")
                failures.append(f"{host}: {e}")

        if failures:
            ctx = (
                f" (restarted on {', '.join(restarted)})" if restarted else ""
            )
            raise OrchestratorError(
                f"Failed to restart {svc_type}: "
                + "; ".join(failures)
                + ctx
            )

        return [f"Restarted {svc_type} on {h}" for h in restarted]

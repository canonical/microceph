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
            # Address from microcluster is in "host:port" format.
            # rpartition splits on the last ":" to separate host from port.
            address = m.get('address', '')
            addr, _, _ = address.rpartition(":")
            if not addr:
                # No ":" found; use the raw address (may be hostname only).
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
            service_name = record['service'] if not record['group_id'] else f"{record['service']}.{record['group_id']}"
            service_host = record['location']
            service_hostlist[service_name].append(service_host)
            logger.debug(f"microcephs record service({service_name}) at ({service_host}) configured({record['info']})")
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

            service_descs.append(ServiceDescription(
                spec=spec,
                running=len(hostlist)
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
            svc_daemon_type = svc['service']
            svc_hostname = svc['location']
            svc_group_id = svc['group_id']
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

        # Resolve which hosts to include based on the filter.
        filter_hosts = None
        if host_filter and host_filter.hosts:
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

    def _get_existing_service_hosts(self, service_type: str) -> set:
        """Return the set of hostnames that already run the given service type."""
        try:
            services = self.microceph.services.list_services()
        except RemoteException:
            logger.warning(f"Failed to list services while checking existing {service_type} hosts")
            return set()

        return {
            svc['location'] for svc in services
            if svc['service'] == service_type
        }

    def _apply_service(self, service_name: str, spec: ServiceSpec, payload: str) -> str:
        """Common logic for applying (enabling) a service on the local node.

        Validates placement hosts and checks whether the service is already
        running. Calls enable_service once; the request goes to the local
        node via the unix socket.

        Returns a summary string on success.
        Raises an exception if placement is invalid or the API call fails.

        NOTE: Per-host targeting is not yet supported. The Python client
        connects via unix socket to the local node only. MicroCeph's Go
        client uses UseTarget() to proxy to specific hosts, but this is
        not exposed through the socket. See todo #6.
        Placement hosts are validated and logged, but the enable call
        always runs on the local node regardless.
        """
        hosts = self._get_placement_hosts(spec)
        existing = self._get_existing_service_hosts(service_name)

        # Check if the service is already active on the local node.
        # Since we can only target the local node, we check if any
        # of the requested hosts that are local already have the service.
        all_existing = all(h in existing for h in hosts)
        if all_existing:
            skipped_str = ', '.join(hosts)
            logger.info(f"Skipping {service_name}: already active on {skipped_str}")
            return f"{service_name}: already active on {skipped_str}"

        if existing:
            logger.info(
                f"{service_name} already active on {', '.join(existing & set(hosts))}; "
                f"enabling for remaining hosts"
            )

        logger.info(f"Enabling {service_name} (requested hosts: {', '.join(hosts)})")
        self.microceph.services.enable_service(
            name=service_name,
            payload=payload,
            wait=True,
        )

        new_hosts = [h for h in hosts if h not in existing]
        parts = [f"enabled on {', '.join(new_hosts)}"]
        skipped_hosts = [h for h in hosts if h in existing]
        if skipped_hosts:
            parts.append(f"already active on {', '.join(skipped_hosts)}")

        return f"{service_name}: {'; '.join(parts)}"

    @handle_orch_error
    def apply_rbd_mirror(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the rbd-mirror service on the target hosts.

        rbd-mirror is a client-like service with no additional parameters.
        The Go API's ClientServicePlacement handler takes no payload.
        """
        logger.debug("Applying rbd-mirror service")
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
        """
        logger.debug("Applying RGW service")

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
        return self._apply_service("nfs", spec, payload)

    @handle_orch_error
    def apply_mon(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the MON service on the target hosts."""
        logger.debug("Applying MON service")
        return self._apply_service("mon", spec, "{}")

    @handle_orch_error
    def apply_mgr(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the MGR service on the target hosts."""
        logger.debug("Applying MGR service")
        return self._apply_service("mgr", spec, "{}")

    @handle_orch_error
    def apply_mds(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the MDS service on the target hosts."""
        logger.debug("Applying MDS service")
        return self._apply_service("mds", spec, "{}")

    @handle_orch_error
    def apply_cephfs_mirror(self, spec: ServiceSpec) -> OrchResult[str]:
        """Enable the cephfs-mirror service on the target hosts.

        cephfs-mirror is a client-like service (same as rbd-mirror)
        with no additional parameters.
        """
        logger.debug("Applying cephfs-mirror service")
        return self._apply_service("cephfs-mirror", spec, "{}")

    @handle_orch_error
    def remove_service(self, service_name: str, force: bool = False) -> OrchResult[str]:
        """Remove a service from the local node.

        Sends a DELETE request to the local MicroCeph API. This removes
        the service from the node connected via the unix socket only;
        it does not remove the service cluster-wide. Per-host targeting
        requires UseTarget support (see todo #6).

        :param service_name: service type or type.id (e.g. "rgw", "nfs.mycluster")
        :param force: unused, kept for interface compatibility
        """
        logger.info(f"Removing service: {service_name}, force={force}")

        svc_type, svc_id = self._parse_service_name(service_name)

        # NFS requires the cluster_id in the delete body
        if svc_type == 'nfs':
            if not svc_id:
                raise ValueError("NFS removal requires service name in 'nfs.<cluster_id>' format")
            self.microceph.services.delete_nfs_service(svc_id)
        else:
            self.microceph.services.delete_service(svc_type)

        return f"Removed service {service_name}"

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

        Currently only 'restart' is supported via the MicroCeph API.

        :param action: one of "start", "stop", "restart", "redeploy", "reconfig"
        :param service_name: service type (e.g. "mon", "rgw")
        """
        logger.info(f"Service action: {action} on {service_name}")

        if action != "restart":
            raise NotImplementedError(
                f"Service action '{action}' is not supported by MicroCeph. "
                "Only 'restart' is currently available."
            )

        svc_type, _ = self._parse_service_name(service_name)

        self.microceph.services.restart_services([svc_type])
        return [f"Restarted {service_name}"]

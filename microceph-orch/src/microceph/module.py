#
# Copyright 2025, Canonical Ltd.
#

from collections import defaultdict
import json
import logging
import time
from typing import Any, Dict, List, Optional, Tuple

from ceph.deployment.inventory import Device, Devices
from ceph.deployment.service_spec import (
    ServiceSpec,
    PlacementSpec,
    RGWSpec,
    MONSpec,
    MDSSpec,
    NFSServiceSpec,
    SMBSpec,
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
    OrchestratorCLICommandBase,
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
    'smb': SMBSpec,
}

class MicroCephOrchestrator(Orchestrator, MgrModule):
    CLICommand = OrchestratorCLICommandBase.make_registry_subtype(
        "MicroCephOrchestratorCLICommand"
    )

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

        return True, "", {}

    def notify(self, notify_type: NotifyType, notify_id: str) -> None:
        """

        :param notify_type:
        :param notify_id:
        :return:
        """
        logger.info(f"noop: notify called with notify_type: {notify_type} and notify_id: {notify_id}")

    def _microceph_hosts(self) -> List[HostSpec]:
        """Return current MicroCeph members as Ceph host specifications."""
        specs = []
        for member in self.microceph.cluster.get_cluster_members():
            addr, _, _ = member['address'].rpartition(":")
            specs.append(HostSpec(member['name'], addr, status=member['status']))
        return specs

    @handle_orch_error
    def get_hosts(self) -> List[HostSpec]:
        """
        Report the hosts in the cluster.

        :return: list of HostSpec
        """
        return self._microceph_hosts()

    def _get_service_hostlist(self, recorded_services: list) -> dict:
        """Get a dict describing the distribution of services"""
        service_hostlist = defaultdict(list)
        for record in recorded_services:
            service_name = record['service'] if not record['group_id'] else f"{record['service']}.{record['group_id']}"
            service_host = record['location']
            service_hostlist[service_name].append(service_host)
            logger.info(f"microcephs record service({service_name}) at ({service_host}) configured({record['info']})")
        return service_hostlist

    def _elaborate_service(self, service: str):
        """Elaborate a service into id and type"""
        if '.' in service:
            segments = service.split('.')
            return segments[0], segments[1]
        else:
            return service, ""

    @staticmethod
    def _smb_config_uri(records: list, cluster_id: str) -> str:
        """Return the consistent stored configuration URI for an SMB cluster."""
        config_uris = set()
        for record in records:
            if record['service'] != 'smb' or record['group_id'] != cluster_id:
                continue
            try:
                info = json.loads(record['info'])
            except (KeyError, TypeError, json.JSONDecodeError) as err:
                raise ValueError(
                    f"invalid stored SMB service information for '{cluster_id}'"
                ) from err
            config_uri = info.get('config_uri')
            if not isinstance(config_uri, str) or not config_uri:
                raise ValueError(
                    f"missing SMB configuration URI for '{cluster_id}'"
                )
            config_uris.add(config_uri)

        if len(config_uris) != 1:
            raise ValueError(
                f"inconsistent SMB configuration URIs for '{cluster_id}'"
            )

        return config_uris.pop()

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
            svc_type, svc_id = self._elaborate_service(svc_name)
            logger.info(f"{svc_name} under description for filter {service_type}")

            # skip unrelated services if a specific daemon type is requested.
            if service_type and svc_type != service_type:
                continue

            placement = PlacementSpec(hosts=hostlist, count=len(hostlist))
            if svc_type == 'smb':
                spec = SMBSpec(
                    service_id=svc_id,
                    placement=placement,
                    cluster_id=svc_id,
                    config_uri=self._smb_config_uri(recorded_services, svc_id),
                )
            elif svc_type in daemon_spec_map:
                spec = daemon_spec_map[svc_type](
                    service_id=svc_id, service_type=svc_type, placement=placement
                )
            else:
                spec = ServiceSpec(
                    service_id=svc_id, service_type=svc_type, placement=placement
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
            svc_group_ip = svc['group_id']
            svc_ip = None
            svc_ports = None
            svc_name = f"{svc_daemon_type}.{svc_group_ip}" if svc_group_ip else svc_daemon_type
            if daemon_type:
                if svc_daemon_type != daemon_type:
                    continue

            if svc_daemon_type == 'nfs':
                info = json.loads(svc['info'])
                svc_ip = None if "0.0.0.0" in info['bind_address'] else info['bind_address']
                svc_ports = [info['bind_port']]
            
            descriptions.append(DaemonDescription(
                service_name=svc_name,
                daemon_type=svc_daemon_type,
                daemon_id=svc_hostname,
                hostname=svc_hostname,
                ip=svc_ip,
                ports=svc_ports
            ))

        logger.info(descriptions)
        return descriptions

    @handle_orch_error
    def get_inventory(self,
                host_filter: Optional[InventoryFilter] = None,
                refresh: bool = False
            ) -> List[InventoryHost]:

        disks = self.microceph.services.list_disks()
        disks_by_host = defaultdict(list)
        for d in disks:
            disks_by_host[d['location']].append(
                Device(path=d['path'])
            )

        inventory = []
        for host, diskettes in disks_by_host.items():
            inventory.append(InventoryHost(
                name=host,
                devices=Devices(diskettes)
            ))

        return inventory

    def _smb_target_hosts(self, spec: SMBSpec) -> List[str]:
        """Resolve an SMB placement to one native smbd process per host."""
        placement = spec.placement
        if placement.count_per_host not in (None, 1):
            raise ValueError("native SMB supports at most one daemon per host")

        hosts = self._microceph_hosts()
        candidates = placement.filter_matching_hostspecs(hosts)
        target_count = placement.get_target_count(hosts)
        targets = candidates[:target_count]
        if not targets:
            raise ValueError("SMB placement did not select any MicroCeph members")

        return targets

    @staticmethod
    def _validate_smb_spec(spec: SMBSpec) -> None:
        """Reject SMBSpec settings that native MicroCeph cannot implement."""
        service_id = getattr(spec, "service_id", spec.cluster_id)
        if service_id != spec.cluster_id:
            raise ValueError(
                "native SMB does not support a service ID distinct from its cluster ID"
            )

        unsupported_fields = (
            ("SMB features", getattr(spec, "features", [])),
            ("domain join sources", getattr(spec, "join_sources", [])),
            ("custom DNS", getattr(spec, "custom_dns", [])),
            ("custom ports", getattr(spec, "custom_ports", None)),
            ("cluster public addresses", getattr(spec, "cluster_public_addrs", [])),
            ("cluster bind addresses", getattr(spec, "bind_addrs", [])),
            ("remote-control certificates", getattr(spec, "remote_control_ssl_cert", None)),
            ("remote-control keys", getattr(spec, "remote_control_ssl_key", None)),
            ("remote-control CA certificates", getattr(spec, "remote_control_ca_cert", None)),
            ("container arguments", getattr(spec, "extra_container_args", [])),
            ("entrypoint arguments", getattr(spec, "extra_entrypoint_args", [])),
            ("custom configuration files", getattr(spec, "custom_configs", [])),
            ("network restrictions", getattr(spec, "networks", [])),
            ("Ceph configuration overrides", getattr(spec, "config", None)),
        )
        for description, value in unsupported_fields:
            if value:
                raise ValueError(f"native SMB does not support {description}")

        if getattr(spec, "unmanaged", False):
            raise ValueError("native SMB does not support unmanaged services")
        if getattr(spec, "preview_only", False):
            raise ValueError("native SMB does not support preview-only services")

        allowed_user = f"client.smb.fs.cluster.{spec.cluster_id}"
        ceph_users = set(getattr(spec, "include_ceph_users", []) or [])
        if ceph_users.difference({allowed_user}):
            raise ValueError("native SMB does not support additional Ceph users")

    @staticmethod
    def _smb_payload(spec: SMBSpec) -> Dict[str, Any]:
        """Convert the upstream SMB spec into the node-local placement payload."""
        return {
            "cluster_id": spec.cluster_id,
            "config_uri": spec.config_uri,
            "features": list(spec.features or []),
            "join_sources": list(spec.join_sources or []),
            "user_sources": list(spec.user_sources or []),
        }

    @handle_orch_error
    def apply_smb(self, spec: SMBSpec) -> str:
        """Reconcile an upstream SMB spec onto selected MicroCeph members."""
        self._validate_smb_spec(spec)

        records = self.microceph.services.list_services() or []
        current = [record for record in records if record['service'] == 'smb']
        current_clusters = {record['group_id'] for record in current}
        other_clusters = current_clusters.difference({spec.cluster_id})
        if other_clusters:
            names = ', '.join(sorted(other_clusters))
            raise ValueError(
                "native MicroCeph supports only one SMB cluster; "
                f"already placed: {names}"
            )

        targets = self._smb_target_hosts(spec)
        target_set = set(targets)
        current_hosts = {
            record['location']
            for record in current
            if record['group_id'] == spec.cluster_id
        }
        payload = self._smb_payload(spec)

        for target in targets:
            self.microceph.services.apply_smb(target, payload)

        for target in sorted(current_hosts.difference(target_set)):
            self.microceph.services.remove_smb(target, spec.cluster_id)

        return f"Applied SMB service '{spec.cluster_id}'"

    @handle_orch_error
    def remove_service(self, service_name: str, force: bool = False) -> str:
        """Remove a native SMB service from every currently placed member."""
        service_type, separator, cluster_id = service_name.partition('.')
        if service_type != 'smb' or not separator or not cluster_id:
            raise NotImplementedError(f"service removal is not supported for {service_name!r}")

        records = self.microceph.services.list_services() or []
        members = sorted(
            record['location']
            for record in records
            if record['service'] == 'smb' and record['group_id'] == cluster_id
        )
        for member in members:
            self.microceph.services.remove_smb(member, cluster_id)

        return f"Removed SMB service '{cluster_id}'"

    def apply_rbd_mirror(self, spec: ServiceSpec) -> OrchResult[str]:
        logger.info(f"Received Apply Request for RBD Mirror: Spec: {vars(spec).items()}")
        raise NotImplementedError() 

    def apply_rgw(self, spec: RGWSpec) -> OrchResult[str]:
        """

        :param spec:
        :return:
        """
        raise NotImplementedError()

    def apply_nfs(self, spec: NFSServiceSpec) -> OrchResult[str]:
        """

        :param spec:
        :return:
        """
        raise NotImplementedError()

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

    @handle_orch_error
    def get_hosts(self) -> List[HostSpec]:
        """
        Report the hosts in the cluster.

        :return: list of HostSpec
        """
        specs = []
        for m in self.microceph.cluster.get_cluster_members():
            addr, _, _ = m['address'].rpartition(":")
            specs.append(HostSpec(m['name'], addr, status=m['status']))

        return specs

    def _get_service_hostlist(self, recorded_services: list) -> dict:
        """Get a dict describing the distribution of services"""
        service_hostlist = defaultdict(list)
        for record in recorded_services:
            service_name = record['service'] if not record['group_id'] else f"{record['service']}.{record['group_id']}"
            service_host = record['location']
            service_hostlist[service_name].append(service_host)
            if record['service'] == 'smb':
                logger.info(f"microcephs record service({service_name}) at ({service_host})")
            else:
                logger.info(f"microcephs record service({service_name}) at ({service_host}) configured({record['info']})")
        return service_hostlist

    def _elaborate_service(self, service: str):
        """Elaborate a service into id and type"""
        if '.' in service:
            segments = service.split('.')
            return segments[0], segments[1]
        else:
            return service, ""

    def _get_smb_config_uri(self, recorded_services: list, cluster_id: str) -> Optional[str]:
        """Return only an agreed applied source reference, never source contents."""
        uris = set()
        prefixes = (f'rados://.smb/{cluster_id}/',
                    f'rados:mon-config-key:smb/config/{cluster_id}/')
        for record in recorded_services:
            if record['service'] != 'smb' or record['group_id'] != cluster_id:
                continue
            try:
                info = json.loads(record.get('info') or '{}')
            except (TypeError, ValueError):
                return None
            uri = info.get('config_uri') if isinstance(info, dict) else None
            if not isinstance(uri, str):
                return None
            # Match the native source-store boundary; do not forward arbitrary
            # legacy strings (which could contain credentials) into inventory.
            object_name = next((uri[len(p):] for p in prefixes if uri.startswith(p)), '')
            if (object_name in ('', '.', '..')
                    or any(c in object_name for c in '/\\\x00\r\n?#%')):
                return None
            uris.add(uri)
        return next(iter(uris)) if len(uris) == 1 else None

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

            if svc_type == 'smb':
                config_uri = self._get_smb_config_uri(recorded_services, svc_id)
                if config_uri is None:
                    logger.warning(f"Omitting SMB group {svc_id} from service inventory: "
                                   "missing, invalid or conflicting applied config_uri; "
                                   "daemon observations remain available via orch ps")
                    continue
                # YAML inventory reconstructs SMBSpec via ServiceSpec.from_json.
                # Supply only its required identity/source fields from applied
                # observations, not a deployable resource or inferred settings.
                spec = SMBSpec(
                    service_id=svc_id, cluster_id=svc_id, config_uri=config_uri,
                    unmanaged=True,
                    placement=PlacementSpec(hosts=hostlist, count=len(hostlist)))
            elif svc_type in daemon_spec_map:
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
            svc_group_ip = svc['group_id']
            svc_ip = None
            svc_ports = None
            svc_status_desc = None
            svc_name = f"{svc_daemon_type}.{svc_group_ip}" if svc_group_ip else svc_daemon_type
            if daemon_type:
                if svc_daemon_type != daemon_type:
                    continue

            if svc_daemon_type == 'nfs':
                info = json.loads(svc['info'])
                svc_ip = None if "0.0.0.0" in info['bind_address'] else info['bind_address']
                svc_ports = [info['bind_port']]
            elif svc_daemon_type == 'smb':
                try:
                    info = json.loads(svc.get('info') or '{}')
                except (TypeError, ValueError):
                    info = {}
                if not isinstance(info, dict):
                    info = {}
                # Legacy rows may contain only smb_port, or no observation.
                # Do not invent a frontend or metrics endpoint from defaults.
                svc_ip = info.get('frontend_address') or None
                port = info.get('smb_port')
                if type(port) is int and 0 < port <= 65535:
                    svc_ports = [port]
                metrics_port = info.get('metrics_port')
                if (info.get('metrics_address') == '0.0.0.0'
                        and type(metrics_port) is int and 0 < metrics_port <= 65535):
                    # DaemonDescription has one IP for all ports. Metrics is an
                    # IPv4 wildcard, NOT a listener on the SMB (possibly IPv6) IP.
                    svc_status_desc = f"smbmetrics listens on 0.0.0.0:{metrics_port}"

            descriptions.append(DaemonDescription(
                service_name=svc_name,
                daemon_type=svc_daemon_type,
                daemon_id=svc_hostname,
                hostname=svc_hostname,
                ip=svc_ip,
                ports=svc_ports,
                status_desc=svc_status_desc
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

#
# Copyright 2025, Canonical Ltd.
#

import os, sys

import logging
from typing import Tuple, Dict, Any, List, Optional
from collections import defaultdict
import time
import json

from ceph.deployment.inventory import Device, Devices
from ceph.deployment.service_spec import (
    ServiceSpec,
    PlacementSpec,
    HostPlacementSpec,
    IngressSpec,
    RGWSpec,
    MONSpec,
    MDSSpec, NFSServiceSpec,
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

        return True, "", {}

    def notify(self, notify_type: NotifyType, notify_id: str) -> None:
        """

        :param notify_type:
        :param notify_id:
        :return:
        """
        logger.info(f"notify called with notify_type: {notify_type} and notify_id: {notify_id}")

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

    @handle_orch_error
    def describe_service(self,
                service_type: Optional[str] = None,
                service_name: Optional[str] = None,
                refresh: bool = False
            ) -> List[ServiceDescription]:

        logger.info(f"describing service... service_type={service_type}, service_name={service_name}, "
                    f"refresh={refresh}")

        services = self.microceph.services.list_services()
        service_map = defaultdict(list)
        for svc in services:
            service_map[svc['service']].append(svc['location'])

        service_descs = []
        for service in services:
            spec = None
            name = service['service']
            hostname = service['location']
            svc_id = service['group_id']
            info = service['info']
            logger.info(f"InfoStr: {info}")
            
            if service_type and name != service_type:
                continue

            if name == 'mon':
                spec = MONSpec(service_type='mon', placement=PlacementSpec(hosts=[hostname], count=1))
            elif name == 'mds':
                spec = MDSSpec(service_type='mds', placement=PlacementSpec(hosts=[hostname], count=1))
            elif name == 'rgw':
                spec = RGWSpec(service_type='rgw', placement=PlacementSpec(hosts=[hostname], count=1))
            elif name == 'nfs':
                spec = NFSServiceSpec(service_id=svc_id, placement=PlacementSpec(hosts=[hostname], count=1))
            else:
                spec = ServiceSpec(service_type=name, placement=PlacementSpec(hosts=[hostname], count=1))

            service_descs.append(ServiceDescription(
                spec=spec
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

    def get_inventory(self,
                host_filter: Optional[InventoryFilter] = None,
                refresh: bool = False
            ) -> OrchResult[List[InventoryHost]]:

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

        return OrchResult(inventory)

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

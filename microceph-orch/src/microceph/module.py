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
    DaemonDescriptionStatus,
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
    def _smb_service_groups(records: List[Dict[str, Any]]) -> Dict[str, Dict[str, Any]]:
        """Collect SMB specs and members from grouped-service records."""
        groups: Dict[str, Dict[str, Any]] = {}
        for record in records:
            if record.get("service") != "smb":
                continue

            cluster_id = record.get("group_id")
            member = record.get("location")
            group_config = record.get("group_config")
            if not isinstance(cluster_id, str) or not cluster_id:
                raise ValueError("invalid SMB grouped-service identity")
            if not isinstance(member, str) or not member:
                raise ValueError("invalid SMB grouped-service member")
            if not isinstance(group_config, str):
                raise ValueError(f"missing SMB group configuration for '{cluster_id}'")

            try:
                config = json.loads(group_config)
            except json.JSONDecodeError as err:
                raise ValueError(
                    f"invalid SMB group configuration for '{cluster_id}'"
                ) from err
            desired_spec = config.get("desired_spec")
            if not isinstance(desired_spec, dict):
                raise ValueError(f"missing SMB desired spec for '{cluster_id}'")

            group = groups.setdefault(
                cluster_id,
                {"desired_spec": desired_spec, "members": []},
            )
            if group["desired_spec"] != desired_spec:
                raise ValueError(f"inconsistent SMB group configuration for '{cluster_id}'")
            group["members"].append(member)

        for group in groups.values():
            group["members"].sort()
        return groups

    def _smb_descriptions(
        self, records: List[Dict[str, Any]], service_name: Optional[str]
    ) -> List[ServiceDescription]:
        """Build SMB descriptions from grouped-service configuration."""
        descriptions = []
        smb_records = [record for record in records if record.get("service") == "smb"]
        cluster_ids = sorted({
            record.get("group_id")
            for record in smb_records
            if isinstance(record.get("group_id"), str) and record.get("group_id")
        })
        for cluster_id in cluster_ids:
            cluster_records = [
                record for record in smb_records
                if record.get("group_id") == cluster_id
            ]
            try:
                group = self._smb_service_groups(cluster_records)[cluster_id]
                spec = SMBSpec.from_json(group["desired_spec"])
            except Exception as err:
                logger.error("skipping invalid SMB service %s: %s", cluster_id, err)
                continue
            expected_service_name = f"smb.{cluster_id}"
            if service_name and service_name != expected_service_name:
                continue
            descriptions.append(ServiceDescription(spec=spec, running=0))
        return descriptions

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
        if service_type in (None, "smb"):
            service_descs = self._smb_descriptions(recorded_services, service_name)
        for svc_name, hostlist in service_hostlist.items():
            spec = None
            svc_type, svc_id = self._elaborate_service(svc_name)
            logger.info(f"{svc_name} under description for filter {service_type}")

            # skip unrelated services if a specific daemon type is requested.
            if service_type and svc_type != service_type:
                continue
            if service_name and svc_name != service_name:
                continue
            if svc_type == "smb":
                continue

            placement = PlacementSpec(hosts=hostlist, count=len(hostlist))
            if svc_type in daemon_spec_map:
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
            if daemon_type and svc_daemon_type != daemon_type:
                continue
            if service_name and svc_name != service_name:
                continue
            if daemon_id and svc_hostname != daemon_id:
                continue
            if host and svc_hostname != host:
                continue
            if svc_daemon_type == "smb":
                descriptions.append(DaemonDescription(
                    service_name=svc_name,
                    daemon_type="smb",
                    daemon_id=svc_hostname,
                    hostname=svc_hostname,
                    status=DaemonDescriptionStatus.unknown,
                    status_desc="runtime state is not reported by the MicroCeph services API",
                    is_active=False,
                ))
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
        """Resolve an SMB placement to validated MicroCeph member names."""
        placement = spec.placement
        if placement.count_per_host not in (None, 1):
            raise ValueError("native SMB supports at most one daemon per host")
        if getattr(placement, "label", None):
            raise ValueError("native SMB placement does not support labels")
        if getattr(placement, "host_pattern", None):
            raise ValueError("native SMB placement does not support host patterns")

        hosts = self._microceph_hosts()
        member_names = [host.hostname for host in hosts]
        explicit_hosts = list(getattr(placement, "hosts", []) or [])
        if explicit_hosts:
            for host in explicit_hosts:
                if getattr(host, "network", ""):
                    raise ValueError(
                        "native SMB placement does not support host network overrides"
                    )
                if getattr(host, "name", ""):
                    raise ValueError(
                        "native SMB placement does not support daemon names"
                    )

            candidates = [host.hostname for host in explicit_hosts]
            unknown_hosts = sorted(set(candidates).difference(member_names))
            if unknown_hosts:
                names = ", ".join(unknown_hosts)
                raise ValueError(f"unknown MicroCeph members: {names}")
        else:
            # Count-based placement must not consume a slot on an offline node.
            candidates = [host.hostname for host in hosts if host.status.upper() == "ONLINE"]

        target_count = placement.get_target_count(hosts)
        if not explicit_hosts and placement.count is None:
            target_count = len(candidates)
        if target_count > len(candidates):
            raise ValueError(
                f"SMB placement requests {target_count} daemons but only "
                f"{len(candidates)} hosts are available"
            )
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

        features = set(getattr(spec, "features", []) or [])
        unsupported_features = sorted(features.difference({"clustered"}))
        if unsupported_features:
            names = ", ".join(unsupported_features)
            raise ValueError(f"native SMB does not support SMB features: {names}")
        if "clustered" in features:
            if not getattr(spec, "cluster_meta_uri", None):
                raise ValueError("clustered native SMB requires cluster metadata")
            if not getattr(spec, "cluster_lock_uri", None):
                raise ValueError("clustered native SMB requires a cluster lock")

        custom_ports = dict(getattr(spec, "custom_ports", None) or {})
        unsupported_ports = sorted(set(custom_ports).difference({"smb", "ctdb"}))
        if unsupported_ports:
            names = ", ".join(unsupported_ports)
            raise ValueError(f"native SMB does not support custom ports: {names}")
        if "ctdb" in custom_ports and custom_ports["ctdb"] != 4379:
            raise ValueError("native SMB does not support a custom CTDB port")
        smb_port = custom_ports.get("smb")
        if smb_port is not None and not 0 < smb_port < 65536:
            raise ValueError("native SMB requires a valid custom SMB port")

        unsupported_fields = (
            ("domain join sources", getattr(spec, "join_sources", [])),
            ("custom DNS", getattr(spec, "custom_dns", [])),
            ("cluster public addresses", getattr(spec, "cluster_public_addrs", [])),
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
    def _smb_rank_state(cluster_id: str, records: List[Dict[str, Any]]) -> Tuple[Dict[str, int], int]:
        """Recover all assigned ranks, including retired hosts, from shared state."""
        ranks: Dict[str, int] = {}
        next_rank = 0
        for record in records:
            raw_config = record.get("group_config")
            if raw_config is None:
                continue
            try:
                config = json.loads(raw_config)
            except (TypeError, ValueError) as err:
                raise ValueError(f"invalid SMB group configuration for '{cluster_id}'") from err
            stored = config.get("ctdb_ranks", {})
            counter = config.get("next_ctdb_rank", 0)
            if not isinstance(stored, dict) or type(counter) is not int or counter < 0:
                raise ValueError(f"invalid CTDB rank state for '{cluster_id}'")
            next_rank = max(next_rank, counter)
            for identity, rank in stored.items():
                if not isinstance(identity, str) or not identity or type(rank) is not int or rank < 0:
                    raise ValueError(f"invalid CTDB rank state for '{cluster_id}'")
                if identity in ranks and ranks[identity] != rank:
                    raise ValueError(f"inconsistent CTDB rank for '{identity}'")
                ranks[identity] = rank

        # Older records can carry a node rank without the shared rank map.
        for record in records:
            raw_info = record.get("info")
            if not raw_info:
                continue
            try:
                info = json.loads(raw_info)
            except (TypeError, ValueError) as err:
                raise ValueError(f"invalid SMB member info for '{cluster_id}'") from err
            rank = info.get("ctdb_rank")
            if rank is None:
                continue
            identity = info.get("ctdb_identity") or f"smb.{cluster_id}.{record['location']}"
            if type(rank) is not int or rank < 0 or not isinstance(identity, str):
                raise ValueError(f"invalid CTDB rank for '{identity}'")
            if identity in ranks and ranks[identity] != rank:
                raise ValueError(f"inconsistent CTDB rank for '{identity}'")
            ranks[identity] = rank

        if len(set(ranks.values())) != len(ranks):
            raise ValueError(f"duplicate CTDB ranks for '{cluster_id}'")
        next_rank = max(next_rank, max(ranks.values(), default=-1) + 1)
        # When upgrading records without rank metadata, reserve a rank for each
        # existing member before allocating any newly placed host.
        for record in sorted(records, key=lambda item: item["location"]):
            identity = f"smb.{cluster_id}.{record['location']}"
            if identity not in ranks:
                ranks[identity] = next_rank
                next_rank += 1
        return ranks, next_rank

    def _smb_payloads(
        self, spec: SMBSpec, targets: List[str], ranks: Dict[str, int], next_rank: int
    ) -> Dict[str, Dict[str, Any]]:
        """Return each target's upstream spec and persistent CTDB rank state."""
        desired_spec = spec.to_json()
        if "clustered" not in (getattr(spec, "features", []) or []):
            return {target: desired_spec for target in targets}
        return {
            target: {
                "service_spec": desired_spec,
                "microceph": {
                    "ctdb": {
                        "rank": ranks[f"smb.{spec.cluster_id}.{target}"],
                        "identity": f"smb.{spec.cluster_id}.{target}",
                    },
                    "ctdb_ranks": ranks.copy(),
                    "next_ctdb_rank": next_rank,
                },
            }
            for target in targets
        }

    @handle_orch_error
    def apply_smb(self, spec: SMBSpec) -> str:
        """Reconcile an upstream SMB spec onto selected MicroCeph members."""
        self._validate_smb_spec(spec)

        records = self.microceph.services.list_services() or []
        current = [record for record in records if record['service'] == 'smb']
        targets = self._smb_target_hosts(spec)
        target_set = set(targets)

        # Multiple SMB clusters are supported on disjoint hosts; a host can
        # only serve one cluster (single smbd, single config, port 445).
        members_by_cluster: Dict[str, set] = {}
        for record in current:
            members_by_cluster.setdefault(record['group_id'], set()).add(
                record['location']
            )
        conflicts = []
        for cluster_id in sorted(members_by_cluster):
            if cluster_id == spec.cluster_id:
                continue
            overlap = sorted(members_by_cluster[cluster_id] & target_set)
            if overlap:
                conflicts.append(
                    f"host(s) {', '.join(overlap)} already serve '{cluster_id}'"
                )
        if conflicts:
            raise ValueError(
                "native MicroCeph hosts serve at most one SMB cluster; "
                + "; ".join(conflicts)
            )

        current_hosts = {
            record['location']
            for record in current
            if record['group_id'] == spec.cluster_id
        }
        cluster_records = [record for record in current if record['group_id'] == spec.cluster_id]
        clustered = "clustered" in (getattr(spec, "features", []) or [])
        if not clustered and len(targets) != 1:
            raise ValueError("non-clustered SMB requires exactly one member")
        for record in cluster_records:
            if not record.get("group_config"):
                continue
            config = json.loads(record["group_config"])
            previous = config.get("desired_spec")
            if previous is not None:
                previous = previous.get("spec", previous)
                if ("clustered" in (previous.get("features") or [])) != clustered:
                    raise ValueError("SMB clustering mode cannot be changed; recreate the cluster")
        if not clustered and current_hosts.difference(target_set):
            raise ValueError("remove the existing non-clustered member before relocating SMB")
        ranks, next_rank = {}, 0
        if clustered:
            ranks, next_rank = self._smb_rank_state(spec.cluster_id, cluster_records)
            for target in sorted(targets):
                identity = f"smb.{spec.cluster_id}.{target}"
                if identity not in ranks:
                    ranks[identity] = next_rank
                    next_rank += 1
        payloads = self._smb_payloads(spec, targets, ranks, next_rank)
        failures = []
        for target in sorted(targets):
            try:
                self.microceph.services.apply_smb(target, payloads[target])
            except Exception as err:
                failures.append(("apply", target, err))

        # A failed replacement must not turn into removal of its healthy source.
        # Retry additions on the next apply before attempting scale-down again.
        if not failures:
            for target in sorted(current_hosts.difference(target_set)):
                try:
                    self.microceph.services.remove_smb(target, spec.cluster_id)
                except Exception as err:
                    failures.append(("remove", target, err))

        if failures:
            details = "; ".join(
                f"{operation} {target}: {err}"
                for operation, target, err in failures
            )
            raise RuntimeError(f"SMB reconciliation incomplete: {details}")

        return f"Applied SMB service '{spec.cluster_id}'"

    @handle_orch_error
    def remove_service(
        self,
        service_name: str,
        force: bool = False,
        force_delete_data: bool = False,
    ) -> str:
        """Remove a native SMB service from every currently placed member."""
        service_type, separator, cluster_id = service_name.partition('.')
        if service_type != 'smb' or not separator or not cluster_id:
            raise NotImplementedError(f"service removal is not supported for {service_name!r}")

        try:
            group = self.microceph.services.get_smb_group(cluster_id)
        except Exception as err:
            raise RuntimeError(f"SMB removal could not read reservation state: {err}") from err
        group_config = group.get("group_config", "")
        if not isinstance(group_config, str):
            raise RuntimeError("SMB removal received invalid reservation state")
        members = set()
        records = self.microceph.services.list_services() or []
        other_smb_members = set()
        for record in records:
            if record['service'] != 'smb':
                continue
            if record['group_id'] == cluster_id:
                # Observed receipts for this cluster always require cleanup.
                members.add(record['location'])
            else:
                other_smb_members.add(record['location'])
        cluster_members = {
            member['name'] for member in self.microceph.cluster.get_cluster_members()
        }
        if group_config:
            try:
                ranks = json.loads(group_config).get("ctdb_ranks", {})
            except (TypeError, ValueError) as err:
                raise RuntimeError(f"SMB removal received invalid reservation state: {err}") from err
            prefix = f"smb.{cluster_id}."
            if not isinstance(ranks, dict):
                raise RuntimeError("SMB removal received invalid CTDB reservations")
            for identity in ranks:
                if isinstance(identity, str) and identity.startswith(prefix) and identity[len(prefix):]:
                    member = identity[len(prefix):]
                    if member in cluster_members and member not in other_smb_members:
                        members.add(member)
        failures = []
        for member in sorted(members):
            try:
                self.microceph.services.remove_smb(member, cluster_id)
            except Exception as err:
                failures.append((member, err))
        if failures:
            details = "; ".join(f"remove {member}: {err}" for member, err in failures)
            raise RuntimeError(f"SMB removal incomplete: {details}")
        try:
            resource = self.remote('smb', 'show', [f'ceph.smb.cluster.{cluster_id}'])
        except Exception as err:
            raise RuntimeError(f"SMB removal could not confirm cluster deletion: {err}") from err
        if self._smb_resource_survives(resource, cluster_id):
            return f"Removed SMB service '{cluster_id}'"
        try:
            self.microceph.services.finalize_smb(cluster_id, group_config)
        except Exception as err:
            raise RuntimeError(f"SMB removal finalization incomplete: {err}") from err

        return f"Removed SMB service '{cluster_id}'"

    @staticmethod
    def _smb_resource_survives(resource: Any, cluster_id: str) -> bool:
        """Return whether the in-process smb module still owns cluster_id."""
        if not isinstance(resource, dict):
            raise ValueError("invalid SMB resource response")
        resources = resource.get("resources")
        if resources is None:
            resources = [resource]
        if not isinstance(resources, list):
            raise ValueError("invalid SMB resource response")
        for item in resources:
            if not isinstance(item, dict):
                raise ValueError("invalid SMB resource response")
            if item.get("resource_type") == "ceph.smb.cluster" and item.get("cluster_id") == cluster_id:
                return True
        return False

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

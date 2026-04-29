# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import logging

from . import service

logger = logging.getLogger(__name__)


class MicroClusterService(service.BaseService):
    """Client for default MicroCluster Service API."""

    def get_cluster_members(self) -> list:
        """List members in the cluster.

        Returns a list of all members in the cluster.
        """
        result = []
        cluster = self._get("/core/1.0/cluster")
        members = cluster.get("metadata") or []
        keys = ["name", "address", "status"]
        for member in members:
            result.append({k: v for k, v in member.items() if k in keys})
        return result

    def remove(self, name: str) -> None:
        """Remove node from the cluster.

        Raises NodeNotExistInClusterException if node does not
        exist in the cluster.
        Raises NodeRemoveFromClusterException if the node is last
        member of the cluster.
        """
        self._delete(f"/core/1.0/cluster/{name}")

class StatusService(service.BaseService):
    """Client for the MicroCeph status API."""

    def is_available(self) -> None:
        """Checks to see if the API is available.

        If the API is available, nothing will be returned and this
        will simply work silently.

        If the API is not available, it will raise a service.RemoteException
        indicating the error.
        """
        self._get("/").get("metadata")


class ExtendedAPIService(service.BaseService):
    """Client for MicroCeph extended Cluster API."""

    def list_services(self) -> list[dict]:
        """List all services."""
        services = self._get("/1.0/services")
        return services.get("metadata") or []

    def list_resources(self) -> list[dict]:
        """List all resources."""
        nodes = self._get("/1.0/resources")
        return nodes.get("metadata") or []

    def list_disks(self) -> list[dict]:
        """List all disks"""
        disks = self._get("/1.0/disks")
        return disks.get("metadata") or []

    def get_status(self) -> dict[str, dict]:
        """Get status of the cluster."""
        cluster = self._get("/1.0/status")
        members = cluster.get("metadata") or []
        return {
            member["name"]: {
                "status": member["status"],
                "address": member["address"],
            }
            for member in members
        }

    def enable_service(self, name: str, payload: str = "", wait: bool = True) -> None:
        """Enable a service on the cluster.

        Sends a PUT request to /1.0/services/<name> with an EnableService payload.
        The Go API dispatches this to ServicePlacementHandler which runs the full
        placement lifecycle: PopulateParams, HospitalityCheck, ServiceInit,
        PostPlacementCheck, DbUpdate.

        :param name: service name (e.g. 'rgw', 'nfs', 'rbd-mirror')
        :param payload: JSON string with service-specific parameters
        :param wait: if True, the server waits for the service to be fully enabled
        """
        # Note: The "bool" key maps to Go's EnableService.Wait field which has
        # the struct tag `json:"bool"` (upstream naming quirk in MicroCeph).
        self._put(f"/1.0/services/{name}", json={
            "name": name,
            "bool": wait,
            "payload": payload,
        })

    def delete_service(self, name: str) -> None:
        """Delete/disable a service on the cluster.

        :param name: service name (e.g. 'rgw', 'nfs', 'rbd-mirror')
        """
        self._delete(f"/1.0/services/{name}")

    def restart_services(self, services: list[str]) -> None:
        """Restart one or more services on the cluster.

        Sends a POST to /1.0/services/restart with a list of service objects.

        :param services: list of service names (e.g. ['mon', 'rgw'])
        """
        payload = [{"service": svc} for svc in services]
        self._post("/1.0/services/restart", json=payload)

    def delete_nfs_service(self, cluster_id: str) -> None:
        """Delete/disable an NFS service by cluster ID.

        NFS deletion requires the cluster_id in the request body, unlike
        other services which are identified by URL path alone.

        :param cluster_id: NFS cluster identifier
        """
        self._delete("/1.0/services/nfs", json={"cluster_id": cluster_id})

# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import json
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
        members = cluster.get("metadata", {})
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

    def apply_smb(self, target: str, cluster_id: str, spec: dict) -> None:
        """Submit a nonblocking, policy-checked member placement request."""
        payload = {"cluster_id": cluster_id, "orchestrator_spec": spec}
        self._put("/1.0/services/smb", params={"target": target}, timeout=30,
                  json={"name": "smb", "bool": False, "payload": json.dumps(payload)},
                  redact_request=True, redact_response=True)

    def remove_smb(self, target: str, cluster_id: str) -> None:
        """Request exact-group retirement without purging private identity."""
        self._delete("/1.0/services/smb", params={"target": target}, timeout=30,
                     json={"cluster_id": cluster_id, "purge": False, "orchestrated": True},
                     redact_response=True)

    def list_services(self) -> list[dict]:
        """List all services."""
        services = self._get("/1.0/services", timeout=30)
        return services.get("metadata")

    def list_resources(self) -> list[dict]:
        """List all resources."""
        nodes = self._get("/1.0/resources")
        return nodes.get("metadata")

    def list_disks(self) -> list[dict]:
        """List all disks"""
        disks = self._get("/1.0/disks")
        return disks.get("metadata")

    def get_status(self) -> dict[str, dict]:
        """Get status of the cluster."""
        cluster = self._get("/1.0/status")
        members = cluster.get("metadata", {})
        return {
            member["name"]: {
                "status": member["status"],
                "address": member["address"],
            }
            for member in members
        }

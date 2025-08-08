# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import json
import logging

from . import service

LOG = logging.getLogger(__name__)


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

    def list_services(self) -> list[dict]:
        """List all services."""
        services = self._get("/1.0/services")
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

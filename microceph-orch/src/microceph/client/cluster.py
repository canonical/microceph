# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import json
import logging

from . import service

LOG = logging.getLogger(__name__)


class MicroClusterService(service.BaseService):
    """Client for default MicroCluster Service API."""

    def bootstrap_cluster(self, name: str, address: str) -> None:
        """Bootstrap the micro cluster.

        Bootstraps the cluster adding local node specified by
        name as bootstrap node. The address should be in
        format <IP>:<PORT> where the microcluster service
        is running.

        Raises NodeAlreadyExistsException if bootstrap is
        invoked on already existing node in cluster.
        """
        data = {"bootstrap": True, "address": address, "name": name}
        self._post("/core/control", data=json.dumps(data))

    def join(self, name: str, address: str, token: str) -> None:
        """Join node to the micro cluster.

        Verified the token with the list of saved tokens and
        joins the node with the given name and address.

        Raises NodeAlreadyExistsException if the node is already
        part of the cluster.
        Raises NodeJoinException if the token doesnot match or not
        part of the generated tokens list.
        """
        data = {"join_token": token, "address": address, "name": name}
        self._post("/core/control", data=json.dumps(data))

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

    def generate_token(self, name: str) -> str:
        """Generate token for the node.

        Generate a new token for the node with name.

        Raises TokenAlreadyGeneratedException if token is already
        generated.
        """
        data = {"name": name}
        result = self._post("/core/control/tokens", data=json.dumps(data))
        return result.get("metadata")

    def list_tokens(self) -> list:
        """List all generated tokens."""
        tokens = self._get("/core/control/tokens")
        return tokens.get("metadata")

    def delete_token(self, name: str) -> None:
        """Delete token for the node.

        Raises TokenNotFoundException if token does not exist.
        """
        self._delete(f"/core/1.0/tokens/{name}")

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

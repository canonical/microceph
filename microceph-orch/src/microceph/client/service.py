# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import logging
from abc import ABC

from requests.exceptions import ConnectionError, HTTPError
from requests.sessions import Session

LOG = logging.getLogger(__name__)


class RemoteException(Exception):
    """An Exception raised when interacting with the remote microclusterd service."""

    pass


class ClusterAlreadyBootstrappedException(RemoteException):
    """Raised when cluster service is already bootstrapped."""

    pass


class ClusterServiceUnavailableException(RemoteException):
    """Raised when cluster service is not yet bootstrapped."""

    pass


class ConfigItemNotFoundException(RemoteException):
    """Raise when ConfigItem cannot be found on the remote."""

    pass


class NodeAlreadyExistsException(RemoteException):
    """Raised when the node already exists."""

    pass


class NodeNotExistInClusterException(RemoteException):
    """Raised when the node does not exist in cluster."""

    pass


class NodeJoinException(RemoteException):
    """Raised when the node not able to join cluster."""

    pass


class LastNodeRemovalFromClusterException(RemoteException):
    """Raised when token is already generated for the node."""

    pass


class TokenAlreadyGeneratedException(RemoteException):
    """Raised when token is already generated for the node."""

    pass


class TokenNotFoundException(RemoteException):
    """Raised when token is not found for the node."""

    pass


class URLNotFoundException(RemoteException):
    """Raise when URL is not found.

    Happens when the URL is not found in the remote or
    microcluster is not bootstrapped.
    """

    pass


class BaseService(ABC):
    """BaseService is the base service class for sunbeam clusterd services."""

    def __init__(
        self,
        session: Session,
        endpoint: str,
        certs=None,
        timeout: int | float | None = None,
    ):
        """Creates a new BaseService for the sunbeam clusterd API.

        The service class is used to provide convenient APIs for clients to
        use when interacting with the sunbeam clusterd api.


        :param session: session to use when interacting with the sunbeam clusterd API
        :type: Session
        """
        self.__session = session
        self._endpoint = endpoint
        self._certs = certs
        self._timeout = timeout

    @property
    def timeout(self):
        """Get the timeout for the service."""
        return self._timeout

    @timeout.setter
    def timeout(self, timeout: int | float | None):
        """Set the timeout for the service."""
        self._timeout = timeout

    def _request(self, method, path, **kwargs):  # noqa: C901 too complex
        if path.startswith("/"):
            path = path[1:]
        netloc = self._endpoint
        url = f"{netloc}/{path}"
        redact_response = kwargs.pop("redact_response", False)
        try:
            LOG.debug("[%s] %s, args=%s", method, url, kwargs)
            response = self.__session.request(
                method=method,
                url=url,
                cert=self._certs,
                timeout=self._timeout,
                **kwargs,
            )
            output = response.text
            if redact_response:
                output = "/* REDACTED */"
            LOG.debug("Response(%s) = %s", response, output)
        except ConnectionError as e:
            msg = str(e)
            if "FileNotFoundError" in msg:
                raise ClusterServiceUnavailableException(
                    "Sunbeam Cluster socket not found, is clusterd running ?"
                    " Check with 'snap services openstack.clusterd'",
                ) from e
            raise ClusterServiceUnavailableException(msg)

        try:
            response.raise_for_status()
        except HTTPError as e:
            # Do some nice translating to sunbeamdexceptions
            error = response.json().get("error")
            if "remote with name" in error:
                raise NodeAlreadyExistsException(
                    "Already node exists in the sunbeam cluster"
                )
            elif "not found" == error:
                raise URLNotFoundException("URL not found")
            elif "No remote exists with the given name" in error:
                raise NodeNotExistInClusterException(
                    "Node does not exist in the sunbeam cluster"
                )
            elif "Node not found" in error:
                raise NodeNotExistInClusterException(
                    "Node does not exist in the sunbeam cluster"
                )
            elif "Failed to join cluster with the given join token" in error:
                raise NodeJoinException(
                    "Join node to cluster failed with the given token"
                )
            elif "UNIQUE constraint failed: internal_token_records.name" in error:
                raise TokenAlreadyGeneratedException(
                    "Token already generated for the node"
                )
            elif "Database is not yet initialized" in error:
                raise ClusterServiceUnavailableException(
                    "Sunbeam Cluster not initialized"
                )
            elif "InternalTokenRecord not found" in error:
                raise TokenNotFoundException("Token not found for the node")
            elif (
                "Cannot remove cluster members, there are no remaining "
                "non-pending members"
            ) in error:
                raise LastNodeRemovalFromClusterException(
                    "Cannot remove cluster member as there are no remaining "
                    "non-pending members. Reset the last node instead."
                )
            elif "already running" in error:
                raise ClusterAlreadyBootstrappedException(
                    "Already cluster is bootstrapped."
                )
            elif "ConfigItem not found" in error:
                raise ConfigItemNotFoundException("ConfigItem not found")
            raise e

        return response.json()

    def _get(self, path, **kwargs):
        kwargs.setdefault("allow_redirects", True)
        return self._request("get", path, **kwargs)

    def _head(self, path, **kwargs):
        kwargs.setdefault("allow_redirects", False)
        return self._request("head", path, **kwargs)

    def _post(self, path, data=None, json=None, **kwargs):
        return self._request("post", path, data=data, json=json, **kwargs)

    def _patch(self, path, data=None, **kwargs):
        return self._request("patch", path, data=data, **kwargs)

    def _put(self, path, data=None, **kwargs):
        return self._request("put", path, data=data, **kwargs)

    def _delete(self, path, **kwargs):
        return self._request("delete", path, **kwargs)

    def _options(self, path, **kwargs):
        kwargs.setdefault("allow_redirects", True)
        return self._request("options", path, **kwargs)
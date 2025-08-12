# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import logging
from urllib.parse import quote

import requests
import requests_unixsocket  # type: ignore [import-untyped]
from snaphelpers import Snap

from .cluster import StatusService, ExtendedAPIService, MicroClusterService

logger = logging.getLogger(__name__)

class Client:
    """A client for interacting with the remote client API."""

    def __init__(
        self,
        endpoint: str,
    ):
        super(Client, self).__init__()
        self._endpoint = endpoint
        self._certs = None
        self._session = requests.sessions.Session()
        if not self._endpoint.startswith(requests_unixsocket.DEFAULT_SCHEME):
            raise ValueError(
                "Expected unix socket, got: {}".format(self._endpoint)
            )

        self._session.mount(
            requests_unixsocket.DEFAULT_SCHEME, requests_unixsocket.UnixAdapter()
        )

        logger.debug("Created microclient for endpoint: %s", self._endpoint)

        self.cluster = MicroClusterService(self._session, self._endpoint, self._certs)
        self.status = StatusService(self._session, self._endpoint, self._certs)
        self.services = ExtendedAPIService(self._session, self._endpoint, self._certs)

    @classmethod
    def from_socket(cls) -> "Client":
        """Return a client initialized to the clusterd socket."""
        escaped_socket_path = quote(
            str(Snap().paths.common / "state" / "control.socket"), safe=""
        )
        return cls("http+unix://" + escaped_socket_path)


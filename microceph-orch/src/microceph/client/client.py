# SPDX-FileCopyrightText: 2023 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0

import atexit
import os
import ssl
import tempfile
from urllib.parse import quote

import requests
import requests.adapters
import requests_unixsocket  # type: ignore [import-untyped]
from snaphelpers import Snap
from urllib3 import poolmanager

from .cluster import StatusService, ExtendedAPIService


class MTLSAdapter(requests.adapters.HTTPAdapter):
    def __init__(self, *args, **kwargs):
        self.certificate_authority = kwargs.pop("certificate_authority", None)
        if self.certificate_authority is None:
            raise ValueError("certificate_authority is required")
        super().__init__(*args, **kwargs)

    def init_poolmanager(self, connections, maxsize, block=False, **kwargs):
        """Create and initialize the urllib3 PoolManager."""
        ctx = ssl.create_default_context(cadata=self.certificate_authority)
        ctx.verify_mode = ssl.CERT_REQUIRED
        # Microcluster cluster certificate does not respect hostname
        # The certificate is shared by every member of the cluster
        ctx.check_hostname = False
        assert_hostname = False
        self.poolmanager = poolmanager.PoolManager(
            num_pools=connections,
            maxsize=maxsize,
            block=block,
            ssl_version=ssl.PROTOCOL_TLS_CLIENT,
            ssl_context=ctx,
            assert_hostname=assert_hostname,
        )


def to_file_path_certs(certificate: str, private_key: str) -> tuple[str, str]:
    """Template certpair in tmp files, return the path."""
    cert_fd, cert_path = tempfile.mkstemp(suffix=".crt")
    pk_fd, pk_path = tempfile.mkstemp(suffix=".key")
    with os.fdopen(cert_fd, "w") as cert_file:
        cert_file.write(certificate)
    with os.fdopen(pk_fd, "w") as pk_file:
        pk_file.write(private_key)
    atexit.register(lambda: os.remove(cert_path))
    atexit.register(lambda: os.remove(pk_path))
    return cert_path, pk_path


class Client:
    """A client for interacting with the remote client API."""

    def __init__(
        self,
        endpoint: str,
        certificate_authority: str | None = None,
        certificate: str | None = None,
        private_key: str | None = None,
    ):
        super(Client, self).__init__()
        self._endpoint = endpoint
        self._certs = None
        self._session = requests.sessions.Session()
        if self._endpoint.startswith(requests_unixsocket.DEFAULT_SCHEME):
            self._session.mount(
                requests_unixsocket.DEFAULT_SCHEME, requests_unixsocket.UnixAdapter()
            )
        else:
            if (
                certificate_authority is None
                or certificate is None
                or private_key is None
            ):
                raise ValueError(
                    "Certificate, private key and certificate authority"
                    " are required http mode."
                )
            self._certs = to_file_path_certs(certificate, private_key)
            self._session.mount(
                "https://",
                MTLSAdapter(certificate_authority=certificate_authority),
            )

        self.status = StatusService(self._session, self._endpoint, self._certs)
        self.services = ExtendedAPIService(self._session, self._endpoint, self._certs)

    @classmethod
    def from_socket(cls) -> "Client":
        """Return a client initialized to the clusterd socket."""
        escaped_socket_path = quote(
            str(Snap().paths.common / "state" / "control.socket"), safe=""
        )
        return cls("http+unix://" + escaped_socket_path)

    @classmethod
    def from_http(
        cls,
        endpoint: str,
        certificate_authority: str | None = None,
        certificate: str | None = None,
        private_key: str | None = None,
    ) -> "Client":
        """Return a client initiliazed to the clusterd http endpoint.

        If both certificate and private_key are provided, the client will
        use them to authenticate to the server.
        """
        return cls(endpoint, certificate_authority, certificate, private_key)
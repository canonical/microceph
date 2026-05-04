"""
Minimal stubs for Ceph types used by the orchestrator module.

The real types live in ceph.deployment.*, mgr_module, and orchestrator;
which are only importable inside the Ceph snap environment. These stubs
replicate just enough behaviour to test our code outside the snap.
"""

from typing import Optional, List, Dict, Any, Generic, TypeVar
from functools import wraps

T = TypeVar("T")


class OrchResult(Generic[T]):
    """Minimal OrchResult stub."""

    def __init__(self, result: Optional[T] = None, exception: Optional[Exception] = None):
        self.result = result
        self.exception = exception
        self.exception_str = str(exception) if exception else ""


def handle_orch_error(f):
    """Stub decorator that wraps return value in OrchResult and catches exceptions."""
    @wraps(f)
    def wrapper(*args, **kwargs):
        try:
            return OrchResult(f(*args, **kwargs))
        except Exception as e:
            return OrchResult(None, exception=e)
    return wrapper


class CLICommandMeta(type):
    """No-op metaclass stub."""
    pass


class HostSpec:
    def __init__(self, hostname, addr=None, labels=None, status=None, **kwargs):
        self.hostname = hostname
        self.addr = addr or hostname
        self.labels = labels or []
        self.status = status or ""


class PlacementSpec:
    def __init__(self, hosts=None, count=None, **kwargs):
        self.hosts = hosts or []
        self.count = count


class ServiceSpec:
    def __init__(self, service_type="", service_id=None, placement=None, **kwargs):
        self.service_type = service_type
        self.service_id = service_id
        self.placement = placement


class RGWSpec(ServiceSpec):
    def __init__(self, rgw_frontend_port=None, rgw_frontend_ssl_certificate=None,
                 ssl=False, **kwargs):
        super().__init__(**kwargs)
        self.rgw_frontend_port = rgw_frontend_port
        self.rgw_frontend_ssl_certificate = rgw_frontend_ssl_certificate
        self.ssl = ssl


class MONSpec(ServiceSpec):
    pass


class MDSSpec(ServiceSpec):
    pass


class NFSServiceSpec(ServiceSpec):
    def __init__(self, port=None, virtual_ip=None, **kwargs):
        super().__init__(**kwargs)
        self.port = port
        self.virtual_ip = virtual_ip


class Device:
    def __init__(self, path="", available=None, **kwargs):
        self.path = path
        self.available = available


class Devices:
    def __init__(self, devices=None):
        self.devices = devices or []


class InventoryFilter:
    def __init__(self, labels=None, hosts=None):
        self.labels = labels
        self.hosts = hosts


class InventoryHost:
    def __init__(self, name="", devices=None):
        self.name = name
        self.devices = devices


class ServiceDescription:
    def __init__(self, spec=None, running=0, **kwargs):
        self.spec = spec
        self.running = running


class DaemonDescription:
    def __init__(self, service_name="", daemon_type="", daemon_id="",
                 hostname="", ip=None, ports=None, **kwargs):
        self.service_name = service_name
        self.daemon_type = daemon_type
        self.daemon_id = daemon_id
        self.hostname = hostname
        self.ip = ip
        self.ports = ports


class Orchestrator:
    pass


class MgrModule:
    def __init__(self, *args, **kwargs):
        pass


class NotifyType:
    pass

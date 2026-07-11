# SPDX-FileCopyrightText: 2026 - Canonical Ltd
# SPDX-License-Identifier: Apache-2.0
#
# The mgr-runtime packages (mgr_module, orchestrator, ceph.deployment)
# only exist inside a ceph-mgr daemon; stub them so importing the
# microceph package (whose __init__ pulls in module.py) works under
# pytest. Also stub snaphelpers, which requires snap environment vars.

import sys
import types


def _module(name, **attrs):
    mod = types.ModuleType(name)
    for key, value in attrs.items():
        setattr(mod, key, value)
    sys.modules.setdefault(name, mod)
    return sys.modules[name]


def _cls(name):
    """A distinct permissive stub class per name (multiple inheritance
    forbids reusing one class as several bases)."""

    def __init__(self, *args, **kwargs):
        for key, value in kwargs.items():
            setattr(self, key, value)

    return type(
        name,
        (object,),
        {
            "__init__": __init__,
            # Tolerate generic annotations like OrchResult[str].
            "__class_getitem__": classmethod(lambda cls, item: cls),
        },
    )


def _identity_decorator(fn):
    return fn


_module(
    "ceph",
)
_module(
    "ceph.deployment",
)
_module(
    "ceph.deployment.inventory",
    Device=_cls("Device"),
    Devices=_cls("Devices"),
)
_module(
    "ceph.deployment.service_spec",
    ServiceSpec=_cls("ServiceSpec"),
    PlacementSpec=_cls("PlacementSpec"),
    RGWSpec=_cls("RGWSpec"),
    MONSpec=_cls("MONSpec"),
    MDSSpec=_cls("MDSSpec"),
    NFSServiceSpec=_cls("NFSServiceSpec"),
    SMBSpec=_cls("SMBSpec"),
)
_module(
    "mgr_module",
    MgrModule=_cls("MgrModule"),
    NotifyType=_cls("NotifyType"),
)
_module(
    "orchestrator",
    Orchestrator=_cls("Orchestrator"),
    HostSpec=_cls("HostSpec"),
    InventoryFilter=_cls("InventoryFilter"),
    InventoryHost=_cls("InventoryHost"),
    ServiceDescription=_cls("ServiceDescription"),
    DaemonDescription=_cls("DaemonDescription"),
    CLICommandMeta=type,
    handle_orch_error=_identity_decorator,
    OrchResult=_cls("OrchResult"),
)
_module(
    "snaphelpers",
    Snap=_cls("Snap"),
)

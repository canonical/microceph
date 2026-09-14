"""Robot observations and temporary controls for individual Pebble OSD services.

These controls deliberately do not use the adapter's fenced removal operation.
Test bodies and cross-OSD assertions belong in the Robot suite; parsing and
polling live here. Nothing is installed in the snap or added to its public API.
"""

import json
import re
import shlex

from microceph_harness import microceph_harness


def _osd_name(osd_id):
    osd_id = str(osd_id)
    if not re.fullmatch(r"0|[1-9][0-9]*", osd_id) or int(osd_id) > 2**63 - 1:
        raise ValueError(f"Invalid OSD ID: {osd_id!r}")
    return f"osd-{osd_id}"


def _json_object(output):
    value = json.loads(output)
    if not isinstance(value, dict):
        raise ValueError("Expected a JSON object")
    return value


def _service_states(output):
    services = _json_object(output).get("services")
    if not isinstance(services, dict):
        raise ValueError("Missing Pebble services map")
    states = {}
    for name, service in services.items():
        if (not isinstance(service, dict) or service.get("name") != name
                or not isinstance(service.get("current"), str) or not service["current"]):
            raise ValueError(f"Invalid Pebble service status: {name!r}: {service!r}")
        if _osd_name(name[4:]) != name:
            raise ValueError(f"Unexpected service in OSD supervisor: {name!r}")
        states[name] = service["current"]
    return states


def _ceph_osd_states(output):
    osds = _json_object(output).get("osds")
    if not isinstance(osds, list):
        raise ValueError("Missing Ceph OSD map")
    states = {}
    for osd in osds:
        if (not isinstance(osd, dict) or type(osd.get("osd")) is not int
                or any(type(osd.get(key)) is not int or osd[key] not in (0, 1)
                       for key in ("up", "in"))):
            raise ValueError(f"Invalid Ceph OSD state: {osd!r}")
        _osd_name(osd["osd"])
        osd_id = str(osd["osd"])
        if osd_id in states:
            raise ValueError(f"Duplicate Ceph OSD: {osd_id}")
        states[osd_id] = {"up": osd["up"], "in": osd["in"]}
    return states


def _osd_identity(output):
    receipt = _json_object(output)
    if (type(receipt.get("pid")) is not int or receipt["pid"] <= 1
            or not isinstance(receipt.get("start-time"), str)
            or not receipt["start-time"].isdigit()
            or not isinstance(receipt.get("boot-id"), str) or not receipt["boot-id"].strip()):
        raise ValueError(f"Invalid OSD process receipt: {receipt!r}")
    return receipt


def _live_process_groups(output):
    """Parse headerless ps pid/pgid/stat/comm rows, retaining live descendants."""
    if not output.strip():
        raise ValueError("Empty process table cannot prove process exit")
    groups = {}
    for line in output.splitlines():
        if not line.strip():
            continue
        pid, pgid, state, command = line.split(None, 3)
        pid, pgid = int(pid), int(pgid)
        if pid < 1 or pgid < 0:
            raise ValueError(f"Invalid process table row: {line!r}")
        if state[0] not in ("Z", "X"):
            groups.setdefault(pgid, {})[pid] = command
    return groups


def _supervisor_identity(output):
    properties = dict(line.split("=", 1) for line in output.splitlines() if line)
    required = {"MainPID", "ExecMainStartTimestampMonotonic", "ActiveState"}
    if not required.issubset(properties):
        raise ValueError(f"Missing OSD supervisor properties: {output!r}")
    return {
        "pid": int(properties["MainPID"]),
        "started": int(properties["ExecMainStartTimestampMonotonic"]),
        "state": properties["ActiveState"],
    }


class pebble_services:
    """Suite-scoped Pebble controls composed with the existing VM exec harness."""

    ROBOT_LIBRARY_SCOPE = "SUITE"

    def __init__(self):
        # The harness reads Robot variables lazily; discovery needs no run context.
        self._harness = microceph_harness()

    def _pebble(self, *args, timeout=30, quiet=True):
        script = (
            'exec env PEBBLE_SOCKET="$SNAP_COMMON/run/pebble/osd/.pebble.socket" '
            '"$SNAP/bin/pebble" ' + shlex.join(args)
        )
        return self._harness.run_in_vm_and_check(
            "sudo snap run --shell microceph.daemon -c " + shlex.quote(script),
            timeout, quiet=quiet,
        )

    def control_pebble_osd(self, action, osd_id):
        """Stop/start/restart one child, without fencing it or restarting Snap."""
        if action not in ("stop", "start", "restart"):
            raise ValueError(f"Unsupported Pebble OSD action: {action!r}")
        return self._pebble(action, _osd_name(osd_id), timeout=330, quiet=False)

    def get_pebble_osd_snapshot(self):
        """Fetch raw observations; receipts alone are not evidence of live OSDs."""
        run = self._harness.run_in_vm_and_check
        services = _service_states(self._pebble("services", "--format=json").stdout)
        osds = _ceph_osd_states(run("sudo microceph.ceph osd dump -f json", 30, quiet=True).stdout)
        identities = {}
        for osd_id in osds:
            path = f"/var/snap/microceph/common/run/pebble/osd/{_osd_name(osd_id)}.json"
            identities[osd_id] = _osd_identity(run(f"sudo cat {path}", 30, quiet=True).stdout)
        groups = _live_process_groups(run("ps -e -o pid=,pgid=,stat=,comm=", 30, quiet=True).stdout)
        supervisor = _supervisor_identity(run(
            "sudo systemctl show snap.microceph.osd.service "
            "--property=MainPID,ExecMainStartTimestampMonotonic,ActiveState",
            30, quiet=True,
        ).stdout)
        return {
            "services": services, "osds": osds, "identities": identities,
            "groups": groups, "supervisor": supervisor,
        }

    @staticmethod
    def pebble_osd_is_in_state(snapshot, osd_id, state):
        """Combine child state, Ceph up/in membership, and live process evidence.

        Inactive requires the entire recorded group to have exited, not just its
        leader. Active requires the recorded leader to be an actual ceph-osd,
        rather than a preparation wrapper or a stale receipt behind a live app.
        """
        name = _osd_name(osd_id)
        if state not in ("active", "inactive"):
            raise ValueError(f"Unsupported OSD state: {state!r}")
        receipt = snapshot["identities"].get(str(osd_id))
        if (not receipt or snapshot["services"].get(name) != state
                or snapshot["osds"].get(str(osd_id)) != {"up": int(state == "active"), "in": 1}):
            return False
        pid = receipt["pid"]
        if state == "inactive":
            return pid not in snapshot["groups"]
        return snapshot["groups"].get(pid, {}).get(pid) == "ceph-osd"

    def wait_for_pebble_osd_state(self, osd_id, state, tries=24, interval=5):
        """Poll read-only observations, returning the converged snapshot."""
        name = _osd_name(osd_id)
        last = {}

        def predicate():
            last.update(self.get_pebble_osd_snapshot())
            return self.pebble_osd_is_in_state(last, osd_id, state)

        self._harness._poll_until(
            predicate, attempts=tries, interval=interval,
            fail_msg=lambda: f"{name} never became {state}; last observation: {last}",
        )
        return last

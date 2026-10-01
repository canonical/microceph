"""Robot Framework library: parsing of "snap services" output.

Keeps the column parsing out of the .robot suite (where repeated awk/wc
pipelines read poorly) in a single unit-testable function.
"""


def service_has_state(snap_services_output, service, startup, current):
    """Return whether *service* has the requested snap startup/current state.

    ``snap services`` reports a header followed by whitespace-delimited columns:
    Service, Startup, Current, and Notes. This intentionally accepts additional
    trailing columns so it remains compatible with snapd additions.
    """
    for line in snap_services_output.splitlines()[1:]:
        cols = line.split()
        if len(cols) < 3:
            continue
        if cols[0] == service and cols[1] == startup and cols[2] == current:
            return True
    return False


def enabled_active_services(snap_services_output):
    """Return the names of services that are both enabled and active.

    *snap_services_output* is the stdout of ``snap services <snap>``, whose
    columns are: Service, Startup, Current, Notes. A service is returned when
    its Startup is ``enabled`` and its Current is ``active``. The header row
    and any short/blank lines are ignored.
    """
    names = []
    for line in snap_services_output.splitlines()[1:]:
        cols = line.split()
        if len(cols) >= 3 and cols[1] == "enabled" and cols[2] == "active":
            names.append(cols[0])
    return names

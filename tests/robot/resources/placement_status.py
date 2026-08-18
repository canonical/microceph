"""Pure parsers for the placement / lifecycle / capabilities API bodies.

These are the *decision* halves of the placement keywords in
microceph_harness.py: the keywords fetch raw JSON from the MicroCeph control
socket (minimum remote I/O) and these helpers parse it locally (the "fetch
raw, decide in Python" rule), replacing the raw-JSON substring assertions and
grep pipelines the original suites used. All functions are module-level and
pure -- no self, no BuiltIn -- so they are unit-testable from
test_harness_helpers.py without a running Robot context.

This module is imported by microceph_harness.py; it is NOT loaded as a Robot
library, so its function names never collide with harness keyword names.
"""

import json


def _parse(raw):
    """Return the decoded JSON value, or None when *raw* is not valid JSON."""
    try:
        return json.loads(raw)
    except (ValueError, TypeError):
        return None


def response_code(raw):
    """Return the code embedded in a microcluster/LXD API response body.

    Sync responses carry ``status_code`` (200); error responses carry
    ``error_code`` (e.g. 400). Returns 0 when the body is not parseable JSON
    or carries neither field, so callers comparing against 200 fail closed.
    """
    data = _parse(raw)
    if not isinstance(data, dict):
        return 0
    code = data.get("status_code", data.get("error_code", 0))
    try:
        return int(code)
    except (ValueError, TypeError):
        return 0


def response_metadata(raw):
    """Return the ``metadata`` object of a sync response, or {} when absent."""
    data = _parse(raw)
    if not isinstance(data, dict) or not isinstance(data.get("metadata"), dict):
        return {}
    return data["metadata"]


def bootstrap_state(raw):
    """Return ``bootstrap_state`` from a GET /1.0/placement body ('' when absent)."""
    return str(response_metadata(raw).get("bootstrap_state", ""))


def placement_active(raw):
    """Return the ``active`` flag from a GET /1.0/placement body."""
    return bool(response_metadata(raw).get("active", False))


def supported_capabilities(raw):
    """Return the capability marker list from a GET /1.0/cluster/capabilities body.

    Returns [] when the body is malformed or ``supported`` is not a list.
    """
    supported = response_metadata(raw).get("supported", [])
    if not isinstance(supported, list):
        return []
    return [str(s) for s in supported]


def observed_member(raw, member):
    """Return the observed placement dict for *member* from a GET /1.0/placement
    body, or {} when the member is absent or the body is malformed.
    """
    observed = response_metadata(raw).get("observed")
    if not isinstance(observed, list):
        return {}
    for entry in observed:
        if isinstance(entry, dict) and entry.get("member") == member:
            return entry
    return {}


def member_rgw_frontend(raw, member):
    """Return *member*'s observed ``rgw_frontend`` object from a GET
    /1.0/placement body, or {} when absent.

    The frontend reports ``port``/``ssl_port``/``ssl`` only; a suite asserts
    these match the requested port/TLS, proving observed state is sourced from
    the rgw_frontends table rather than fanned out.
    """
    frontend = observed_member(raw, member).get("rgw_frontend")
    if not isinstance(frontend, dict):
        return {}
    return frontend


def placement_leaks_rgw_secrets(raw):
    """Return True when a GET /1.0/placement body contains any RGW SSL key
    material, i.e. a non-empty ``ssl_certificate`` or ``ssl_private_key`` under
    the stored policy's members.

    The snap MUST strip these before storage and redact them on GET; a suite
    asserts this returns False. Fails safe: a body it cannot parse as the
    expected shape reports no leak (there is nothing to leak), but a present
    non-empty secret string is always detected.
    """
    policy = response_metadata(raw).get("policy")
    if not isinstance(policy, dict):
        return False
    members = policy.get("members")
    if not isinstance(members, dict):
        return False
    for entry in members.values():
        if not isinstance(entry, dict):
            continue
        rgw = entry.get("rgw")
        if not isinstance(rgw, dict):
            continue
        if rgw.get("ssl_certificate") or rgw.get("ssl_private_key"):
            return True
    return False


def mon_count(raw):
    """Return the monmap daemon count from ``ceph -s -f json`` output.

    Prefers ``monmap.num_mons`` (the count behind the "mon: N daemons" status
    line the original grep pipeline scraped); falls back to the length of
    ``quorum_names`` on schemas without it. Returns 0 on parse failure so poll
    loops treat unreachable clusters as zero mons.
    """
    data = _parse(raw)
    if not isinstance(data, dict):
        return 0
    monmap = data.get("monmap")
    if isinstance(monmap, dict) and isinstance(monmap.get("num_mons"), int):
        return monmap["num_mons"]
    quorum = data.get("quorum_names")
    if isinstance(quorum, list):
        return len(quorum)
    return 0


def mon_quorum_names(raw):
    """Return MON names in quorum from ``ceph quorum_status -f json``.

    Current Ceph output includes ``quorum_names`` directly. On schemas where
    that field is absent, ``quorum`` contains numeric monitor ranks; resolve
    those ranks through ``monmap.mons``. Malformed or incomplete responses
    return an empty list so assertions fail closed.
    """
    data = _parse(raw)
    if not isinstance(data, dict):
        return []

    if "quorum_names" in data:
        quorum_names = data["quorum_names"]
        if not isinstance(quorum_names, list):
            return []
        if not all(isinstance(name, str) and name for name in quorum_names):
            return []
        return quorum_names

    quorum = data.get("quorum")
    monmap = data.get("monmap")
    if not isinstance(quorum, list) or not isinstance(monmap, dict):
        return []
    mons = monmap.get("mons")
    if not isinstance(mons, list):
        return []

    names_by_rank = {}
    for mon in mons:
        if not isinstance(mon, dict):
            return []
        rank = mon.get("rank")
        name = mon.get("name")
        if type(rank) is not int or not isinstance(name, str) or not name:
            return []
        names_by_rank[rank] = name

    names = []
    for rank in quorum:
        if type(rank) is not int or rank not in names_by_rank:
            return []
        names.append(names_by_rank[rank])
    return names


def control_service_presence(mon_raw, mgr_raw, mds_raw, member):
    """Return explicit MON/MGR/MDS membership for *member* from Ceph JSON.

    This is stricter than a substring search over ``ceph -s``: a hostname
    appearing anywhere in status does not prove that all three role-managed
    control services are present.

    Raises :class:`ValueError` when any of the three raw bodies is not
    parseable into its expected shape (bad or empty output). Treating an
    unparseable body as "service absent" would let an absence assertion pass on
    garbage rather than on a genuine removal, so callers must decide explicitly
    whether to fail or retry -- see :meth:`wait_for_member_control_services`,
    which retries, and :meth:`assert_member_has_control_services`, which fails.
    """
    mon = _parse(mon_raw)
    if not isinstance(mon, dict):
        raise ValueError(f"unparseable mon quorum_status output: {mon_raw!r}")
    mgr = _parse(mgr_raw)
    if not isinstance(mgr, list):
        raise ValueError(f"unparseable mgr metadata output: {mgr_raw!r}")
    mds = _parse(mds_raw)
    if not isinstance(mds, dict) or not isinstance(mds.get("fsmap"), dict):
        raise ValueError(f"unparseable mds stat output: {mds_raw!r}")

    mon_names = mon_quorum_names(mon_raw)

    mgr_names = [entry.get("name") for entry in mgr if isinstance(entry, dict)]

    mds_names = []
    fsmap = mds["fsmap"]
    standbys = fsmap.get("standbys", [])
    if isinstance(standbys, list):
        mds_names.extend(
            entry.get("name") for entry in standbys if isinstance(entry, dict)
        )
    filesystems = fsmap.get("filesystems", [])
    if isinstance(filesystems, list):
        for filesystem in filesystems:
            if not isinstance(filesystem, dict):
                continue
            mdsmap = filesystem.get("mdsmap", {})
            info = mdsmap.get("info", {}) if isinstance(mdsmap, dict) else {}
            if isinstance(info, dict):
                mds_names.extend(
                    entry.get("name") for entry in info.values() if isinstance(entry, dict)
                )

    return {
        "mon": member in mon_names,
        "mgr": member in mgr_names,
        "mds": member in mds_names,
    }


def member_in_ceph_status(status_text, member):
    """Return True when *member* appears in ``ceph -s`` output.

    Retained for callers that explicitly need the historical broad status-text
    check. Control-placement assertions use :func:`control_service_presence`.
    """
    return member in (status_text or "")

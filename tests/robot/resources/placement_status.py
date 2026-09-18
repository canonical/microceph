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
import re
import shlex


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
    # Error bodies carry both keys, with status_code set to 0, so the first
    # non-zero code wins rather than the first present key.
    code = data.get("status_code") or data.get("error_code") or 0
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


def placement_metadata(raw):
    """Return the ``metadata`` object of a sync API response, strictly.

    Unlike :func:`response_metadata` (lenient, for poll loops), this raises
    ValueError when the body is not JSON, not an object, not a successful sync
    response (``status_code`` 200), or lacks a metadata object. Secret and
    accepted-state checks use this gate so a malformed body fails the caller
    instead of reading as "clean" or "empty".
    """
    data = _parse(raw)
    if not isinstance(data, dict):
        raise ValueError("unparseable API response body")
    if data.get("status_code") != 200 or not isinstance(data.get("metadata"), dict):
        raise ValueError("not a successful sync response")
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


def member_rgw_frontend(raw, member):
    """Return known frontend settings; malformed responses are not absence."""
    metadata = placement_metadata(raw)
    if "observed" not in metadata:
        raise ValueError("placement response lacks observed state")
    observed = metadata["observed"]
    if observed is None:
        return {}
    if not isinstance(observed, list):
        raise ValueError("observed placement is not a list")
    for entry in observed:
        if not isinstance(entry, dict) or not isinstance(entry.get("member"), str):
            raise ValueError("observed member is malformed")
        if entry["member"] != member:
            continue
        frontend = entry.get("rgw_frontend")
        if frontend is None:
            return {}
        if not isinstance(frontend, dict) or type(frontend.get("ssl")) is not bool:
            raise ValueError("observed frontend is malformed")
        for key in ("port", "ssl_port"):
            if key in frontend and (type(frontend[key]) is not int or not 0 <= frontend[key] <= 65535):
                raise ValueError("observed listener port is malformed")
        return frontend
    return {}


def placement_leaks_rgw_secrets(raw):
    """Return True when the stored policy carries RGW SSL key material.

    The stored policy must carry only non-secret TLS intent: a non-empty
    ``ssl_certificate`` or ``ssl_private_key`` under any member's rgw entry
    is a leak. Raises ValueError when the body is not a successful sync
    response or the stored policy has an unexpected shape -- a malformed
    status body must fail the check, never read as "nothing to leak".
    """
    policy = placement_metadata(raw).get("policy")
    if policy is None:
        return False
    if not isinstance(policy, dict) or not isinstance(policy.get("members"), dict):
        raise ValueError("stored policy is not the expected shape")
    for entry in policy["members"].values():
        if not isinstance(entry, dict):
            raise ValueError("stored member intent is not an object")
        rgw = entry.get("rgw")
        if rgw is None:
            continue
        if not isinstance(rgw, dict):
            raise ValueError("stored RGW intent is not an object")
        if rgw.get("ssl_certificate") or rgw.get("ssl_private_key"):
            return True
    return False


def placement_refusal(raw):
    """Return the recorded ``placement_refusal`` ('' when none) from a GET body.

    Raises ValueError on malformed bodies: refusal assertions must never
    inspect a defaulted empty string.
    """
    return str(placement_metadata(raw).get("placement_refusal", ""))


def stored_policy_rgw(raw, member):
    """Return the stored policy's rgw intent for *member*.

    Returns {} when no policy is stored or the member/rgw entry is absent.
    Raises ValueError on malformed bodies or a misshaped stored policy: the
    "invalid request mutated nothing" assertions compare this before/after, so
    garbage must fail rather than compare equal to {}.
    """
    policy = placement_metadata(raw).get("policy")
    if policy is None:
        return {}
    if not isinstance(policy, dict) or not isinstance(policy.get("members"), dict):
        raise ValueError("stored policy is not the expected shape")
    if member not in policy["members"]:
        return {}
    entry = policy["members"][member]
    if not isinstance(entry, dict):
        raise ValueError("stored member intent is not an object")
    rgw = entry.get("rgw")
    if rgw is None:
        return {}
    if not isinstance(rgw, dict):
        raise ValueError("stored RGW intent is not an object")
    return dict(rgw)


def observed_rgw_members(raw):
    """Return {member: rgw-running flag} from the observed placement list.

    Raises ValueError on malformed bodies or a misshaped ``observed`` entry;
    the scale-to-zero and down-member assertions must read real parsed state,
    never a lenient default.
    """
    observed = placement_metadata(raw).get("observed")
    if not isinstance(observed, list):
        raise ValueError(f"observed placement is not a list: {observed!r}")
    flags = {}
    for entry in observed:
        if not isinstance(entry, dict) or not isinstance(entry.get("member"), str):
            raise ValueError(f"observed entry is malformed: {entry!r}")
        if type(entry.get("rgw")) is not bool:
            raise ValueError("observed RGW state is not a boolean")
        flags[entry["member"]] = entry["rgw"]
    return flags


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


_MEMBER_LINE_RE = re.compile(r"^- (.+) \((.+)\)$")


def cluster_member_names(status_text):
    """Return the set of cluster member names from ``microceph status`` output.

    Parses the ``- <name> (<address>)`` lines under "MicroCeph deployment
    summary:" (see cmd/microceph/status.go). Matching the whole-line shape,
    rather than a substring search over the entire status text, means a
    member name that happens to be a prefix of another member's name (e.g.
    "rgw-mvm-first" inside "rgw-mvm-first-2") is never reported present
    unless it is genuinely a member on its own line.
    """
    names = set()
    for line in (status_text or "").splitlines():
        match = _MEMBER_LINE_RE.match(line)
        if match:
            names.add(match.group(1))
    return names


def migration_samples(text):
    """Return the verdict of an in-guest migration sampler's output.

    Each line is ``<started> <new_ok> <old_ok>`` (0/1); a final ``END`` line
    marks a finished sampler. Only samples taken after the PUT started count
    as in-flight. ``available`` is True only when every in-flight sample read
    the object from one of the two gateways; ``replacement_ready`` is True when
    the new gateway served it at least once.
    """
    in_flight = []
    replacement = False
    complete = False
    for line in (text or "").splitlines():
        if line.strip() == "END":
            complete = True
            continue
        fields = line.split()
        if len(fields) != 3 or any(f not in ("0", "1") for f in fields):
            raise ValueError(f"malformed migration sample: {line!r}")
        started, new_ok, old_ok = (f == "1" for f in fields)
        replacement = replacement or new_ok
        if started:
            in_flight.append(new_ok or old_ok)
    return {
        "samples": len(in_flight),
        "available": bool(in_flight) and all(in_flight),
        "replacement_ready": replacement,
        "complete": complete,
    }


def rgw_frontend_conf_ports(conf_text):
    """Return listener settings from the generated Beast frontend line."""
    fields = _rgw_frontend_fields(conf_text)
    try:
        port = int(fields.get("port", 0))
        ssl_port = int(fields.get("ssl_port", 0))
    except ValueError as exc:
        raise ValueError("malformed RGW listener port") from exc
    return {"port": port, "ssl_port": ssl_port, "ssl": ssl_port != 0}


def rgw_frontend_tls_paths(conf_text):
    """Return the exact referenced pair, not every file named server.key."""
    fields = _rgw_frontend_fields(conf_text)
    paths = [fields.get("ssl_certificate"), fields.get("ssl_private_key")]
    if paths == [None, None]:
        return []
    if not all(paths):
        raise ValueError("incomplete RGW TLS references")
    return paths


def _rgw_frontend_fields(conf_text):
    for line in (conf_text or "").splitlines():
        name, separator, value = line.partition("=")
        if separator and name.strip() == "rgw frontends":
            return dict(token.split("=", 1) for token in shlex.split(value) if "=" in token)
    raise ValueError("no rgw frontends line in radosgw.conf")

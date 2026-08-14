"""Robot Framework library: parsing of RGW admin-ops API responses.

Keeps the JSON shape checks for the /admin/log endpoints, and the rgw log
crash-signature match the issue #810 regression suite relies on, out of the
.robot suites as single unit-testable functions.

The shape helpers deliberately raise rather than return a bool: a response that
is empty, truncated, or an error document means the request never really
reached the handler under test, and a regression suite that only asked "did
anything come back" would pass in exactly that case.
"""

import json
import re

# Ceph's fatal-signal handler always writes this banner, independent of the
# configured debug level, followed by a backtrace.
CRASH_BANNER = "Caught signal (Segmentation fault)"

# Frames unique to the issue #810 crash path. The banner alone would also match
# an unrelated segfault elsewhere in the daemon, so a match requires both.
CRASH_FRAMES = re.compile(r"run_coro|RGWOp_DATALog|rethrow_exception")


def curl_body_and_status(curl_stdout):
    """Split the stdout of ``curl -w '\\n%{http_code}'`` into (body, status).

    curl writes the status code on its own final line after the response body,
    so everything before the last newline is the body. A request whose server
    died part-way through produces no body and the status ``000``.
    """
    body, _, status = curl_stdout.rpartition("\n")
    return body.strip(), status.strip()


def rgw_user_s3_credentials(user_json_text):
    """Return the (access_key, secret_key) of the first S3 key of an RGW user.

    *user_json_text* is the stdout of ``radosgw-admin user create`` or
    ``radosgw-admin user info``.
    """
    doc = _decode(user_json_text, "radosgw-admin user")
    keys = doc.get("keys")
    if not keys:
        raise AssertionError(f"radosgw-admin user has no S3 keys: {user_json_text!r}")
    return keys[0]["access_key"], keys[0]["secret_key"]


def datalog_num_objects(body):
    """Return num_objects from a ``GET /admin/log?type=data`` response.

    The healthy document is ``{"num_objects": <n>}``, written by
    RGWOp_DATALog_Info::send_response.
    """
    doc = _decode(body, "datalog info")
    if "num_objects" not in doc:
        raise AssertionError(f'datalog info response has no "num_objects": {body!r}')
    return doc["num_objects"]


def datalog_shard_info_marker(body):
    """Return the marker from a ``GET /admin/log?type=data&id=<n>&info`` response.

    The healthy document is ``{"marker": <str>, "last_update": <str>}``.
    RGWOp_DATALog_ShardInfo::send_response writes it as
    ``encode_json("info", RGWDataChangesLogInfo)``, but "info" is the root
    section and Ceph's JSON formatter drops the root section's name, so the
    dumped fields land at the top level rather than nested under it -- the same
    reason ``open_object_section("num_objects")`` yields ``{"num_objects": <n>}``
    and not ``{"num_objects": {"num_objects": <n>}}``.

    An idle shard's marker is legitimately empty, so callers must not treat ""
    as a failure -- returning at all is what this asserts.
    """
    doc = _decode(body, "datalog shard info")
    missing = [key for key in ("marker", "last_update") if key not in doc]
    if missing:
        raise AssertionError(f"datalog shard info is missing {missing}: {body!r}")
    return doc["marker"]


def datalog_list_entry_count(body):
    """Return the entry count of a ``GET /admin/log?type=data&id=<n>`` response.

    The healthy document is ``{"marker": ..., "last_update": ..., "truncated":
    <bool>, "entries": [...]}``, written by RGWOp_DATALog_List::send_response.
    An idle shard returns an empty array, so 0 is a valid result.
    """
    doc = _decode(body, "datalog list")
    entries = doc.get("entries")
    if not isinstance(entries, list):
        raise AssertionError(f'datalog list response has no "entries" array: {body!r}')
    return len(entries)


def datalog_crash_signature(log_text, context_lines=20):
    """Return the issue #810 SIGSEGV excerpt in *log_text*, or "" if absent.

    A match needs the fatal-signal banner followed within *context_lines* by a
    frame from the crashing path, so an unrelated segfault elsewhere in radosgw
    is not misreported as this bug.

    *log_text* must be only the log written since the request under test --
    passing a whole log file would match a stale banner from an earlier crash.
    """
    lines = log_text.splitlines()
    for index, line in enumerate(lines):
        if CRASH_BANNER not in line:
            continue
        excerpt = "\n".join(lines[index:index + context_lines])
        if CRASH_FRAMES.search(excerpt):
            return excerpt
    return ""


def _decode(body, what):
    """json.loads *body*, reporting a parse failure as a response-shape error."""
    if not body:
        raise AssertionError(f"{what} response body is empty -- nothing came back")
    try:
        doc = json.loads(body)
    except ValueError as exc:
        raise AssertionError(f"{what} response is not JSON ({exc}): {body!r}")
    if not isinstance(doc, dict):
        raise AssertionError(f"{what} response is not a JSON object: {body!r}")
    return doc

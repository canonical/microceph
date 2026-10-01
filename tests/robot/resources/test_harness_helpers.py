"""Unit tests for the pure helpers in the MicroCeph Robot Framework harness.

These cover the @staticmethod parsers and the generic _poll_until poller on the
microceph_harness class, plus the standalone snap_services / cephfs_replication
helpers. The helpers are pure (no self, no BuiltIn), so importing the module and
calling them needs no running Robot context -- only that robotframework is
importable (microceph_harness imports robot.api at module top).

Run with pytest:
    pytest tests/robot/resources/test_harness_helpers.py
"""

import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import types

import yaml

import placement_status
from microceph_harness import microceph_harness as H
from cluster_ops import parse_migration_status
from snap_services import enabled_active_services, service_has_state
from cephfs_replication import cephfs_replication_list_has_volume, verify_cephfs_list_entry_types
from rbd_replication import (
    rbd_mirror_health,
    rbd_primary_image_count,
    rbd_synced_image_count,
)
from streaming_process import run_streaming_process


def test_restore_node_ip_on_network_uses_original_address_and_prefix(monkeypatch):
    harness = H()
    calls = []

    def fake_exec(container, *argv, timeout, check):
        calls.append((container, argv, timeout, check))

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)

    harness.restore_node_ip_on_network("node-wrk1", "10.33.104.11", "10.33.104.1/24", "eth1")

    assert calls == [("node-wrk1", ("ip", "addr", "add", "10.33.104.11/24", "dev", "eth1"), 10, True)]


def test_get_node_ip_waits_for_public_ipv4_after_restart(monkeypatch):
    _with_logger(monkeypatch)
    harness = H()
    outputs = iter(["10.101.181.88 fd42::1", "10.101.181.88 10.33.104.10 fd42::1"])
    calls = []

    def fake_exec(container, *argv, timeout):
        calls.append((container, argv, timeout))
        return _Res(0, next(outputs), "")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    assert harness.get_node_ip("node-wrk1", "10.33.104.1/24") == "10.33.104.10"
    assert calls == [("node-wrk1", ("hostname", "-I"), 30)] * 2


def test_select_ip_on_network_skips_management_address():
    assert H._select_ip_on_network("10.101.181.88 10.33.104.10", "10.33.104.1/24") == "10.33.104.10"


def test_select_ip_on_network_reports_missing_address():
    assert H._select_ip_on_network("10.101.181.88", "10.33.104.1/24") == ""


# ---------------------------------------------------------------------------
# _csv_lists_instance
# ---------------------------------------------------------------------------

def test_csv_lists_instance_matches_first_column():
    assert H._csv_lists_instance("microceph-test-vm,RUNNING,10.0.0.1", "microceph-test-vm")


def test_csv_lists_instance_ignores_other_names():
    assert not H._csv_lists_instance("other-vm,RUNNING,10.0.0.2\nnode-wrk0,STOPPED,", "microceph-test-vm")


def test_csv_lists_instance_empty_output_is_absent():
    assert not H._csv_lists_instance("", "microceph-test-vm")


def test_csv_lists_instance_none_output_is_absent():
    assert not H._csv_lists_instance(None, "microceph-test-vm")


def test_csv_lists_instance_name_must_be_whole_first_column():
    assert not H._csv_lists_instance("microceph-test-vm-2,RUNNING,", "microceph-test-vm")


# ---------------------------------------------------------------------------
# _lxc_instance_exists -- fails closed: an unanswered probe means "still
# there", never "gone". (_mh, _Res are defined further down this file; that's
# fine here since these bodies only run once the whole module has loaded.)
# ---------------------------------------------------------------------------

def test_lxc_instance_exists_true_when_listed(monkeypatch):
    h = H()
    monkeypatch.setattr(h, "_exec", lambda argv, timeout: _Res(0, "microceph-test-vm,RUNNING,", ""))
    assert h._lxc_instance_exists("microceph-test-vm") is True


def test_lxc_instance_exists_false_when_not_listed(monkeypatch):
    h = H()
    monkeypatch.setattr(h, "_exec", lambda argv, timeout: _Res(0, "", ""))
    assert h._lxc_instance_exists("microceph-test-vm") is False


def test_lxc_instance_exists_fails_closed_on_error(monkeypatch):
    h = H()
    monkeypatch.setattr(h, "_exec", lambda argv, timeout: _Res(1, "", "error: not found"))
    assert h._lxc_instance_exists("microceph-test-vm") is True


def test_lxc_instance_exists_fails_closed_on_timeout(monkeypatch):
    h = H()
    monkeypatch.setattr(h, "_exec", lambda argv, timeout: _Res(124, "", ""))
    assert h._lxc_instance_exists("microceph-test-vm") is True


# ---------------------------------------------------------------------------
# _delete_instance_synced -- the delete is re-issued between probes, not just
# attempted once before the wait.
# ---------------------------------------------------------------------------

def test_delete_instance_synced_gone_on_first_probe_deletes_once(monkeypatch):
    h = H()
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    calls = {"delete": 0}

    def fake_exec(argv, timeout):
        if argv[:2] == ["lxc", "delete"]:
            calls["delete"] += 1
            return _Res(0, "", "")
        if argv[:2] == ["lxc", "list"]:
            return _Res(0, "", "")  # never listed: already gone
        raise AssertionError(f"unexpected exec: {argv}")

    monkeypatch.setattr(h, "_exec", fake_exec)
    h._delete_instance_synced("microceph-test-vm")
    assert calls["delete"] == 1


def test_delete_instance_synced_reissues_delete_between_probes(monkeypatch):
    h = H()
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    calls = {"delete": 0}
    still_listed = [True, False]  # first probe: still there, second: gone

    def fake_exec(argv, timeout):
        if argv[:2] == ["lxc", "delete"]:
            calls["delete"] += 1
            return _Res(0, "", "")
        if argv[:2] == ["lxc", "list"]:
            listed = still_listed.pop(0)
            return _Res(0, "microceph-test-vm,RUNNING," if listed else "", "")
        raise AssertionError(f"unexpected exec: {argv}")

    monkeypatch.setattr(h, "_exec", fake_exec)
    h._delete_instance_synced("microceph-test-vm")
    assert calls["delete"] == 2


def test_delete_instance_synced_never_gone_raises(monkeypatch):
    h = H()
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    def fake_exec(argv, timeout):
        if argv[:2] == ["lxc", "delete"]:
            return _Res(0, "", "")
        if argv[:2] == ["lxc", "list"]:
            return _Res(0, "microceph-test-vm,RUNNING,", "")
        raise AssertionError(f"unexpected exec: {argv}")

    monkeypatch.setattr(h, "_exec", fake_exec)
    with pytest.raises(AssertionError) as exc:
        h._delete_instance_synced("microceph-test-vm")
    assert str(exc.value) == "microceph-test-vm still listed by lxc after delete"


# ---------------------------------------------------------------------------
# Robot CLI wrapper
# ---------------------------------------------------------------------------

def test_robot_wrapper_exports_snapd_channel(monkeypatch):
    """The wrapper gives Robot and child scripts one snapd channel value."""
    wrapper_path = Path(__file__).parents[1] / "robot.py"
    spec = importlib.util.spec_from_file_location("microceph_robot_wrapper", wrapper_path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    captured = {}

    def fake_run(command, env):
        captured["command"] = command
        captured["env"] = env
        return types.SimpleNamespace(returncode=0)

    monkeypatch.setattr(module.subprocess, "run", fake_run)
    monkeypatch.setattr(
        sys,
        "argv",
        ["robot.py", "--test-suite", "unit-tests", "--snapd-channel", "latest/edge"],
    )

    assert module.main() == 0
    assert "SNAPD_CHANNEL:latest/edge" in captured["command"]
    assert captured["env"]["SNAPD_CHANNEL"] == "latest/edge"


# ---------------------------------------------------------------------------
# _safe_int
# ---------------------------------------------------------------------------

def test_safe_int_plain_digits():
    assert H._safe_int("3") == 3


def test_safe_int_strips_whitespace():
    assert H._safe_int(" 5 ") == 5


def test_safe_int_empty_is_zero():
    assert H._safe_int("") == 0


def test_safe_int_non_numeric_is_zero():
    assert H._safe_int("x") == 0


def test_safe_int_negative_is_zero():
    # isdigit() is False for a leading '-', so this falls back to 0.
    assert H._safe_int("-1") == 0


# ---------------------------------------------------------------------------
# _ceph_conf_value
# ---------------------------------------------------------------------------

CEPH_CONF_SAMPLE = """# Generated by MicroCeph, DO NOT EDIT.
[global]
run dir = /var/snap/microceph/current/run
fsid = aabbccdd-1234
mon host = 10.20.0.10
public_network = 10.10.0.1/24,10.20.0.1/24
ms bind ipv4 = true
ms bind ipv6 = false
"""


def test_ceph_conf_value_multi_subnet_public_network():
    # The comma-delimited list is returned verbatim (the multi-subnet invariant).
    assert (
        H._ceph_conf_value(CEPH_CONF_SAMPLE, "public_network")
        == "10.10.0.1/24,10.20.0.1/24"
    )


def test_ceph_conf_value_key_with_space():
    assert H._ceph_conf_value(CEPH_CONF_SAMPLE, "mon host") == "10.20.0.10"


def test_ceph_conf_value_absent_key_is_empty():
    assert H._ceph_conf_value(CEPH_CONF_SAMPLE, "cluster_network") == ""


def test_ceph_conf_value_skips_section_headers_and_blanks():
    assert H._ceph_conf_value("[global]\n\nfsid = x\n", "fsid") == "x"


# ---------------------------------------------------------------------------
# _last_eth_interface
# ---------------------------------------------------------------------------

IP_A_SAMPLE = """1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
2: eth0@if5: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default qlen 1000
    link/ether 00:16:3e:aa:bb:cc brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.10.0.10/24 scope global eth0
3: eth2@if7: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default qlen 1000
    link/ether 00:16:3e:dd:ee:ff brd ff:ff:ff:ff:ff:ff link-netnsid 0
4: eth3@if9: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default qlen 1000
    link/ether 00:16:3e:11:22:33 brd ff:ff:ff:ff:ff:ff link-netnsid 0
"""


def test_last_eth_interface_picks_highest_indexed():
    # The most recently attached NIC has the highest ifindex, so it is last.
    assert H._last_eth_interface(IP_A_SAMPLE) == "eth3"


def test_last_eth_interface_strips_veth_peer_and_skips_sublines():
    # Only header lines match; the @peer suffix is dropped and inet sub-lines ignored.
    assert H._last_eth_interface("2: eth0@if5: <UP>\n    inet 10.0.0.1/24\n") == "eth0"


def test_last_eth_interface_handles_no_peer_suffix():
    assert H._last_eth_interface("5: eth7: <UP> mtu 1500\n") == "eth7"


def test_last_eth_interface_none_present_returns_empty():
    assert H._last_eth_interface("1: lo: <LOOPBACK>\n2: enp5s0: <UP>\n") == ""


# ---------------------------------------------------------------------------
# _coerce_xtrace
# ---------------------------------------------------------------------------

def test_coerce_xtrace_bool_false():
    assert H._coerce_xtrace(False) is False


def test_coerce_xtrace_string_true():
    assert H._coerce_xtrace("True") is True


def test_coerce_xtrace_yes():
    assert H._coerce_xtrace("yes") is True


def test_coerce_xtrace_one():
    assert H._coerce_xtrace("1") is True


def test_coerce_xtrace_off():
    assert H._coerce_xtrace("off") is False


def test_coerce_xtrace_empty():
    assert H._coerce_xtrace("") is False


def test_coerce_xtrace_bool_true():
    assert H._coerce_xtrace(True) is True


# ---------------------------------------------------------------------------
# _ceph_osd_counts
# ---------------------------------------------------------------------------

def test_ceph_osd_counts_valid():
    payload = json.dumps({"osdmap": {"num_up_osds": 3, "num_in_osds": 2}})
    assert H._ceph_osd_counts(payload) == (3, 2)


def test_ceph_osd_counts_missing_osdmap():
    assert H._ceph_osd_counts(json.dumps({})) == (0, 0)


def test_ceph_osd_counts_empty_string():
    assert H._ceph_osd_counts("") == (0, 0)


def test_ceph_osd_counts_garbage():
    assert H._ceph_osd_counts("not json at all") == (0, 0)


# ---------------------------------------------------------------------------
# _legacy_cephx_health_is_compatible
# ---------------------------------------------------------------------------

def test_legacy_cephx_health_accepts_only_auth_insecure_checks():
    payload = json.dumps(
        {
            "status": "HEALTH_ERR",
            "checks": {
                "AUTH_INSECURE_CLIENT_KEY_TYPE": {"severity": "HEALTH_WARN"},
                "AUTH_INSECURE_SERVICE_KEY_TYPE": {"severity": "HEALTH_ERR"},
            },
        }
    )

    assert H._legacy_cephx_health_is_compatible(payload) is True


def test_legacy_cephx_health_rejects_non_auth_warning():
    payload = json.dumps(
        {
            "status": "HEALTH_WARN",
            "checks": {
                "AUTH_INSECURE_CLIENT_KEY_TYPE": {"severity": "HEALTH_WARN"},
                "OSD_DOWN": {"severity": "HEALTH_WARN"},
            },
        }
    )

    assert H._legacy_cephx_health_is_compatible(payload) is False


def test_legacy_cephx_health_rejects_non_health_or_empty_checks():
    assert H._legacy_cephx_health_is_compatible(json.dumps({"status": "HEALTH_OK", "checks": {}})) is False
    assert H._legacy_cephx_health_is_compatible(
        json.dumps({"status": "HEALTH_UNKNOWN", "checks": {"AUTH_INSECURE_CLIENT_KEY_TYPE": {"severity": "HEALTH_ERR"}}})
    ) is False


# ---------------------------------------------------------------------------
# _rgw_daemon_count
# ---------------------------------------------------------------------------

def test_rgw_daemon_count_present():
    text = (
        "  services:\n"
        "    mon: 1 daemons, quorum node-wrk0\n"
        "    rgw: 2 daemons active (1 hosts, 1 zones)\n"
    )
    assert H._rgw_daemon_count(text) == 2


def test_rgw_daemon_count_no_rgw_line():
    text = (
        "  services:\n"
        "    mon: 1 daemons, quorum node-wrk0\n"
        "    osd: 3 osds: 3 up, 3 in\n"
    )
    assert H._rgw_daemon_count(text) == 0


def test_rgw_daemon_count_empty_string():
    assert H._rgw_daemon_count("") == 0


def test_rgw_daemon_count_rgw_line_no_digit_match():
    # An "rgw:" line with no "<n> daemon" match returns 0 rather than raising.
    assert H._rgw_daemon_count("    rgw: active\n") == 0


# ---------------------------------------------------------------------------
# _cephfs_snaps_synced_total
# ---------------------------------------------------------------------------

def test_cephfs_snaps_synced_total_dict_shape_real_api():
    # Real microceph output: peers AND mirror_status are JSON OBJECTS (Go maps,
    # api/types/replication_cephfs.go:115,121), keyed by uuid / dir-path. Each is
    # iterated by VALUE, matching the pre-refactor jq '.peers[].mirror_status | .[]'.
    payload = json.dumps(
        {
            "volume": "vol1",
            "mirror_path_count": 2,
            "peers": {
                "uuid-1": {"name": "siteb", "mirror_status": {"/d1": {"snaps_synced": 5}}},
                "uuid-2": {"name": "sitec", "mirror_status": {"/d2": {"snaps_synced": 3}}},
            },
        }
    )
    assert H._cephfs_snaps_synced_total(payload) == 8


def test_cephfs_snaps_synced_total_list_fallback():
    # List-shaped peers/mirror_status are still accepted for robustness.
    payload = json.dumps(
        {
            "peers": [
                {"mirror_status": [{"snaps_synced": 2}, {"snaps_synced": 3}]},
                {"mirror_status": [{"snaps_synced": 5}]},
            ]
        }
    )
    assert H._cephfs_snaps_synced_total(payload) == 10


def test_cephfs_snaps_synced_total_mirror_status_dict_value_iterated():
    payload = json.dumps(
        {"peers": {"u": {"mirror_status": {"a": {"snaps_synced": 4}, "b": {"snaps_synced": 6}}}}}
    )
    assert H._cephfs_snaps_synced_total(payload) == 10


def test_cephfs_snaps_synced_total_missing_field_defaults_zero():
    payload = json.dumps({"peers": {"u": {"mirror_status": {"a": {}, "b": {"snaps_synced": 7}}}}})
    assert H._cephfs_snaps_synced_total(payload) == 7


def test_cephfs_snaps_synced_total_null_peers_is_zero():
    # A nil Go map marshals to JSON null; jq's // 0 coerced it, so must we.
    assert H._cephfs_snaps_synced_total(json.dumps({"peers": None})) == 0


def test_cephfs_snaps_synced_total_null_mirror_status_is_zero():
    payload = json.dumps({"peers": {"u": {"mirror_status": None}}})
    assert H._cephfs_snaps_synced_total(payload) == 0


def test_cephfs_snaps_synced_total_null_snaps_synced_is_zero():
    payload = json.dumps({"peers": {"u": {"mirror_status": {"a": {"snaps_synced": None}}}}})
    assert H._cephfs_snaps_synced_total(payload) == 0


def test_cephfs_snaps_synced_total_empty_string():
    assert H._cephfs_snaps_synced_total("") == 0


def test_cephfs_snaps_synced_total_garbage():
    assert H._cephfs_snaps_synced_total("garbage") == 0


# ---------------------------------------------------------------------------
# _parse_network_cidr
# ---------------------------------------------------------------------------

def test_parse_network_cidr_public_row():
    # Mirrors `lxc network list --format=csv` columns
    # (NAME,TYPE,MANAGED,IPV4,...); cut -d, -f4 == the IPv4 CIDR at index 3.
    csv_text = (
        "lxdbr0,bridge,YES,10.123.45.1/24,fd42::/64,,1,CREATED\n"
        "public,bridge,YES,10.0.0.1/24,,,0,CREATED\n"
        "internal,bridge,YES,10.1.0.1/24,,,0,CREATED\n"
    )
    assert H._parse_network_cidr(csv_text, "public") == "10.0.0.1/24"


def test_parse_network_cidr_internal_row():
    csv_text = (
        "public,bridge,YES,10.0.0.1/24,,,0,CREATED\n"
        "internal,bridge,YES,10.1.0.1/24,,,0,CREATED\n"
    )
    assert H._parse_network_cidr(csv_text, "internal") == "10.1.0.1/24"


def test_parse_network_cidr_no_match_returns_empty():
    csv_text = "public,bridge,YES,10.0.0.1/24,,,0,CREATED\n"
    assert H._parse_network_cidr(csv_text, "internal") == ""


def test_parse_network_cidr_empty_input_returns_empty():
    assert H._parse_network_cidr("", "public") == ""


def test_parse_network_cidr_returns_first_match():
    csv_text = (
        "public,bridge,YES,10.0.0.1/24,,,0,CREATED\n"
        "public,bridge,YES,10.9.9.1/24,,,0,CREATED\n"
    )
    assert H._parse_network_cidr(csv_text, "public") == "10.0.0.1/24"


def test_parse_network_cidr_short_row_returns_empty():
    # A matching line with fewer than 4 comma fields yields "".
    assert H._parse_network_cidr("public,bridge,YES\n", "public") == ""


# ---------------------------------------------------------------------------
# _host_address
# ---------------------------------------------------------------------------

def test_host_address_standard_24():
    # The ".10" head host the suite assigns on a /24 gateway CIDR.
    assert H._host_address("10.0.0.1/24", 10) == "10.0.0.10"


def test_host_address_gateway_host_part_ignored():
    # Arithmetic is on the network address, so the gateway's host part is
    # irrelevant
    assert H._host_address("10.0.0.249/24", 10) == "10.0.0.10"


def test_host_address_16_prefix():
    # strict=False masks the host bits before computing the network address.
    assert H._host_address("10.1.0.1/16", 10) == "10.1.0.10"


# ---------------------------------------------------------------------------
# _count_configured_disks
# ---------------------------------------------------------------------------

def test_count_configured_disks_single_substring():
    payload = json.dumps(
        {
            "ConfiguredDisks": [
                {"path": "/dev/vgtst/lvtest"},
                {"path": "/dev/sdc"},
            ]
        }
    )
    assert H._count_configured_disks(payload, "/dev/vgtst/lvtest") == 1


def test_count_configured_disks_multiple_substrings():
    payload = json.dumps(
        {
            "ConfiguredDisks": [
                {"path": "/dev/sdia"},
                {"path": "/dev/sdib"},
                {"path": "/dev/sdc"},
            ]
        }
    )
    assert H._count_configured_disks(payload, "/dev/sdia", "/dev/sdib") == 2


def test_count_configured_disks_no_match_is_zero():
    payload = json.dumps({"ConfiguredDisks": [{"path": "/dev/sdc"}]})
    assert H._count_configured_disks(payload, "/dev/sdia") == 0


def test_count_configured_disks_missing_key_is_zero():
    assert H._count_configured_disks(json.dumps({}), "/dev/sdia") == 0


def test_count_configured_disks_garbage_is_zero():
    assert H._count_configured_disks("not json at all", "/dev/sdia") == 0


def test_count_configured_disks_empty_string_is_zero():
    assert H._count_configured_disks("", "/dev/sdia") == 0


def test_count_configured_disks_entry_without_path_is_skipped():
    payload = json.dumps({"ConfiguredDisks": [{}, {"path": "/dev/sdia"}]})
    assert H._count_configured_disks(payload, "/dev/sdia") == 1


# ---------------------------------------------------------------------------
# _poll_until
# ---------------------------------------------------------------------------

def test_poll_until_returns_when_predicate_true_on_third_call():
    calls = []

    def predicate():
        calls.append(1)
        return len(calls) == 3

    H._poll_until(predicate, attempts=10, interval=0, fail_msg="boom")
    assert len(calls) == 3


def test_poll_until_raises_after_attempts_when_always_false():
    calls = []

    def predicate():
        calls.append(1)
        return False

    raised = False
    try:
        H._poll_until(predicate, attempts=4, interval=0, fail_msg="never happened")
    except AssertionError as exc:
        raised = True
        assert str(exc) == "never happened"
    assert raised
    assert len(calls) == 4


def test_poll_until_invokes_between_between_probes():
    between_calls = []

    def predicate():
        return False

    def between():
        between_calls.append(1)

    try:
        H._poll_until(
            predicate,
            attempts=3,
            interval=0,
            fail_msg="x",
            between=between,
        )
    except AssertionError:
        pass
    # between runs after each failed probe -> once per attempt.
    assert len(between_calls) == 3


def test_poll_until_invokes_on_fail_on_exhaustion():
    on_fail_calls = []

    def predicate():
        return False

    def on_fail():
        on_fail_calls.append(1)

    try:
        H._poll_until(
            predicate,
            attempts=2,
            interval=0,
            fail_msg="x",
            on_fail=on_fail,
        )
    except AssertionError:
        pass
    assert len(on_fail_calls) == 1


def test_poll_until_no_raise_when_raise_on_timeout_false():
    def predicate():
        return False

    # Should simply return without raising.
    H._poll_until(
        predicate,
        attempts=2,
        interval=0,
        fail_msg="should not be raised",
        raise_on_timeout=False,
    )


def test_poll_until_accepts_string_interval():
    # Production callers pass Robot time strings ('3s'/'15s'/'5s'); '0s' exercises the
    # timestr_to_secs branch without actually sleeping.
    calls = []

    def predicate():
        calls.append(1)
        return False

    try:
        H._poll_until(predicate, attempts=3, interval="0s", fail_msg="x")
    except AssertionError:
        pass
    assert len(calls) == 3


def test_poll_until_between_not_called_on_success():
    between_calls = []

    def predicate():
        return True

    def between():
        between_calls.append(1)

    H._poll_until(predicate, attempts=3, interval=0, fail_msg="x", between=between)
    assert between_calls == []


# ---------------------------------------------------------------------------
# _advance_consecutive
# ---------------------------------------------------------------------------

def test_advance_consecutive_increments_on_success():
    assert H._advance_consecutive(1, True) == 2
    assert H._advance_consecutive(0, True) == 1


def test_advance_consecutive_resets_on_dip():
    assert H._advance_consecutive(1, False) == 0


def test_advance_consecutive_two_consecutive_polls_is_enough():
    # mirrors the two-consecutive-polls gate in upgrade_multi_node
    count = 0
    count = H._advance_consecutive(count, True)
    assert count < 2
    count = H._advance_consecutive(count, True)
    assert count >= 2


def test_advance_consecutive_dip_resets_the_gate():
    # a dip before the second success means the count reaches 2 only on the last poll
    count = 0
    reached_two_at = None
    for i, ok in enumerate([True, False, True, True]):
        count = H._advance_consecutive(count, ok)
        if count >= 2 and reached_two_at is None:
            reached_two_at = i
    assert reached_two_at == 3


# ---------------------------------------------------------------------------
# enabled_active_services (snap_services.py)
# ---------------------------------------------------------------------------

def test_enabled_active_services_filters_enabled_and_active():
    output = (
        "Service                 Startup   Current   Notes\n"
        "microceph.daemon        enabled   active    -\n"
        "microceph.mds           enabled   inactive  -\n"
        "microceph.mgr           disabled  active    -\n"
        "microceph.osd           enabled   active    -\n"
    )
    assert enabled_active_services(output) == ["microceph.daemon", "microceph.osd"]


def test_enabled_active_services_empty_string():
    assert enabled_active_services("") == []


def test_enabled_active_services_header_only():
    assert enabled_active_services("Service  Startup  Current  Notes\n") == []


def test_service_has_state_matches_target_service():
    output = (
        "Service                 Startup   Current   Notes\n"
        "microceph.daemon        enabled   active    -\n"
        "microceph.smbd          disabled  inactive  -\n"
    )
    assert service_has_state(output, "microceph.smbd", "disabled", "inactive") is True


def test_service_has_state_rejects_different_state_or_service():
    output = (
        "Service                 Startup   Current   Notes\n"
        "microceph.smbd          enabled   active    -\n"
    )
    assert service_has_state(output, "microceph.smbd", "disabled", "inactive") is False
    assert service_has_state(output, "microceph.mgr", "enabled", "active") is False


# ---------------------------------------------------------------------------
# cephfs_replication_list_has_volume (cephfs_replication.py)
# ---------------------------------------------------------------------------

def test_cephfs_replication_list_has_volume_present_nonempty():
    payload = json.dumps({"myfs": [{"resource_path": "/a", "resource_type": "directory"}]})
    assert cephfs_replication_list_has_volume(payload, "myfs") is True


def test_cephfs_replication_list_has_volume_absent_key():
    payload = json.dumps({"otherfs": [{"resource_path": "/a"}]})
    assert cephfs_replication_list_has_volume(payload, "myfs") is False


def test_cephfs_replication_list_has_volume_empty_object():
    assert cephfs_replication_list_has_volume(json.dumps({}), "myfs") is False


def test_cephfs_replication_list_has_volume_empty_list_value():
    # Key present but maps to an empty list -> not synced yet -> False (bool([]) path).
    assert cephfs_replication_list_has_volume(json.dumps({"myfs": []}), "myfs") is False


def test_cephfs_replication_list_has_volume_bad_json():
    assert cephfs_replication_list_has_volume("not json", "myfs") is False


# ---------------------------------------------------------------------------
# verify_cephfs_list_entry_types (cephfs_replication.py) -- imported per spec
# ---------------------------------------------------------------------------

def test_verify_cephfs_list_entry_types_ok():
    payload = json.dumps(
        {
            "vol": [
                {"resource_path": "/volumes/sub", "resource_type": "subvolume"},
                {"resource_path": "/data", "resource_type": "directory"},
            ]
        }
    )
    items = verify_cephfs_list_entry_types(payload, "vol")
    assert len(items) == 2


def test_verify_cephfs_list_entry_types_mismatch_raises():
    payload = json.dumps(
        {"vol": [{"resource_path": "/volumes/sub", "resource_type": "directory"}]}
    )
    raised = False
    try:
        verify_cephfs_list_entry_types(payload, "vol")
    except AssertionError:
        raised = True
    assert raised


def test_verify_cephfs_list_entry_types_absent_volume_raises():
    # Volume key not present at all -> AssertionError (was an empty-jq -> empty-list case).
    payload = json.dumps({"other": [{"resource_path": "/d", "resource_type": "directory"}]})
    raised = False
    try:
        verify_cephfs_list_entry_types(payload, "vol")
    except AssertionError:
        raised = True
    assert raised


def test_verify_cephfs_list_entry_types_empty_volume_raises():
    # Volume present but maps to no entries -> AssertionError.
    raised = False
    try:
        verify_cephfs_list_entry_types(json.dumps({"vol": []}), "vol")
    except AssertionError:
        raised = True
    assert raised


def test_verify_cephfs_list_entry_types_bad_json_raises():
    # Malformed JSON now raises AssertionError rather than letting JSONDecodeError escape.
    raised = False
    try:
        verify_cephfs_list_entry_types("not json", "vol")
    except AssertionError:
        raised = True
    assert raised


# ---------------------------------------------------------------------------
# _rbd_synced_image_count
# ---------------------------------------------------------------------------

def test_rbd_synced_image_count_sums_images():
    payload = json.dumps(
        [
            {"Images": [{"name": "img1"}, {"name": "img2"}]},
            {"Images": [{"name": "img3"}]},
        ]
    )
    assert rbd_synced_image_count(payload) == 3


def test_rbd_synced_image_count_entry_without_images_is_zero():
    payload = json.dumps([{}, {"Images": [{"name": "img1"}]}])
    assert rbd_synced_image_count(payload) == 1


def test_rbd_synced_image_count_empty_list_is_zero():
    assert rbd_synced_image_count("[]") == 0


def test_rbd_synced_image_count_garbage_is_zero():
    assert rbd_synced_image_count("not json at all") == 0


def test_rbd_synced_image_count_empty_string_is_zero():
    assert rbd_synced_image_count("") == 0


def test_rbd_synced_image_count_null_images_is_zero():
    # A null Images list (jq //-equivalent) must not raise; the Go marshaller emits
    # [] today but the helper guards null for parity with jq.
    assert rbd_synced_image_count(json.dumps([{"Images": None}])) == 0


# ---------------------------------------------------------------------------
# _rbd_primary_image_count
# ---------------------------------------------------------------------------

def test_rbd_primary_image_count_counts_primary():
    payload = json.dumps(
        [
            {"Images": [{"is_primary": True}, {"is_primary": False}]},
            {"Images": [{"is_primary": True}]},
        ]
    )
    assert rbd_primary_image_count(payload) == 2


def test_rbd_primary_image_count_none_primary_is_zero():
    payload = json.dumps([{"Images": [{"is_primary": False}, {"is_primary": False}]}])
    assert rbd_primary_image_count(payload) == 0


def test_rbd_primary_image_count_missing_flag_is_zero():
    payload = json.dumps([{"Images": [{"name": "img1"}]}])
    assert rbd_primary_image_count(payload) == 0


def test_rbd_primary_image_count_garbage_is_zero():
    assert rbd_primary_image_count("not json at all") == 0


def test_rbd_primary_image_count_empty_string_is_zero():
    assert rbd_primary_image_count("") == 0


def test_rbd_primary_image_count_null_images_is_zero():
    assert rbd_primary_image_count(json.dumps([{"Images": None}])) == 0


# ---------------------------------------------------------------------------
# _rbd_mirror_health
# ---------------------------------------------------------------------------

def test_rbd_mirror_health_ok():
    text = (
        "health: OK\n"
        "daemon health: OK\n"
        "image health: OK\n"
    )
    assert rbd_mirror_health(text) == "OK"


def test_rbd_mirror_health_first_line_wins():
    text = "health: WARNING\nhealth: OK\n"
    assert rbd_mirror_health(text) == "WARNING"


def test_rbd_mirror_health_no_health_line_is_unknown():
    text = "daemon health: OK\nsome other line\n"
    assert rbd_mirror_health(text) == "UNKNOWN"


def test_rbd_mirror_health_empty_value_is_unknown():
    assert rbd_mirror_health("health: \n") == "UNKNOWN"


def test_rbd_mirror_health_empty_text_is_unknown():
    assert rbd_mirror_health("") == "UNKNOWN"


# ---------------------------------------------------------------------------
# _remote_list_has
# ---------------------------------------------------------------------------

def test_remote_list_has_matching_name():
    payload = json.dumps([{"name": "siteb", "local_name": "sitea"}])
    assert H._remote_list_has(payload, "name", "siteb") is True


def test_remote_list_has_matching_local_name():
    payload = json.dumps([{"name": "siteb", "local_name": "sitea"}])
    assert H._remote_list_has(payload, "local_name", "sitea") is True


def test_remote_list_has_no_match():
    payload = json.dumps([{"name": "siteb", "local_name": "sitea"}])
    assert H._remote_list_has(payload, "name", "sitec") is False


def test_remote_list_has_empty_list():
    assert H._remote_list_has("[]", "name", "siteb") is False


def test_remote_list_has_garbage_is_false():
    assert H._remote_list_has("not json", "name", "siteb") is False


def test_remote_list_has_null_is_false():
    assert H._remote_list_has("null", "name", "siteb") is False


def test_export_cluster_token_retries_a_transient_control_socket_timeout(monkeypatch):
    harness = H()
    export_token = getattr(harness, "export_cluster_token", None)
    assert export_token is not None, "cluster token export must retry transient control-socket failures"

    results = iter(
        [
            _Res(1, "", 'Error: failed to fetch cluster state: Get "http://control.socket/1.0/cluster": context deadline exceeded\n'),
            _Res(0, "token-for-sitea\n", ""),
        ]
    )
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append((container, argv, timeout, quiet))
        return next(results)

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    token = export_token("node-wrk2", "sitea", attempts=2, interval=0)

    assert token == "token-for-sitea"
    assert calls == [
        ("node-wrk2", ("microceph", "cluster", "export", "sitea"), 60, True),
        ("node-wrk2", ("microceph", "cluster", "export", "sitea"), 60, True),
    ]


def test_export_cluster_token_does_not_retry_a_permanent_failure(monkeypatch):
    harness = H()
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append((container, argv, timeout, quiet))
        return _Res(1, "", "Error: access denied\n")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    with pytest.raises(AssertionError) as exc:
        harness.export_cluster_token("node-wrk2", "sitea", attempts=2, interval=0)

    assert str(exc.value) == "failed to export cluster token for sitea on node-wrk2; last error: Error: access denied"
    assert calls == [
        ("node-wrk2", ("microceph", "cluster", "export", "sitea"), 60, True),
    ]


def test_prepare_snapd_in_vm_uses_snap_store_retry_for_install_and_refresh(monkeypatch):
    harness = H()
    results = iter([
        _Res(0, "snapd is already installed\n", ""),
        _Res(0, "snapd refreshed\n", ""),
    ])
    calls = []

    monkeypatch.setattr(harness, "_snapd_channel", lambda: "latest/edge")

    def fake_retry(command, timeout):
        calls.append((command, timeout))
        return next(results)

    monkeypatch.setattr(harness, "run_in_vm_with_snap_retry", fake_retry)

    harness.prepare_snapd_in_vm()

    assert calls == [
        ("sudo snap install snapd --channel=latest/edge", 600),
        ("sudo snap refresh snapd --channel=latest/edge", 600),
    ]


# ---------------------------------------------------------------------------
# run_streaming_process (streaming_process.py)
#
# Exercised with trivial local subprocesses -- no LXD needed. Covers the
# [rc, combined_output] return shape, xtrace prefixing, and the process-group
# timeout kill.
# ---------------------------------------------------------------------------

def test_run_streaming_process_echo_returns_rc_and_output():
    rc, out = run_streaming_process("echo hello-stream")
    assert rc == 0
    assert "hello-stream" in out


def test_run_streaming_process_nonzero_rc():
    rc, out = run_streaming_process("false")
    assert rc != 0


def test_run_streaming_process_xtrace_traces_script(tmp_path):
    # xtrace prepends `bash -x`, which traces the script body to stderr (merged into
    # stdout); the '+ echo ...' trace marker proves the prefix was applied.
    script = tmp_path / "traced.sh"
    script.write_text("echo traced-content\n")
    rc, out = run_streaming_process(str(script), xtrace=True)
    assert rc == 0
    assert "traced-content" in out
    assert "+ echo traced-content" in out


def test_run_streaming_process_timeout_kills_process_group():
    raised = False
    try:
        # The shell one-liner spawns a grandchild sleep; the process-group kill must
        # reap it so the call returns promptly instead of blocking on the open pipe.
        run_streaming_process("sleep 60", timeout=1)
    except RuntimeError as exc:
        raised = True
        assert "timed out" in str(exc)
    assert raised


def test_run_streaming_process_argv_list_runs_without_shell():
    # The argv-list form runs with shell=False; the components reach the program
    # verbatim with no shell word-splitting or metacharacter interpretation.
    rc, out = run_streaming_process(["printf", "%s", "hello-argv"])
    assert rc == 0
    assert "hello-argv" in out


# ---------------------------------------------------------------------------
# _is_member_not_found_error (microceph_harness.py)
#
# Pure replacement for the inline Robot Evaluate that decided whether a
# 'microceph cluster remove' failure means the member was already gone.
# ---------------------------------------------------------------------------

def test_is_member_not_found_error_matches_real_cli_text():
    stderr = 'Error: cluster member "node-wrk3" not found'
    assert H._is_member_not_found_error(stderr) is True


def test_is_member_not_found_error_does_not_match_context_canceled():
    assert H._is_member_not_found_error("Error: context canceled") is False


def test_is_member_not_found_error_does_not_match_context_deadline_exceeded():
    assert H._is_member_not_found_error("Error: context deadline exceeded") is False


def test_is_member_not_found_error_none_is_false():
    assert H._is_member_not_found_error(None) is False


def test_is_member_not_found_error_empty_is_false():
    assert H._is_member_not_found_error("") is False


# ---------------------------------------------------------------------------
# parse_migration_status (cluster_ops.py)
#
# Pure replacement for the `microceph status | grep -F -A 1 <node> | grep -qE
# '^  Services: ...$'` decision the service-migration test used. Two leading
# spaces and the exact service list are load-bearing -- the tests pin both.
# ---------------------------------------------------------------------------

_MIGRATED_STATUS = (
    "MicroCeph deployment summary:\n"
    "- node-wrk1 (10.0.0.11)\n"
    "  Services: osd\n"
    "  Disks: 1\n"
    "- node-wrk3 (10.0.0.13)\n"
    "  Services: mds, mgr, mon\n"
    "  Disks: 0\n"
)


def test_parse_migration_status_both_migrated():
    assert parse_migration_status(_MIGRATED_STATUS, "node-wrk1", "node-wrk3") == (True, True)


def test_parse_migration_status_neither_migrated():
    # Pre-migration: src still has everything, dst has only osd.
    text = (
        "- node-wrk1 (10.0.0.11)\n"
        "  Services: mds, mgr, mon, osd\n"
        "- node-wrk3 (10.0.0.13)\n"
        "  Services: osd\n"
    )
    assert parse_migration_status(text, "node-wrk1", "node-wrk3") == (False, False)


def test_parse_migration_status_partial():
    # src reduced to osd-only but dst not yet mds,mgr,mon -> (True, False).
    text = "- node-wrk1\n  Services: osd\n- node-wrk3\n  Services: osd\n"
    assert parse_migration_status(text, "node-wrk1", "node-wrk3") == (True, False)


def test_parse_migration_status_requires_exact_services():
    # An osd-only anchor must not match a line that lists extra services.
    text = "- node-wrk1\n  Services: mds, osd\n"
    src_ok, _ = parse_migration_status(text, "node-wrk1", "node-wrk9")
    assert src_ok is False


def test_parse_migration_status_requires_two_space_indent():
    # The original regex anchors exactly two leading spaces; other indents must not match.
    text = "- node-wrk1\n    Services: osd\n"  # four spaces
    src_ok, _ = parse_migration_status(text, "node-wrk1", "node-wrk9")
    assert src_ok is False


def test_parse_migration_status_absent_node_is_false():
    src_ok, dst_ok = parse_migration_status(_MIGRATED_STATUS, "node-wrk7", "node-wrk8")
    assert (src_ok, dst_ok) == (False, False)


# ---------------------------------------------------------------------------
# placement_status
# ---------------------------------------------------------------------------

_SYNC_PLACEMENT_RESPONSE = json.dumps({
    "type": "sync",
    "status": "Success",
    "status_code": 200,
    "metadata": {
        "active": True,
        "observed": None,
        "bootstrap_state": "not_bootstrapped",
    },
})

_ERROR_RESPONSE = json.dumps({
    "type": "error",
    "error_code": 400,
    "error": "keep-one invariant: refused to remove last mon on node-a",
})


def test_response_code_sync_success():
    assert placement_status.response_code(_SYNC_PLACEMENT_RESPONSE) == 200


def test_response_code_error_body():
    assert placement_status.response_code(_ERROR_RESPONSE) == 400


def test_response_code_garbage_is_zero():
    # Fail closed: comparisons against 200 must fail on unparseable bodies.
    assert placement_status.response_code("curl: (7) connection refused") == 0
    assert placement_status.response_code("") == 0
    assert placement_status.response_code(None) == 0


def test_response_code_non_dict_json_is_zero():
    assert placement_status.response_code("[1, 2, 3]") == 0


def test_bootstrap_state_from_metadata():
    assert placement_status.bootstrap_state(_SYNC_PLACEMENT_RESPONSE) == "not_bootstrapped"


def test_bootstrap_state_absent_is_empty():
    assert placement_status.bootstrap_state(_ERROR_RESPONSE) == ""
    assert placement_status.bootstrap_state("garbage") == ""


def test_placement_active_flag():
    assert placement_status.placement_active(_SYNC_PLACEMENT_RESPONSE) is True
    inactive = json.dumps({"status_code": 200, "metadata": {"active": False}})
    assert placement_status.placement_active(inactive) is False
    assert placement_status.placement_active("garbage") is False


def test_supported_capabilities_list():
    raw = json.dumps({
        "status_code": 200,
        "metadata": {"supported": ["deferred-ceph-bootstrap", "ceph-only-bootstrap"]},
    })
    assert placement_status.supported_capabilities(raw) == [
        "deferred-ceph-bootstrap",
        "ceph-only-bootstrap",
    ]


def test_supported_capabilities_malformed_is_empty():
    assert placement_status.supported_capabilities("garbage") == []
    non_list = json.dumps({"status_code": 200, "metadata": {"supported": "nope"}})
    assert placement_status.supported_capabilities(non_list) == []


def test_mon_count_prefers_monmap_num_mons():
    raw = json.dumps({"monmap": {"num_mons": 3}, "quorum_names": ["a", "b"]})
    assert placement_status.mon_count(raw) == 3


def test_mon_count_falls_back_to_quorum_names():
    raw = json.dumps({"quorum_names": ["a", "b"]})
    assert placement_status.mon_count(raw) == 2


def test_mon_count_garbage_is_zero():
    # Poll loops treat an unreachable/unready cluster as zero mons.
    assert placement_status.mon_count("Error connecting to cluster") == 0
    assert placement_status.mon_count("") == 0
    assert placement_status.mon_count("{}") == 0


def test_mon_quorum_names_prefers_explicit_names():
    raw = json.dumps(
        {
            "quorum_names": ["node-a", "node-b"],
            "quorum": [1],
            "monmap": {"mons": [{"rank": 1, "name": "wrong-fallback"}]},
        }
    )
    assert placement_status.mon_quorum_names(raw) == ["node-a", "node-b"]


def test_mon_quorum_names_maps_numeric_ranks_through_monmap():
    raw = json.dumps(
        {
            "quorum": [2, 0],
            "monmap": {
                "mons": [
                    {"rank": 0, "name": "node-a"},
                    {"rank": 1, "name": "node-b"},
                    {"rank": 2, "name": "node-c"},
                ]
            },
        }
    )
    assert placement_status.mon_quorum_names(raw) == ["node-c", "node-a"]


def test_mon_quorum_names_malformed_fails_closed():
    malformed = [
        "bad",
        "[]",
        "{}",
        json.dumps({"quorum": [0]}),
        json.dumps({"quorum": [0], "monmap": {"mons": []}}),
        json.dumps(
            {
                "quorum": [1],
                "monmap": {"mons": [{"rank": 0, "name": "node-a"}]},
            }
        ),
        json.dumps({"quorum_names": ["node-a", 1]}),
        json.dumps(
            {
                "quorum_names": None,
                "quorum": [0],
                "monmap": {"mons": [{"rank": 0, "name": "node-a"}]},
            }
        ),
    ]
    for raw in malformed:
        assert placement_status.mon_quorum_names(raw) == []


def test_control_service_presence_requires_each_explicit_service():
    mon = json.dumps({"quorum_names": ["node-a", "node-b"]})
    mgr = json.dumps([{"name": "node-a"}, {"name": "node-b"}])
    mds = json.dumps(
        {
            "fsmap": {
                "standbys": [{"name": "node-b", "state": "up:standby"}],
                "filesystems": [
                    {
                        "mdsmap": {
                            "info": {
                                "1": {"name": "node-a", "state": "up:active"}
                            }
                        }
                    }
                ],
            }
        }
    )

    assert placement_status.control_service_presence(mon, mgr, mds, "node-a") == {
        "mon": True,
        "mgr": True,
        "mds": True,
    }
    assert placement_status.control_service_presence(mon, mgr, mds, "node-c") == {
        "mon": False,
        "mgr": False,
        "mds": False,
    }


def test_control_service_presence_malformed_raises():
    # Unparseable output must not be reported as "service absent": an absence
    # assertion would otherwise pass on garbage rather than a genuine removal.
    import pytest as _pytest

    mon = json.dumps({"quorum_names": ["node-a"]})
    mgr = json.dumps([{"name": "node-a"}])
    mds = json.dumps({"fsmap": {"standbys": [], "filesystems": []}})

    for bad_mon, bad_mgr, bad_mds in (
        ("bad", mgr, mds),
        (mon, "bad", mds),
        (mon, mgr, "bad"),
        ("", "", ""),
    ):
        with _pytest.raises(ValueError):
            placement_status.control_service_presence(
                bad_mon, bad_mgr, bad_mds, "node-a"
            )


def test_member_in_ceph_status_substring():
    status = "  services:\n    mon: 2 daemons, quorum node-wrk0,node-wrk1\n"
    assert placement_status.member_in_ceph_status(status, "node-wrk1") is True
    assert placement_status.member_in_ceph_status(status, "node-wrk3") is False
    assert placement_status.member_in_ceph_status("", "node-wrk0") is False
    assert placement_status.member_in_ceph_status(None, "node-wrk0") is False


import pytest


# ---------------------------------------------------------------------------
# _poll_until failure semantics
# ---------------------------------------------------------------------------

def test_poll_until_callable_fail_msg_folds_in_last_value():
    seen = {"n": 0}

    def predicate():
        seen["n"] = 2
        return False

    with pytest.raises(AssertionError) as exc:
        H._poll_until(predicate, attempts=1, interval=0,
                      fail_msg=lambda: f"never reached 3 (last saw {seen['n']})")
    assert "last saw 2" in str(exc.value)


def test_poll_until_string_fail_msg_still_works():
    with pytest.raises(AssertionError) as exc:
        H._poll_until(lambda: False, attempts=1, interval=0, fail_msg="static message")
    assert str(exc.value) == "static message"


def test_poll_until_raising_on_fail_does_not_replace_fail_msg():
    def raising_on_fail():
        raise AssertionError("ceph -s cannot connect to cluster")

    with pytest.raises(AssertionError) as exc:
        H._poll_until(lambda: False, attempts=1, interval=0,
                      fail_msg="Never reached 3 OSD(s)", on_fail=raising_on_fail)
    assert str(exc.value) == "Never reached 3 OSD(s)"


def test_poll_until_success_never_evaluates_fail_msg_or_on_fail():
    calls = {"on_fail": 0}
    H._poll_until(lambda: True, attempts=3, interval=0,
                  fail_msg=lambda: 1 / 0, on_fail=lambda: calls.__setitem__("on_fail", 1))
    assert calls["on_fail"] == 0


# ---------------------------------------------------------------------------
# _echo_cmd / _log_exec -- command tracing routes to console (bash -x style)
# unless quiet: the command before it runs, its output after.
# ---------------------------------------------------------------------------

import microceph_harness as _mh
from collections import namedtuple as _nt

_Res = _nt("Res", ["rc", "stdout", "stderr"])


class _CapLogger:
    def __init__(self):
        self.console_lines = []
        self.info_lines = []

    def console(self, s):
        self.console_lines.append(s)

    def info(self, s):
        self.info_lines.append(s)

    def warn(self, s):
        self.info_lines.append("WARN:" + s)


def _with_logger(monkeypatch):
    cap = _CapLogger()
    monkeypatch.setattr(_mh, "logger", cap)
    return cap


def test_echo_cmd_prints_the_command(monkeypatch):
    cap = _with_logger(monkeypatch)
    H()._echo_cmd("microceph.ceph -s", quiet=False)
    assert cap.console_lines == ["+ microceph.ceph -s"]


def test_echo_cmd_quiet_prints_nothing(monkeypatch):
    cap = _with_logger(monkeypatch)
    H()._echo_cmd("microceph.ceph -s -f json", quiet=True)
    assert cap.console_lines == []


def test_log_exec_echoes_output_to_console(monkeypatch):
    cap = _with_logger(monkeypatch)
    H()._log_exec("microceph.ceph -s", _Res(0, "  cluster:\n    health: HEALTH_OK\n", ""), quiet=False)
    assert "health: HEALTH_OK" in "\n".join(cap.console_lines)


def test_log_exec_quiet_keeps_console_clean(monkeypatch):
    cap = _with_logger(monkeypatch)
    H()._log_exec("microceph.ceph -s -f json", _Res(0, '{"osdmap": {}}', ""), quiet=True)
    assert cap.console_lines == []
    # still captured in log.html (logger.info)
    assert any("microceph.ceph -s -f json" in s for s in cap.info_lines)


def test_log_exec_no_output_prints_nothing(monkeypatch):
    cap = _with_logger(monkeypatch)
    H()._log_exec("mkdir -p ~/x", _Res(0, "", ""), quiet=False)
    assert cap.console_lines == []


# ---------------------------------------------------------------------------
# _is_forkfile_socket_error / _infra_annotation_line
# ---------------------------------------------------------------------------

def test_forkfile_error_matches_connection_reset():
    stderr = "Error: forkfile2: .../forkfile.sock: read: connection reset by peer"
    assert H._is_forkfile_socket_error(stderr)


def test_forkfile_error_matches_missing_socket():
    stderr = "Error: dial unix /var/lib/lxd/.../forkfile.sock: connect: no such file or directory"
    assert H._is_forkfile_socket_error(stderr)


def test_forkfile_error_ignores_other_failures():
    assert not H._is_forkfile_socket_error("Error: Instance not found")
    assert not H._is_forkfile_socket_error("")


def test_infra_annotation_line_format():
    assert H._infra_annotation_line("lxd-socket", "boom") == "::error title=Infra::kind=lxd-socket boom"


# ---------------------------------------------------------------------------
# _push_with_forkfile_retry / _infra_annotate
# ---------------------------------------------------------------------------

def test_push_with_forkfile_retry_succeeds_after_two_forkfile_errors(monkeypatch):
    cap = _with_logger(monkeypatch)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    h = H()
    results = [
        _Res(1, "", "Error: forkfile2: .../forkfile.sock: read: connection reset by peer"),
        _Res(1, "", "Error: forkfile2: .../forkfile.sock: read: connection reset by peer"),
        _Res(0, "ok", ""),
    ]
    calls = []

    def fake_exec(argv, timeout):
        calls.append(argv)
        return results[len(calls) - 1]

    monkeypatch.setattr(h, "_exec", fake_exec)

    res = h._push_with_forkfile_retry(["lxc", "file", "push", "a", "b"], "push script to outer VM")

    assert res.rc == 0
    assert len(calls) == 3
    assert not any(line.startswith("::error title=Infra::kind=lxd-socket") for line in cap.console_lines)


def test_push_with_forkfile_retry_fails_at_once_on_other_error(monkeypatch):
    cap = _with_logger(monkeypatch)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    h = H()
    calls = []

    def fake_exec(argv, timeout):
        calls.append(argv)
        return _Res(1, "", "Error: Instance not found")

    monkeypatch.setattr(h, "_exec", fake_exec)

    with pytest.raises(AssertionError) as exc:
        h._push_with_forkfile_retry(["lxc", "file", "push", "a", "b"], "push script to outer VM")

    assert str(exc.value) == "Failed to push script to outer VM: Error: Instance not found"
    assert len(calls) == 1
    assert not any(line.startswith("::error title=Infra::kind=lxd-socket") for line in cap.console_lines)


def test_push_with_forkfile_retry_exhausts_and_annotates(monkeypatch):
    cap = _with_logger(monkeypatch)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    h = H()
    calls = []

    def fake_exec(argv, timeout):
        calls.append(argv)
        return _Res(1, "", "Error: forkfile2: .../forkfile.sock: read: connection reset by peer")

    monkeypatch.setattr(h, "_exec", fake_exec)

    with pytest.raises(AssertionError):
        h._push_with_forkfile_retry(["lxc", "file", "push", "a", "b"], "push script to outer VM")

    assert len(calls) == 3
    infra_lines = [
        line for line in cap.console_lines if line.startswith("::error title=Infra::kind=lxd-socket")
    ]
    assert len(infra_lines) == 1


def test_infra_annotate_appends_to_step_summary(monkeypatch, tmp_path):
    _with_logger(monkeypatch)
    summary = tmp_path / "summary.md"
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))

    H()._infra_annotate("lxd-socket", "boom")

    assert summary.read_text() == "kind=lxd-socket boom\n"


def test_infra_annotate_without_step_summary_writes_nothing(monkeypatch):
    _with_logger(monkeypatch)
    monkeypatch.delenv("GITHUB_STEP_SUMMARY", raising=False)

    H()._infra_annotate("lxd-socket", "boom")  # must not raise, must not touch a file


# ---------------------------------------------------------------------------
# in-instance preflight probe: pure helpers
# ---------------------------------------------------------------------------

def test_last_line_returns_last_non_blank_line():
    assert H._last_line("first\n  curl: (28) Connection timed out  \n\n") == "curl: (28) Connection timed out"
    assert H._last_line("") == ""
    assert H._last_line(None) == ""


def test_preflight_argv_curl_is_single_attempt_with_headers():
    argv = H._preflight_argv("https://api.snapcraft.io/v2/snaps/info/lxd", ("Snap-Device-Series: 16",))
    assert argv == [
        "curl", "-sSf", "-o", "/dev/null", "--connect-timeout", "5", "--max-time", "10",
        "-H", "Snap-Device-Series: 16", "https://api.snapcraft.io/v2/snaps/info/lxd",
    ]
    assert "--retry" not in argv


def test_preflight_endpoints_match_the_runner_side_gate():
    """The in-instance probe checks the URLs and header preflight.sh checks from the runner."""
    script = (Path(__file__).parents[2] / "scripts" / "preflight.sh").read_text()
    for _, url, headers in _mh.PREFLIGHT_ENDPOINTS:
        assert url in script
        for header in headers:
            assert header in script


def test_preflight_endpoint_parses_name_equals_url():
    assert H._preflight_endpoint("ceph-ppa=https://ppa.example/ubuntu/dists/?a=b") == (
        "ceph-ppa", "https://ppa.example/ubuntu/dists/?a=b", (),
    )


def test_preflight_endpoint_rejects_malformed_spec():
    for spec in ("https://no-name.example/", "=https://x.example/", "name="):
        with pytest.raises(ValueError):
            H._preflight_endpoint(spec)


def test_preflight_message_names_instance_and_every_endpoint():
    failed = [("snap-store", "https://s.example/", ("H: 1",)), ("ubuntu-archive", "http://a.example/", ())]
    assert H._preflight_message("outer VM vm1", failed) == (
        "PREFLIGHT: endpoint checks failed from outer VM vm1: "
        "snap-store (https://s.example/), ubuntu-archive (http://a.example/)"
    )


# ---------------------------------------------------------------------------
# probe_instance_network (stubbed exec)
# ---------------------------------------------------------------------------

def _probe_harness(monkeypatch, rc_for):
    """Harness whose _preflight_exec is stubbed: every probe call returns
    rc_for(url, nth_call_for_that_url)."""
    cap = _with_logger(monkeypatch)
    sleeps = []
    monkeypatch.setattr(_mh.time, "sleep", lambda secs: sleeps.append(secs))
    h = H()
    monkeypatch.setattr(h, "_outer_vm", lambda: "vm1")
    calls = []
    seen = {}

    def fake_exec(container, argv, timeout):
        calls.append((container, argv, timeout))
        url = argv[-1]
        seen[url] = seen.get(url, 0) + 1
        rc = rc_for(url, seen[url])
        return _Res(rc, "", "" if rc == 0 else "noise\ncurl: (28) Connection timed out after 5001 milliseconds\n")

    monkeypatch.setattr(h, "_preflight_exec", fake_exec)
    return h, cap, calls, sleeps


def _infra_lines(cap, kind):
    return [line for line in cap.console_lines if line.startswith(f"::error title=Infra::kind={kind} ")]


def test_probe_instance_network_happy_path_probes_each_endpoint_once(monkeypatch):
    h, cap, calls, sleeps = _probe_harness(monkeypatch, lambda url, n: 0)

    h.probe_instance_network()

    assert [c[1][0] for c in calls] == ["curl", "curl"]
    assert all(c[0] == "" for c in calls)
    assert sleeps == []
    assert _infra_lines(cap, "preflight") == []


def test_probe_instance_network_missing_curl_is_a_harness_error_not_preflight(monkeypatch):
    h, cap, calls, sleeps = _probe_harness(monkeypatch, lambda url, n: 127)

    with pytest.raises(AssertionError) as exc:
        h.probe_instance_network("microceph-img-builder")

    assert str(exc.value) == (
        "[preflight] curl not found in container microceph-img-builder in outer VM vm1; "
        "the reachability probe needs curl"
    )
    # Raised on the first probe: no retry, no backoff, no Infra annotation of any kind.
    assert len(calls) == 1
    assert sleeps == []
    assert not any(line.startswith("::error title=Infra::") for line in cap.console_lines)


def test_probe_instance_network_recovers_and_reprobes_only_the_failed_endpoint(monkeypatch):
    archive = "http://archive.ubuntu.com/ubuntu/dists/"
    h, cap, calls, sleeps = _probe_harness(monkeypatch, lambda url, n: 1 if (url == archive and n < 3) else 0)

    h.probe_instance_network()

    probed = [c[1][-1] for c in calls]
    assert probed.count(archive) == 3
    assert probed.count("https://api.snapcraft.io/v2/snaps/info/lxd") == 1
    assert sleeps == [2, 2]
    assert _infra_lines(cap, "preflight") == []


def test_probe_instance_network_outage_fails_with_preflight_annotation(monkeypatch, tmp_path):
    summary = tmp_path / "summary.md"
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))
    h, cap, calls, sleeps = _probe_harness(monkeypatch, lambda url, n: 28)

    with pytest.raises(AssertionError) as exc:
        h.probe_instance_network("microceph-img-builder")

    expected = (
        "PREFLIGHT: endpoint checks failed from container microceph-img-builder in outer VM vm1: "
        "snap-store (https://api.snapcraft.io/v2/snaps/info/lxd), "
        "ubuntu-archive (http://archive.ubuntu.com/ubuntu/dists/)"
    )
    assert str(exc.value) == expected
    assert len(calls) == 3 * 2
    # No backoff sleep after the last attempt.
    assert sleeps == [2, 2]
    assert _infra_lines(cap, "preflight") == [f"::error title=Infra::kind=preflight {expected}"]
    assert summary.read_text() == f"kind=preflight {expected}\n"
    assert any("(attempt 3/3): curl: (28) Connection timed out" in line for line in cap.console_lines)


def test_probe_instance_network_names_only_the_unreachable_endpoint(monkeypatch):
    archive = "http://archive.ubuntu.com/ubuntu/dists/"
    h, cap, _, _ = _probe_harness(monkeypatch, lambda url, n: 7 if url == archive else 0)

    with pytest.raises(AssertionError) as exc:
        h.probe_instance_network()

    assert str(exc.value) == f"PREFLIGHT: endpoint checks failed from outer VM vm1: ubuntu-archive ({archive})"
    assert len(_infra_lines(cap, "preflight")) == 1


def test_probe_instance_network_probes_extra_endpoints(monkeypatch):
    h, _, calls, _ = _probe_harness(monkeypatch, lambda url, n: 0)

    h.probe_instance_network("", "ceph-ppa=https://ppa.example/ubuntu/dists/")

    assert calls[-1][1][-1] == "https://ppa.example/ubuntu/dists/"


def test_preflight_exec_targets_the_vm_or_the_container(monkeypatch):
    _with_logger(monkeypatch)
    h = H()
    vm_calls, ct_calls = [], []
    monkeypatch.setattr(h, "run_in_vm", lambda cmd, timeout, quiet=False: vm_calls.append((cmd, timeout, quiet)))
    monkeypatch.setattr(
        h, "exec_in_container",
        lambda container, *argv, timeout, quiet: ct_calls.append((container, argv, timeout, quiet)),
    )

    h._preflight_exec("", ["curl", "-H", "Snap-Device-Series: 16", "http://x/"], 20)
    h._preflight_exec("node-wrk0", ["curl", "http://x/"], 20)

    assert vm_calls == [("curl -H 'Snap-Device-Series: 16' http://x/", 20, True)]
    assert ct_calls == [("node-wrk0", ("curl", "http://x/"), 20, True)]


# ---------------------------------------------------------------------------
# probe call sites: setup_lxd_in_vm / install_tools / build_base_lxd_image
# ---------------------------------------------------------------------------

def _recording_harness(monkeypatch, snap_list_count="0"):
    """Harness that records every probe/exec helper call in order instead of running it."""
    _with_logger(monkeypatch)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    h = H()
    monkeypatch.setattr(h, "_outer_vm", lambda: "vm1")
    monkeypatch.setattr(h, "_snapd_channel", lambda: "latest/edge")
    events = []

    def fake_run_in_vm(cmd, timeout=300, quiet=False):
        events.append(("vm", cmd, timeout))
        return _Res(0, snap_list_count + "\n" if "snap list" in cmd else "", "")

    def fake_run_in_container(container, cmd, timeout=300, shell="sh", quiet=False):
        events.append(("ct", container, cmd, timeout))
        return _Res(0, "", "")

    def fake_exec_in_container(container, *argv, timeout=300, check=False, quiet=False):
        events.append(("exec", container, argv))
        return _Res(0, "", "")

    monkeypatch.setattr(h, "probe_instance_network", lambda container="", *extra: events.append(("probe", container)))
    monkeypatch.setattr(h, "run_in_vm", fake_run_in_vm)
    monkeypatch.setattr(h, "run_in_vm_and_check", fake_run_in_vm)
    monkeypatch.setattr(h, "run_in_container_unchecked", fake_run_in_container)
    monkeypatch.setattr(h, "run_in_container_and_check", fake_run_in_container)
    monkeypatch.setattr(h, "exec_in_container", fake_exec_in_container)
    monkeypatch.setattr(
        h, "run_in_vm_with_snap_retry",
        lambda cmd, timeout=300: events.append(("vm-snap-retry", cmd, timeout)),
    )
    monkeypatch.setattr(
        h, "run_in_container_with_snap_retry",
        lambda container, cmd, timeout=300, shell="sh": (
            events.append(("ct-snap-retry", container, cmd, timeout)) or _Res(0, "", "")
        ),
    )
    # Stubbed below apt_update / apt_install, so call-site tests see the exact apt-get
    # string those methods build (flags included) and the target they aim it at.
    monkeypatch.setattr(
        h, "_run_apt",
        lambda container, cmd, timeout, label: events.append(("apt", container, cmd, timeout)),
    )
    return h, events


APT_FLAGS = "-o Acquire::Retries=3 -o Acquire::http::Timeout=30"


class _FakeBuiltIn:
    def get_variable_value(self, name, default=None):
        return default


def test_setup_lxd_in_vm_probes_then_installs_lxd_when_absent(monkeypatch):
    h, events = _recording_harness(monkeypatch, snap_list_count="0")
    monkeypatch.setattr(_mh, "BuiltIn", _FakeBuiltIn)

    h.setup_lxd_in_vm()

    assert events == [
        ("probe", ""),
        ("vm", 'sudo snap list | grep -cF "lxd" || true', 30),
        ("vm-snap-retry", "sudo snap install lxd", 300),
        ("vm-snap-retry", "sudo snap refresh", 300),
        ("vm", "sudo snap set lxd daemon.group=adm", 30),
        ("vm", "sudo lxd init --auto --storage-backend btrfs --storage-create-loop 25", 60),
    ]


def test_setup_lxd_in_vm_skips_the_install_when_lxd_is_present(monkeypatch):
    h, events = _recording_harness(monkeypatch, snap_list_count="1")
    monkeypatch.setattr(_mh, "BuiltIn", _FakeBuiltIn)

    h.setup_lxd_in_vm()

    assert ("vm-snap-retry", "sudo snap install lxd", 300) not in events
    assert ("vm-snap-retry", "sudo snap refresh", 300) in events
    assert events[0] == ("probe", "")


def test_setup_lxd_in_vm_is_no_longer_a_resource_keyword():
    """A resource keyword of the same name would shadow the Python method."""
    resource = (Path(__file__).parent / "microceph_harness.resource").read_text()
    assert "\nSetup LXD In VM\n" not in resource
    assert "    Setup LXD In VM\n" in resource  # Provision Multinode VM still calls it


def test_install_tools_probes_the_outer_vm_first(monkeypatch):
    h, events = _recording_harness(monkeypatch)

    h.install_tools()

    assert events == [
        ("probe", ""),
        ("apt", "", f"sudo apt-get {APT_FLAGS} update -qq", 120),
        ("apt", "", f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq", 300),
    ]


def test_build_base_lxd_image_probes_the_builder_before_apt(monkeypatch):
    h, events = _recording_harness(monkeypatch)

    h.build_base_lxd_image("/root")

    probe_at = events.index(("probe", "microceph-img-builder"))
    first_apt = next(i for i, e in enumerate(events) if e[0] == "apt")
    assert probe_at == first_apt - 1
    assert events[first_apt:first_apt + 2] == [
        ("apt", "microceph-img-builder", f"sudo apt-get {APT_FLAGS} update -qq", 120),
        ("apt", "microceph-img-builder", f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq", 300),
    ]
    assert [e for e in events if e[0] == "probe"] == [("probe", "microceph-img-builder")]
    assert ("vm", "lxc publish microceph-img-builder --alias ubuntu-22.04-microceph --compression none", 300) in events


# ---------------------------------------------------------------------------
# _is_transient_snap_store_error -- verbatim CI error texts (#838)
# ---------------------------------------------------------------------------

_SNAP_408_NONCE = (
    "error: cannot perform the following tasks:\n"
    '- Fetch and check assertions for snap "snapd" (27710) '
    "(cannot get nonce from store: store server returned status 408)\n"
)
_SNAP_408_ASSERTION = (
    "error: cannot perform the following tasks:\n"
    '- Fetch and check assertions for snap "snapd" (27738) (cannot fetch assertion: got unexpected '
    'HTTP status code 408 via GET to "https://api.snapcraft.io/v2/assertions/snap-revision/3Dvx?max-format=0")\n'
)
_SNAP_CLIENT_TIMEOUT = (
    "error: cannot perform the following tasks:\n"
    '- Ensure prerequisites for "microceph" are available (cannot install snap base "core24": '
    'cannot get nonce from store: Post "https://api.snapcraft.io/api/v1/snaps/auth/nonces": net/http: '
    "request canceled while waiting for connection (Client.Timeout exceeded while awaiting headers))\n"
)


@pytest.mark.parametrize("stderr", [
    _SNAP_408_NONCE,
    _SNAP_408_ASSERTION,
    _SNAP_CLIENT_TIMEOUT,
    "(cannot get nonce from store: store server returned status 503)",
    "(cannot fetch assertion: got unexpected HTTP status code 502 via GET to ...)",
])
def test_transient_snap_store_error_matches(stderr):
    assert H._is_transient_snap_store_error(stderr)


@pytest.mark.parametrize("stderr", [
    'error: snap "bogus" not found',
    "error: unable to contact snap store",
    "error: too early for operation, device not yet seeded or device model not acknowledged",
    "(cannot get nonce from store: store server returned status 401)",
    "(cannot fetch assertion: got unexpected HTTP status code 404 via GET to ...)",
    'error: cannot install "microceph": snap has no updates available',
    "",
    None,
])
def test_transient_snap_store_error_ignores_other_failures(stderr):
    assert not H._is_transient_snap_store_error(stderr)


# ---------------------------------------------------------------------------
# _retry_transient / run_in_*_with_snap_retry (stubbed exec)
# ---------------------------------------------------------------------------

def _retry_harness(monkeypatch, results):
    """Harness whose _exec returns *results* in order (the last one repeats)."""
    cap = _with_logger(monkeypatch)
    sleeps = []
    monkeypatch.setattr(_mh.time, "sleep", lambda secs: sleeps.append(secs))
    h = H()
    monkeypatch.setattr(h, "_outer_vm", lambda: "vm1")
    calls = []

    def fake_exec(argv, timeout):
        calls.append((argv, timeout))
        return results[min(len(calls), len(results)) - 1]

    monkeypatch.setattr(h, "_exec", fake_exec)
    return h, cap, calls, sleeps


def test_retry_transient_recovers_after_two_transient_failures(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(1, "", "blip"), _Res(1, "", "blip"), _Res(0, "ok", "")])

    res = h._retry_transient(
        lambda: h._exec(["x"], 1), lambda r: r.stderr == "blip", 3, 7, "snap-store", "install x"
    )

    assert res == _Res(0, "ok", "")
    assert len(calls) == 3
    assert sleeps == [7, 7]
    assert _infra_lines(cap, "snap-store") == []
    assert sum("retrying in 7s" in line for line in cap.console_lines) == 2


def test_retry_transient_fails_at_once_on_other_error(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(1, "out", 'error: snap "bogus" not found')])

    with pytest.raises(AssertionError) as exc:
        h._retry_transient(lambda: h._exec(["x"], 1), lambda r: False, 3, 7, "snap-store", "install x")

    assert str(exc.value) == 'Command failed (rc=1):\nSTDERR: error: snap "bogus" not found\nSTDOUT: out'
    assert len(calls) == 1
    assert sleeps == []
    assert _infra_lines(cap, "snap-store") == []


def test_retry_transient_exhausts_annotates_once_and_fails(monkeypatch, tmp_path):
    summary = tmp_path / "summary.md"
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(1, "", "first line\nblip\n")])

    with pytest.raises(AssertionError) as exc:
        h._retry_transient(lambda: h._exec(["x"], 1), lambda r: True, 3, 7, "snap-store", "install x")

    assert str(exc.value).startswith("Command failed (rc=1):")
    assert len(calls) == 3
    # No backoff sleep after the last attempt.
    assert sleeps == [7, 7]
    assert _infra_lines(cap, "snap-store") == [
        "::error title=Infra::kind=snap-store install x failed after 3 attempts: blip"
    ]
    assert summary.read_text() == "kind=snap-store install x failed after 3 attempts: blip\n"


def test_retry_transient_annotation_says_so_when_stderr_is_empty(monkeypatch):
    h, cap, _, _ = _retry_harness(monkeypatch, [_Res(124, "", "")])

    with pytest.raises(AssertionError):
        h._retry_transient(lambda: h._exec(["x"], 1), lambda r: True, 2, 0, "apt", "update")

    assert _infra_lines(cap, "apt") == [
        "::error title=Infra::kind=apt update failed after 2 attempts: rc=124 with no error output"
    ]


def test_run_in_vm_with_snap_retry_recovers_from_a_408(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(1, "", _SNAP_408_NONCE), _Res(0, "lxd installed", "")])

    res = h.run_in_vm_with_snap_retry("sudo snap install lxd", 300)

    assert res.rc == 0
    assert [c[0] for c in calls] == [
        ["lxc", "exec", "-n", "vm1", "--", "bash", "-eo", "pipefail", "-c", "sudo snap install lxd"]
    ] * 2
    assert [c[1] for c in calls] == [300, 300]
    assert sleeps == [5]
    assert _infra_lines(cap, "snap-store") == []


def test_run_in_vm_with_snap_retry_exhaustion_is_a_snap_store_infra_failure(monkeypatch):
    h, cap, calls, _ = _retry_harness(monkeypatch, [_Res(1, "", _SNAP_408_ASSERTION)])

    with pytest.raises(AssertionError):
        h.run_in_vm_with_snap_retry("sudo snap install lxd", 300)

    assert len(calls) == 3
    lines = _infra_lines(cap, "snap-store")
    assert len(lines) == 1
    assert "'sudo snap install lxd' in outer VM vm1 failed after 3 attempts" in lines[0]
    assert "got unexpected HTTP status code 408" in lines[0]
    assert "\n" not in lines[0]


def test_run_in_vm_with_snap_retry_does_not_retry_a_timeout_or_a_missing_snap(monkeypatch):
    for res in (_Res(124, "", "\nCommand timed out after 300s"), _Res(1, "", 'error: snap "lxd" not found')):
        h, cap, calls, sleeps = _retry_harness(monkeypatch, [res])
        with pytest.raises(AssertionError):
            h.run_in_vm_with_snap_retry("sudo snap install lxd", 300)
        assert len(calls) == 1
        assert sleeps == []
        assert _infra_lines(cap, "snap-store") == []


def test_run_in_container_with_snap_retry_uses_the_non_raising_sh_helper(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(1, "", _SNAP_CLIENT_TIMEOUT), _Res(0, "", "")])

    res = h.run_in_container_with_snap_retry("node-wrk1", "sudo snap install microceph --channel squid/stable", 600)

    assert res.rc == 0
    assert calls[0] == (
        ["lxc", "exec", "-n", "vm1", "--", "lxc", "exec", "-n", "node-wrk1", "--",
         "sh", "-c", "sudo snap install microceph --channel squid/stable"],
        600,
    )
    assert len(calls) == 2
    assert sleeps == [5]


# ---------------------------------------------------------------------------
# apt-get stall retry (#842)
# ---------------------------------------------------------------------------

def test_apt_bounded_cmd_kills_the_command_inside_the_instance():
    assert H._apt_bounded_cmd("sudo apt-get update -qq && echo 'done'", 120) == (
        "timeout --kill-after=10 120 sh -c 'sudo apt-get update -qq && echo '\"'\"'done'\"'\"''"
    )


@pytest.mark.parametrize("res, transient", [
    (_Res(124, "", ""), True),
    (_Res(124, "", "\nCommand timed out after 135s"), True),
    (_Res(124, "\n", ""), True),
    # dpkg had started unpacking: re-running is not known to be safe.
    (_Res(124, "Selecting previously unselected package s3cmd.\n", ""), False),
    (_Res(100, "", "E: Unable to locate package s3cmd"), False),
    (_Res(100, "", "E: Failed to fetch http://security.ubuntu.com/... 404  Not Found"), False),
    (_Res(137, "", ""), False),
    (_Res(0, "", ""), False),
])
def test_is_transient_apt_stall(res, transient):
    assert H._is_transient_apt_stall(res) is transient


def test_apt_label_timeout_only_touches_a_silent_rc_124():
    assert H._apt_label_timeout(_Res(124, "", ""), 120) == _Res(
        124, "", "Command timed out after 120s (killed inside the instance)"
    )
    harness_timeout = _Res(124, "", "\nCommand timed out after 135s")
    assert H._apt_label_timeout(harness_timeout, 120) == harness_timeout
    apt_error = _Res(100, "", "")
    assert H._apt_label_timeout(apt_error, 120) == apt_error


def test_apt_cmd_spells_the_retry_flags_exactly_once():
    assert H._apt_cmd("update -qq") == f"sudo apt-get {APT_FLAGS} update -qq"
    assert H._apt_cmd("-qq -y install", ["s3cmd", "jq"]) == f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq"
    assert H._apt_cmd("-qq -y install", ("s3cmd",)) == f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd"
    # A Robot call passes the package list as one string.
    assert H._apt_cmd("-qq -y install", "s3cmd jq") == f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq"
    assert H._apt_cmd("-qq -y install", ["s3cmd", "jq"]).count(APT_FLAGS) == 1
    assert H._apt_cmd("-qq -y install", ["s3cmd", "jq"]).count("apt-get") == 1


def _apt_target_harness(monkeypatch):
    """Harness whose _run_apt only records what apt_update / apt_install hand it."""
    h = H()
    calls = []
    monkeypatch.setattr(
        h, "_run_apt", lambda container, cmd, timeout, label: calls.append((container, cmd, timeout, label))
    )
    return h, calls


def test_apt_update_targets_the_outer_vm_by_default_and_a_container_when_named(monkeypatch):
    h, calls = _apt_target_harness(monkeypatch)

    h.apt_update()
    h.apt_update("node-wrk0")
    h.apt_update("node-wrk0", 60)

    assert calls == [
        ("", f"sudo apt-get {APT_FLAGS} update -qq", 120, "apt-get update"),
        ("node-wrk0", f"sudo apt-get {APT_FLAGS} update -qq", 120, "apt-get update"),
        ("node-wrk0", f"sudo apt-get {APT_FLAGS} update -qq", 60, "apt-get update"),
    ]


def test_apt_install_adds_the_flags_and_joins_the_package_list(monkeypatch):
    h, calls = _apt_target_harness(monkeypatch)

    h.apt_install(["s3cmd", "jq"])
    h.apt_install(("s3cmd",), "node-wrk0")
    h.apt_install("s3cmd jq", "microceph-img-builder", 90)

    assert calls == [
        ("", f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq", 300, "apt-get install s3cmd jq"),
        ("node-wrk0", f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd", 300, "apt-get install s3cmd"),
        ("microceph-img-builder", f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq", 90, "apt-get install s3cmd jq"),
    ]


def test_apt_install_rejects_an_empty_package_list(monkeypatch):
    h, calls = _apt_target_harness(monkeypatch)

    with pytest.raises(ValueError):
        h.apt_install([])
    with pytest.raises(ValueError):
        h.apt_install("")

    assert calls == []


def test_apt_update_in_the_vm_second_attempt_succeeds(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(124, "", ""), _Res(0, "", "")])

    res = h.apt_update()

    assert res.rc == 0
    bounded = f"timeout --kill-after=10 120 sh -c 'sudo apt-get {APT_FLAGS} update -qq'"
    assert [c[0][-1] for c in calls] == [bounded, bounded]
    assert calls[0][0][:4] == ["lxc", "exec", "-n", "vm1"]
    assert "microceph-img-builder" not in calls[0][0]
    # The harness timeout is only the backstop behind the in-instance one.
    assert [c[1] for c in calls] == [135, 135]
    assert sleeps == [10]
    assert _infra_lines(cap, "apt") == []


def test_apt_update_exhaustion_is_an_apt_infra_failure(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(124, "", "")])

    with pytest.raises(AssertionError) as exc:
        h.apt_update()

    assert str(exc.value) == (
        "Command failed (rc=124):\nSTDERR: Command timed out after 120s (killed inside the instance)\nSTDOUT: "
    )
    assert len(calls) == 2
    assert sleeps == [10]
    assert _infra_lines(cap, "apt") == [
        "::error title=Infra::kind=apt 'apt-get update' in outer VM vm1 "
        "failed after 2 attempts: Command timed out after 120s (killed inside the instance)"
    ]


def test_apt_install_real_apt_error_is_fatal_at_once(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(100, "", "E: Unable to locate package s3cmd")])

    with pytest.raises(AssertionError) as exc:
        h.apt_install(["s3cmd", "jq"])

    assert str(exc.value) == "Command failed (rc=100):\nSTDERR: E: Unable to locate package s3cmd\nSTDOUT: "
    assert calls[0][0][-1] == f"timeout --kill-after=10 300 sh -c 'sudo apt-get {APT_FLAGS} -qq -y install s3cmd jq'"
    assert len(calls) == 1
    assert sleeps == []
    assert _infra_lines(cap, "apt") == []


def test_apt_install_in_a_container_bounds_and_retries_in_the_container(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(124, "", ""), _Res(0, "", "")])

    h.apt_install(["jq"], "microceph-img-builder", 300)

    assert calls[0] == (
        ["lxc", "exec", "-n", "vm1", "--", "lxc", "exec", "-n", "microceph-img-builder", "--", "sh", "-c",
         f"timeout --kill-after=10 300 sh -c 'sudo apt-get {APT_FLAGS} -qq -y install jq'"],
        315,
    )
    assert len(calls) == 2
    assert sleeps == [10]
    assert _infra_lines(cap, "apt") == []


def test_apt_install_exhaustion_in_a_container_names_the_container(monkeypatch):
    h, cap, calls, sleeps = _retry_harness(monkeypatch, [_Res(124, "", "")])

    with pytest.raises(AssertionError):
        h.apt_install(["s3cmd"], "node-wrk0", 120)

    assert _infra_lines(cap, "apt") == [
        "::error title=Infra::kind=apt 'apt-get install s3cmd' in container node-wrk0 "
        "failed after 2 attempts: Command timed out after 120s (killed inside the instance)"
    ]


def test_no_public_apt_wrapper_takes_a_hand_built_command():
    assert not hasattr(H, "run_in_vm_with_apt_retry")
    assert not hasattr(H, "run_in_container_with_apt_retry")
    source = (Path(__file__).parent / "microceph_harness.py").read_text()
    # The flags are spelled in the constant and used in _apt_cmd, nowhere else.
    assert source.count("APT_RETRY_FLAGS") == 3


# ---------------------------------------------------------------------------
# snap retry call sites
# ---------------------------------------------------------------------------

def test_build_base_lxd_image_retries_the_builder_snap_install(monkeypatch):
    h, events = _recording_harness(monkeypatch)

    h.build_base_lxd_image("/root")

    assert ("ct-snap-retry", "microceph-img-builder", "snap install --dangerous /mnt/microceph_*.snap", 600) in events


def test_store_install_is_split_so_only_the_snap_install_is_retried(monkeypatch):
    h, events = _recording_harness(monkeypatch)
    monkeypatch.setattr(h, "ensure_snap_mount_healthy", lambda container: events.append(("mount", container)))

    h.install_microceph_from_store_on_all_nodes("squid/stable")

    per_node = [e for e in events if e[1] == "node-wrk0"]
    assert per_node == [
        ("mount", "node-wrk0"),
        ("ct", "node-wrk0", "sudo snap remove --purge microceph >/dev/null 2>&1 || true", 60),
        ("apt", "node-wrk0", f"sudo apt-get {APT_FLAGS} update -qq", 120),
        ("apt", "node-wrk0", f"sudo apt-get {APT_FLAGS} -qq -y install s3cmd", 300),
        ("ct-snap-retry", "node-wrk0", "sudo snap install microceph --channel squid/stable", 600),
    ]
    assert len(events) == 4 * len(per_node)


# ---------------------------------------------------------------------------
# join_worker_nodes_to_cluster
# ---------------------------------------------------------------------------

def test_join_worker_nodes_to_cluster_honours_worker_count(monkeypatch):
    _with_logger(monkeypatch)
    harness = H()
    added = []
    joined = []
    waited = []

    monkeypatch.setattr(harness, "_network_cidr", lambda _mode: "10.0.0./24")

    def fake_exec(container, *argv, timeout):
        added.append((container, argv))
        return _Res(0, f"token-{argv[-1]}", "")

    def fake_run(container, cmd, timeout):
        joined.append((container, cmd))
        return _Res(0, "", "")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(harness, "run_in_container", fake_run)
    monkeypatch.setattr(
        harness,
        "run_in_container_unchecked",
        lambda *_args, **_kwargs: _Res(0, "1\n", ""),
    )
    monkeypatch.setattr(
        harness, "wait_for_n_nodes_in_cluster", lambda count: waited.append(count)
    )

    harness.join_worker_nodes_to_cluster("public", worker_count="2")

    assert [call[1][-1] for call in added] == ["node-wrk1", "node-wrk2"]
    join_calls = [call for call in joined if "microceph cluster join" in call[1]]
    assert [call[0] for call in join_calls] == ["node-wrk1", "node-wrk2"]
    assert waited == [3]


# ---------------------------------------------------------------------------
# wait_for_legacy_cephx_compatibility
# ---------------------------------------------------------------------------

def test_wait_for_legacy_cephx_compatibility_checks_health_detail_json(monkeypatch):
    cap = _with_logger(monkeypatch)
    harness = H()
    calls = []
    health = json.dumps(
        {
            "status": "HEALTH_WARN",
            "checks": {"AUTH_INSECURE_CLIENT_KEY_TYPE": {"severity": "HEALTH_WARN"}},
        }
    )

    def fake_exec(container, *argv, timeout, quiet):
        calls.append((container, argv, timeout, quiet))
        return _Res(0, health, "")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    harness.wait_for_legacy_cephx_compatibility(tries=1, interval=0)

    assert calls == [
        ("node-wrk0", ("microceph.ceph", "health", "detail", "-f", "json"), 30, True)
    ]
    assert "[health] Legacy CephX checks are the only remaining health checks" in cap.console_lines


# ---------------------------------------------------------------------------
# wait_for_smb_service (single-node VM vs multinode container polling)
# ---------------------------------------------------------------------------

@pytest.mark.parametrize(("keyword", "service", "node"), [
    ("wait_for_smb_service", "smbd", None),
    ("wait_for_smb_service", "smbd", "node-wrk1"),
    ("wait_for_ctdb_service", "ctdbd", "node-wrk1"),
    ("wait_for_ctdb_nodes_service", "ctdb-nodes", "node-wrk1"),
])
def test_smb_service_wait_routes_to_vm_or_container(monkeypatch, keyword, service, node):
    _with_logger(monkeypatch)
    harness, calls = H(), []

    def capture(*args, **kwargs):
        calls.append((args, kwargs))
        return _Res(0, f"Service Startup Current Notes\nmicroceph.{service} enabled active -\n", "")

    helper = "exec_in_container" if node else "run_in_vm"
    monkeypatch.setattr(harness, helper, capture)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    options = {"node": node} if node else {}
    getattr(harness, keyword)(tries=1, interval=0, **options)
    expected = ((node, "snap", "services", f"microceph.{service}"), {"timeout": 15, "quiet": True}) if node else (
        (f"snap services microceph.{service}", 15), {"quiet": True})
    assert calls == [expected]


def test_run_in_vm_and_check_eventually_retries_transient_failure(monkeypatch):
    _with_logger(monkeypatch)
    harness = H()
    results = iter([_Res(1, "tree connect failed", ""), _Res(0, "success", "")])
    calls = []

    def fake_run(command, timeout, quiet):
        calls.append((command, timeout, quiet))
        return next(results)

    monkeypatch.setattr(harness, "run_in_vm", fake_run)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    result = harness.run_in_vm_and_check_eventually("smbclient ...", 3, 0, 30)

    assert result.stdout == "success"
    assert calls == [("smbclient ...", 30, True)] * 2


def test_wait_for_ctdb_healthy_nodes_accepts_degraded_cluster(monkeypatch):
    _with_logger(monkeypatch)
    harness = H()
    outputs = iter(
        [
            _Res(0, "Number of nodes:2\npnn:0 10.0.0.1 OK\npnn:1 10.0.0.2 OK\n", ""),
            _Res(0, "Number of nodes:2\npnn:0 10.0.0.1 DISCONNECTED\npnn:1 10.0.0.2 OK\n", ""),
        ]
    )
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append((container, argv, timeout, quiet))
        return next(outputs)

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    harness.wait_for_ctdb_healthy_nodes(2, 1, tries=2, interval=0, node="node-wrk2")

    assert calls == [
        ("node-wrk2", ("microceph.ctdb", "status"), 15, True),
        ("node-wrk2", ("microceph.ctdb", "status"), 15, True),
    ]


# ---------------------------------------------------------------------------
# wait_for_member_control_services (polling absence/presence with convergence)
# ---------------------------------------------------------------------------

def _harness_with_observations(monkeypatch, observations):
    """Return a harness whose _observe_control_services yields *observations*
    in order (the last value repeats once exhausted), counting calls."""
    h = H()
    seq = list(observations)
    state = {"calls": 0}

    def fake_observe(member):
        idx = min(state["calls"], len(seq) - 1)
        state["calls"] += 1
        return seq[idx]

    monkeypatch.setattr(h, "_observe_control_services", fake_observe)
    # Avoid real sleeps between probes.
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    return h, state


def test_wait_for_control_services_absent_after_convergence(monkeypatch):
    # mgr lingers for the first two probes, then converges to fully absent.
    observations = [
        {"mon": False, "mgr": True, "mds": False},
        {"mon": False, "mgr": True, "mds": False},
        {"mon": False, "mgr": False, "mds": False},
    ]
    h, state = _harness_with_observations(monkeypatch, observations)
    h.wait_for_member_control_services("node-wrk1", "no", tries=10, interval=0)
    assert state["calls"] == 3


def test_wait_for_control_services_present_immediately(monkeypatch):
    observations = [{"mon": True, "mgr": True, "mds": True}]
    h, state = _harness_with_observations(monkeypatch, observations)
    h.wait_for_member_control_services("node-wrk0", "yes", tries=5, interval=0)
    assert state["calls"] == 1


def test_wait_for_control_services_absent_timeout_folds_last_observed(monkeypatch):
    observations = [{"mon": False, "mgr": True, "mds": False}]
    h, _ = _harness_with_observations(monkeypatch, observations)
    with pytest.raises(AssertionError) as exc:
        h.wait_for_member_control_services("node-wrk1", "no", tries=3, interval=0)
    msg = str(exc.value)
    assert "never became absent" in msg
    assert "'mgr': True" in msg


def test_wait_for_control_services_absent_ignores_unparseable_then_converges(monkeypatch):
    # Bad/empty Ceph output (ValueError) must NOT be treated as absence: the
    # poll keeps going until a genuine all-absent reading arrives.
    h = H()
    seq = [
        ValueError("unparseable mds stat output: ''"),
        {"mon": False, "mgr": True, "mds": False},
        {"mon": False, "mgr": False, "mds": False},
    ]
    state = {"calls": 0}

    def fake_observe(member):
        idx = min(state["calls"], len(seq) - 1)
        state["calls"] += 1
        result = seq[idx]
        if isinstance(result, Exception):
            raise result
        return result

    monkeypatch.setattr(h, "_observe_control_services", fake_observe)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    h.wait_for_member_control_services("node-wrk1", "no", tries=10, interval=0)
    assert state["calls"] == 3


def test_wait_for_control_services_absent_timeout_on_persistent_bad_output(monkeypatch):
    # Unparseable output that never recovers must time out (fail), never pass.
    h = H()

    def fake_observe(member):
        raise ValueError("unparseable mds stat output: ''")

    monkeypatch.setattr(h, "_observe_control_services", fake_observe)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    with pytest.raises(AssertionError) as exc:
        h.wait_for_member_control_services("node-wrk1", "no", tries=3, interval=0)
    msg = str(exc.value)
    assert "never became absent" in msg
    assert "unparseable" in msg


# ---------------------------------------------------------------------------
# Single-system suite state sequencing
# ---------------------------------------------------------------------------

def test_mgr_remote_call_reuses_the_waitready_cluster():
    """The shared-VM mgr test must not bootstrap MicroCluster a second time."""
    suite_path = Path(__file__).parents[1] / "single-system-tests" / "single_system_tests.robot"
    suite = suite_path.read_text()
    mgr_test = suite.split("Test Mgr Remote Module Call", maxsplit=1)[1].split(
        "Add OSD With Failure", maxsplit=1
    )[0]

    assert "Install MicroCeph From Local Snap" not in mgr_test
    assert "Bootstrap MicroCeph Cluster" not in mgr_test


def test_public_network_cidr_is_canonical(monkeypatch):
    h = H()
    monkeypatch.setattr(h, "_network_cidr", lambda name: "10.107.88.1/24")
    assert h.get_public_network_cidr() == "10.107.88.0/24"


def test_add_apt_repository_uses_bounded_apt_retry(monkeypatch):
    h, events = _recording_harness(monkeypatch)
    h.add_apt_repository("ppa:lmlogiudice/ceph-lp2166817-updates")
    assert events == [("apt", "", "sudo add-apt-repository --yes --no-update ppa:lmlogiudice/ceph-lp2166817-updates", 120)]


def test_resolute_ceph_client_setup_is_shared():
    """The two Resolute-client suites use one common setup implementation."""
    robot_root = Path(__file__).parents[1]
    resource = (robot_root / "resources" / "microceph_harness.resource").read_text()

    assert "${CEPH_PPA}" in resource
    assert "lmlogiudice/ceph-lp2166817-updates" in resource
    assert "Verify Resolute Outer VM" in resource
    assert "Install Ceph Client From PPA" in resource
    assert "Add Apt Repository    ppa:${CEPH_PPA}" in resource
    assert "Apt Update" in resource
    assert "Apt Install    ceph-common" in resource
    assert "Should Contain    ${policy.stdout}    ${CEPH_PPA}" in resource

    suites = (
        robot_root / "cephfs-replication-test" / "cephfs_replication_tests.robot",
        robot_root / "nfs-test" / "nfs_tests.robot",
    )
    for suite_path in suites:
        suite = suite_path.read_text()
        assert "${OUTER_VM_IMAGE}    ubuntu:26.04" in suite
        assert "Verify Resolute Outer VM" in suite
        assert "Install Ceph Client From PPA" in suite
        assert "Verify Resolute Outer VM\n    [Documentation]" not in suite
        assert "Install Ceph Client From PPA\n    [Documentation]" not in suite
        assert "${CEPH_PPA}" not in suite


def _fake_snapd_result(stdout=""):
    class _Result:
        rc = 0
        stderr = ""

        def __init__(self, stdout):
            self.stdout = stdout

    return _Result(stdout)


def test_local_snap_install_refreshes_a_preinstalled_snapd(monkeypatch):
    """An already-installed snapd is refreshed onto the configured channel."""
    _with_logger(monkeypatch)
    harness = H()
    commands = []

    def fake_run_in_vm_and_check(command, timeout):
        commands.append((command, timeout))
        return _fake_snapd_result("ok")

    retried = []

    def fake_retry(command, timeout=300):
        retried.append((command, timeout))
        if "snap install snapd" in command:
            return _fake_snapd_result(
                'snap "snapd" is already installed, see \'snap refresh --help\'.'
            )
        return _fake_snapd_result("ok")

    monkeypatch.setattr(harness, "run_in_vm_and_check", fake_run_in_vm_and_check)
    monkeypatch.setattr(harness, "run_in_vm_with_snap_retry", fake_retry)
    monkeypatch.setattr(harness, "_snapd_channel", lambda: "latest/edge")

    harness.install_microceph_from_local_snap("/tmp/microceph.snap")

    assert commands[0] == ("sudo snap install core26 || true", 120)
    # Snapd preparation and the local install both retry transient store errors.
    assert retried == [
        ("sudo snap install snapd --channel=latest/edge", 600),
        ("sudo snap refresh snapd --channel=latest/edge", 600),
        ("sudo snap install --dangerous ~/microceph_*.snap", 600),
    ]


def test_local_snap_install_skips_refresh_for_a_fresh_snapd(monkeypatch):
    """A fresh snapd install does not trigger a redundant refresh."""
    _with_logger(monkeypatch)
    harness = H()
    commands = []

    def fake_run_in_vm_and_check(command, timeout):
        commands.append((command, timeout))
        return _fake_snapd_result("ok")

    retried = []

    def fake_retry(command, timeout=300):
        retried.append((command, timeout))
        return _fake_snapd_result("snapd (edge) 2.78 installed")

    monkeypatch.setattr(harness, "run_in_vm_and_check", fake_run_in_vm_and_check)
    monkeypatch.setattr(harness, "_snapd_channel", lambda: "latest/edge")
    monkeypatch.setattr(harness, "run_in_vm_with_snap_retry", fake_retry)

    harness.install_microceph_from_local_snap("/tmp/microceph.snap")

    assert commands[0] == ("sudo snap install core26 || true", 120)
    assert retried == [
        ("sudo snap install snapd --channel=latest/edge", 600),
        ("sudo snap install --dangerous ~/microceph_*.snap", 600),
    ]


def test_snapd_channel_flows_from_the_cli_wrapper_into_suites_and_scripts():
    """The channel is chosen once by the wrapper and inherited everywhere else."""
    robot_root = Path(__file__).parents[1]
    resource = (robot_root / "resources" / "microceph_harness.resource").read_text()
    dsl_suite = (robot_root / "dsl-functional-tests" / "dsl_functional_tests.robot").read_text()
    adopt_suite = (robot_root / "cephadm-adopt-test" / "cephadm_adopt_tests.robot").read_text()
    api_suite = (robot_root / "api-tests" / "api_tests.robot").read_text()

    assert "${SNAPD_CHANNEL}      latest/stable" in resource
    for suite in (dsl_suite, adopt_suite, api_suite):
        assert "Set Environment Variable    SNAPD_CHANNEL" not in suite
        assert "env SNAPD_CHANNEL=" not in suite


def test_all_local_snap_install_paths_prepare_configured_snapd():
    """Every guest path prepares snapd before installing the local snap."""
    repo_root = Path(__file__).parents[3]
    harness = (repo_root / "tests" / "robot" / "resources" / "microceph_harness.py").read_text()
    actionutils = (repo_root / "tests" / "scripts" / "actionutils.sh").read_text()
    adoptutils = (repo_root / "tests" / "scripts" / "adoptutils.sh").read_text()
    dsl = (repo_root / "tests" / "scripts" / "test_dsl_functest.sh").read_text()

    builder = harness.split("def build_base_lxd_image", 1)[1].split(
        "def create_lxd_containers_with_loop_devices", 1
    )[0]
    assert "self._snapd_channel()" in builder
    assert builder.index("snap install snapd") < builder.index("snap install --dangerous")

    for script in (actionutils, adoptutils, dsl):
        assert 'SNAPD_CHANNEL="${SNAPD_CHANNEL:-latest/stable}"' in script
        assert "ensure_snapd_channel" in script
        # Install first; refresh only when snap reports it was already installed.
        assert "already installed" in script
        assert script.index("snap install snapd") < script.index("snap refresh snapd")

    # actionutils prepares snapd only in its one live local-install path
    # (verify_pristine_check -> install_microceph); store-channel upgrade
    # workflows and the dead multinode helpers must not be touched.
    assert "ensure_snapd_channel_in_instance" not in actionutils
    assert actionutils.count("    ensure_snapd_channel || return 1\n") == 1

    assert actionutils.index("ensure_snapd_channel") < actionutils.index(
        "sudo snap install --dangerous ~/microceph_*.snap"
    )
    assert adoptutils.index("ensure_snapd_channel") < adoptutils.index(
        'sudo snap install --dangerous /root/microceph_*.snap'
    )
    assert dsl.index("ensure_snapd_channel") < dsl.index(
        "snap install /tmp/microceph.snap --dangerous"
    )


# ---------------------------------------------------------------------------
# SMB Core26 packaging
# ---------------------------------------------------------------------------

def test_ci_runs_smb_on_edge_and_keeps_a_stable_snapd_gate():
    """CI exercises SMB with 2.78 while stable compatibility remains blocking."""
    repo_root = Path(__file__).parents[3]
    workflow = (repo_root / ".github" / "workflows" / "tests.yml").read_text()
    stable_suite = (
        repo_root
        / "tests"
        / "robot"
        / "snapd-stable-compatibility"
        / "snapd_stable_compatibility.robot"
    ).read_text()
    smb_suite = (repo_root / "tests" / "robot" / "smb-test" / "smb_tests.robot").read_text()

    assert "id: smb-test" in workflow
    assert "suite: smb-test" in workflow
    assert "id: smb-multinode-test" in workflow
    assert "suite: smb-multinode-test" in workflow
    assert "id: snapd-stable-compatibility" in workflow
    assert "name: Snapd stable compatibility gate" in workflow
    assert "snapd_channel: latest/stable" in workflow
    assert "--snapd-channel '" in workflow
    assert "matrix.snapd_channel || 'latest/edge'" in workflow

    assert "${OUTER_VM_IMAGE}    ubuntu:26.04" in stable_suite
    assert "Prepare Snapd In VM" in stable_suite
    assert "sudo snap install core26 || true" in stable_suite
    assert "sudo snap install --dangerous ~/microceph_*.snap" in stable_suite
    assert "Install MicroCeph From Local Snap" not in stable_suite
    assert "SMB_SNAPD_CHANNEL" not in smb_suite
    assert "snap install snapd" not in smb_suite


def test_smb_packaging_contract():
    """Check policy and dependencies structurally, without tying tests to YAML formatting."""
    root = Path(__file__).parents[3]
    manifest = yaml.safe_load((root / "snap/snapcraft.yaml").read_text())
    apps, parts, plugs = (manifest[key] for key in ("apps", "parts", "plugs"))
    assert "snapd2.78" in manifest["assumes"]
    assert plugs["smb-identity"] == {"interface": "microceph-support", "user-identity-switching": True}
    assert plugs["ctdb-run"] == {"interface": "system-files", "write": ["/run/ctdb"]}
    assert "smb-identity" not in apps["daemon"]["plugs"]
    assert "process-control" not in apps["smbd"]["plugs"]
    for app, dependencies, required_plugs in (
        ("smbd", ["daemon", "ctdbd"], {"smb-identity", "ctdb-run"}),
        ("ctdbd", ["daemon"], {"smb-identity", "ctdb-run"}),
        ("ctdb-nodes", ["daemon", "ctdbd"], {"ctdb-run"}),
    ):
        assert apps[app]["command"] == f"commands/{app}.start"
        assert apps[app]["daemon"] == "simple"
        assert apps[app]["after"] == dependencies
        assert required_plugs <= set(apps[app]["plugs"])
    assert apps["ctdb"]["command"] == "commands/ctdb"
    assert {"samba-vfs-ceph", "ctdb", "python3-samba", "libnss-wrapper", "libpopt0", "libtirpc3t64"} <= set(parts["samba"]["stage-packages"])
    assert "--target=$CRAFT_PART_INSTALL/lib/python3.14/site-packages" in parts["sambacc"]["override-build"]
    assert "$SNAP/lib/$CRAFT_ARCH_TRIPLET_BUILD_FOR/samba" in manifest["environment"]["LD_LIBRARY_PATH"].split(":")
    assert {"/etc/samba", "/etc/ctdb", "/usr/libexec/samba", "/usr/libexec/ctdb", "/usr/share/ctdb",
            "/var/cache/samba", "/var/lib/samba", "/var/lib/ctdb", "/var/log/samba"} <= manifest["layout"].keys()
    assert "/run/samba" not in manifest["layout"]
    for filename in ("functions", "notify.sh"):
        assert parts["samba"]["organize"][f"etc/ctdb/{filename}"] == f"usr/share/ctdb/{filename}"


def test_smb_startup_uses_confined_helpers():
    root = Path(__file__).parents[3] / "snapcraft/commands"
    required = {
        "smb-common": ['export CTDB_SOCKET="/run/ctdb/ctdbd.socket"', 'export LD_PRELOAD="${nss_wrapper}"',
                       'export NSS_WRAPPER_PASSWD="${smb_identity_dir}/passwd"', 'export NSS_WRAPPER_GROUP="${smb_identity_dir}/group"',
                       'smb_config="${SNAP_DATA}/conf/samba/smb.conf"', '--samba-command-prefix "${SNAP}/commands/samba-command"'],
        "ctdbd.start": ["ctdb-set-node", "ctdb-list-nodes", "--setup=ctdb_config", "--setup=ctdb_etc"],
        "ctdb-nodes.start": ["smb_wait_for_ctdb_ready", "ctdb_ready.py", "ctdb-monitor-nodes", "--reload=all"],
        "smbd.start": ["smb_import_users", "--setup=smb_ctdb", "--wait-for=ctdb"],
    }
    for filename, fragments in required.items():
        source = (root / filename).read_text()
        for fragment in fragments:
            assert fragment in source, (filename, fragment)
    runtime = (root / "smb-common").read_text()
    assert "CRAFT_ARCH_TRIPLET_BUILD_FOR" not in runtime
    assert "cp /etc/passwd" not in runtime and "cp /etc/group" not in runtime


def test_strip_recipe_selects_only_elf_files(tmp_path):
    manifest = yaml.safe_load((Path(__file__).parents[3] / "snap/snapcraft.yaml").read_text())
    library, binary = tmp_path / "lib", tmp_path / "bin"
    library.mkdir()
    binary.mkdir()
    (library / "elf with spaces").write_bytes(b"\x7fELFfixture")
    (library / "text").write_text("not an ELF file")
    recorder = binary / "strip"
    recorder.write_text('#!/bin/sh\nprintf "%s\\n" "$@" >> "$STRIP_LOG"\n')
    recorder.chmod(0o755)
    log = tmp_path / "strip.log"
    subprocess.run(["sh", "-eu", "-c", manifest["parts"]["strip"]["override-prime"]], check=True,
                   capture_output=True, env={**os.environ, "CRAFT_PRIME": str(tmp_path), "STRIP_LOG": str(log),
                                            "PATH": str(binary) + os.pathsep + os.environ["PATH"]})
    assert log.read_text().splitlines() == ["-s", str(library / "elf with spaces")]


def test_samba_command_does_not_inject_samba_options_into_ctdb(tmp_path):
    repo_root = Path(__file__).parents[3]
    command = repo_root / "snapcraft" / "commands" / "samba-command"
    bindir = tmp_path / "bin"
    bindir.mkdir()
    target = bindir / "ctdb"
    target.write_text("#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
    target.chmod(0o755)

    result = subprocess.run(
        [command, "ctdb", "status"],
        check=True,
        capture_output=True,
        text=True,
        env={**os.environ, "SNAP": str(tmp_path)},
    )

    assert result.stdout.splitlines() == ["status"]


def _ctdb_ready_namespace():
    path = Path(__file__).parents[3] / "snapcraft" / "commands" / "ctdb_ready.py"
    namespace = {"__name__": "ctdb_ready_test"}
    exec(compile(path.read_text(), str(path), "exec"), namespace)
    return namespace


def test_ctdb_ready_marks_only_the_local_node_ready():
    data = {
        "nodes": [
            {"identity": "smb.files.node-a", "pnn": 0, "state": "ready"},
            {"identity": "smb.files.node-b", "pnn": 1, "state": "new"},
        ]
    }

    updated = _ctdb_ready_namespace()["mark_ready"](data, "smb.files.node-b", 1)

    assert updated["nodes"] == [
        {"identity": "smb.files.node-a", "pnn": 0, "state": "ready"},
        {"identity": "smb.files.node-b", "pnn": 1, "state": "ready"},
    ]


def _sambacc_runtime_namespace():
    path = Path(__file__).parents[3] / "snapcraft" / "commands" / "sambacc_runtime.py"
    namespace = {"__name__": "sambacc_runtime_test"}
    exec(compile(path.read_text(), str(path), "exec"), namespace)
    return namespace


def test_ctdb_nodes_preserve_missing_and_removed_slots():
    render = _sambacc_runtime_namespace()["ctdb_nodes_with_reserved_slots"]
    nodes = [{"identity": "node-b", "pnn": 1, "node": "192.0.2.2", "state": "ready"}]
    assert render(nodes) == ["#", "192.0.2.2"]
    nodes.append({"identity": "node-a", "pnn": 0, "node": "192.0.2.1", "state": "ready"})
    assert render(nodes) == ["192.0.2.1", "192.0.2.2"]
    nodes[1]["state"] = "gone"
    assert render(nodes) == ["#", "192.0.2.2"]
    nodes.append({"identity": "node-c", "pnn": 2, "node": "192.0.2.3", "state": "new"})
    assert render(nodes) == ["#", "192.0.2.2", "192.0.2.3"]
    assert render([]) == []


def test_ctdb_nodes_reject_invalid_or_duplicate_ranks():
    render = _sambacc_runtime_namespace()["ctdb_nodes_with_reserved_slots"]
    for ranks in ([-1], [True], [0, 0]):
        nodes = [{"pnn": rank, "node": "192.0.2.1", "state": "ready"} for rank in ranks]
        with pytest.raises(ValueError):
            render(nodes)


def test_ctdb_retirement_is_idempotent_and_keeps_survivor():
    retire = _ctdb_ready_namespace()["mark_removed"]
    data = {"nodes": [
        {"identity": "node-a", "pnn": 0, "node": "192.0.2.1", "state": "ready"},
        {"identity": "node-b", "pnn": 1, "node": "192.0.2.2", "state": "ready"},
    ]}
    retire(data, "node-a", 0)
    retire(data, "node-a", 0)
    retire(data, "different-identity", 1)
    assert data["nodes"][0]["state"] == "gone"
    assert data["nodes"][1]["state"] == "ready"
    assert retire({"nodes": []}, "never-started", 2) == {"nodes": []}


def test_ctdb_clean_removal_allows_same_address_rejoin_without_renumbering():
    namespace = _ctdb_ready_namespace()
    data = {"nodes": [
        {"identity": "node-a", "pnn": 0, "node": "192.0.2.1", "state": "ready"},
        {"identity": "node-b", "pnn": 1, "node": "192.0.2.2", "state": "ready"},
    ]}
    namespace["mark_removed"](data, "node-a", 0)
    namespace["prepare_node"](data, "node-a", 0, "192.0.2.1")
    assert [entry["pnn"] for entry in data["nodes"]] == [0, 1]
    assert [entry["state"] for entry in data["nodes"]] == ["ready", "ready"]
    with pytest.raises(ValueError, match="address change"):
        namespace["prepare_node"](data, "node-a", 0, "192.0.2.99")
    assert data["nodes"][0]["node"] == "192.0.2.1"
    assert data["nodes"][0]["state"] == "ready"


def test_sambacc_runtime_creates_no_run_samba_directory(tmp_path):
    """The sambacc replacement creates its state below the Samba data layout."""
    _sambacc_runtime_namespace()["ensure_runtime_dirs"](tmp_path)

    for relative_path in (
        "var/lib/samba",
        "var/lib/samba/private",
        "var/lib/samba/lock",
        "var/lib/samba/run",
        "var/lib/samba/ncalrpc",
        "var/lib/samba/winbindd",
    ):
        assert (tmp_path / relative_path).is_dir()
    assert not (tmp_path / "run/samba").exists()


@pytest.mark.parametrize("config", [None, "/etc/samba/custom.conf"])
def test_sambacc_runtime_sets_paths_before_loading_registry_config(config):
    """LoadParm must receive runtime paths before either registry configuration is opened."""
    events = []
    loadparm = types.SimpleNamespace(
        set=lambda name, value: events.append(("set", name, value)),
        load_default=lambda: events.append(("load_default",)),
        load=lambda path: events.append(("load", path)),
    )

    def get_context():
        events.append(("get_context",))
        return loadparm

    result = _sambacc_runtime_namespace()["load_runtime_loadparm"](types.SimpleNamespace(get_context=get_context), config)
    assert result is loadparm
    assert events == [
        ("get_context",),
        ("set", "lock directory", "/var/lib/samba/lock"),
        ("set", "pid directory", "/var/lib/samba/run"),
        ("set", "ncalrpc dir", "/var/lib/samba/ncalrpc"),
        ("set", "winbindd socket directory", "/var/lib/samba/winbindd"),
        ("load", config) if config else ("load_default",),
    ]


def test_samba_command_injects_runtime_paths(tmp_path):
    """The sambacc command prefix keeps helper processes out of /run/samba."""
    repo_root = Path(__file__).parents[3]
    command = repo_root / "snapcraft" / "commands" / "samba-command"
    target = tmp_path / "capture-args"
    target.write_text("#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
    target.chmod(0o755)

    result = subprocess.run(
        [command, target, "conf", "import", "config.smb"],
        check=True,
        capture_output=True,
        text=True,
    )

    assert result.stdout.splitlines() == [
        "--option=lock directory=/var/lib/samba/lock",
        "--option=pid directory=/var/lib/samba/run",
        "--option=ncalrpc dir=/var/lib/samba/ncalrpc",
        "--option=winbindd socket directory=/var/lib/samba/winbindd",
        "conf",
        "import",
        "config.smb",
    ]


def _run_smb_helper(script, *args, timeout=10):
    common = Path(__file__).parents[3] / "snapcraft" / "commands" / "smb-common"
    return subprocess.run(
        ["bash", "-eu", "-c", '. "$1"; shift; ' + script, "test-smb", str(common), *map(str, args)],
        capture_output=True, text=True, timeout=timeout,
    )


def test_smb_file_wait_reports_all_missing_and_empty_files(tmp_path):
    empty = tmp_path / "empty"
    empty.touch()
    missing = tmp_path / "missing"
    result = _run_smb_helper('SMB_WAIT_TIMEOUT=0; smb_wait_for_files "configuration" "$@"', empty, missing)
    assert result.returncode != 0
    assert "Timed out" in result.stderr
    assert str(empty) in result.stderr
    assert str(missing) in result.stderr


def test_smb_file_wait_succeeds_when_all_files_ready(tmp_path):
    ready = tmp_path / "ready"
    ready.write_text("configured")
    result = _run_smb_helper('SMB_WAIT_TIMEOUT=0; smb_wait_for_files "configuration" "$@"', ready)
    assert result.returncode == 0, result.stderr


def test_smb_file_wait_observes_files_appearing(tmp_path):
    path = tmp_path / "delayed"
    result = _run_smb_helper('sleep() { printf ready > "$file"; }; file="$1"; SMB_WAIT_TIMEOUT=2; smb_wait_for_files configuration "$file"', path)
    assert result.returncode == 0, result.stderr


def _stage_test_timeout(root):
    binary = root / "bin"
    binary.mkdir()
    (binary / "timeout").symlink_to(shutil.which("timeout"))


def test_smb_ctdb_wait_uses_packaged_timeout(tmp_path):
    _stage_test_timeout(tmp_path)
    commands = tmp_path / "commands"
    commands.mkdir()
    probe = commands / "samba-command"
    probe.write_text("#!/bin/sh\nexit 0\n")
    probe.chmod(0o755)
    result = _run_smb_helper(
        'timeout() { return 126; }; SNAP="$1"; SMB_WAIT_TIMEOUT=1; '
        'SMB_POLL_INTERVAL=0.01; smb_wait_for_ctdb_ready', tmp_path,
    )
    assert result.returncode == 0, result.stderr


def test_smb_timeout_and_its_core26_symlink_target_are_primed():
    recipe = (Path(__file__).parents[3] / "snap" / "snapcraft.yaml").read_text()
    ceph = recipe.split("\n  ceph:\n", 1)[1].split("\n  dqlite:\n", 1)[0]
    prime = ceph.split("\n    prime:\n", 1)[1]
    assert "      - bin/timeout\n" in prime
    assert "      - lib/cargo/bin/coreutils/timeout\n" in prime


@pytest.mark.parametrize("probe", ["exit 1", "trap '' TERM; sleep 30"])
def test_smb_ctdb_wait_bounds_failed_and_hung_probes(tmp_path, probe):
    _stage_test_timeout(tmp_path)
    commands = tmp_path / "commands"
    commands.mkdir()
    executable = commands / "samba-command"
    executable.write_text("#!/bin/bash\n" + probe + "\n")
    executable.chmod(0o755)
    result = _run_smb_helper('SNAP="$1"; SMB_WAIT_TIMEOUT=1; SMB_POLL_INTERVAL=0.01; SMB_CTDB_PROBE_TIMEOUT=0.1; smb_wait_for_ctdb_ready', tmp_path, timeout=8)
    assert result.returncode != 0
    assert "Timed out waiting for CTDB" in result.stderr


def test_smb_ctdb_wait_accepts_successful_probe(tmp_path):
    _stage_test_timeout(tmp_path)
    commands = tmp_path / "commands"
    commands.mkdir()
    executable = commands / "samba-command"
    executable.write_text("#!/bin/sh\nexit 0\n")
    executable.chmod(0o755)
    result = _run_smb_helper('SNAP="$1"; SMB_WAIT_TIMEOUT=2; smb_wait_for_ctdb_ready', tmp_path)
    assert result.returncode == 0, result.stderr


def test_smb_identity_baseline_is_minimal_and_not_reset_by_ctdb(tmp_path):
    result = _run_smb_helper('smb_identity_dir="$1"; smb_prepare_identity_files', tmp_path)
    assert result.returncode == 0, result.stderr
    passwd = tmp_path / "passwd"
    group = tmp_path / "group"
    assert [line.split(":")[0] for line in passwd.read_text().splitlines()] == ["root", "nobody"]
    assert [line.split(":")[0] for line in group.read_text().splitlines()] == ["root", "nogroup"]
    passwd.write_text(passwd.read_text() + "alice:x:1000:1000::/invalid:/bin/false\n")
    result = _run_smb_helper('smb_identity_dir="$1"; smb_prepare_identity_files', tmp_path)
    assert result.returncode == 0, result.stderr
    assert "alice:" in passwd.read_text(), "CTDB's independent startup must preserve active Samba identities"


def test_smb_identity_import_publishes_only_after_success(tmp_path):
    identity = tmp_path / "identity"
    identity.mkdir()
    (identity / "passwd").write_text("old-passwd\n")
    (identity / "group").write_text("old-group\n")
    binary = tmp_path / "bin"
    binary.mkdir()
    importer = binary / "python3"
    importer.write_text('#!/bin/sh\nprintf new-passwd > "$NSS_WRAPPER_PASSWD"\nprintf new-group > "$NSS_WRAPPER_GROUP"\nexit 1\n')
    importer.chmod(0o755)
    script = 'SNAP="$1"; smb_identity_dir="$1/identity"; smb_sambacc_args=(); smb_import_users'
    failed = _run_smb_helper(script, tmp_path)
    assert failed.returncode != 0
    assert (identity / "passwd").read_text() == "old-passwd\n"
    assert (identity / "group").read_text() == "old-group\n"
    importer.write_text(importer.read_text().replace("exit 1", "exit 0"))
    succeeded = _run_smb_helper(script, tmp_path)
    assert succeeded.returncode == 0, succeeded.stderr
    assert (identity / "passwd").read_text() == "new-passwd"
    assert (identity / "group").read_text() == "new-group"
    assert not list(identity.glob("identity-import.*"))

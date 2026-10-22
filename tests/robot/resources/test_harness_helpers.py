"""Unit tests for the pure helpers in the MicroCeph Robot Framework harness.

These cover the @staticmethod parsers and the generic _poll_until poller on the
microceph_harness class, plus the standalone snap_services / cephfs_replication
helpers. The helpers are pure (no self, no BuiltIn), so importing the module and
calling them needs no running Robot context -- only that robotframework is
importable (microceph_harness imports robot.api at module top).

Run with pytest:
    pytest tests/robot/resources/test_harness_helpers.py
"""

import base64
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
import socket
import sys
from pathlib import Path
import socketserver
import subprocess
import sys
import threading

import pytest

import placement_status
import rgw_probe
import rgw_scenario
import rgw_placement
from microceph_harness import microceph_harness as H
from cluster_ops import parse_migration_status
from snap_services import enabled_active_services
from cephfs_replication import cephfs_replication_list_has_volume, verify_cephfs_list_entry_types
from rbd_replication import (
    rbd_mirror_health,
    rbd_primary_image_count,
    rbd_synced_image_count,
)
from streaming_process import run_streaming_process


@pytest.mark.parametrize("instances, expected", [
    ([{"name": "target", "status": "Stopped"}], True),
    ([{"name": "target", "status": "Running"}], False),
    ([{"name": "target-other", "status": "Stopped"}], False),
    ([{"name": "target", "status": "Error"}], False),
    ([{"name": "target"}], False),
    ([], False),
    ({}, False),
])
def test_named_vm_stopped_requires_exact_stopped_member(instances, expected):
    assert H._named_vm_stopped(json.dumps(instances), "target") is expected


def test_named_vm_stopped_rejects_malformed_output():
    with pytest.raises(ValueError):
        H._named_vm_stopped("not JSON", "target")


@pytest.mark.parametrize("rc", [1, 124])
def test_stop_named_vm_rejects_failed_stop(monkeypatch, rc):
    harness = H()
    monkeypatch.setattr(harness, "_exec", lambda *_: _Res(rc, "", "stop failed"))
    with pytest.raises(AssertionError, match="failed to stop target"):
        harness.stop_named_vm("target")


@pytest.mark.parametrize("result", [
    (1, "", "query failed"),
    (124, "", "query timed out"),
    (0, '[{"name":"target","status":"Running"}]', ""),
    (0, "[]", ""),
])
def test_stop_named_vm_requires_confirmed_offline_state(monkeypatch, result):
    harness = H()
    responses = iter([_Res(0, "", ""), _Res(*result)])
    monkeypatch.setattr(harness, "_exec", lambda *_: next(responses))
    with pytest.raises(AssertionError):
        harness.stop_named_vm("target")


# ---------------------------------------------------------------------------
# Owned VM launch (no pre-existing deletion or application retries).
# ---------------------------------------------------------------------------

def test_owned_vm_launch_tags_before_start_and_attaches_persistent_disk(monkeypatch):
    h = H()
    assert callable(getattr(h, "launch_owned_test_vm", None)), "missing owned VM lifecycle primitive"
    calls = []
    def execute(argv, timeout):
        calls.append(argv)
        return _Res(0, "", "")
    monkeypatch.setattr(h, "_exec", execute)
    monkeypatch.setattr(h, "wait_for_vm_agent", lambda name: None)
    h.launch_owned_test_vm("test-vm", "owner-token", data_volume="test-osd")
    init = next(argv for argv in calls if argv[:2] == ["lxc", "init"])
    assert "user.microceph-test-owner=owner-token" in init
    assert "--vm" in init and "local:test-vm" in init
    assert not any(arg.startswith('cloud-init.user-data=') for arg in init)
    attach = next(argv for argv in calls if argv[:4] == ["lxc", "config", "device", "add"])
    assert "source=test-osd" in attach and "pool=default" in attach
    assert calls.index(attach) < next(i for i, argv in enumerate(calls) if argv[:2] == ["lxc", "start"])
    assert not any("delete" in argv for argv in calls)


def test_owned_vm_launch_refuses_existing_or_unknown_state(monkeypatch):
    import pytest
    for result in (_Res(0, "test-vm\n", ""), _Res(1, "", "unavailable")):
        h = H()
        assert callable(getattr(h, "launch_owned_test_vm", None)), "missing owned VM lifecycle primitive"
        calls = []
        def execute(argv, timeout):
            calls.append(argv)
            return result
        monkeypatch.setattr(h, "_exec", execute)
        with pytest.raises(AssertionError):
            h.launch_owned_test_vm("test-vm", "owner-token")
        assert len(calls) == 1


# ---------------------------------------------------------------------------
# Private uploads: confined root cannot bypass a foreign-owned mode0600 file.
# ---------------------------------------------------------------------------

def test_lxc_file_push_preserves_default_transfer_arguments(monkeypatch):
    h = H()
    calls = []
    result = _Res(0, '', '')
    monkeypatch.setattr(h, '_exec', lambda argv, timeout: calls.append((argv, timeout)) or result)
    assert h._lxc_file_push('source', 'local:node/destination', 60, 'copy file') is result
    assert calls == [(['lxc', 'file', 'push', 'source', 'local:node/destination'], 60)]


def test_lxc_file_push_private_sets_root_owner_and_restrictive_mode(monkeypatch):
    h = H()
    calls = []
    result = _Res(0, '', '')
    monkeypatch.setattr(h, '_exec', lambda argv, timeout: calls.append((argv, timeout)) or result)
    assert h._lxc_file_push('source', 'local:node/private', 60, 'copy private file', private=True) is result
    assert calls == [(['lxc', 'file', 'push', '--uid=0', '--gid=0', '--mode=0600',
                      'source', 'local:node/private'], 60)]


def test_lxc_file_push_private_propagates_transfer_failure(monkeypatch):
    import pytest
    h = H()
    monkeypatch.setattr(h, '_exec', lambda argv, timeout: _Res(1, '', 'push denied'))
    with pytest.raises(AssertionError, match='Failed to copy private file: push denied'):
        h._lxc_file_push('source', 'local:node/private', 60, 'copy private file', private=True)


def test_smb_private_transfer_call_paths_use_root_owned_mode0600(tmp_path, monkeypatch):
    """Exercise gateway, AD/DC, secret and join-token transfers, not just one script."""
    import hashlib
    m = _smb_module()
    snap = tmp_path / 'candidate.snap'
    snap.write_bytes(b'test-only candidate')
    digest = hashlib.sha256(snap.read_bytes()).hexdigest()
    commands = []

    class Node:
        def __init__(self, name):
            self.name = name
        def probe_instance_network(self):
            pass
        def observe_time(self):
            pass
        def copy_snap_to_vm(self, path):
            pass
        def install_microceph_from_local_snap(self, *args, **kwargs):
            pass
        def wait_for_microceph_control_socket(self):
            pass
        def apt_update(self):
            pass
        def apt_install(self, packages):
            pass
        def command(self, *args, **kwargs):
            commands.append((self.name, args))
            stdout = ''
            if args[0] == 'sha256sum':
                stdout = digest
            elif args == ('snap', 'debug', 'confinement'):
                stdout = 'strict'
            elif args[:2] == ('ip', '-4'):
                stdout = '[{"prefsrc":"192.0.2.1"}]'
            return _Res(0, stdout, '')

    monkeypatch.setattr(m, '_VM', Node)
    transfers = {}
    for phase in ('gateway', 'ad', 'join'):
        library = m.smb_ops()
        pushes = []
        monkeypatch.setattr(library, '_capacity', lambda _: None)
        monkeypatch.setattr(library, '_variable', lambda key, default: str(snap) if key == 'SNAP_PATH' else default)
        monkeypatch.setattr(library.harness, 'launch_owned_test_vm', lambda *args, **kwargs: None)
        monkeypatch.setattr(library.harness, '_exec', lambda argv, timeout: pushes.append(argv) or _Res(0, '', ''))
        def host(*args):
            if args[1:] == ('remote', 'get-default'):
                return 'local'
            if args[1:3] == ('remote', 'list'):
                return '{"local":{}}'
            if args[1:3] == ('file', 'pull'):
                Path(args[4]).write_text('test-only private input')
            return ''
        monkeypatch.setattr(library, '_host', host)
        if phase == 'gateway':
            library.provision_smb_lab()
            expected = ['smb_guest.py'] * 3
        else:
            library.owner = 'test-owner'
            library.nodes = [Node(f'node{i}') for i in range(3)]
            if phase == 'ad':
                library.provision_smb_ad_directory()
                expected = ['smb_ad_guest.py'] * 3 + ['smb_guest.py', 'smb_ad_guest.py', 'ad-secrets.json']
            else:
                library.join_smb_member(1)
                expected = ['join.token']
        assert [Path(argv[-1]).name for argv in pushes] == expected
        transfers[phase] = pushes
    for phase, pushes in transfers.items():
        assert all(argv[:6] == ['lxc', 'file', 'push', '--uid=0', '--gid=0', '--mode=0600'] for argv in pushes), transfers
    assert any(args == ('chmod', '0700', library._base) for _, args in commands)
    assert not any('0644' in args or '0777' in args for _, args in commands)


# ---------------------------------------------------------------------------
# SMB integration observations and ownership (no Robot run context required).
# ---------------------------------------------------------------------------

def _smb_module():
    import importlib
    spec = importlib.util.find_spec("smb_ops")
    assert spec is not None, "missing SMB area library"
    return importlib.import_module("smb_ops")


def _smb_snapshot(node="node0", clustered=True):
    roles = {r: {"ActiveState": "active", "MainPID": str(i + 10),
                 "ExecMainStartTimestampMonotonic": str(i + 100)}
             for i, r in enumerate(("smbd", "smbmetrics", "ctdbd", "ctdb-nodes", "daemon"))}
    if not clustered:
        for role in ("ctdbd", "ctdb-nodes"):
            roles[role]["ActiveState"] = "inactive"
            roles[role]["MainPID"] = "0"
    return {"node": node, "owner": "robot-smb", "status": {"cluster_id": "robot-smb", "ready": True, "stop_requested": False},
            "context": {"clustered": clustered, "smb_port": 1445, "metrics_port": 19922},
            "applied": True, "pending": False, "applied_mtime": 123, "boot_id": "boot-a", "roles": roles}


def test_smb_readiness_requires_owner_applied_error_free_roles():
    import copy
    m = _smb_module()
    s = _smb_snapshot()
    assert m.snapshot_ready(s, "robot-smb", 1445, True)
    for field, value in (("owner", "other"), ("applied", False), ("pending", True), ("status", None)):
        bad = copy.deepcopy(s); bad[field] = value
        assert not m.snapshot_ready(bad, "robot-smb", 1445, True)
    bad = copy.deepcopy(s); bad["status"]["error"] = "local update failed"
    assert not m.snapshot_ready(bad, "robot-smb", 1445, True)
    bad = copy.deepcopy(s); bad["roles"]["ctdbd"]["ActiveState"] = "failed"
    assert not m.snapshot_ready(bad, "robot-smb", 1445, True)
    bad = copy.deepcopy(s); bad["roles"]["smbmetrics"]["MainPID"] = ""
    assert not m.snapshot_ready(bad, "robot-smb", 1445, True)
    assert not m.snapshot_ready(s, "robot-smb", 2445, True)
    assert m.snapshot_ready(_smb_snapshot(clustered=False), "robot-smb", 1445, False)
    assert not m.snapshot_ready(s, "robot-smb", 1445, False)


def test_smb_stability_never_hides_a_transient_error_or_restart():
    import copy
    m = _smb_module()
    before = [_smb_snapshot("a"), _smb_snapshot("b")]
    assert m.observations_stable(before, [copy.deepcopy(before)])
    assert not m.observations_stable(before, [])
    assert not m.observations_stable(before, [[before[0]]])
    for mutate in (lambda s: s[0]["status"].update(error="failed"),
                   lambda s: s[0]["roles"]["smbd"].update(MainPID="999"),
                   lambda s: s[0].update(applied_mtime=456)):
        bad = copy.deepcopy(before); mutate(bad)
        assert not m.observations_stable(before, [bad, copy.deepcopy(before)])


def test_smb_owned_resources_refuse_foreign_tags_and_ignore_unlisted_names():
    import pytest
    m = _smb_module()
    rows = [{"name": "mine", "config": {"user.microceph-test-owner": "token"}},
            {"name": "unrelated", "config": {}}]
    assert m.owned_resources(rows, ["mine", "absent"], "token") == ["mine"]
    with pytest.raises(AssertionError):
        m.owned_resources(rows, ["unrelated"], "token")


def test_smb_snapshot_parser_preserves_real_control_observations():
    m = _smb_module()
    assert callable(getattr(m, "parse_snapshot", None)), "missing raw SMB snapshot parser"
    raw = {"api": json.dumps({"metadata": {"cluster_id": "robot-smb", "ready": True}}),
           "services": "Id=snap.microceph.smbd.service\nMainPID=12\nActiveState=active\nExecMainStartTimestampMonotonic=99\n",
           "owner": "robot-smb"}
    parsed = m.parse_snapshot(json.dumps(raw), "node0")
    assert parsed["node"] == "node0" and parsed["status"]["ready"]
    assert parsed["roles"]["smbd"]["MainPID"] == "12"
    raw["api"] = "interrupted response"
    assert m.parse_snapshot(json.dumps(raw), "node0")["status"] is None


def test_smb_guest_resources_and_redaction_are_explicit():
    import smb_guest
    docs = smb_guest.resource_documents(["192.0.2.1", "192.0.2.2", "192.0.2.3"], "PRIVATE-PASSWORD")
    assert docs["clustered"]["resources"][1]["custom_ports"] == {"smb": 1445, "smbmetrics": 19922}
    assert docs["refresh"]["custom_ports"] == {"smb": 2445, "smbmetrics": 29922}
    assert docs["nonclustered"]["resources"][0]["custom_ports"] == {"smb": 3445, "smbmetrics": 39922}
    assert docs["nonclustered"]["resources"][0]["clustering"] == "never"
    independent = docs["independent"]["resources"]
    assert independent[0]["cluster_id"] != docs["nonclustered"]["resources"][0]["cluster_id"]
    assert independent[0]["clustering"] == "never"
    assert independent[1]["cephfs"]["path"] != docs["nonclustered"]["resources"][1]["cephfs"]["path"]
    assert set(smb_guest.DATA_PATHS.values()) == {"/clustered", "/independent", "/independent-other"}
    raw = json.dumps({"success": False, "results": [{"resource": {"password": "PRIVATE-PASSWORD"}, "msg": "rejected PRIVATE-PASSWORD"}]})
    result = smb_guest.safe_apply_result(0, raw, "PRIVATE-PASSWORD", ["PRIVATE-PASSWORD"])
    assert result["success"] is False and "PRIVATE-PASSWORD" not in json.dumps(result)


def test_smb_area_instantiates_without_robot_and_never_retries_mutations():
    m = _smb_module()
    assert callable(getattr(m, "smb_ops", None)), "missing SMB Robot class"
    library = m.smb_ops()
    calls = []
    class Node:
        name = "node0"
        def command(self, *args, **kwargs):
            calls.append(args)
            return _Res(1, "", "first failure")
    library.nodes = [Node()]
    result = library.smb_node_command(0, "microceph", "enable", "smb", "--cluster-id", "robot-smb")
    assert result.rc == 1 and len(calls) == 1


def test_smb_fixture_retirement_never_retries_or_touches_other_owners(monkeypatch):
    import pytest
    m = _smb_module()
    library = m.smb_ops()
    assert callable(getattr(library, "retire_smb_fixture_placements", None)), "missing explicit fixture retirement"
    calls = []
    class Node:
        name = "node0"
        def command(self, *args, **kwargs):
            calls.append(args)
            return _Res(0, "", "")
    library.nodes = [Node()]
    monkeypatch.setattr(library, "smb_snapshot", lambda *args: {"owner": "other-cluster"})
    with pytest.raises(AssertionError):
        library.retire_smb_fixture_placements()
    assert calls == []
    monkeypatch.setattr(library, "smb_snapshot", lambda *args: {"owner": "robot-smb"})
    library.retire_smb_fixture_placements()
    assert len(calls) == 1 and calls[0] == ("microceph", "disable", "smb", "--cluster-id", "robot-smb")


def test_smb_capacity_failure_reports_observed_and_required_resources(monkeypatch):
    import pytest
    library = _smb_module().smb_ops()
    monkeypatch.setattr(library, "_host", lambda *args: json.dumps({"space": {"total": 100 * 1024 ** 3, "used": 0}}))
    monkeypatch.setattr(Path, "read_text", lambda *args, **kwargs: "MemAvailable: 16777216 kB\n")
    with pytest.raises(AssertionError) as failure:
        library._capacity(3)
    assert "disk free=100.0 GiB, required=122.0 GiB" in str(failure.value)
    assert "memory available=16.0 GiB, required=13.0 GiB" in str(failure.value)


def test_smb_private_shared_state_compares_keys_without_logging_them():
    m = _smb_module()
    resources = '{"resources":[{"resource_type":"ceph.smb.cluster","cluster_id":"robot-never"}]}'
    a = m.shared_state(resources, '{"auth_dump":[{"entity":"client.smb","key":"PRIVATE-KEY","caps":{"mon":"allow r"}}]}')
    b = m.shared_state(resources, '{"auth_dump":[{"entity":"client.smb","key":"OTHER-KEY","caps":{"mon":"allow r"}}]}')
    assert a != b and a == m.shared_state(resources, '{"auth_dump":[{"caps":{"mon":"allow r"},"key":"PRIVATE-KEY","entity":"client.smb"}]}')
    assert "PRIVATE-KEY" not in repr(a) and "PRIVATE-KEY" not in str(a)
    import pytest
    with pytest.raises(AssertionError):
        m.shared_state('{"resources":[]}', '{"auth_dump":[{"entity":"client.smb","key":"PRIVATE","caps":{}}]}')


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
# _health_is_ok_ignoring
# ---------------------------------------------------------------------------

def test_health_is_ok_ignoring_accepts_health_ok():
    assert H._health_is_ok_ignoring(json.dumps({"status": "HEALTH_OK", "checks": {}})) is True
    assert H._health_is_ok_ignoring(json.dumps({"status": "HEALTH_OK", "checks": {}}), {"MON_CLOCK_SKEW"}) is True


def test_health_is_ok_ignoring_accepts_warn_made_only_of_ignored_checks():
    payload = json.dumps(
        {"status": "HEALTH_WARN", "checks": {"MON_CLOCK_SKEW": {"severity": "HEALTH_WARN"}}}
    )

    assert H._health_is_ok_ignoring(payload, {"MON_CLOCK_SKEW"}) is True
    assert H._health_is_ok_ignoring(payload, ()) is False
    assert H._health_is_ok_ignoring(payload) is False


def test_health_is_ok_ignoring_rejects_other_checks_err_and_malformed():
    mixed = json.dumps(
        {
            "status": "HEALTH_WARN",
            "checks": {
                "MON_CLOCK_SKEW": {"severity": "HEALTH_WARN"},
                "OSD_DOWN": {"severity": "HEALTH_WARN"},
            },
        }
    )
    err = json.dumps(
        {"status": "HEALTH_ERR", "checks": {"MON_CLOCK_SKEW": {"severity": "HEALTH_WARN"}}}
    )

    assert H._health_is_ok_ignoring(mixed, {"MON_CLOCK_SKEW"}) is False
    assert H._health_is_ok_ignoring(err, {"MON_CLOCK_SKEW"}) is False
    assert H._health_is_ok_ignoring(json.dumps({"status": "HEALTH_WARN", "checks": {}}), {"MON_CLOCK_SKEW"}) is False
    assert H._health_is_ok_ignoring("not json", {"MON_CLOCK_SKEW"}) is False
    assert H._health_is_ok_ignoring(json.dumps([]), {"MON_CLOCK_SKEW"}) is False


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
# cluster_list_names
#
# Pure replacement for `microceph cluster list -f json | jq '.[]["name"]' |
# grep -q <host>`: the remote command only fetches the JSON.
# ---------------------------------------------------------------------------

def test_cluster_list_names_returns_each_member_name():
    payload = json.dumps(
        [
            {"name": "node-a", "address": "10.0.0.1:7443", "role": "voter", "status": "ONLINE"},
            {"name": "node-b", "address": "10.0.0.2:7443", "role": "voter", "status": "ONLINE"},
        ]
    )
    assert H.cluster_list_names(payload) == ["node-a", "node-b"]


def test_cluster_list_names_accepts_the_cli_trailing_newline():
    # The CLI encodes the list with a json.Encoder, which ends the document with a newline.
    assert H.cluster_list_names('[{"name": "node-a"}]\n') == ["node-a"]


def test_cluster_list_names_empty_list_is_empty():
    assert H.cluster_list_names("[]") == []


def test_cluster_list_names_malformed_json_raises_clear_error():
    with pytest.raises(AssertionError) as exc:
        H.cluster_list_names("Error: not a cluster")
    assert "not valid JSON" in str(exc.value)
    assert "Error: not a cluster" in str(exc.value)


def test_cluster_list_names_empty_output_raises_clear_error():
    with pytest.raises(AssertionError) as exc:
        H.cluster_list_names("")
    assert "not valid JSON" in str(exc.value)


def test_cluster_list_names_non_array_raises():
    with pytest.raises(AssertionError) as exc:
        H.cluster_list_names('{"name": "node-a"}')
    assert "not an array" in str(exc.value)


def test_cluster_list_names_member_without_name_raises():
    with pytest.raises(AssertionError) as exc:
        H.cluster_list_names('[{"address": "10.0.0.1:7443"}]')
    assert "no string name" in str(exc.value)


def test_cluster_list_names_malformed_member_raises():
    # Neither a member that is not an object nor a name that is not a string is skipped or returned.
    for payload in ('["node-a"]', "[null]", '[{"name": 5}]', '[{"name": null}]'):
        with pytest.raises(AssertionError) as exc:
            H.cluster_list_names(payload)
        assert "no string name" in str(exc.value), payload
        assert repr(json.loads(payload)[0]) in str(exc.value), payload


def test_cluster_list_names_quotes_only_the_start_of_long_output():
    with pytest.raises(AssertionError) as exc:
        H.cluster_list_names("x" * 500)
    assert "x" * 150 in str(exc.value)
    assert "x" * 250 not in str(exc.value)


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


_EXPORT_DEADLINE = (
    'Error: failed to fetch cluster state: Get "http://control.socket/1.0/cluster": context deadline exceeded'
)


def test_cluster_export_deadline_matches_the_ci_error():
    assert H._is_cluster_export_deadline(_EXPORT_DEADLINE)
    assert H._is_cluster_export_deadline(_EXPORT_DEADLINE + "\n")


def test_cluster_export_deadline_ignores_other_failures():
    for stderr in (
        "Error: access denied",
        'Error: failed to fetch cluster state: Get "http://control.socket/1.0/cluster": EOF',
        'Get "https://api.snapcraft.io/v2/snaps/info/microceph": context deadline exceeded',
        "Command timed out after 60s",
        "",
        None,
    ):
        assert not H._is_cluster_export_deadline(stderr), stderr


def test_export_cluster_token_retries_a_transient_control_socket_timeout(monkeypatch):
    cap = _with_logger(monkeypatch)
    harness = H()
    export_token = getattr(harness, "export_cluster_token", None)
    assert export_token is not None, "cluster token export must retry transient control-socket failures"

    results = iter(
        [
            _Res(1, "", _EXPORT_DEADLINE + "\n"),
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
    # The command is echoed once and the retry is announced, because the probes themselves are
    # quiet. Nothing else reaches the console.
    assert cap.console_lines == [
        "+ [node-wrk2] microceph cluster export sitea",
        "[replication] transient cluster export error (sitea on node-wrk2), "
        f"attempt 1/2: {_EXPORT_DEADLINE}; retrying...",
    ]


def test_export_cluster_token_gives_up_after_the_last_attempt(monkeypatch):
    cap = _with_logger(monkeypatch)
    harness = H()
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append(argv)
        return _Res(1, "", _EXPORT_DEADLINE + "\n")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    with pytest.raises(AssertionError) as exc:
        harness.export_cluster_token("node-wrk0", "siteb", attempts=3, interval=0)

    assert str(exc.value) == f"failed to export cluster token for siteb on node-wrk0; last error: {_EXPORT_DEADLINE}"
    assert len(calls) == 3
    # The command is echoed once; only the two attempts that are followed by another one are announced.
    assert len(cap.console_lines) == 3
    assert cap.console_lines[0] == "+ [node-wrk0] microceph cluster export siteb"
    assert "attempt 1/3" in cap.console_lines[1]
    assert "attempt 2/3" in cap.console_lines[2]


def test_export_cluster_token_defaults_to_ten_attempts_three_seconds_apart(monkeypatch):
    """replication.resource calls the keyword without arguments, so the defaults are the retry budget."""
    _with_logger(monkeypatch)
    harness = H()
    calls = []
    sleeps = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append(argv)
        return _Res(1, "", _EXPORT_DEADLINE + "\n")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", sleeps.append)

    with pytest.raises(AssertionError):
        harness.export_cluster_token("node-wrk0", "siteb")

    assert len(calls) == 10
    assert set(sleeps) == {3}


def test_export_cluster_token_does_not_retry_a_permanent_failure(monkeypatch):
    cap = _with_logger(monkeypatch)
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
    # Echoed, but not announced as a retry.
    assert cap.console_lines == ["+ [node-wrk2] microceph cluster export sitea"]


def test_export_cluster_token_does_not_retry_an_exit_0_without_a_token(monkeypatch):
    _with_logger(monkeypatch)
    harness = H()
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append(argv)
        return _Res(0, "\n", "")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    with pytest.raises(AssertionError) as exc:
        harness.export_cluster_token("node-wrk0", "siteb", attempts=3, interval=0)

    assert str(exc.value) == "failed to export cluster token for siteb on node-wrk0; last error: empty token"
    assert len(calls) == 1


def test_export_cluster_token_names_the_exit_code_when_stderr_is_empty(monkeypatch):
    _with_logger(monkeypatch)
    harness = H()
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append(argv)
        return _Res(255, "", "")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    with pytest.raises(AssertionError) as exc:
        harness.export_cluster_token("node-wrk0", "siteb", attempts=3, interval=0)

    assert str(exc.value) == "failed to export cluster token for siteb on node-wrk0; last error: exit code 255"
    assert len(calls) == 1


def test_export_cluster_token_does_not_retry_a_harness_timeout(monkeypatch):
    """A snap with a longer client deadline stalls past the 60 s exec timeout; that fails at once."""
    _with_logger(monkeypatch)
    harness = H()
    calls = []

    def fake_exec(container, *argv, timeout, quiet):
        calls.append(argv)
        return _Res(124, "", "\nCommand timed out after 60s")

    monkeypatch.setattr(harness, "exec_in_container", fake_exec)
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)

    with pytest.raises(AssertionError) as exc:
        harness.export_cluster_token("node-wrk0", "siteb", attempts=3, interval=0)

    assert str(exc.value) == (
        "failed to export cluster token for siteb on node-wrk0; last error: Command timed out after 60s"
    )
    assert len(calls) == 1


def test_exchange_remote_site_tokens_exports_with_retry_and_checks_each_import():
    """A failed export must stop the exchange, and every import must be checked on its own node."""
    from robot.running import ResourceFile

    resource = ResourceFile.from_file_system(Path(__file__).with_name("replication.resource"))
    keyword = next(kw for kw in resource.keywords if kw.name == "Exchange Remote Site Tokens")
    steps = [
        (item.name, item.args[0])
        for item in keyword.body
        if item.name in ("Export Cluster Token", "Run In Container", "Assert Remote List Has")
    ]
    assert steps == [
        ("Export Cluster Token", "node-wrk0"),
        ("Export Cluster Token", "node-wrk2"),
        ("Run In Container", "node-wrk0"),
        ("Assert Remote List Has", "node-wrk0"),
        ("Run In Container", "node-wrk2"),
        ("Assert Remote List Has", "node-wrk2"),
    ]
    asserted = [item.args for item in keyword.body if item.name == "Assert Remote List Has"]
    assert asserted == [("node-wrk0", "name", "siteb"), ("node-wrk2", "name", "sitea")]


def test_replication_setup_tests_end_the_run_on_failure():
    """A failed setup test stops the run; the later tests would otherwise poll for over an hour."""
    from robot.api import TestSuite

    robot_root = Path(__file__).parents[1]
    for suite_file in (
        "rbd-replication-test/rbd_replication_tests.robot",
        "cephfs-replication-test/cephfs_replication_tests.robot",
    ):
        suite = TestSuite.from_file_system(robot_root / suite_file)
        tags = {test.name: [str(tag) for tag in test.tags] for test in suite.tests}
        for name in ("Test Bootstrap Two Sites", "Test Exchange Remote Tokens", "Test Verify Remote Authentication"):
            assert "robot:exit-on-failure" in tags[name], f"{suite_file}: {name} must carry robot:exit-on-failure"


def test_installed_robot_framework_honours_the_exit_on_failure_tag():
    """Robot Framework 7.0 and 7.1 ignore the tag, so tests/robot/requirements.txt must exclude them."""
    import re
    from robot.version import VERSION

    major, minor = (int(part) for part in re.match(r"(\d+)\.(\d+)", VERSION).groups())
    assert (major, minor) >= (7, 2), f"Robot Framework {VERSION} ignores robot:exit-on-failure; use 7.2 or newer"


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


def test_response_code_real_error_body_uses_error_code():
    # microcluster error bodies carry status_code 0 next to the real error_code.
    body = json.dumps({"type": "error", "status": "", "status_code": 0, "operation": "",
                       "error_code": 400, "error": "bad request", "metadata": None})
    assert placement_status.response_code(body) == 400
    conflict = json.dumps({"type": "error", "status_code": 0, "error_code": 409, "error": "in progress"})
    assert placement_status.response_code(conflict) == 409


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


# GET /1.0/placement body carrying an observed RGW member with a frontend, plus
# a stored policy whose rgw entry has been stripped/redacted (no key material).
_RGW_PLACEMENT_RESPONSE = json.dumps({
    "status_code": 200,
    "metadata": {
        "active": True,
        "policy": {
            "mode": "reconcile",
            "members": {
                "node-a": {"rgw": {"enabled": True, "port": 8080, "ssl_port": 443}},
            },
        },
        "observed": [
            {"member": "node-a", "rgw": True,
             "rgw_frontend": {"port": 8080, "ssl_port": 443, "ssl": True}},
            {"member": "node-b", "control": True},
        ],
    },
})


def test_member_rgw_frontend_reports_ports_and_tls():
    fe = placement_status.member_rgw_frontend(_RGW_PLACEMENT_RESPONSE, "node-a")
    assert fe == {"port": 8080, "ssl_port": 443, "ssl": True}


def test_member_rgw_frontend_absent_member_is_empty():
    # An existing member with no rgw_frontend key, and a member not present
    # at all, both read as "no frontend reported" -- not an error.
    assert placement_status.member_rgw_frontend(_RGW_PLACEMENT_RESPONSE, "node-b") == {}
    assert placement_status.member_rgw_frontend(_RGW_PLACEMENT_RESPONSE, "node-z") == {}


def test_member_rgw_frontend_malformed_body_raises():
    # A malformed body must never read as "no frontend reported": absence and
    # "cannot tell" are different outcomes, so garbage must fail closed.
    for bad in ("garbage", "", _ERROR_RESPONSE, json.dumps({"status_code": 200})):
        with pytest.raises(ValueError):
            placement_status.member_rgw_frontend(bad, "node-a")


def test_member_rgw_frontend_missing_observed_key_raises():
    raw = json.dumps({"status_code": 200, "metadata": {"active": True}})
    with pytest.raises(ValueError):
        placement_status.member_rgw_frontend(raw, "node-a")


def test_member_rgw_frontend_malformed_observed_shape_raises():
    not_a_list = json.dumps({"status_code": 200, "metadata": {"observed": "nope"}})
    with pytest.raises(ValueError):
        placement_status.member_rgw_frontend(not_a_list, "node-a")

    bad_frontend = json.dumps({
        "status_code": 200,
        "metadata": {"observed": [
            {"member": "node-a", "rgw_frontend": {"port": 8080, "ssl": "not-a-bool"}},
        ]},
    })
    with pytest.raises(ValueError):
        placement_status.member_rgw_frontend(bad_frontend, "node-a")

    bad_port = json.dumps({
        "status_code": 200,
        "metadata": {"observed": [
            {"member": "node-a", "rgw_frontend": {"port": 70000, "ssl": True}},
        ]},
    })
    with pytest.raises(ValueError):
        placement_status.member_rgw_frontend(bad_port, "node-a")


def test_placement_leaks_rgw_secrets_false_when_stripped():
    # The stored policy carries port/ssl_port but no cert/key: no leak.
    assert placement_status.placement_leaks_rgw_secrets(_RGW_PLACEMENT_RESPONSE) is False


def test_placement_leaks_rgw_secrets_reject_malformed_status():
    # Secret checks must fail closed: garbage or error bodies must not read as
    # "nothing to leak".
    for bad in ("garbage", "", _ERROR_RESPONSE, json.dumps({"status_code": 200})):
        with pytest.raises(ValueError):
            placement_status.placement_leaks_rgw_secrets(bad)
    misshapen = json.dumps({
        "status_code": 200,
        "metadata": {"policy": {"members": "not-a-map"}},
    })
    with pytest.raises(ValueError):
        placement_status.placement_leaks_rgw_secrets(misshapen)


def test_placement_leaks_rgw_secrets_true_when_present():
    leaky = json.dumps({
        "status_code": 200,
        "metadata": {"policy": {"members": {
            "node-a": {"rgw": {"enabled": True, "ssl_certificate": "Y2VydA=="}},
        }}},
    })
    assert placement_status.placement_leaks_rgw_secrets(leaky) is True
    leaky_key = json.dumps({
        "status_code": 200,
        "metadata": {"policy": {"members": {
            "node-a": {"rgw": {"enabled": True, "ssl_private_key": "a2V5"}},
        }}},
    })
    assert placement_status.placement_leaks_rgw_secrets(leaky_key) is True


_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL = json.dumps({
    "status_code": 200,
    "metadata": {
        "active": True,
        "policy": {
            "mode": "reconcile",
            "members": {
                "node-a": {"rgw": {"enabled": True, "ssl": True, "ssl_port": 8443}},
                "node-b": {"rgw": {"enabled": False}},
            },
        },
        "observed": [
            {"member": "node-a", "rgw": True, "rgw_frontend": {"ssl_port": 8443, "ssl": True}},
            {"member": "node-b", "rgw": False},
        ],
        "placement_refusal": "keep-one invariant: refused to remove last mon on node-c",
    },
})


def test_placement_metadata_strict_parse():
    meta = placement_status.placement_metadata(_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL)
    assert meta["active"] is True
    assert placement_status.placement_metadata(_RGW_PLACEMENT_RESPONSE) == {
        "active": True,
        "policy": {"mode": "reconcile", "members": {
            "node-a": {"rgw": {"enabled": True, "port": 8080, "ssl_port": 443}},
        }},
        "observed": [
            {"member": "node-a", "rgw": True,
             "rgw_frontend": {"port": 8080, "ssl_port": 443, "ssl": True}},
            {"member": "node-b", "control": True},
        ],
    }


def test_placement_metadata_rejects_malformed_bodies():
    for bad in ("garbage", "", "[1, 2, 3]", _ERROR_RESPONSE,
                json.dumps({"status_code": 200}),
                json.dumps({"status_code": 200, "metadata": "not-an-object"})):
        with pytest.raises(ValueError):
            placement_status.placement_metadata(bad)


def test_placement_refusal_present_absent_and_malformed():
    assert placement_status.placement_refusal(_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL) == \
        "keep-one invariant: refused to remove last mon on node-c"
    # No recorded refusal reads as empty on a valid body.
    assert placement_status.placement_refusal(_RGW_PLACEMENT_RESPONSE) == ""
    with pytest.raises(ValueError):
        placement_status.placement_refusal("garbage")
    with pytest.raises(ValueError):
        placement_status.placement_refusal(_ERROR_RESPONSE)


def test_stored_policy_rgw_returns_member_intent():
    intent = placement_status.stored_policy_rgw(_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL, "node-a")
    assert intent == {"enabled": True, "ssl": True, "ssl_port": 8443}
    assert placement_status.stored_policy_rgw(_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL, "node-b") == \
        {"enabled": False}


def test_stored_policy_rgw_absent_member_and_policy_are_empty():
    assert placement_status.stored_policy_rgw(_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL, "node-z") == {}
    no_policy = json.dumps({"status_code": 200, "metadata": {"active": False}})
    assert placement_status.stored_policy_rgw(no_policy, "node-a") == {}
    # A member entry without an rgw field (omission policy) is also empty.
    omitted = json.dumps({
        "status_code": 200,
        "metadata": {"policy": {"mode": "reconcile", "members": {"node-a": {"control": True}}}},
    })
    assert placement_status.stored_policy_rgw(omitted, "node-a") == {}


def test_stored_policy_rgw_rejects_malformed_bodies():
    # Before/after comparisons of accepted state must fail on garbage rather
    # than compare {} == {}.
    for bad in ("garbage", "", _ERROR_RESPONSE):
        with pytest.raises(ValueError):
            placement_status.stored_policy_rgw(bad, "node-a")
    # Not a stored-boolean-policy compatibility check (out of scope; new
    # placement requests are object-only) -- just another malformed rgw
    # intent shape, a list where an object is required.
    misshapen = json.dumps({
        "status_code": 200,
        "metadata": {"policy": {"members": {"node-a": {"rgw": ["not", "an", "object"]}}}},
    })
    with pytest.raises(ValueError):
        placement_status.stored_policy_rgw(misshapen, "node-a")


def test_observed_rgw_members_flags():
    flags = placement_status.observed_rgw_members(_RGW_PLACEMENT_RESPONSE_WITH_REFUSAL)
    assert flags == {"node-a": True, "node-b": False}


def test_observed_rgw_members_rejects_malformed_bodies():
    for bad in ("garbage", "", _ERROR_RESPONSE,
                json.dumps({"status_code": 200, "metadata": {"observed": "nope"}})):
        with pytest.raises(ValueError):
            placement_status.observed_rgw_members(bad)
    bad_entry = json.dumps({
        "status_code": 200,
        "metadata": {"observed": [{"member": "node-a", "rgw": True}, "not-an-entry"]},
    })
    with pytest.raises(ValueError):
        placement_status.observed_rgw_members(bad_entry)


def test_rgw_frontend_conf_ports_plaintext():
    conf = "rgw frontends = beast port=8080\n"
    assert placement_status.rgw_frontend_conf_ports(conf) == {
        "port": 8080, "ssl_port": 0, "ssl": False,
    }


def test_rgw_frontend_conf_ports_tls_only():
    conf = "rgw frontends = beast ssl_port=443 ssl_certificate=/x/server.crt ssl_private_key=/x/server.key\n"
    assert placement_status.rgw_frontend_conf_ports(conf) == {
        "port": 0, "ssl_port": 443, "ssl": True,
    }


def test_rgw_frontend_conf_ports_dual_listener():
    conf = "rgw frontends = beast port=8080 ssl_port=8443 ssl_certificate=/x/server.crt ssl_private_key=/x/server.key\n"
    assert placement_status.rgw_frontend_conf_ports(conf) == {
        "port": 8080, "ssl_port": 8443, "ssl": True,
    }


def test_rgw_frontend_conf_ports_rejects_missing_line_and_bad_values():
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_conf_ports("[global]\nrun dir = /x\n")
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_conf_ports("")
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_conf_ports("rgw frontends = beast port=notaport\n")


# ---------------------------------------------------------------------------
# rgw_frontend_tls_paths
# ---------------------------------------------------------------------------

def test_rgw_frontend_tls_paths_plaintext_is_empty():
    conf = "rgw frontends = beast port=8080\n"
    assert placement_status.rgw_frontend_tls_paths(conf) == []


def test_rgw_frontend_tls_paths_full_pair_returns_exact_paths():
    conf = (
        "rgw frontends = beast port=8080 ssl_port=8443 "
        "ssl_certificate=/var/snap/microceph/common/rgw-tls/abc/server.crt "
        "ssl_private_key=/var/snap/microceph/common/rgw-tls/abc/server.key\n"
    )
    assert placement_status.rgw_frontend_tls_paths(conf) == [
        "/var/snap/microceph/common/rgw-tls/abc/server.crt",
        "/var/snap/microceph/common/rgw-tls/abc/server.key",
    ]


def test_rgw_frontend_tls_paths_half_pair_raises():
    # A certificate with no matching key (or vice versa) must never read as
    # "nothing to reference"; it is a broken reference and must fail.
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_tls_paths(
            "rgw frontends = beast ssl_port=8443 ssl_certificate=/x/server.crt\n"
        )
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_tls_paths(
            "rgw frontends = beast ssl_port=8443 ssl_private_key=/x/server.key\n"
        )


def test_rgw_frontend_tls_paths_missing_line_raises():
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_tls_paths("[global]\nrun dir = /x\n")
    with pytest.raises(ValueError):
        placement_status.rgw_frontend_tls_paths("")


def test_rgw_frontend_tls_paths_quoted_values_via_shlex():
    # Paths containing spaces are only recovered correctly if the tokenizer
    # honours shell quoting rather than splitting on every space.
    conf = (
        'rgw frontends = beast ssl_port=8443 '
        'ssl_certificate="/var/snap/microceph/common/rgw tls/abc/server.crt" '
        'ssl_private_key="/var/snap/microceph/common/rgw tls/abc/server.key"\n'
    )
    assert placement_status.rgw_frontend_tls_paths(conf) == [
        "/var/snap/microceph/common/rgw tls/abc/server.crt",
        "/var/snap/microceph/common/rgw tls/abc/server.key",
    ]


# ---------------------------------------------------------------------------
# cluster_member_names
# ---------------------------------------------------------------------------

_DEPLOYMENT_SUMMARY = (
    "MicroCeph deployment summary:\n"
    "- rgw-mvm-first (10.0.0.11)\n"
    "  Services: mds, mgr, mon, osd\n"
    "  Disks: 1\n"
    "- rgw-mvm-first-2 (10.0.0.12)\n"
    "  Services: osd\n"
    "  Disks: 1\n"
)


def test_cluster_member_names_parses_deployment_summary():
    assert placement_status.cluster_member_names(_DEPLOYMENT_SUMMARY) == {
        "rgw-mvm-first", "rgw-mvm-first-2",
    }


def test_cluster_member_names_does_not_treat_prefix_as_present():
    # "rgw-mvm-first" is a substring of "rgw-mvm-first-2"; only the member
    # whose own line actually names it may count as present.
    text = "MicroCeph deployment summary:\n- rgw-mvm-first-2 (10.0.0.12)\n"
    names = placement_status.cluster_member_names(text)
    assert "rgw-mvm-first-2" in names
    assert "rgw-mvm-first" not in names


def test_cluster_member_names_ignores_service_and_disk_lines():
    text = "MicroCeph deployment summary:\n- node-a (10.0.0.1)\n  Services: osd\n  Disks: 1\n"
    assert placement_status.cluster_member_names(text) == {"node-a"}


def test_cluster_member_names_empty_or_no_members_is_empty_set():
    assert placement_status.cluster_member_names("") == set()
    assert placement_status.cluster_member_names(None) == set()
    assert placement_status.cluster_member_names("MicroCeph deployment summary:\n") == set()


# ---------------------------------------------------------------------------
# rgw_probe.material_needles
# ---------------------------------------------------------------------------

# A banner line, one long (>=32 byte) base64-looking body line, and a second
# banner -- shaped like a real PEM without needing a real key.
_PEM_BODY_LINE = b"A" * 44
_PEM = b"-----BEGIN CERTIFICATE-----\n" + _PEM_BODY_LINE + b"\n-----END CERTIFICATE-----\n"


def test_material_needles_includes_raw_base64_and_json_escaped_forms():
    needles = rgw_probe.material_needles(_PEM)
    assert _PEM in needles
    assert base64.b64encode(_PEM) in needles
    # JSON-embedding a PEM escapes its newlines to a literal backslash-n.
    assert json.dumps(_PEM.decode("ascii"))[1:-1].encode() in needles


def test_material_needles_includes_long_body_lines_excludes_banners():
    needles = rgw_probe.material_needles(_PEM)
    assert _PEM_BODY_LINE in needles
    assert b"-----BEGIN CERTIFICATE-----" not in needles
    assert b"-----END CERTIFICATE-----" not in needles


def test_material_needles_excludes_short_body_lines():
    short_pem = b"-----BEGIN CERTIFICATE-----\nshort\n-----END CERTIFICATE-----\n"
    needles = rgw_probe.material_needles(short_pem)
    assert b"short" not in needles


def test_material_needles_dedups_across_repeated_material():
    assert rgw_probe.material_needles(_PEM, _PEM) == rgw_probe.material_needles(_PEM)


# ---------------------------------------------------------------------------
# rgw_probe.file_contains_material
# ---------------------------------------------------------------------------

def test_file_contains_material_matches_in_first_chunk(tmp_path):
    needle = b"super-secret-material"
    path = tmp_path / "leak.txt"
    path.write_bytes(b"prefix " + needle + b" suffix")
    assert rgw_probe.file_contains_material(path, (needle,)) is True


def test_file_contains_material_no_match(tmp_path):
    path = tmp_path / "clean.txt"
    path.write_bytes(b"nothing interesting in here")
    assert rgw_probe.file_contains_material(path, (b"super-secret-material",)) is False


def test_file_contains_material_empty_file_is_false(tmp_path):
    path = tmp_path / "empty.txt"
    path.write_bytes(b"")
    assert rgw_probe.file_contains_material(path, (b"needle",)) is False


def test_file_contains_material_matches_across_chunk_boundary(tmp_path):
    # The reader works in 64KiB (65536-byte) chunks; place the needle so it
    # starts just before that boundary and ends just after it, proving the
    # overlap-retention logic (not a single unbroken chunk) finds the match.
    needle = b"boundary-spanning-secret-material-marker"
    before = b"a" * (65536 - 10)
    after = b"b" * 4096
    path = tmp_path / "boundary.txt"
    path.write_bytes(before + needle + after)
    assert rgw_probe.file_contains_material(path, (needle,)) is True


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
    mon = json.dumps({"quorum_names": ["node-a"]})
    mgr = json.dumps([{"name": "node-a"}])
    mds = json.dumps({"fsmap": {"standbys": [], "filesystems": []}})

    for bad_mon, bad_mgr, bad_mds in (
        ("bad", mgr, mds),
        (mon, "bad", mds),
        (mon, mgr, "bad"),
        ("", "", ""),
    ):
        with pytest.raises(ValueError):
            placement_status.control_service_presence(
                bad_mon, bad_mgr, bad_mds, "node-a"
            )


def test_member_in_ceph_status_substring():
    status = "  services:\n    mon: 2 daemons, quorum node-wrk0,node-wrk1\n"
    assert placement_status.member_in_ceph_status(status, "node-wrk1") is True
    assert placement_status.member_in_ceph_status(status, "node-wrk3") is False
    assert placement_status.member_in_ceph_status("", "node-wrk0") is False
    assert placement_status.member_in_ceph_status(None, "node-wrk0") is False


@pytest.fixture(autouse=True)
def isolated_guest_snapd_channel(monkeypatch):
    # Unit tests opt in explicitly, even when invoked by a configured CI run.
    monkeypatch.delenv("MICROCEPH_TEST_SNAPD_CHANNEL", raising=False)


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


# ---------------------------------------------------------------------------
# RGW endpoint probe -- only an observed refusal counts as closed
# ---------------------------------------------------------------------------

def _rgw_probe_with_results(monkeypatch, results):
    probe = rgw_placement.rgw_placement()
    calls = []
    replies = iter(results)

    def fake_exec(args, vm_name=None, timeout=30, check=True):
        calls.append((args, vm_name, check))
        return next(replies)

    monkeypatch.setattr(probe, "_exec", fake_exec)
    return probe, calls


@pytest.mark.parametrize("response, reachable", [
    (_Res(0, "reachable\n", ""), True),
    (_Res(0, "refused\n", ""), False),
])
def test_rgw_endpoint_probe_recognizes_guest_socket_observation(monkeypatch, response, reachable):
    probe, _ = _rgw_probe_with_results(monkeypatch, [response])

    assert probe.rgw_endpoint_reachable_in_vm("10.0.0.3", 7480, vm_name="node-wrk1") is reachable


@pytest.mark.parametrize("error", [
    _Res(124, "refused\n", "Command timed out after 30s"),
    _Res(1, "", "Error: Instance not found"),
    _Res(1, "", "TimeoutError: timed out"),
    _Res(0, "refused\n", "LXD warning: probe output incomplete"),
])
def test_rgw_endpoint_probe_rejects_execution_failures(monkeypatch, error):
    probe, _ = _rgw_probe_with_results(monkeypatch, [error])

    with pytest.raises(AssertionError):
        probe.rgw_endpoint_reachable_in_vm("10.0.0.3", 7480)


@pytest.mark.parametrize("response", [
    "",
    "False\n",
    "refused",
    "refused\nreachable\n",
    "timeout\n",
])
def test_rgw_endpoint_probe_rejects_malformed_guest_response(monkeypatch, response):
    probe, _ = _rgw_probe_with_results(monkeypatch, [_Res(0, response, "")])

    with pytest.raises(AssertionError, match="Unexpected RGW endpoint probe response"):
        probe.rgw_endpoint_reachable_in_vm("10.0.0.3", 7480)


@pytest.mark.parametrize("outcomes, response", [
    ([None], "reachable\n"),
    ([ConnectionRefusedError()], "refused\n"),
    ([ConnectionRefusedError(), ConnectionRefusedError()], "refused\n"),
    ([ConnectionRefusedError(), None], "reachable\n"),
    ([socket.timeout("timed out"), None], "reachable\n"),
    ([socket.timeout("timed out")], None),
    ([socket.timeout("timed out"), ConnectionRefusedError()], None),
    ([ConnectionRefusedError(), socket.timeout("timed out")], None),
    ([OSError("network unreachable"), ConnectionRefusedError()], None),
])
def test_rgw_endpoint_guest_script_preserves_all_address_outcomes(monkeypatch, capsys, outcomes, response):
    probe, calls = _rgw_probe_with_results(monkeypatch, [_Res(0, "reachable\n", "")])
    probe.rgw_endpoint_reachable_in_vm("gateway.example", 7480)
    script = calls[0][0][2]
    addresses = [(f"192.0.2.{index + 1}", 7480) for index in range(len(outcomes))]
    errors = dict(zip(addresses, outcomes))
    connections = []

    class Connection:
        def __init__(self, *args):
            self.closed = False
            connections.append(self)

        def settimeout(self, timeout):
            pass

        def connect(self, address):
            error = errors[address]
            if error is not None:
                raise error

        def close(self):
            self.closed = True

    monkeypatch.setattr(socket, "getaddrinfo", lambda *args: [
        (socket.AF_INET, socket.SOCK_STREAM, 6, "", address) for address in addresses
    ])
    monkeypatch.setattr(socket, "socket", Connection)
    monkeypatch.setattr(sys, "argv", ["python3", "gateway.example", "7480"])
    if response is None:
        with pytest.raises(ExceptionGroup):
            exec(script, {"__name__": "__main__"})
        assert capsys.readouterr().out == ""
    else:
        exec(script, {"__name__": "__main__"})
        assert capsys.readouterr().out == response
    assert all(connection.closed for connection in connections)


def test_rgw_endpoint_guest_script_rejects_dns_failure(monkeypatch, capsys):
    probe, calls = _rgw_probe_with_results(monkeypatch, [_Res(0, "reachable\n", "")])
    probe.rgw_endpoint_reachable_in_vm("gateway.example", 7480)

    def fail_dns(*args):
        raise socket.gaierror("name resolution failed")

    monkeypatch.setattr(socket, "getaddrinfo", fail_dns)
    monkeypatch.setattr(sys, "argv", ["python3", "gateway.example", "7480"])
    with pytest.raises(socket.gaierror):
        exec(calls[0][0][2], {"__name__": "__main__"})
    assert capsys.readouterr().out == ""


def test_wait_for_rgw_endpoint_closed_requires_observed_refusal(monkeypatch):
    probe, calls = _rgw_probe_with_results(monkeypatch, [
        _Res(0, "reachable\n", ""),
        _Res(0, "refused\n", ""),
    ])

    assert probe.wait_for_rgw_endpoint_closed_in_vm(
        "10.0.0.3", 7480, vm_name="node-wrk1", attempts=3, interval=0
    ) is False
    assert len(calls) == 2
    assert all(vm_name == "node-wrk1" for _, vm_name, _ in calls)


def test_wait_for_rgw_endpoint_closed_remains_open_without_refusal(monkeypatch):
    probe, calls = _rgw_probe_with_results(monkeypatch, [_Res(0, "reachable\n", "")] * 2)

    assert probe.wait_for_rgw_endpoint_closed_in_vm("10.0.0.3", 7480, attempts=2, interval=0) is True
    assert len(calls) == 2


@pytest.mark.parametrize("failure", [
    _Res(124, "refused\n", "Command timed out after 30s"),
    _Res(1, "", "Error: Instance not found"),
    _Res(0, "", ""),
    _Res(0, "refused\n", "LXD warning: probe output incomplete"),
])
def test_wait_for_rgw_endpoint_closed_never_accepts_probe_failure(monkeypatch, failure):
    probe, calls = _rgw_probe_with_results(monkeypatch, [_Res(0, "reachable\n", ""), failure])

    with pytest.raises(AssertionError):
        probe.wait_for_rgw_endpoint_closed_in_vm("10.0.0.3", 7480, attempts=3, interval=0)
    assert len(calls) == 2


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


def test_remove_cluster_member_with_retry_accepts_not_found_after_transient_failure(monkeypatch):
    harness = H()
    results = iter([
        _Res(1, "", "context deadline exceeded"),
        _Res(1, "", 'Error: cluster member "node-wrk3" not found'),
    ])
    calls = []

    def run_in_container_unchecked(container, cmd, timeout, quiet):
        calls.append((container, cmd, timeout, quiet))
        return next(results)

    monkeypatch.setattr(harness, "run_in_container_unchecked", run_in_container_unchecked)

    result = harness.remove_cluster_member_with_retry(
        "node-wrk0", "node-wrk3", force=True, attempts=3, interval=0, timeout=120
    )

    assert result == _Res(1, "", 'Error: cluster member "node-wrk3" not found')
    assert calls == [
        ("node-wrk0", "microceph cluster remove node-wrk3 --force", 120, False),
        ("node-wrk0", "microceph cluster remove node-wrk3 --force", 120, False),
    ]


def test_remove_cluster_member_with_retry_rejects_initial_not_found(monkeypatch):
    harness = H()
    result = _Res(1, "", 'Error: cluster member "node-wrk3" not found')
    calls = []

    def run_in_container_unchecked(container, cmd, timeout, quiet):
        calls.append((container, cmd, timeout, quiet))
        return result

    monkeypatch.setattr(harness, "run_in_container_unchecked", run_in_container_unchecked)

    with pytest.raises(AssertionError, match="Command failed after 3 attempts"):
        harness.remove_cluster_member_with_retry(
            "node-wrk0", "node-wrk3", force=False, attempts=3, interval=0, timeout=120
        )

    assert calls == [
        ("node-wrk0", "microceph cluster remove node-wrk3", 120, False),
        ("node-wrk0", "microceph cluster remove node-wrk3", 120, False),
        ("node-wrk0", "microceph cluster remove node-wrk3", 120, False),
    ]


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

    def fake_exec(container, argv, timeout, vm_name=None):
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
    monkeypatch.setattr(
        h, "run_in_vm", lambda cmd, timeout, quiet=False, vm_name=None: vm_calls.append((cmd, timeout, quiet, vm_name))
    )
    monkeypatch.setattr(
        h, "exec_in_container",
        lambda container, *argv, timeout, quiet: ct_calls.append((container, argv, timeout, quiet)),
    )

    h._preflight_exec("", ["curl", "-H", "Snap-Device-Series: 16", "http://x/"], 20)
    h._preflight_exec("node-wrk0", ["curl", "http://x/"], 20)
    h._preflight_exec("", ["curl", "http://x/"], 20, vm_name="guest-vm")

    assert vm_calls == [
        ("curl -H 'Snap-Device-Series: 16' http://x/", 20, True, None),
        ("curl http://x/", 20, True, "guest-vm"),
    ]
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
    events = []

    def fake_run_in_vm(cmd, timeout=300, quiet=False, vm_name=None):
        events.append(("vm", cmd, timeout))
        return _Res(0, snap_list_count + "\n" if "snap list" in cmd else "", "")

    def fake_run_in_container(container, cmd, timeout=300, shell="sh", quiet=False):
        events.append(("ct", container, cmd, timeout))
        return _Res(0, "", "")

    def fake_exec_in_container(container, *argv, timeout=300, check=False, quiet=False):
        events.append(("exec", container, argv))
        return _Res(0, "", "")

    monkeypatch.setattr(
        h, "probe_instance_network", lambda container="", *extra, vm_name=None: events.append(("probe", container))
    )
    monkeypatch.setattr(h, "run_in_vm", fake_run_in_vm)
    monkeypatch.setattr(h, "run_in_vm_and_check", fake_run_in_vm)
    monkeypatch.setattr(h, "run_in_container_unchecked", fake_run_in_container)
    monkeypatch.setattr(h, "run_in_container_and_check", fake_run_in_container)
    monkeypatch.setattr(h, "exec_in_container", fake_exec_in_container)
    monkeypatch.setattr(
        h, "run_in_vm_with_snap_retry",
        lambda cmd, timeout=300, vm_name=None: events.append(("vm-snap-retry", cmd, timeout)),
    )
    monkeypatch.setattr(
        h, "run_in_container_with_snap_retry",
        lambda container, cmd, timeout=300, shell="sh": events.append(("ct-snap-retry", container, cmd, timeout)),
    )
    # Stubbed below apt_update / apt_install, so call-site tests see the exact apt-get
    # string those methods build (flags included) and the target they aim it at.
    monkeypatch.setattr(
        h, "_run_apt",
        lambda container, cmd, timeout, label, vm_name=None: events.append(("apt", container, cmd, timeout)),
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
        h, "_run_apt",
        lambda container, cmd, timeout, label, vm_name=None: calls.append((container, cmd, timeout, label)),
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

@pytest.mark.parametrize("container,installed", [("", True), ("node-wrk0", False)])
def test_local_artifact_snapd_channel_is_prepared_in_the_guest(monkeypatch, container, installed):
    h, events = _recording_harness(monkeypatch)
    assert callable(getattr(h, "prepare_snapd_for_local_snap", None)), "missing guest snapd preparation"
    monkeypatch.setenv("MICROCEPH_TEST_SNAPD_CHANNEL", "latest/candidate")
    def execute(command, timeout, quiet=False):
        events.append(("query", container, command))
        return _Res(0 if installed or "wait" in command else 1, "", "")
    monkeypatch.setattr(h, "run_in_vm", execute)
    monkeypatch.setattr(h, "run_in_container_unchecked", lambda node, command, timeout, quiet=False: execute(command, timeout, quiet))
    h.prepare_snapd_for_local_snap(container)
    action = "refresh" if installed else "install"
    expected = ("ct-snap-retry", container) if container else ("vm-snap-retry",)
    assert events[-1] == (*expected, f"sudo snap {action} snapd --channel=latest/candidate", 300)
    assert events[0] == ("query", container, "sudo snap wait system seed.loaded")


def test_local_artifact_snapd_preparation_targets_explicit_vm(monkeypatch):
    h, events = _recording_harness(monkeypatch)
    monkeypatch.setenv("MICROCEPH_TEST_SNAPD_CHANNEL", "latest/candidate")

    def query(command, timeout, quiet=False, vm_name=None):
        events.append(("query", vm_name, command))
        return _Res(0, "", "")

    def install(command, timeout, vm_name=None):
        events.append(("install", vm_name, command))
        return _Res(0, "", "")

    monkeypatch.setattr(h, "run_in_vm", query)
    monkeypatch.setattr(h, "run_in_vm_with_snap_retry", install)
    monkeypatch.setattr(h, "run_in_vm_and_check", install)
    h.install_microceph_from_local_snap("candidate.snap", vm_name="rgw-node")
    assert events
    assert all(event[1] == "rgw-node" for event in events)
    snapd = next(i for i, event in enumerate(events) if "snap refresh snapd" in event[2])
    artifact = next(i for i, event in enumerate(events) if "snap install --dangerous" in event[2])
    assert snapd < artifact


def test_local_artifact_snapd_preparation_is_opt_in(monkeypatch):
    h, events = _recording_harness(monkeypatch)
    assert callable(getattr(h, "prepare_snapd_for_local_snap", None)), "missing guest snapd preparation"
    monkeypatch.delenv("MICROCEPH_TEST_SNAPD_CHANNEL", raising=False)
    h.prepare_snapd_for_local_snap()
    assert events == []


@pytest.mark.parametrize("path", ["vm", "builder", "upgrade"])
def test_local_snap_install_paths_prepare_snapd_before_artifact(monkeypatch, path):
    h, events = _recording_harness(monkeypatch)
    assert callable(getattr(h, "prepare_snapd_for_local_snap", None)), "missing guest snapd preparation"
    monkeypatch.setattr(h, "prepare_snapd_for_local_snap", lambda container="", channel=None: events.append(("snapd", container)))
    monkeypatch.setattr(h, "_poll_until", lambda *args, **kwargs: None)
    if path == "vm":
        h.install_microceph_from_local_snap("candidate.snap")
    elif path == "builder":
        h.build_base_lxd_image("/root")
    else:
        h.upgrade_multi_node()
    installs = [(i, e) for i, e in enumerate(events) if e[0] in ("vm-snap-retry", "ct-snap-retry") and "snap install --dangerous" in e[-2]]
    assert installs
    assert sum(e[0] == "snapd" for e in events) == len(installs)
    for index, event in installs:
        target = event[1] if event[0] == "ct-snap-retry" else ""
        assert ("snapd", target) in events[:index]


@pytest.mark.parametrize("channel,installed", [("", True), ("latest/candidate", True), ("latest/candidate", False)])
def test_dsl_snapd_preparation_stays_in_the_vm(monkeypatch, tmp_path, channel, installed):
    import subprocess
    monkeypatch.setenv("MICROCEPH_TEST_SNAPD_CHANNEL", channel)
    script = Path(__file__).resolve().parents[2] / "scripts/test_dsl_functest.sh"
    trace = tmp_path / "calls"
    result = subprocess.run(["bash", "-c", '''
source "$1"
TRACE_FILE="$2"
INSTALLED="$3"
log() { :; }
vm_exec() {
    printf '%s\\n' "$*" >> "$TRACE_FILE"
    if [[ "$*" == "snap list snapd" && "$INSTALLED" == "false" ]]; then return 1; fi
}
declare -F prepare_snapd_for_local_snap >/dev/null || { echo 'missing DSL snapd preparation'; exit 1; }
prepare_snapd_for_local_snap
''', "test", str(script), str(trace), str(installed).lower()], capture_output=True, text=True)
    assert result.returncode == 0, result.stdout + result.stderr
    calls = trace.read_text().splitlines() if trace.exists() else []
    expected = ["snap wait system seed.loaded", "snap list snapd", f"snap {'refresh' if installed else 'install'} snapd --channel={channel}"] if channel else []
    assert calls == expected


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
# wait_for_cluster_members_in_vm (must decide via cluster_member_names, not a
# substring search over the raw `microceph status` text)
# ---------------------------------------------------------------------------

def test_wait_for_cluster_members_in_vm_rejects_prefix_match(monkeypatch):
    # Only "rgw-mvm-first-2" is actually a member; a substring search would
    # wrongly report "rgw-mvm-first" present too.
    h = H()
    status_text = (
        "MicroCeph deployment summary:\n"
        "- rgw-mvm-first-2 (10.0.0.12)\n"
        "  Services: osd\n"
        "  Disks: 1\n"
    )
    monkeypatch.setattr(h, "run_in_vm", lambda *a, **k: _Res(0, status_text, ""))
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    with pytest.raises(AssertionError) as exc:
        h.wait_for_cluster_members_in_vm("rgw-mvm-first", tries=1)
    assert "rgw-mvm-first" in str(exc.value)


def test_wait_for_cluster_members_in_vm_succeeds_on_exact_names(monkeypatch):
    h = H()
    status_text = (
        "MicroCeph deployment summary:\n"
        "- rgw-mvm-first (10.0.0.11)\n"
        "- rgw-mvm-later (10.0.0.13)\n"
    )
    monkeypatch.setattr(h, "run_in_vm", lambda *a, **k: _Res(0, status_text, ""))
    monkeypatch.setattr(_mh.time, "sleep", lambda *_: None)
    # Must not raise: both requested members are exact matches.
    h.wait_for_cluster_members_in_vm("rgw-mvm-first", "rgw-mvm-later", tries=1)


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


def test_resolute_ceph_client_setup_is_shared():
    """The two Resolute-client suites use one common setup implementation."""
    robot_root = Path(__file__).parents[1]
    resource = (robot_root / "resources" / "microceph_harness.resource").read_text()

    assert "${CEPH_PPA}" in resource
    assert "lmlogiudice/ceph-lp2166817-updates" in resource
    assert "Verify Resolute Outer VM" in resource
    assert "Install Ceph Client From PPA" in resource
    assert "sudo add-apt-repository --yes ppa:${CEPH_PPA}" in resource
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


def test_local_snap_install_caches_core26(monkeypatch):
    """Local core26 snap installs prefetch their matching base snap."""
    _with_logger(monkeypatch)
    harness = H()
    commands = []

    def fake_run_in_vm_and_check(command, timeout, quiet=False, vm_name=None):
        commands.append((command, timeout))
        return None

    retried = []
    monkeypatch.setattr(harness, "run_in_vm_and_check", fake_run_in_vm_and_check)
    monkeypatch.setattr(
        harness, "run_in_vm_with_snap_retry",
        lambda command, timeout=300, vm_name=None: retried.append((command, timeout)),
    )

    harness.install_microceph_from_local_snap("/tmp/microceph.snap")

    assert commands[0] == ("sudo snap install core26 || true", 120)
    # The --dangerous install is where a swallowed core26 store error resurfaces.
    assert retried == [("sudo snap install --dangerous ~/microceph_*.snap", 600)]


def test_ceph_mgr_patch_is_checked_against_the_staging_tree():
    """The build validates the patch against the manager module it will patch."""
    repo_root = Path(__file__).parents[3]
    snapcraft = (repo_root / "snap" / "snapcraft.yaml").read_text()
    script = (repo_root / "tests" / "scripts" / "test_ceph_mgr_notify_patch.sh").read_text()
    unit_suite = (Path(__file__).parents[1] / "unit-tests" / "unit_tests.robot").read_text()

    assert 'test_ceph_mgr_notify_patch.sh" "$CRAFT_STAGE"' in snapcraft
    assert 'mgr_module="$staging_dir/share/ceph/mgr/mgr_module.py"' in script
    assert 'cp "$mgr_module"' in script
    assert "dpkg-deb -x" not in script
    assert "cat >" not in script
    assert "Run Ceph Manager Staging Patch Test" not in unit_suite


# Vaultlocker fixture helpers run without root, LXD, Vault or a Robot context.
def _vaultlocker_fixture_module():
    import importlib.util

    path = Path(__file__).parents[2] / "scripts" / "vaultlocker_fixture.py"
    spec = importlib.util.spec_from_file_location("vaultlocker_fixture", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_vaultlocker_snap_revision_is_pinned_without_channel():
    from vaultlocker_ci import snap_install_args

    assert snap_install_args("vaultlocker", "12", "") == [
        "snap", "install", "vaultlocker", "--revision=12",
    ]
    assert snap_install_args("vaultlocker", "", "latest/edge") == [
        "snap", "install", "vaultlocker", "--channel=latest/edge",
    ]


@pytest.mark.parametrize("revision,channel", [("12", "latest/edge"), ("", ""), ("bad", "")])
def test_vaultlocker_snap_selection_rejects_ambiguous_or_invalid_input(revision, channel):
    from vaultlocker_ci import snap_install_args

    with pytest.raises(ValueError):
        snap_install_args("vaultlocker", revision, channel)


def test_vaultlocker_result_whitelists_metadata_and_validates_uuid():
    fixture = _vaultlocker_fixture_module()
    uuid = "32d91bc4-5b4c-43bc-87d4-db08a23c341a"
    mapper = f"/dev/mapper/crypt-{uuid}"
    result = fixture.parse_mapper_result(json.dumps({
        "luks_uuid": uuid, "mapper_path": mapper, "unexpected_secret": "do not return",
    }))
    assert result == {"luks_uuid": uuid, "mapper_path": mapper}


@pytest.mark.parametrize("result", [
    {}, {"luks_uuid": "not-a-uuid", "mapper_path": "/dev/mapper/wrong"},
    {"luks_uuid": "32d91bc4-5b4c-43bc-87d4-db08a23c341a", "mapper_path": "/dev/sda"},
])
def test_vaultlocker_result_rejects_invalid_or_unrelated_devices(result):
    with pytest.raises(ValueError):
        _vaultlocker_fixture_module().parse_mapper_result(json.dumps(result))


def test_vaultlocker_loop_binding_refuses_unowned_devices():
    fixture = _vaultlocker_fixture_module()
    records = {"loopdevices": [{"name": "/dev/loop4", "back-file": "/fixture/fresh.img"}]}
    assert fixture.loop_binding_matches(records, "/dev/loop4", "/fixture/fresh.img")
    assert not fixture.loop_binding_matches(records, "/dev/loop4", "/fixture/other.img")
    assert not fixture.loop_binding_matches(records, "/dev/sda", "/fixture/fresh.img")
    assert not fixture.loop_binding_matches({}, "/dev/loop4", "/fixture/fresh.img")


def test_vaultlocker_failure_never_returns_output_or_command_arguments():
    import subprocess

    secret = "private-token-never-log"
    error = subprocess.CalledProcessError(
        3, ["vaultlocker", secret], output=secret, stderr=secret
    )
    result = _vaultlocker_fixture_module().safe_failure(error)
    assert result == "CalledProcessError (exit 3)"
    assert secret not in result


def test_vaultlocker_private_logging_failure_cannot_expose_exception(monkeypatch, tmp_path, capsys):
    import os
    import sys

    fixture = _vaultlocker_fixture_module()
    secret = "private-token-never-log"

    def fail_setup():
        raise RuntimeError(secret)

    def fail_write(*args, **kwargs):
        raise PermissionError("Cannot write private diagnostics")

    monkeypatch.setattr(fixture, "setup", fail_setup)
    monkeypatch.setattr(fixture, "PRIVATE", tmp_path)
    monkeypatch.setattr(Path, "write_text", fail_write)
    monkeypatch.setattr(sys, "argv", ["fixture", "setup"])
    previous_umask = os.umask(0o077)
    outcome = None
    try:
        fixture.main()
    except BaseException as error:
        outcome = error
    finally:
        os.umask(previous_umask)
    assert isinstance(outcome, SystemExit)
    assert outcome.code == 1
    captured = capsys.readouterr()
    assert "Fixture setup/fresh: RuntimeError" in captured.err
    assert secret not in captured.out + captured.err


class _ScenarioControlServer(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    pass


@pytest.fixture
def rgw_scenario_servers():
    servers = []

    def start(server):
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        servers.append((server, thread))
        return server

    yield start
    for server, thread in servers:
        server.shutdown()
        server.server_close()
        thread.join()


def _run_rgw_scenario(*arguments):
    return subprocess.run(
        [sys.executable, rgw_scenario.__file__, *arguments],
        env={**os.environ, "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost"},
        capture_output=True, text=True, timeout=10,
    )


@pytest.mark.parametrize("scenario", ["handover", "outage", "wrong object"])
def test_rgw_scenario_migration(scenario, tmp_path, rgw_scenario_servers):
    # Complete a handover between the old and new object reads. The PUT stays
    # in flight until the sampler has observed that pair of HTTP responses.
    sampled = threading.Event()
    ready = {"old": scenario != "outage", "new": scenario == "wrong object"}
    requests = 0

    class ObjectHandler(BaseHTTPRequestHandler):
        def do_GET(self):
            nonlocal requests
            gateway = "new" if self.headers["Host"].startswith("localhost:") else "old"
            serves = ready[gateway]
            requests += 1
            if scenario == "handover" and requests == 1:
                ready["new"] = True
                ready["old"] = False
            self.send_response(200 if serves else 503)
            self.end_headers()
            self.wfile.write(b"wrong object" if scenario == "wrong object" else b"expected object")
            if requests >= 2:
                sampled.set()

        def log_message(self, *args):
            pass

    class ControlHandler(BaseHTTPRequestHandler):
        def do_PUT(self):
            self.rfile.read(int(self.headers["Content-Length"]))
            observed = sampled.wait(5)
            self.send_response(200 if observed else 500)
            self.end_headers()
            self.wfile.write(json.dumps({"status_code": 200 if observed else 500}).encode())

        def log_message(self, *args):
            pass

    objects = rgw_scenario_servers(HTTPServer(("127.0.0.1", 0), ObjectHandler))
    socket_path = str(tmp_path / "control.socket")
    rgw_scenario_servers(_ScenarioControlServer(socket_path, ControlHandler))
    result = _run_rgw_scenario(
        "--socket", socket_path, "--timeout", "5", "migration", "{}",
        "127.0.0.1", "localhost", str(objects.server_port), "/object", "expected object",
    )
    assert result.returncode == 0, result.stderr
    result = json.loads(result.stdout)
    assert placement_status.response_code(result["response"]) == 200
    assert result["observation"]["samples"] > 0
    assert result["observation"]["available"] is (scenario == "handover")
    assert result["observation"]["replacement_ready"] is (scenario == "handover")


@pytest.mark.parametrize("winner", ["a", "b"])
def test_rgw_scenario_concurrent_requests_accept_either_winner(winner, tmp_path, rgw_scenario_servers):
    locked = threading.Event()
    rejected = threading.Event()
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def do_PUT(self):
            name = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if name != winner:
                locked.wait(5)
            if lock.acquire(blocking=False):
                locked.set()
                overlapped = rejected.wait(5)
                lock.release()
                code = 200 if overlapped else 500
            else:
                rejected.set()
                code = 409
            self.send_response(code)
            self.end_headers()
            self.wfile.write(json.dumps({"status_code" if code == 200 else "error_code": code}).encode())

        def log_message(self, *args):
            pass

    socket_path = str(tmp_path / "control.socket")
    rgw_scenario_servers(_ScenarioControlServer(socket_path, Handler))
    result = _run_rgw_scenario(
        "--socket", socket_path, "--timeout", "5", "concurrent", '"a"', '"b"', "member-b",
    )
    assert result.returncode == 0, result.stderr
    codes = [placement_status.response_code(response) for response in json.loads(result.stdout)["responses"]]
    assert codes == ([200, 409] if winner == "a" else [409, 200])


@pytest.mark.parametrize("failure", ["connection refused", "timeout"])
def test_rgw_scenario_stops_sampler_when_request_fails(failure, tmp_path, rgw_scenario_servers):
    release = threading.Event()

    class ObjectHandler(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"expected object")

        def log_message(self, *args):
            pass

    class StalledControlHandler(BaseHTTPRequestHandler):
        def do_PUT(self):
            self.rfile.read(int(self.headers["Content-Length"]))
            release.wait(5)

        def log_message(self, *args):
            pass

    objects = rgw_scenario_servers(HTTPServer(("127.0.0.1", 0), ObjectHandler))
    socket_path = str(tmp_path / "control.socket")
    if failure == "timeout":
        rgw_scenario_servers(_ScenarioControlServer(socket_path, StalledControlHandler))
    try:
        # A lost stop signal would leave the non-daemon sampler running and
        # make the foreground process exceed the outer test deadline.
        result = _run_rgw_scenario(
            "--socket", socket_path, "--timeout", "0.25", "migration", "{}",
            "127.0.0.1", "localhost", str(objects.server_port), "/object", "expected object",
        )
        assert result.returncode != 0
        assert result.stdout == ""
    finally:
        release.set()


# External directory fixtures: assertions remain in the Robot suite.
def test_smb_ad_resource_allowlist_and_wrong_join_are_separate():
    import smb_ad_guest as ad
    docs = ad.resource_documents(['192.0.2.1', '192.0.2.2'], '192.0.2.4', 'PRIVATE')
    good = docs['ad']['resources']
    cluster = next(r for r in good if r['resource_type'] == 'ceph.smb.cluster')
    assert cluster['auth_mode'] == 'active-directory' and cluster['clustering'] == 'always'
    assert cluster['domain_settings']['realm'] == ad.REALM
    assert cluster['custom_dns'] == ['192.0.2.4']
    shares = [r for r in good if r['resource_type'] == 'ceph.smb.share']
    assert {s['share_id'] for s in shares} == {'data', 'read', 'auth'}
    assert all(s['restrict_access'] for s in shares)
    assert shares[0]['login_control'] == [{'name': 'ROBOT\\writers', 'category': 'group', 'access': 'read-write'}]
    assert next(s for s in shares if s['share_id'] == 'read')['readonly'] is True
    wrong = docs['ad-wrong']['resources']
    assert next(r for r in wrong if r['resource_type'] == 'ceph.smb.cluster')['cluster_id'] != cluster['cluster_id']
    assert next(r for r in wrong if r['resource_type'] == 'ceph.smb.join.auth')['auth']['password'] != 'PRIVATE'
    refresh = docs['ad-refresh']
    assert refresh['resource_type'] == 'ceph.smb.join.auth' and refresh['auth']['username'] == 'bob'


def test_smb_ad_negative_cluster_does_not_claim_positive_data_paths():
    import smb_ad_guest as ad
    docs = ad.resource_documents(['192.0.2.1', '192.0.2.2'], '192.0.2.4', 'PRIVATE')
    def paths(kind):
        return {(r['cephfs']['volume'], r['cephfs']['path'])
                for r in docs[kind]['resources'] if r['resource_type'] == 'ceph.smb.share'}
    # Retirement retains mgr resources/earmarks; both logical clusters coexist.
    assert paths('ad') == {('robotsmb', '/ad')}
    assert paths('ad-wrong')
    assert paths('ad-wrong').isdisjoint(paths('ad'))


def test_smb_ad_prepares_isolated_paths_before_and_after_identity_mapping(monkeypatch, tmp_path):
    import sys
    from types import SimpleNamespace
    import smb_ad_guest as ad
    created, ownership, modes, shutdowns = set(), [], [], []
    monkeypatch.setattr(ad, 'BASE', tmp_path)
    (tmp_path / 'ad-wrong.json').write_text(json.dumps(ad.resource_documents(['192.0.2.1'], '192.0.2.4', 'PRIVATE')['ad-wrong']))
    fs = SimpleNamespace(
        conf_set=lambda *args: None,
        mount=lambda: None,
        mkdirs=lambda path, mode: created.add(path),
        chown=lambda *args: ownership.append(args),
        chmod=lambda *args: modes.append(args),
        shutdown=lambda: shutdowns.append(True),
    )
    monkeypatch.setitem(sys.modules, 'cephfs', SimpleNamespace(LibCephFS=lambda **kwargs: fs))
    monkeypatch.setenv('SNAP_DATA', '/fixture/snap-data')
    for uid, gid in ((0, 0), (2201, 2203)):
        ad.prepare_data(uid, gid)
        assert created == {'/ad', '/ad-wrong'}
        assert ('/ad', uid, gid) in ownership
        assert ('/ad', 0o755) in modes
    assert len(shutdowns) == 2


def test_smb_ad_backing_stat_uses_namedtuple_fields(monkeypatch, capsys):
    import hashlib
    import sys
    from collections import namedtuple
    from types import SimpleNamespace
    import smb_ad_guest as ad
    # Match the independently observed packaged LibCephFS.stat return contract.
    fields = ('st_dev st_ino st_mode st_nlink st_uid st_gid st_rdev st_size '
              'st_blksize st_blocks st_atime st_mtime st_ctime').split()
    StatResult = namedtuple('StatResult', fields, defaults=[0] * len(fields))
    closed, shutdowns, reads = [], [], []
    fs = SimpleNamespace(
        conf_set=lambda *args: None,
        mount=lambda: None,
        stat=lambda path: StatResult(st_uid=103103, st_gid=102513, st_mode=0o100640),
        open=lambda path, mode: 17,
        read=lambda *args: reads.append(args) or ad.PAYLOAD,
        close=lambda fd: closed.append(fd),
        shutdown=lambda: shutdowns.append(True),
    )
    monkeypatch.setitem(sys.modules, 'cephfs', SimpleNamespace(LibCephFS=lambda **kwargs: fs))
    monkeypatch.setenv('SNAP_DATA', '/fixture/snap-data')
    ad.data_stat('kerberos.bin')
    assert json.loads(capsys.readouterr().out) == {
        'uid': 103103, 'gid': 102513, 'mode': 0o640,
        'digest': hashlib.sha256(ad.PAYLOAD).hexdigest(),
    }
    assert reads == [(17, 0, len(ad.PAYLOAD) + 1)]
    assert closed == [17] and shutdowns == [True]


def test_smb_ad_protocols_cannot_fallback_or_leak_passwords():
    import smb_ad_guest as ad
    kerberos = ad.client_argv('192.0.2.2', 'data', 'alice', 'kerberos', 'get sentinel.bin /tmp/read')
    assert '//robot-ad.robot.test/data' in kerberos and '--use-kerberos=required' in kerberos
    assert '-I' in kerberos and '192.0.2.2' in kerberos and '-A' not in kerberos
    # Samba's -N marks a NULL password as specified, invalidating the cache in POST.
    assert '-N' not in kerberos and '--no-pass' not in kerberos
    assert '--use-krb5-ccache=FILE:' + str(ad.BASE / 'client.ccache') in kerberos
    assert '-U' not in kerberos and '--password' not in kerberos
    ntlm = ad.client_argv('192.0.2.2', 'auth', 'bob', 'ntlm', 'ls')
    assert '--use-kerberos=off' in ntlm and '--option=client ntlmv2 auth=yes' in ntlm
    assert '-A' in ntlm and '-U' not in ntlm
    import pytest
    with pytest.raises(ValueError):
        ad.client_argv('192.0.2.2', 'data', 'alice', 'desired', 'ls')
    with pytest.raises(ValueError):
        ad.client_argv('192.0.2.2', 'data', 'alice%secret', 'ntlm', 'ls')


def test_smb_ad_mapping_parser_requires_actual_sid_uid_gid_and_group():
    import smb_ad_guest as ad
    import pytest
    raw = {'alice': ['S-1-5-21-1-2-3-1101 SID_USER (1)', 'ROBOT\\alice:*:2201:2200::/:/bin/false'],
           'bob': ['S-1-5-21-1-2-3-1102 SID_USER (1)', 'ROBOT\\bob:*:2202:2200::/:/bin/false'],
           'writers': ['S-1-5-21-1-2-3-1103 SID_DOM_GROUP (2)', 'ROBOT\\writers:x:2203:']}
    value = ad.parse_mappings(raw)
    assert value['alice'] == {'sid': 'S-1-5-21-1-2-3-1101', 'uid': 2201, 'gid': 2200}
    assert value['writers']['gid'] == 2203
    raw['alice'][1] = ''
    with pytest.raises(ValueError):
        ad.parse_mappings(raw)


def test_smb_ad_readiness_includes_winbind():
    import copy
    m = _smb_module()
    state = _smb_snapshot()
    state['context']['ad_member'] = {'realm': 'ROBOT.TEST'}
    assert not m.snapshot_ready(state, 'robot-smb')
    state['roles']['winbindd'] = copy.deepcopy(state['roles']['smbd'])
    assert m.snapshot_ready(state, 'robot-smb')


def test_smb_ad_client_uses_fresh_cache_and_single_io_process(tmp_path, monkeypatch, capsys):
    import smb_ad_guest as ad
    monkeypatch.setattr(ad, 'BASE', tmp_path)
    (tmp_path / 'ad-secrets.json').write_text(json.dumps({'alice': 'PRIVATE', 'bob': 'BOB'}))
    (tmp_path / 'payload.bin').write_bytes(b'payload')
    (tmp_path / 'client.ccache').write_text('stale')
    bin_dir = tmp_path / 'bin'; bin_dir.mkdir()
    (bin_dir / 'kinit').write_text('#!/bin/sh\n[ ! -f "${KRB5CCNAME#FILE:}" ] || exit 9\nread -r pw\n[ "$pw" = PRIVATE ] || exit 8\nprintf fresh > "${KRB5CCNAME#FILE:}"\n')
    (bin_dir / 'smbclient').write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "' + str(tmp_path / 'calls') + '"\necho PRIVATE\nexit 1\n')
    (bin_dir / 'klist').write_text('#!/bin/sh\necho cifs/robot-ad.robot.test@ROBOT.TEST\n')
    for f in bin_dir.iterdir():
        f.chmod(0o700)
    import os
    monkeypatch.setenv('PATH', str(bin_dir) + ':' + os.environ['PATH'])
    ad.client('192.0.2.1', 'data', 'alice', 'kerberos', 'get', 'sentinel.bin')
    value = json.loads(capsys.readouterr().out)
    assert value['rc'] == 1 and 'PRIVATE' not in value['message'] and 'cifs/' in value['ticket']
    assert len((tmp_path / 'calls').read_text().splitlines()) == 1
    assert (tmp_path / 'client.ccache').read_text() == 'fresh'


def test_smb_ad_dc_observes_account_identity_not_trust_secrets():
    import smb_ad_guest as ad
    value = ad.account_values({'objectSid': ['S-1-5-21-1-2-3-1101'], 'pwdLastSet': ['123'], 'msDS-KeyVersionNumber': ['2'], 'unicodePwd': ['SECRET']})
    assert value == {'objectSid': 'S-1-5-21-1-2-3-1101', 'pwdLastSet': '123', 'msDS-KeyVersionNumber': '2'}
    import pytest
    with pytest.raises(ValueError):
        ad.account_values({})


def test_smb_ad_capacity_counts_four_guests(monkeypatch):
    library = _smb_module().smb_ops()
    monkeypatch.setattr(library, '_variable', lambda name, default: default)
    monkeypatch.setattr(library, '_host', lambda *args: json.dumps({'space': {'total': 140 * 1024 ** 3, 'used': 0}}))
    monkeypatch.setattr(Path, 'read_text', lambda *args, **kwargs: 'MemAvailable: 20000000 kB\n')
    import pytest
    with pytest.raises(AssertionError, match='required=146.0 GiB'):
        library.require_smb_ad_capacity()


def test_smb_ad_client_reports_bounded_directory_timeout_without_raw_output(tmp_path, monkeypatch, capsys):
    import smb_ad_guest as ad
    import subprocess
    monkeypatch.setattr(ad, 'BASE', tmp_path)
    (tmp_path / 'ad-secrets.json').write_text('{"alice":"PRIVATE"}')
    def expired(*args, **kwargs):
        raise subprocess.TimeoutExpired(args[0], 65, output='PRIVATE')
    monkeypatch.setattr(ad, 'run', expired)
    ad.client('192.0.2.1', 'data', 'alice', 'ntlm', 'get', 'sentinel.bin')
    result = json.loads(capsys.readouterr().out)
    assert result['rc'] == 124 and result['digest'] == ''
    assert 'PRIVATE' not in repr(result)


def test_smb_ad_resources_use_actual_pinned_mgr_allowlist():
    import os
    import pytest
    import subprocess
    source = os.environ.get('MICROCEPH_SMB_TEST_CEPH_SRC')
    if not source:
        pytest.skip('requires pinned Ceph source via MICROCEPH_SMB_TEST_CEPH_SRC')
    # A package/type alias bypasses only mgr daemon imports in the sparse source;
    # actual load/validation and login-control generation execute unmodified.
    code = '''
import sys, types, typing
from pathlib import Path
src=Path(sys.argv[1])
sys.path[:0]=[str(src/'pybind/mgr'),str(src/'python-common'),sys.argv[2]]
pkg=types.ModuleType('smb');pkg.__path__=[str(src/'pybind/mgr/smb')];sys.modules['smb']=pkg
mgr=types.ModuleType('mgr_module');mgr.HandlerFuncType=typing.Callable;sys.modules['mgr_module']=mgr
from smb.resources import load, Share
from smb.handler import _generate_share_login_control
from smb_ad_guest import resource_documents
for name, doc in resource_documents(['192.0.2.1','192.0.2.2'],'192.0.2.4','private').items():
    for resource in load(doc):
        if isinstance(resource, Share):
            config={'options':{}}
            _generate_share_login_control(resource, config)
            assert config['options']['valid users'] in ('@ROBOT\\\\writers','@ROBOT\\\\readers')
            key='write list' if resource.share_id == 'data' else 'read list'
            assert config['options'][key] == config['options']['valid users']
# Exercise the unmodified manager's ownership check in actual suite order.
# Only filesystem I/O is replaced; retirement deliberately retains earmarks.
from smb.config_store import MemConfigStore
from smb.staging import Staging, cross_check_resource
staging = Staging(MemConfigStore())
earmarks = {}
def set_earmark(path, volume, value):
    earmarks[(volume, path)] = value
resolver = types.SimpleNamespace(
    get_earmark=lambda path, volume: earmarks.get((volume, path)),
    set_earmark=set_earmark,
    check_earmark=lambda value, scope: value.startswith(scope.value + '.'),
)
paths = types.SimpleNamespace(resolve_exists=lambda volume, group, subvolume, path: path)
docs = resource_documents(['192.0.2.1','192.0.2.2'],'192.0.2.4','private')
for kind in ('ad-wrong', 'ad'):
    loaded = list(load(docs[kind]))
    for resource in loaded:
        staging.stage(resource)
    for resource in loaded:
        if isinstance(resource, Share):
            cross_check_resource(resource, staging=staging,
                                 path_resolver=paths, earmark_resolver=resolver)
assert set(earmarks.values()) == {'smb.cluster.robot-bad', 'smb.cluster.robot-ad'}
'''
    result = subprocess.run([os.environ.get('MICROCEPH_SMB_TEST_PYTHON', '/usr/bin/python3'), '-B', '-c', code,
                             source, str(Path(__file__).parent)], capture_output=True, text=True, timeout=20)
    assert result.returncode == 0, result.stderr


def test_smb_ad_partial_setup_diagnostics_do_not_hide_the_original_failure():
    _smb_module().smb_ops().smb_ad_diagnostics()

# Shared SMB fixture prerequisites: exact owned device identity and guest clocks.
def _owned_osd_inventory():
    m = _smb_module()
    instance = {'name': 'node', 'expanded_config': {m.OWNER_KEY: 'owner'},
                'expanded_devices': {'root': {'type': 'disk', 'path': '/', 'pool': 'default'},
                                     'test-data': {'type': 'disk', 'pool': 'default', 'source': 'node-osd'}}}
    volume = {'name': 'node-osd', 'content_type': 'block', 'config': {m.OWNER_KEY: 'owner'}}
    blocks = {'blockdevices': [
        {'name': '/dev/sdb', 'type': 'disk', 'size': 20 * 1024**3, 'serial': 'lxd_root',
         'pkname': None, 'mountpoints': [], 'children': [
             {'name': '/dev/sdb1', 'type': 'part', 'pkname': '/dev/sdb', 'serial': None, 'mountpoints': ['/']}]},
        {'name': '/dev/sdc', 'type': 'disk', 'size': 4 * 1024**3, 'serial': 'lxd_test--data',
         'pkname': None, 'mountpoints': []}]}
    return instance, volume, blocks


def test_smb_osd_selector_uses_owned_attachment_serial_not_letter_or_size():
    m = _smb_module()
    assert callable(getattr(m, 'select_owned_osd', None)), 'missing exact owned OSD selector'
    instance, volume, blocks = _owned_osd_inventory()
    blocks['blockdevices'].append({'name': '/dev/sda', 'type': 'disk', 'size': 4 * 1024**3,
                                   'serial': 'unrelated', 'pkname': None, 'mountpoints': []})
    assert m.select_owned_osd(instance, volume, blocks, 'node', 'node-osd', 'default', 'owner') == '/dev/sdc'


def test_smb_osd_selector_refuses_unsafe_or_unproven_identity():
    import copy
    import pytest
    m = _smb_module()
    assert callable(getattr(m, 'select_owned_osd', None)), 'missing exact owned OSD selector'
    mutations = [
        lambda i, v, b: i['expanded_config'].update({m.OWNER_KEY: 'foreign'}),
        lambda i, v, b: v['config'].update({m.OWNER_KEY: 'foreign'}),
        lambda i, v, b: i['expanded_devices']['test-data'].update(source='other-volume'),
        lambda i, v, b: i['expanded_devices']['test-data'].update(pool='other-pool'),
        lambda i, v, b: i['expanded_devices']['test-data'].update(path='/'),
        lambda i, v, b: v.update(content_type='filesystem'),
        lambda i, v, b: b['blockdevices'][1].update(serial=None),
        lambda i, v, b: b['blockdevices'][1].update(size=20 * 1024**3),
        lambda i, v, b: b['blockdevices'][1].update(type='part', pkname='/dev/sdb'),
        lambda i, v, b: b['blockdevices'][1].update(mountpoints=['/mnt']),
        lambda i, v, b: b['blockdevices'][1].update(children=[{'name': '/dev/sdc1', 'mountpoints': ['/']}]),
        lambda i, v, b: b['blockdevices'].append(copy.deepcopy(b['blockdevices'][1])),
        lambda i, v, b: b['blockdevices'][0]['children'][0].update(mountpoints=[]),
        lambda i, v, b: b['blockdevices'][1].pop('mountpoints'),
        lambda i, v, b: b['blockdevices'][1].pop('pkname'),
        lambda i, v, b: b['blockdevices'][0]['children'].append(b['blockdevices'].pop()),
        lambda i, v, b: b['blockdevices'][1].update(name='/dev/../sdc'),
    ]
    for mutation in mutations:
        instance, volume, blocks = copy.deepcopy(_owned_osd_inventory())
        mutation(instance, volume, blocks)
        with pytest.raises(AssertionError):
            m.select_owned_osd(instance, volume, blocks, 'node', 'node-osd', 'default', 'owner')


def test_smb_osd_path_revalidates_host_attachment_and_guest_inventory():
    from types import SimpleNamespace
    library = _smb_module().smb_ops()
    assert callable(getattr(library, 'smb_osd_path', None)), 'missing per-node OSD discovery'
    instance, volume, blocks = _owned_osd_inventory()
    calls = []
    def command(*args, **kwargs):
        calls.append(args)
        return _Res(0, json.dumps(blocks), '')
    library.owner, library.volumes = 'owner', ['node-osd']
    library.nodes = [SimpleNamespace(name='node', command=command)]
    library._host = lambda *args: json.dumps(instance if '/instances/' in args[-1] else volume)
    assert library.smb_osd_path(0) == '/dev/sdc'
    assert calls[0][0] == 'lsblk' and '--json' in calls[0] and 'SERIAL' in calls[0][-1]
    blocks['blockdevices'][1]['mountpoints'] = ['/mounted-since-first-probe']
    import pytest
    with pytest.raises(AssertionError):
        library.smb_osd_path(0)
    assert len(calls) == 2


def test_smb_guest_clock_diagnostics_are_read_only_without_ntp_readiness(monkeypatch):
    m = _smb_module()
    expected = [('date', '--utc', '--iso-8601=ns'), ('timedatectl',),
                ('chronyc', 'tracking'), ('chronyc', '-n', 'sources', '-v'),
                ('journalctl', '-b', '-u', 'chrony', '--no-pager', '-n', '60')]
    # No selected source, a large remaining correction, or no chrony installation
    # must not replace the actual Ceph clock-health/Kerberos authentication gates.
    for result in (_Res(0, 'NTPSynchronized=no\nStratum: 0\nLeap status: Not synchronised', ''),
                   _Res(0, 'System time: 0.286 seconds slow of NTP time', ''),
                   _Res(127, '', 'chronyc: command not found')):
        node = m._VM('owned-node')
        assert callable(getattr(node, 'observe_time', None)), 'missing read-only clock diagnostics'
        calls = []
        def command(*args, **kwargs):
            calls.append(args)
            assert kwargs == {'timeout': 10}
            return result
        monkeypatch.setattr(node, 'command', command)
        node.observe_time()
        assert calls == expected


def test_smb_guest_clock_diagnostic_timeout_keeps_remaining_evidence(monkeypatch):
    m = _smb_module()
    node = m._VM('owned-node')
    assert callable(getattr(node, 'observe_time', None)), 'missing read-only clock diagnostics'
    calls, warnings = [], []
    def command(*args, **kwargs):
        calls.append(args)
        if args == ('timedatectl',):
            raise TimeoutError('time service unavailable')
        return _Res(0, '', '')
    monkeypatch.setattr(node, 'command', command)
    monkeypatch.setattr(m.logger, 'warn', warnings.append)
    node.observe_time()
    assert len(calls) == 5
    assert calls[-1] == ('journalctl', '-b', '-u', 'chrony', '--no-pager', '-n', '60')
    assert len(warnings) == 1 and 'time service unavailable' in warnings[0]


def test_smb_gateway_and_dc_observe_clocks_without_clock_configuration(tmp_path, monkeypatch):
    import pytest
    m = _smb_module()
    class Observed(Exception):
        pass
    class Node:
        def __init__(self, name):
            self.name = name
        def probe_instance_network(self):
            pass
        def observe_time(self):
            raise Observed()
    monkeypatch.setattr(m, '_VM', Node)
    snap = tmp_path / 'candidate.snap'
    snap.write_bytes(b'test-only')
    for ad in (False, True):
        library = m.smb_ops()
        monkeypatch.setattr(library, '_variable', lambda key, default: str(snap) if key == 'SNAP_PATH' else default)
        monkeypatch.setattr(library, '_capacity', lambda _: None)
        launches, host_calls = [], []
        def host(*args):
            host_calls.append(args)
            return 'local' if args[1:] == ('remote', 'get-default') else '{"local":{}}'
        monkeypatch.setattr(library.harness, 'launch_owned_test_vm', lambda *args, **kwargs: launches.append(kwargs))
        monkeypatch.setattr(library, '_host', host)
        with pytest.raises(Observed):
            (library.provision_smb_ad_directory if ad else library.provision_smb_lab)()
        assert 'cloud_config' not in launches[0]
        assert launches[0]['image'] == 'ubuntu:26.04'
        assert all(args[0] == 'lxc' for args in host_calls)


def test_smb_ceph_health_observation_records_clock_status():
    from types import SimpleNamespace
    library = _smb_module().smb_ops()
    assert callable(getattr(library, 'observe_smb_ceph_health', None)), 'missing Ceph clock gate observation'
    calls = []
    def command(*args, **kwargs):
        calls.append(args)
        return _Res(0, '{"status":"HEALTH_WARN","checks":{"MON_CLOCK_SKEW":{}}}', '')
    library.nodes = [SimpleNamespace(command=command)]
    assert 'MON_CLOCK_SKEW' in library.observe_smb_ceph_health()['checks']
    assert ('microceph.ceph', 'time-sync-status', '-f', 'json') in calls


def test_smb_ad_setup_rechecks_ceph_clock_health_after_directory_preparation():
    from robot.api import get_model
    model = get_model(Path(__file__).parent / 'smb_suite.resource')
    setup = next(block for section in model.sections for block in section.body
                 if getattr(block, 'name', '') == 'SMB Shared Lab Setup' and hasattr(block, 'body'))
    calls = [statement.keyword for statement in setup.body if hasattr(statement, 'keyword')]
    assert calls.index('Require SMB Ceph Health') > calls.index('Provision SMB AD Directory')


# Captured Ceph startup placement transition: phase06, not an AD acceptance pass.
def _smb_ceph_readiness_fixture():
    return json.loads((Path(__file__).parent / 'fixtures' / 'smb_ceph_readiness.json').read_text())


def test_smb_ceph_readiness_rejects_premature_health_ok_during_placement():
    m = _smb_module()
    sample = _smb_ceph_readiness_fixture()
    ready = m.smb_ceph_readiness_signature
    assert ready(sample['health_ok'], sample['clock'], sample['unsettled_map'],
                 sample['pgs'], sample['unsettled_map']) is None
    assert ready(sample['health_warn'], sample['clock'], sample['settled_map'],
                 sample['pgs'], sample['settled_map']) is None
    assert ready(sample['health_ok'], sample['clock'], sample['settled_map'],
                 sample['pgs'], sample['settled_map']) is not None


def test_smb_ceph_readiness_fails_closed_for_incomplete_or_inconsistent_data():
    import copy
    m = _smb_module()
    sample = _smb_ceph_readiness_fixture()
    baseline = [sample['health_ok'], sample['clock'], sample['settled_map'],
                sample['pgs'], copy.deepcopy(sample['settled_map'])]
    cases = []
    for index in range(len(baseline)):
        for value in (None, [], {}, 'invalid'):
            args = copy.deepcopy(baseline)
            args[index] = value
            cases.append(args)
    mutations = [
        (0, lambda d: d.update(checks={'MON_CLOCK_SKEW': {}})),
        (1, lambda d: d.update(time_skew_status={})),
        (1, lambda d: d.pop('timechecks')),
        (1, lambda d: next(iter(d['time_skew_status'].values())).pop('health')),
        (1, lambda d: next(iter(d['time_skew_status'].values())).update(health='HEALTH_WARN')),
        (2, lambda d: d.update(pools=[])),
        (2, lambda d: d.update(osds=[])),
        (2, lambda d: d['osds'][0].update(up=0)),
        (2, lambda d: d['pools'][0].pop('pg_placement_num_target')),
        (2, lambda d: d['pools'][0].update(pg_num_target=2)),
        (2, lambda d: d['pools'][0].update(pg_num_pending=2)),
        (2, lambda d: d['pools'][0].update(pg_num=True)),
        (2, lambda d: d['pools'].append(d['pools'][0])),
        (3, lambda d: d.update(pg_ready=False)),
        (3, lambda d: d.update(pg_stats=[])),
        (3, lambda d: d['pg_stats'].pop()),
        (3, lambda d: d['pg_stats'].append(d['pg_stats'][0])),
        (3, lambda d: d['pg_stats'][0].update(state='peering')),
        (3, lambda d: d['pg_stats'][0].update(pgid='99.0')),
        (3, lambda d: d['pg_stats'][0].update(acting=[])),
        (3, lambda d: d['pg_stats'][0].update(up=[999, 2, 3])),
        (4, lambda d: d.update(epoch=d['epoch'] + 1)),
        (4, lambda d: d.update(fsid='different-cluster')),
    ]
    for index, mutate in mutations:
        args = copy.deepcopy(baseline)
        mutate(args[index])
        if index == 2:
            args[4] = copy.deepcopy(args[2])
        cases.append(args)
    for args in cases:
        assert m.smb_ceph_readiness_signature(*args) is None, args


def _smb_ceph_readiness_node(sample, observations, clock, calls, seconds_per_read=1):
    from types import SimpleNamespace
    state = {'index': 0, 'maps': 0}
    def command(*argv, timeout, **kwargs):
        assert 0 < timeout <= min(30, 300 - clock[0])
        calls.append((argv, timeout))
        clock[0] += min(seconds_per_read, timeout)
        observation = observations[min(state['index'], len(observations) - 1)]
        if argv[1:] == ('osd', 'dump', '-f', 'json'):
            value = observation['map']
            state['maps'] += 1
            if state['maps'] % 2 == 0:
                state['index'] += 1
        elif argv[1:] == ('pg', 'dump', 'pgs_brief', '-f', 'json'):
            value = observation.get('pgs', sample['pgs'])
        elif argv[1:] == ('health', 'detail', '-f', 'json'):
            value = observation['health']
        elif argv[1:] == ('time-sync-status', '-f', 'json'):
            value = sample['clock']
        else:
            raise AssertionError(f'Unexpected readiness command: {argv}')
        return _Res(0, json.dumps(value), '')
    return SimpleNamespace(command=command, wait_for_cluster_health_ok=lambda: None)


def test_smb_ceph_wait_requires_two_coherent_settled_observations(monkeypatch):
    m = _smb_module()
    sample = _smb_ceph_readiness_fixture()
    settled = {'health': sample['health_ok'], 'map': sample['settled_map']}
    observations = [
        {'health': sample['health_ok'], 'map': sample['unsettled_map']},
        {'health': sample['health_warn'], 'map': sample['settled_map']},
        settled,
        dict(settled, pgs={'pg_ready': True, 'pg_stats': []}),
        settled,
        settled,
    ]
    clock, calls = [0], []
    monkeypatch.setattr(m.time, 'monotonic', lambda: clock[0])
    monkeypatch.setattr(m.time, 'sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    library = m.smb_ops()
    library.nodes = [_smb_ceph_readiness_node(sample, observations, clock, calls)]
    assert library.wait_for_smb_ceph_health() == sample['health_ok']
    assert len(calls) == 5 * len(observations)
    assert not any(argv == ('microceph.ceph', 'health') for argv, _ in calls)


def test_smb_ceph_wait_bounds_all_reads_and_sleeps_by_original_budget(monkeypatch):
    import pytest
    m = _smb_module()
    sample = _smb_ceph_readiness_fixture()
    clock, calls = [0], []
    monkeypatch.setattr(m.time, 'monotonic', lambda: clock[0])
    monkeypatch.setattr(m.time, 'sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    library = m.smb_ops()
    library.nodes = [_smb_ceph_readiness_node(sample,
        [{'health': sample['health_ok'], 'map': sample['settled_map']}], clock, calls, seconds_per_read=30)]
    with pytest.raises(AssertionError, match='SMB Ceph readiness'):
        library.wait_for_smb_ceph_health()
    assert clock[0] <= 300
    assert calls[-1][1] < 30


def test_smb_ceph_readiness_json_rejects_malformed_read(monkeypatch):
    import pytest
    from types import SimpleNamespace
    m = _smb_module()
    monkeypatch.setattr(m.time, 'monotonic', lambda: 0)
    library = m.smb_ops()
    library.nodes = [SimpleNamespace(command=lambda *args, **kwargs: _Res(0, '{invalid', ''))]
    with pytest.raises(AssertionError, match='SMB Ceph readiness received malformed JSON'):
        library.wait_for_smb_ceph_health()


def test_smb_ceph_robot_asserts_the_accepted_detailed_observation():
    from robot.api import get_model
    model = get_model(Path(__file__).parent / 'smb_suite.resource')
    gate = next(block for section in model.sections for block in section.body
                if getattr(block, 'name', '') == 'Require SMB Ceph Health' and hasattr(block, 'body'))
    calls = [statement for statement in gate.body if hasattr(statement, 'keyword')]
    assert calls[0].keyword == 'Wait For SMB Ceph Health'
    assert calls[0].assign == ('${health}=',)
    assert all(statement.keyword != 'Observe SMB Ceph Health' for statement in calls)
    assert [statement.keyword for statement in calls[1:]] == ['Should Be Equal', 'Dictionary Should Not Contain Key']


# Shared-lab SMB CI: suites are independent, cases within each stay ordered.
def _smb_runner_module():
    import importlib.util
    path = Path(__file__).parents[1] / 'smb_runner.py'
    assert path.is_file(), 'missing shared-lab SMB coordinator'
    spec = importlib.util.spec_from_file_location('tested_smb_runner', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_smb_shared_runner_plans_all_orders_and_repeats():
    import itertools
    import pytest
    m = _smb_runner_module()
    assert m.suite_plan(['ad', 'singleton', 'clustered'], repeat=2) == ['ad', 'singleton', 'clustered'] * 2
    plan = m.suite_plan(list(m.SUITES), permutations=True)
    assert [tuple(plan[i:i + 3]) for i in range(0, len(plan), 3)] == list(itertools.permutations(m.SUITES))
    for order in ([], ['unknown'], ['ad', 'ad']):
        with pytest.raises(ValueError):
            m.suite_plan(order)
    with pytest.raises(ValueError):
        m.suite_plan(['ad'], repeat=0)


def test_smb_shared_runner_failure_never_becomes_success_and_cleanup_always_runs():
    m = _smb_runner_module()
    calls = []
    def execute(name, ordinal):
        calls.append(('run', name))
        return {'rc': int(name == 'ad')}
    def cleanup(name, ordinal):
        calls.append(('cleanup', name))
    result = m.run_sequence(['ad', 'clustered'], execute, cleanup)
    assert result['rc'] != 0
    assert [r['status'] for r in result['suites']] == ['FAIL', 'PASS']
    assert calls == [('run', 'ad'), ('cleanup', 'ad'), ('run', 'clustered'), ('cleanup', 'clustered')]


def test_smb_shared_runner_uncertain_cleanup_blocks_later_suites():
    m = _smb_runner_module()
    calls = []
    def execute(name, ordinal):
        calls.append(name)
        raise RuntimeError('first failure')
    def cleanup(name, ordinal):
        raise RuntimeError('stop unconfirmed')
    result = m.run_sequence(['ad', 'singleton'], execute, cleanup)
    assert result['rc'] != 0 and calls == ['ad']
    assert [r['status'] for r in result['suites']] == ['FAIL', 'BLOCKED']
    assert 'stop unconfirmed' in result['suites'][0]['cleanup_error']


def test_smb_singleton_resource_inputs_are_self_contained():
    import smb_guest
    docs = smb_guest.resource_documents(['192.0.2.1'], 'PRIVATE')
    for name in ('nonclustered', 'independent'):
        assert any(r['resource_type'] == 'ceph.smb.usersgroups' for r in docs[name]['resources']), 'singleton inputs rely on clustered suite'


def test_smb_ad_negative_execution_namespace_is_explicit_and_ticket_output_is_safe():
    import inspect
    import smb_ad_guest as ad
    assert 'negative_id' in inspect.signature(ad.resource_documents).parameters, 'negative join IDs cannot be reused across executions'
    docs = ad.resource_documents(['192.0.2.1'], '192.0.2.4', 'PRIVATE', negative_id='bad-1234')
    resources = docs['ad-wrong']['resources']
    assert next(r['cluster_id'] for r in resources if r['resource_type'] == 'ceph.smb.cluster') == 'bad-1234'
    assert next(r['auth_id'] for r in resources if r['resource_type'] == 'ceph.smb.join.auth') == 'bad-1234-join'
    assert {r['cephfs']['path'] for r in resources if r['resource_type'] == 'ceph.smb.share'} == {'/ad-wrong-bad-1234'}
    assert callable(getattr(ad, 'ticket_evidence', None)), 'raw ticket listings must remain inside guest'
    listing = 'PRIVATE-CACHE-LISTING\n' + 'cifs/' + ad.SERVICE + '@' + ad.REALM
    assert ad.ticket_evidence(listing) == 'cifs/' + ad.SERVICE + '@' + ad.REALM
    assert 'PRIVATE' not in ad.ticket_evidence(listing)
    assert ad.ticket_evidence('unrelated service ticket') == ''
    assert ad.ticket_evidence('ldap/' + ad.SERVICE + '@' + ad.REALM) == ''


def test_smb_shared_manifest_binds_only_owned_names_without_robot_context(tmp_path):
    import pytest
    m = _smb_module()
    lib = m.smb_ops()
    assert callable(getattr(lib, 'use_smb_lab', None)), 'missing shared manifest binding'
    owner = 'a' * 32
    doc = {'owner': owner, 'pool': 'default', 'nodes': [f'smb-ci-{owner[:8]}-n{i}' for i in range(3)] + [f'smb-ci-{owner[:8]}-dc'],
           'volumes': [f'smb-ci-{owner[:8]}-n{i}-osd' for i in range(3)]}
    path = tmp_path / 'lab.json';path.write_text(json.dumps(doc))
    lib.use_smb_lab(str(path))
    assert len(lib.nodes) == 3 and lib.ad_dc.name.endswith('-dc')
    doc['nodes'][0] = 'foreign-vm';path.write_text(json.dumps(doc))
    with pytest.raises(AssertionError):
        lib.use_smb_lab(str(path))


def test_smb_scenario_retirement_refuses_foreign_owners_and_never_purges_AD(monkeypatch):
    import pytest
    m = _smb_module();lib = m.smb_ops()
    assert callable(getattr(lib, 'retire_smb_scenario', None)), 'missing scenario release'
    calls = []
    class Node:
        name = 'node0'
        def command(self, *args, **kwargs):
            calls.append(args)
            return _Res(0, '', '')
    lib.nodes = [Node()]
    monkeypatch.setattr(lib, 'smb_snapshot', lambda *args: {'owner': 'foreign'})
    with pytest.raises(AssertionError):
        lib.retire_smb_scenario()
    assert not calls
    snapshots = iter([{'owner': 'robot-ad'}, {'owner': '', 'roles': {role: {'ActiveState': 'inactive'} for role in (*m.ROLES, 'winbindd')}}])
    monkeypatch.setattr(lib, 'smb_snapshot', lambda *args: next(snapshots))
    lib.retire_smb_scenario()
    assert calls == [('microceph', 'disable', 'smb', '--cluster-id', 'robot-ad')]


def test_smb_three_suite_partition_has_no_cross_suite_prerequisite():
    from robot.api import TestSuiteBuilder
    root = Path(__file__).parents[1]
    for folder, count in [('smb-singleton-tests', 3), ('smb-clustered-tests', 6), ('smb-ad-tests', 9)]:
        assert (root / folder).is_dir(), 'missing independent SMB suite: ' + folder
        suite = TestSuiteBuilder().build(str(next((root / folder).glob('*.robot'))))
        assert len(suite.tests) == count
        if folder == 'smb-singleton-tests':
            assert all(kw.name != 'Require Clustered Deployment' for test in suite.tests for kw in test.body)


def test_smb_ad_repeated_inputs_archive_old_attempt_and_never_reuse_its_id(tmp_path, monkeypatch):
    import smb_ad_guest as ad
    monkeypatch.setattr(ad, 'BASE', tmp_path)
    (tmp_path / 'ad-secrets.json').write_text(json.dumps({'Administrator': 'ADMIN-PRIVATE', 'alice': 'ALICE', 'bob': 'BOB'}))
    ad.initialize(['192.0.2.1'], '192.0.2.4')
    (tmp_path / 'ad-wrong.result').write_text('original failed outcome')
    ad.initialize(['192.0.2.1'], '192.0.2.4', 'bad-12345678')
    assert (tmp_path / 'history/robot-bad/ad-wrong.result').read_text() == 'original failed outcome'
    first = json.loads((tmp_path / 'ad-wrong.json').read_text())
    ad.initialize(['192.0.2.1'], '192.0.2.4', 'bad-87654321')
    assert json.loads((tmp_path / 'history/bad-12345678/ad-wrong.json').read_text()) == first
    second = json.loads((tmp_path / 'ad-wrong.json').read_text())
    assert first != second
    assert (tmp_path / 'history/robot-bad/ad-wrong.result').read_text() == 'original failed outcome'


def test_smb_shared_runner_counts_skips_missing_cases_and_failures_as_non_success(tmp_path):
    m = _smb_runner_module()
    for statuses, expected in [(['PASS'], 1), (['PASS', 'SKIP'], 1), (['FAIL'], 1), ([], 1)]:
        tests = ''.join('<test><status status="' + status + '"/></test>' for status in statuses)
        (tmp_path / 'output.xml').write_text('<robot><suite>' + tests + '</suite></robot>')
        result = m._result(tmp_path, expected)
        assert (result['rc'] == 0) is (statuses == ['PASS'])


def test_smb_coordinator_allocates_one_fixture_reuses_manifest_and_tears_down_once(tmp_path, monkeypatch):
    import robot
    from types import SimpleNamespace
    m = _smb_runner_module()
    calls = []
    manifests = []
    def execute(target, directory, variables, options, dryrun=False):
        manifest = next(v.split(':', 1)[1] for v in variables if v.startswith('SMB_LAB_MANIFEST:'))
        manifests.append(manifest)
        name = directory.name
        calls.append(name)
        if name == 'setup':
            Path(manifest).write_text('{}')
            count = 1
        else:
            count = m.SUITES[name.split('-', 1)[1]][1]
        directory.mkdir(parents=True, exist_ok=True)
        (directory / 'output.xml').write_text('<robot><suite>' + '<test><status status="PASS"/></test>' * count + '</suite></robot>')
        return 0
    class Owner:
        owner = 'owner'
        ad_dc = SimpleNamespace(name='dc', command=lambda *a, **k: _Res(0, 'active', ''))
        def use_smb_lab(self, *args, **kwargs): calls.append('bind')
        def verify_smb_lab(self): calls.append('verify')
        def assert_smb_scenario_idle(self): calls.append('idle')
        def wait_for_smb_ceph_health(self): return {'status': 'HEALTH_OK'}
        def _host(self, *args): return json.dumps([{'name': 'dc', 'config': {'user.microceph-test-owner': 'owner'}}])
        def destroy_smb_lab(self): calls.append('destroy')
    monkeypatch.setattr(m, '_robot', execute)
    monkeypatch.setattr(_smb_module(), 'smb_ops', Owner)
    monkeypatch.setattr(robot, 'rebot', lambda *args, **kwargs: 0)
    assert m.main(['--outputdir', str(tmp_path), '--repeat', '2']) == 0
    assert calls.count('setup') == 1 and calls.count('destroy') == 1
    assert len(set(manifests)) == 1
    result = json.loads((tmp_path / 'smb-summary.json').read_text())
    assert result['shared_lab_setup_executions'] == 1 and len(result['suites']) == 6
    assert all(row['status'] == 'PASS' for row in result['suites'])


def test_smb_fresh_negative_refuses_existing_namespace_before_mutation(monkeypatch):
    import pytest
    from types import SimpleNamespace
    m = _smb_module();lib = m.smb_ops();calls = []
    def command(*args, **kwargs):
        calls.append(args)
        if args[:3] == ('microceph.ceph', 'smb', 'show'):
            return _Res(0, '{"resources":[{"cluster_id":"already-consumed"}]}', '')
        return _Res(0, '[{"prefsrc":"192.0.2.1"}]', '')
    lib.nodes = [SimpleNamespace(command=command)] * 3
    lib.ad_dc = SimpleNamespace(command=command)
    monkeypatch.setattr(m, 'BuiltIn', lambda: SimpleNamespace(set_suite_variable=lambda *args: None))
    with pytest.raises(AssertionError, match='namespace'):
        lib.fresh_smb_ad_inputs()
    assert not any(args[0] == 'python3' for args in calls)


def test_smb_CI_uses_one_shared_job_and_never_uploads_private_manifest():
    import yaml
    workflow = yaml.safe_load((Path(__file__).parents[3] / '.github/workflows/tests.yml').read_text())
    job = workflow['jobs']['robot-tests']
    entries = [row for row in job['strategy']['matrix']['include'] if row['id'].startswith('smb-')]
    assert len(entries) == 1 and entries[0]['suite'] == 'smb-integration'
    assert entries[0]['runner'] == 'self-hosted-linux-amd64-noble-2xlarge-extra'
    assert entries[0]['lxd_args'] == '--storage-backend=dir'
    assert 'SMB_SNAPD_CHANNEL:latest/candidate' in entries[0]['args']
    paths = next(step['with']['path'] for step in job['steps'] if step.get('name') == 'Upload Robot logs')
    assert 'smb-summary.json' in paths and 'smb-results/*/output.xml' in paths
    assert 'private' not in paths and 'smb-results/**' not in paths

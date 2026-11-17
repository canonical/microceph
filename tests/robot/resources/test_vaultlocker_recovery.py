"""Host-only tests for the lifecycle primitive used by the reboot regression."""

import pytest

from microceph_harness import microceph_harness as H


class Result:
    def __init__(self, rc=0, stdout="", stderr=""):
        self.rc, self.stdout, self.stderr = rc, stdout, stderr


def test_restart_waits_for_agent_and_checks_boot_changed(monkeypatch):
    harness = H()
    ids = iter(("before", "after"))
    calls = []
    monkeypatch.setattr(harness, "_outer_vm", lambda: "owned-test-vm")
    monkeypatch.setattr(harness, "run_in_vm_and_check", lambda *a: Result(stdout=next(ids)))
    monkeypatch.setattr(harness, "_exec", lambda *a: calls.append(a) or Result())
    monkeypatch.setattr(harness, "wait_for_vm_agent", lambda vm: calls.append(vm))
    assert harness.restart_outer_test_vm() == "after"
    assert calls == [(["lxc", "restart", "owned-test-vm"], 180), "owned-test-vm"]


def test_restart_fails_if_boot_id_does_not_change(monkeypatch):
    harness = H()
    monkeypatch.setattr(harness, "_outer_vm", lambda: "owned-test-vm")
    monkeypatch.setattr(harness, "run_in_vm_and_check", lambda *a: Result(stdout="same"))
    monkeypatch.setattr(harness, "_exec", lambda *a: Result())
    monkeypatch.setattr(harness, "wait_for_vm_agent", lambda vm: None)
    with pytest.raises(AssertionError, match="boot ID"):
        harness.restart_outer_test_vm()


def test_mapper_sampler_keeps_results_with_shared_void_poller(monkeypatch):
    import vaultlocker_ci as module
    from types import SimpleNamespace

    helper = module.vaultlocker_ci()

    def poll(predicate, **kwargs):
        for _ in range(19):
            if predicate():
                return  # The shared poller deliberately returns no predicate value.

    helper._harness = SimpleNamespace(_poll_until=poll)
    monkeypatch.setattr(helper, "run_vaultlocker_fixture", lambda *a: {"locked": True})
    result = helper.sample_locked_vaultlocker_mapper()
    assert result["samples"] == [True] * 19


def test_object_waiter_keeps_results_with_shared_void_poller(monkeypatch):
    import vaultlocker_ci as module
    from types import SimpleNamespace

    helper = module.vaultlocker_ci()

    def poll(predicate, **kwargs):
        assert predicate()

    helper._harness = SimpleNamespace(_poll_until=poll)
    monkeypatch.setattr(helper, "run_vaultlocker_fixture", lambda *a: {"restored_bytes": 1048576})
    assert helper.wait_for_vaultlocker_object() == {"restored_bytes": 1048576}

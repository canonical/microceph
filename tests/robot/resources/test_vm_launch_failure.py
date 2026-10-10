"""Regression tests for outer VM teardown after launch and readiness failures."""

from collections import namedtuple

import pytest

import microceph_harness as harness_module


Result = namedtuple("Result", "rc stdout stderr")
REQUESTED_VM = "microceph-requested-vm"
DEFAULT_VM = "microceph-test-vm"


def _failing_launch(monkeypatch, fail_at):
    variables = {"${OUTER_VM}": DEFAULT_VM}
    calls = []

    class FakeBuiltIn:
        def get_variable_value(self, name, default=None):
            return variables.get(name, default)

        def set_suite_variable(self, name, value):
            variables[name] = value

    monkeypatch.setattr(harness_module, "BuiltIn", FakeBuiltIn)
    monkeypatch.setattr(harness_module.time, "sleep", lambda _: None)
    harness = harness_module.microceph_harness()
    monkeypatch.setattr(harness, "require_host_commands", lambda *_: None)
    monkeypatch.setattr(harness, "collect_microceph_diagnostics", lambda: None)
    monkeypatch.setattr(harness, "detach_loop_devices", lambda: None)

    def delete_instance(name):
        calls.append(("delete_synced", name))
        if fail_at == "delete":
            raise AssertionError("pre-launch deletion failed")

    def wait_for_agent(name):
        calls.append(("agent", name))
        if fail_at == "agent":
            raise AssertionError("VM agent unavailable")

    def exec_lxc(argv, timeout):
        calls.append(tuple(argv))
        if argv[:2] == ["lxc", "launch"]:
            return Result(1 if fail_at == "launch" else 0, "", "launch failed")
        if argv[:2] == ["lxc", "exec"]:
            assert argv == ["lxc", "exec", "-n", REQUESTED_VM, "--", "cloud-init", "status", "--wait"]
            return Result(1 if fail_at == "cloud-init" else 0, "", "cloud-init failed")
        if argv[:2] in (["lxc", "stop"], ["lxc", "delete"]):
            return Result(0, "", "")
        raise AssertionError(f"unexpected lxc command: {argv}")

    monkeypatch.setattr(harness, "_delete_instance_synced", delete_instance)
    monkeypatch.setattr(harness, "wait_for_vm_agent", wait_for_agent)
    monkeypatch.setattr(harness, "_exec", exec_lxc)
    return harness, variables, calls


@pytest.mark.parametrize("fail_at", ["agent", "cloud-init"])
def test_readiness_failure_tears_down_only_requested_vm(monkeypatch, fail_at):
    harness, variables, calls = _failing_launch(monkeypatch, fail_at)

    with pytest.raises(AssertionError, match="VM agent unavailable|cloud-init failed"):
        harness.launch_outer_test_vm(vm_name=REQUESTED_VM)

    assert variables["${OUTER_VM}"] == REQUESTED_VM
    assert ("agent", REQUESTED_VM) in calls
    assert any(call[:2] == ("lxc", "launch") and call[3] == REQUESTED_VM for call in calls)
    if fail_at == "agent":
        assert not any(call[:2] == ("lxc", "exec") for call in calls)
    else:
        assert any(call[:2] == ("lxc", "exec") for call in calls)

    harness.teardown_microceph_environment()
    assert calls[-2:] == [
        ("lxc", "stop", REQUESTED_VM, "--force"),
        ("lxc", "delete", REQUESTED_VM, "--force"),
    ]
    assert not any(DEFAULT_VM in call for call in calls)


@pytest.mark.parametrize("fail_at", ["delete", "launch"])
def test_pre_readiness_failure_never_tears_down_previous_vm(monkeypatch, fail_at):
    harness, variables, calls = _failing_launch(monkeypatch, fail_at)

    with pytest.raises(AssertionError, match="pre-launch deletion failed|Failed to launch VM"):
        harness.launch_outer_test_vm(vm_name=REQUESTED_VM)

    assert variables["${OUTER_VM}"] == REQUESTED_VM
    if fail_at == "delete":
        assert not any(call[:2] == ("lxc", "launch") for call in calls)
    else:
        assert sum(call[:2] == ("lxc", "launch") for call in calls) == 3
    harness.teardown_microceph_environment()
    assert calls[-2:] == [
        ("lxc", "stop", REQUESTED_VM, "--force"),
        ("lxc", "delete", REQUESTED_VM, "--force"),
    ]
    assert not any(DEFAULT_VM in call for call in calls)

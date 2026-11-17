"""Reboot fixture recovery must preserve ownership and never re-encrypt devices."""

import importlib.util
import json
from pathlib import Path

import pytest

spec = importlib.util.spec_from_file_location(
    "reboot_fixture", Path(__file__).with_name("vaultlocker_fixture.py")
)
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


@pytest.fixture
def environment(tmp_path, monkeypatch):
    common = tmp_path / "common"
    common.mkdir()
    private = tmp_path / "run"
    private.mkdir()
    links = tmp_path / "by-id"
    links.mkdir()
    state = {
        "token": "private-test-token",
        "unseal_key": "private-test-key",
        "devices": {"fresh": "/dev/loop9"},
        "mappers": {
            "fresh": {"luks_uuid": "test-uuid", "mapper_path": "/dev/mapper/crypt-test-uuid"}
        },
    }
    (common / "fresh.img").touch()
    monkeypatch.setattr(fixture, "COMMON", common)
    monkeypatch.setattr(fixture, "PRIVATE", private)
    monkeypatch.setattr(fixture, "STATE", private / "state.json")
    monkeypatch.setattr(fixture, "REBOOT_STATE", common / "reboot-state.json", raising=False)
    monkeypatch.setattr(fixture, "REBOOT_UNIT", tmp_path / "reboot.service", raising=False)
    monkeypatch.setattr(fixture, "BY_ID", links, raising=False)
    fixture.save(state)
    calls = []

    def run(*args, **kwargs):
        calls.append(args)
        if args[0] == "losetup":
            if "--json" in args:
                return '{"loopdevices": []}'
            return "/dev/loop42\n"
        if args[0] == "blkid":
            return "test-uuid\n"
        return ""

    monkeypatch.setattr(fixture, "run", run)
    return state, calls


def test_arm_reboot_persists_private_state_without_exporting_it(environment):
    state, calls = environment
    assert fixture.arm_reboot(state) == {"armed": True}
    assert json.loads(fixture.REBOOT_STATE.read_text()) == state
    assert fixture.REBOOT_STATE.stat().st_mode & 0o777 == 0o600
    assert state["token"] not in fixture.REBOOT_UNIT.read_text()
    assert state["unseal_key"] not in fixture.REBOOT_UNIT.read_text()


def test_restore_rebinds_owned_image_and_stable_alias_without_encrypting(environment):
    state, calls = environment
    fixture.write_private(fixture.REBOOT_STATE, json.dumps(state))
    fixture.STATE.unlink()
    assert fixture.restore_after_reboot() == {"restored": True}
    restored = json.loads(fixture.STATE.read_text())
    assert restored["devices"]["fresh"] == "/dev/loop42"
    assert (fixture.BY_ID / "microceph-vaultlocker-ci-fresh").readlink() == Path("/dev/loop42")
    assert all(command[0] not in ("vaultlocker", "cryptsetup") for command in calls)


def test_restore_rejects_luks_uuid_mismatch(environment, monkeypatch):
    state, _ = environment
    fixture.write_private(fixture.REBOOT_STATE, json.dumps(state))
    original = fixture.run
    monkeypatch.setattr(
        fixture, "run", lambda *a, **kw: "wrong-uuid" if a[0] == "blkid" else original(*a, **kw)
    )
    with pytest.raises(ValueError, match="UUID"):
        fixture.restore_after_reboot()

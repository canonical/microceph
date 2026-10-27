"""Host-only wrapper dispatch; fixtures never allocate VMs."""
import importlib.util
import json
import os
from pathlib import Path
import sys
from types import SimpleNamespace
import xml.etree.ElementTree as ET

import pytest
# Import before pytest's package setup can cache tests/robot as `robot`.
from robot.api import ExecutionResult

HERE = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def full_fixture(monkeypatch, tmp_path):
    full = load("microceph_full_runner", HERE / "full_runner.py")
    coordinator = load("microceph_smb_coordinator", HERE / "smb_runner.py")
    root = tmp_path / "robot"
    for name, tests in (("aa-before", "Before"), ("zz-after", "After")):
        directory = root / name
        directory.mkdir(parents=True)
        (directory / "scenario.robot").write_text("*** Settings ***\nTest Tags    ordinary\n*** Test Cases ***\n" + tests + "\n    No Operation\n")
    directory = root / "smb-singleton-tests"
    directory.mkdir()
    (directory / "scenario.robot").write_text("""*** Settings ***
Test Tags    smb
*** Test Cases ***
SMB One
    Should Be Equal    ${SNAP_PATH}    /candidate.snap
    Should Not Be Empty    ${SMB_LAB_MANIFEST}
SMB Two
    Should Be Equal    ${BUDGET}    10
SMB Three
    No Operation
""")
    monkeypatch.setattr(full, "HERE", root)
    monkeypatch.setattr(coordinator, "HERE", root)
    real_run = full._run
    calls = []
    control = {"smb_rc": 0, "ordinary_rc": 0}
    def execute(command):
        calls.append(command)
        if command[2] == str(root / "smb_runner.py"):
            previous = os.umask(0o077)
            try:
                rc = coordinator.main(command[3:])
            finally:
                os.umask(previous)
            return rc or control["smb_rc"]
        return real_run(command) or control["ordinary_rc"]
    monkeypatch.setattr(full, "_run", execute)
    return full, calls, control, root


@pytest.mark.parametrize("selection,count,smb", [
    ([], 5, True), (["-i", "ordinary"], 2, False),
    (["-s", "Robot.Smb-Singleton-Tests"], 3, True),
    (["-t", "Robot.Smb-Singleton-Tests.Scenario.SMB *"], 3, True),
    (["--exclude", "smb"], 2, False),
])
def test_full_real_selection_coordinator_and_aggregation(full_fixture, tmp_path, selection, count, smb):
    full, calls, _, _ = full_fixture
    output = tmp_path / "results"
    rc = full.main(["--snap-path", "/candidate.snap", "-v", "BUDGET:10", *selection,
                    "-d", str(output), "-o", "combined.xml", "-l", "details.html", "-r", "verdict.html", "-x", "junit.xml", "--console", "quiet"])
    assert rc == 0
    assert ExecutionResult(output / "combined.xml").suite.test_count == count
    tests = ET.parse(output / "combined.xml").findall(".//test")
    assert len(tests) == count
    assert all(test.find("status").get("status") == "PASS" for test in tests)
    assert all((output / name).is_file() for name in ("details.html", "verdict.html", "junit.xml"))
    coordinator_calls = [call for call in calls if call[2].endswith("smb_runner.py")]
    assert len(coordinator_calls) == int(smb)
    if smb:
        summary = json.loads((output / "smb-summary.json").read_text())
        assert summary["cases"] == 3 and summary["passed"] == 3 and summary["rc"] == 0
        assert not summary["dryrun"]
        assert "--suite" not in coordinator_calls[0] and "--test" not in coordinator_calls[0]
    # Suite order remains ordinary-before / singleton / ordinary-after.
    assert [test.get("name") for test in tests] == (["Before"] if count in (2, 5) else []) + (["SMB One", "SMB Two", "SMB Three"] if smb else []) + (["After"] if count in (2, 5) else [])


@pytest.mark.parametrize("directory,names", [
    ("aa-before", ["Before"]),
    ("zz-after", ["After"]),
    ("smb-singleton-tests", ["SMB One", "SMB Two", "SMB Three"]),
])
def test_full_parseinclude_directory_preserves_selection(full_fixture, tmp_path, directory, names):
    full, calls, _, root = full_fixture
    output = tmp_path / "filtered-results"
    rc = full.main(["--snap-path", "/candidate.snap", "-v", "BUDGET:10",
                    "--parseinclude", str(root / directory),
                    "-d", str(output), "--console", "quiet"])
    assert rc == 0
    tests = ET.parse(output / "output.xml").findall(".//test")
    assert [test.get("name") for test in tests] == names
    assert all(test.find("status").get("status") == "PASS" for test in tests)
    assert sum(call[2].endswith("smb_runner.py") for call in calls) == int(directory == "smb-singleton-tests")


@pytest.mark.parametrize("filtered", [False, True])
def test_full_incomplete_smb_discovery_never_allocates(full_fixture, tmp_path, filtered):
    full, calls, _, root = full_fixture
    source = root / "smb-singleton-tests/scenario.robot"
    source.write_text(source.read_text().replace("SMB Three\n    No Operation\n", ""))
    selection = ["--parseinclude", str(source.parent)] if filtered else []
    assert full.main([*selection, "-d", str(tmp_path / "incomplete-results"), "--console", "quiet"]) == 252
    assert calls == []


@pytest.mark.parametrize("selection", [["-t", "*SMB One"], ["-i", "nothing"], ["--not-a-robot-option"]])
def test_full_partial_empty_and_discovery_errors_never_allocate(full_fixture, tmp_path, selection):
    full, calls, _, _ = full_fixture
    assert full.main([*selection, "-d", str(tmp_path / "results")]) == 252
    assert calls == []


def test_full_intentionally_empty_and_dryrun_semantics(full_fixture, tmp_path):
    full, calls, _, root = full_fixture
    empty = tmp_path / "empty"
    assert full.main(["-i", "nothing", "--runemptysuite", "-d", str(empty), "--console", "quiet"]) == 0
    assert not ET.parse(empty / "output.xml").findall(".//test")
    assert not any(call[2].endswith("smb_runner.py") for call in calls)
    # A setup that always fails must not execute in dry-run discovery.
    source = root / "smb-singleton-tests/scenario.robot"
    source.write_text(source.read_text().replace("Test Tags    smb", "Test Tags    smb\nSuite Setup    Fail    not a dry-run"))
    dry = tmp_path / "dry"
    assert full.main(["--dryrun", "-d", str(dry), "--output", "NONE", "--log", "NONE", "--report", "NONE", "--console", "quiet"]) == 0
    assert json.loads((dry / "smb-summary.json").read_text())["dryrun"] is True
    assert not any((dry / name).exists() for name in ("output.xml", "log.html", "report.html"))


@pytest.mark.parametrize("failure,exit_early,count", [("smb_rc", False, 5), ("ordinary_rc", False, 5), ("ordinary_rc", True, 1), ("smb_rc", True, 4)])
def test_full_subprocess_and_cleanup_verdicts_cannot_turn_green(full_fixture, tmp_path, failure, exit_early, count):
    full, _, control, _ = full_fixture
    control[failure] = 7
    output = tmp_path / "results"
    args = ["--snap-path", "/candidate.snap", "-v", "BUDGET:10", "-d", str(output), "--console", "quiet"]
    if exit_early:
        args.append("--exitonfailure")
    assert full.main(args) == 7
    assert len(ET.parse(output / "output.xml").findall(".//test")) == count


def test_full_rebot_failure_is_not_ignored(full_fixture, monkeypatch, tmp_path):
    full, _, _, _ = full_fixture
    monkeypatch.setattr(full.ResultWriter, "write_results", lambda *args, **kwargs: 9)
    assert full.main(["--dryrun", "-d", str(tmp_path / "results"), "--console", "quiet"]) == 9


@pytest.mark.parametrize("selection", [[], ["--all"]])
def test_default_and_all_use_owned_full_runner(monkeypatch, selection):
    wrapper = load("microceph_wrapper", HERE / "robot.py")
    commands = []
    monkeypatch.setattr(sys, "argv", ["robot.py", *selection, "--snap-path", "/candidate.snap", "--dryrun", "--outputdir", "/results"])
    monkeypatch.setattr(wrapper.subprocess, "run", lambda command, **kwargs: commands.append(command) or SimpleNamespace(returncode=7))
    assert wrapper.main() == 7
    assert commands == [[sys.executable, "-u", str(HERE / "full_runner.py"), "--snap-path", "/candidate.snap", "--console", "verbose", "--variable", "SNAP_PATH:/candidate.snap", "--dryrun", "--outputdir", "/results"]]


@pytest.mark.parametrize("selection", ["smb-integration", "smb-singleton-tests", "unit-tests"])
def test_explicit_routes_preserve_arguments(monkeypatch, selection):
    wrapper = load("microceph_wrapper", HERE / "robot.py")
    commands = []
    monkeypatch.setattr(sys, "argv", ["robot.py", "--test-suite", selection, "--snap-path", "/candidate.snap", "--dryrun", "--console", "quiet"])
    monkeypatch.setattr(wrapper.subprocess, "run", lambda command, **kwargs: commands.append(command) or SimpleNamespace(returncode=3))
    assert wrapper.main() == 3
    if selection.startswith("smb"):
        assert commands == [[sys.executable, "-u", str(HERE / "smb_runner.py"), "--snap-path", "/candidate.snap", "--dryrun", "--console", "quiet"]]
    else:
        assert commands == [[sys.executable, "-u", "-m", "robot", "--variable", "SNAP_PATH:/candidate.snap", "--dryrun", "--console", "quiet", str(HERE / selection)]]

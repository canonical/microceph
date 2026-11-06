#!/usr/bin/env python3
"""Own serial singleton/clustered/AD labs and exact installed-case accounting."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
EXPECTED_CASES = 3
SCENARIOS = {"singleton": 3, "clustered": 6, "ad": 9}

# The adjacent CLI wrapper must not shadow the installed Robot Framework.
sys.path[:] = [path for path in sys.path if Path(path or os.getcwd()).resolve() != HERE]
sys.path.insert(0, str(HERE / "resources"))
from robot.errors import DataError, Information
from robot.run import RobotFramework
from robot_discovery import arguments as _arguments, build_suite, case_names


def _preflight(kinds, arguments):
    source = HERE / ("smb-" + kinds[0] + "-tests")
    options, sources = RobotFramework().parse_arguments([*arguments, str(source)])
    if [Path(path).resolve() for path in sources] != [source.resolve()]:
        raise DataError("Only owned complete SMB scenario sources are supported")
    for kind in kinds:
        settings, suite = build_suite(options, [str(HERE / ("smb-" + kind + "-tests"))])
        expected = case_names(suite)
        if suite.test_count != SCENARIOS[kind] or len(expected) != SCENARIOS[kind]:
            raise DataError(f"Discovery must contain the complete {SCENARIOS[kind]}-case SMB {kind} scenario")
        suite.configure(**settings.suite_config)
        if suite.test_count != SCENARIOS[kind] or case_names(suite) != expected:
            raise DataError(f"Select the complete {SCENARIOS[kind]}-case SMB {kind} scenario; partial scenarios cannot allocate a lab")
    return options, settings


def _result(directory, expected=EXPECTED_CASES):
    tests = ET.parse(directory / "output.xml").findall(".//test")
    statuses = [test.find("status").attrib["status"] for test in tests]
    return {"rc": int(len(tests) != expected or any(status != "PASS" for status in statuses)),
            "passed": statuses.count("PASS"), "failed": statuses.count("FAIL"),
            "skipped": statuses.count("SKIP"), "cases": len(tests), "output": str(directory / "output.xml")}


def _robot(directory, variables, options, dryrun):
    directory.mkdir(parents=True, exist_ok=True)
    command = [sys.executable, "-u", "-m", "robot", *options, "--outputdir", str(directory), "--exitonfailure"]
    if dryrun:
        command.append("--dryrun")
    for variable in variables:
        command += ["--variable", variable]
    kind = directory.name
    command.append(str(HERE / ("smb-" + kind + "-tests")))
    # Environment/argument-file options were expanded once during preflight.
    env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1")
    env.pop("ROBOT_OPTIONS", None)
    return subprocess.run(command, cwd=HERE.parents[1], env=env).returncode


def _cleanup(manifest):
    sys.path.insert(0, str(HERE / "resources"))
    from smb_ops import smb_ops
    owner = smb_ops()
    owner.use_smb_lab(str(manifest))
    try:
        owner.assert_smb_scenario_idle()
    finally:
        owner.destroy_smb_lab()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, allow_abbrev=False)
    parser.add_argument("--snap-path", default="")
    parser.add_argument("--suites", default="singleton")
    args, options = parser.parse_known_args(argv)
    kinds = args.suites.split(',')
    if not kinds or len(set(kinds)) != len(kinds) or any(kind not in SCENARIOS for kind in kinds):
        parser.error("Select complete singleton/clustered/AD scenarios without duplicates")
    try:
        parsed, settings = _preflight(kinds, options)
    except Information as error:
        print(error)
        return 251
    except (DataError, OSError, ValueError) as error:
        print("SMB Robot preflight failed: " + str(error), file=sys.stderr)
        return 252
    options = _arguments(parsed)
    dryrun = settings.dry_run
    os.umask(0o077)
    root = settings.output_directory
    root.mkdir(parents=True, exist_ok=True)
    results = root / "smb-results"
    results.mkdir(exist_ok=False)
    private = results / "private"
    private.mkdir(mode=0o700)
    summary = {"rc": 0, "suite": args.suites, "dryrun": dryrun, "errors": [], "results": []}
    for kind in kinds:
        manifest = private / ("lab.json" if len(kinds) == 1 else kind + ".json")
        variables = ["SNAP_PATH:" + args.snap_path, "SMB_LAB_MANIFEST:" + str(manifest)]
        try:
            rc = _robot(results / kind, variables, options, dryrun)
            result = _result(results / kind, SCENARIOS[kind])
            result['suite'] = kind
            summary['results'].append(result)
            summary['rc'] = int(summary['rc'] != 0 or rc != 0 or result['rc'] != 0)
        except Exception as error:
            summary['rc'] = 1
            summary['errors'].append(str(error))
        finally:
            if not dryrun and manifest.exists():
                try:
                    _cleanup(manifest)
                except Exception as error:
                    summary['errors'].append("Owned lab teardown failed: " + str(error))
            summary['rc'] = int(summary['rc'] != 0 or bool(summary['errors']))
            for key in ('passed', 'failed', 'skipped', 'cases'):
                summary[key] = sum(result[key] for result in summary['results'])
            (root / "smb-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
        if summary['rc']:
            break
    return summary['rc']


if __name__ == "__main__":
    raise SystemExit(main())

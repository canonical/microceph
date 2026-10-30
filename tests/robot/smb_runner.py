#!/usr/bin/env python3
"""Own serial singleton/clustered labs and exact installed-case accounting."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
EXPECTED_CASES = 3
SCENARIOS = {"singleton": 3, "clustered": 6}


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
    return subprocess.run(command, cwd=HERE.parents[1], env=dict(os.environ, PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1")).returncode


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
    parser.add_argument("--dryrun", action="store_true")
    parser.add_argument("--outputdir", default=".")
    parser.add_argument("--suites", default="singleton")
    args, options = parser.parse_known_args(argv)
    kinds = args.suites.split(',')
    if not kinds or len(set(kinds)) != len(kinds) or any(kind not in SCENARIOS for kind in kinds):
        parser.error("Select complete singleton/clustered scenarios without duplicates")
    if any(option.split("=", 1)[0] in ("--test", "--suite", "--repeat", "--permutations") for option in options):
        parser.error("Only complete installed SMB scenarios are supported")
    sys.path[:] = [path for path in sys.path if Path(path or os.getcwd()).resolve() != HERE]
    os.umask(0o077)
    root = Path(args.outputdir).resolve()
    root.mkdir(parents=True, exist_ok=True)
    results = root / "smb-results"
    results.mkdir(exist_ok=False)
    private = results / "private"
    private.mkdir(mode=0o700)
    summary = {"rc": 0, "suite": args.suites, "dryrun": args.dryrun, "errors": [], "results": []}
    for kind in kinds:
        manifest = private / ("lab.json" if len(kinds) == 1 else kind + ".json")
        variables = ["SNAP_PATH:" + args.snap_path, "SMB_LAB_MANIFEST:" + str(manifest)]
        try:
            rc = _robot(results / kind, variables, options, args.dryrun)
            result = _result(results / kind, SCENARIOS[kind])
            result['suite'] = kind
            summary['results'].append(result)
            summary['rc'] = int(summary['rc'] != 0 or rc != 0 or result['rc'] != 0)
        except Exception as error:
            summary['rc'] = 1
            summary['errors'].append(str(error))
        finally:
            if not args.dryrun and manifest.exists():
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

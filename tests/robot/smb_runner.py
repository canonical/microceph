#!/usr/bin/env python3
"""Own one singleton-only lab, private output and exact installed-case accounting."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
EXPECTED_CASES = 3


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
    command.append(str(HERE / "smb-singleton-tests"))
    return subprocess.run(command, cwd=HERE.parents[1], env=dict(os.environ, PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1")).returncode


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, allow_abbrev=False)
    parser.add_argument("--snap-path", default="")
    parser.add_argument("--dryrun", action="store_true")
    parser.add_argument("--outputdir", default=".")
    args, options = parser.parse_known_args(argv)
    if any(option.split("=", 1)[0] in ("--test", "--suite", "--suites", "--repeat", "--permutations") for option in options):
        parser.error("Only the complete singleton scenario is supported")
    # Do not let the adjacent CLI wrapper robot.py shadow Robot Framework.
    sys.path[:] = [path for path in sys.path if Path(path or os.getcwd()).resolve() != HERE]
    os.umask(0o077)
    root = Path(args.outputdir).resolve()
    root.mkdir(parents=True, exist_ok=True)
    results = root / "smb-results"
    results.mkdir(exist_ok=False)
    private = results / "private"
    private.mkdir(mode=0o700)
    manifest = private / "lab.json"
    variables = ["SNAP_PATH:" + args.snap_path, "SMB_LAB_MANIFEST:" + str(manifest)]
    summary = {"rc": 1, "suite": "singleton", "dryrun": args.dryrun, "errors": []}
    try:
        rc = _robot(results / "singleton", variables, options, args.dryrun)
        summary.update(_result(results / "singleton"))
        summary["rc"] = int(rc != 0 or summary["rc"] != 0)
    except Exception as error:
        summary["errors"].append(str(error))
    finally:
        if not args.dryrun and manifest.exists():
            try:
                sys.path.insert(0, str(HERE / "resources"))
                from smb_ops import smb_ops
                owner = smb_ops()
                owner.use_smb_lab(str(manifest))
                # Suite teardown attempted retirement once. Never retry an
                # uncertain stop; fail the verdict, then destroy only owned VMs.
                try:
                    owner.assert_smb_scenario_idle()
                finally:
                    owner.destroy_smb_lab()
            except Exception as error:
                summary["errors"].append("Owned lab teardown failed: " + str(error))
        summary["rc"] = int(summary["rc"] != 0 or bool(summary["errors"]))
        (root / "smb-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    return summary["rc"]


if __name__ == "__main__":
    raise SystemExit(main())

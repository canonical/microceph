#!/usr/bin/env python3
"""Robot Framework CLI wrapper for MicroCeph."""

import subprocess
import sys
import os


def main():
    """Run Robot Framework with MicroCeph-specific defaults."""
    import argparse

    parser = argparse.ArgumentParser(
        description="Robot Framework CLI for MicroCeph",
        add_help=False,
        allow_abbrev=False,
    )
    parser.add_argument("--snap-path", help="Path to the MicroCeph snap file")
    parser.add_argument("--test-suite", help="Test suite to run (relative to tests/robot/)")
    parser.add_argument("--all", action="store_true", help="Run all tests in tests/robot/")
    parser.add_argument("--help", "-h", action="store_true", help="Show this help and exit")

    args, remaining = parser.parse_known_args()

    if args.help:
        parser.print_help()
        return 0

    # Build robot flags (our defaults first, then user pass-through flags).
    # Default --console verbose unless the user already supplied --console.
    robot_flags = []
    if "--console" not in remaining:
        robot_flags.extend(["--console", "verbose"])

    if args.snap_path:
        robot_flags.extend(["--variable", f"SNAP_PATH:{args.snap_path}"])

    # Any extra flags the user passed through (e.g. --console dotted, --loglevel DEBUG).
    robot_flags.extend(remaining)

    # Resolve the target path (always required by robot).
    here = os.path.dirname(__file__)
    if args.test_suite:
        target = os.path.join(here, args.test_suite)
    else:
        # Default to the full suite so an unqualified run exercises everything.
        target = here

    robot_args = robot_flags + [target]
    cmd = [sys.executable, "-u", "-m", "robot"] + robot_args
    print(f"robot {' '.join(robot_args)}", flush=True)
    env = os.environ.copy()
    env["PYTHONUNBUFFERED"] = "1"
    result = subprocess.run(cmd, env=env)
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())

"""Harness regressions; all privileged, service and sleep commands are stubs."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("actionutils.sh")


class RunnerCleanupTests(unittest.TestCase):
    def invoke(self, images="", listing_status=0):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sudo = root / "sudo"
            sudo.write_text("""#!/usr/bin/env bash
case "$1" in
    rm) exit 0 ;;
    docker) shift; exec docker "$@" ;;
    *) exit 99 ;;
esac
""")
            docker = root / "docker"
            docker.write_text("""#!/usr/bin/env bash
case "$1" in
    images)
        if [ "$FAKE_LISTING_STATUS" != 0 ]; then exit "$FAKE_LISTING_STATUS"; fi
        printf '%s' "$FAKE_IMAGES"
        ;;
    rmi)
        printf '%s\\n' "$@" >> "$FAKE_LOG"
        if [ "$#" -lt 2 ]; then exit 1; fi
        ;;
    *) exit 99 ;;
esac
""")
            sudo.chmod(0o755)
            docker.chmod(0o755)
            log = root / "docker.log"
            env = {k: v for k, v in os.environ.items() if not k.startswith("BASH_FUNC_") and k not in ("BASH_ENV", "ENV")}
            env.update(PATH=str(root) + os.pathsep + env["PATH"], FAKE_IMAGES=images, FAKE_LISTING_STATUS=str(listing_status), FAKE_LOG=str(log))
            result = subprocess.run(["bash", str(SCRIPT), "free_runner_disk"], env=env, capture_output=True, text=True, timeout=10)
            calls = log.read_text().splitlines() if log.exists() else []
            return result, calls

    def test_no_images_is_success_without_removal(self):
        result, calls = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [])

    def test_existing_images_are_removed(self):
        result, calls = self.invoke("abc123\ndef456\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["rmi", "abc123", "def456"])

    def test_image_listing_errors_are_not_hidden(self):
        result, calls = self.invoke(listing_status=7)
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertEqual(calls, [])


class OSDReadinessTests(unittest.TestCase):
    def invoke(self, statuses):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sudo = root / "sudo"
            sudo.write_text(f"#!{sys.executable}\n" + '''import json, os, sys
from pathlib import Path
if sys.argv[1:3] != ['microceph.ceph', '-s']:
    sys.exit(99)
state = Path(os.environ['FAKE_STATUS_COUNT'])
index = int(state.read_text()) if state.exists() else 0
state.write_text(str(index + 1))
statuses = json.loads(os.environ['FAKE_STATUSES'])
status = statuses[min(index, len(statuses) - 1)]
if status is None:
    print('invalid status output')
elif '-f' in sys.argv:
    up, inside = status
    print(json.dumps({'osdmap': {'num_up_osds': up, 'num_in_osds': inside}}))
else:
    up, inside = status
    print(f'osd: 3 osds: {up} up, {inside} in (since 1s)')
''')
            sleep = root / "sleep"
            sleep.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$FAKE_SLEEP_LOG"\n')
            sudo.chmod(0o755)
            sleep.chmod(0o755)
            sleep_log = root / "sleep.log"
            env = {k: v for k, v in os.environ.items() if not k.startswith("BASH_FUNC_") and k not in ("BASH_ENV", "ENV")}
            env.update(PATH=str(root) + os.pathsep + env["PATH"],
                       FAKE_STATUS_COUNT=str(root / "status-count"),
                       FAKE_STATUSES=json.dumps(statuses), FAKE_SLEEP_LOG=str(sleep_log))
            result = subprocess.run(["bash", str(SCRIPT), "wait_for_osds", "3"],
                                    env=env, capture_output=True, text=True, timeout=10)
            sleeps = sleep_log.read_text().splitlines() if sleep_log.exists() else []
            return result, sleeps

    def test_waits_until_all_expected_osds_are_up(self):
        result, sleeps = self.invoke([(2, 3), (3, 3)])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sleeps, ["2"], "in-but-not-up OSDs must not be declared ready")

    def test_waits_until_all_expected_osds_are_in(self):
        result, sleeps = self.invoke([(3, 2), (3, 3)])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sleeps, ["2"])

    def test_ready_osds_do_not_sleep(self):
        result, sleeps = self.invoke([(3, 3)])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sleeps, [])

    def test_osds_staying_down_exhaust_existing_poll_budget(self):
        result, sleeps = self.invoke([(2, 3)])
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("Found >=3 OSDs", result.stdout)
        self.assertEqual(sleeps, ["2"] * 8)

    def test_invalid_status_is_not_readiness(self):
        result, sleeps = self.invoke([None])
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("Found >=3 OSDs", result.stdout)
        self.assertEqual(sleeps, ["2"] * 8)


if __name__ == "__main__":
    unittest.main()

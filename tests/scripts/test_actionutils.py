"""Runner-cleanup regressions; sudo and Docker are stubs, never real cleanup."""
import os
from pathlib import Path
import subprocess
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


if __name__ == "__main__":
    unittest.main()

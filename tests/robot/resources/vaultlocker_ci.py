"""Snap-only Vaultlocker CI helpers; no Juju or charm dependency.

Shared VM execution, snap retry, apt and copying stay in microceph_harness. Secret
handling and scratch-device ownership stay in the root-only guest fixture.
"""

import json
import shlex
import time

from robot.api import logger
from robot.libraries.BuiltIn import BuiltIn

from microceph_harness import microceph_harness

GUEST_FIXTURE = "/root/vaultlocker_fixture.py"


def snap_install_args(name, revision, channel):
    """Select one reproducible revision or one explicit channel."""
    if bool(revision) == bool(channel):
        raise ValueError("Select exactly one snap revision or channel")
    if revision and not str(revision).isdigit():
        raise ValueError("Snap revision must be numeric")
    return [
        "snap",
        "install",
        name,
        f"--revision={revision}" if revision else f"--channel={channel}",
    ]


class vaultlocker_ci:
    """Only Vaultlocker-specific keywords; do not re-export the shared harness."""

    ROBOT_LIBRARY_SCOPE = "SUITE"

    def __init__(self):
        self._harness = microceph_harness()

    def prepare_vaultlocker_fixture(self):
        """Install pinned real snaps and copy the private guest fixture."""
        harness = self._harness
        harness.apt_update()
        harness.apt_install("cryptsetup-bin e2fsprogs fdisk util-linux")
        variables = BuiltIn()
        for name, revision in (("vault", "2487"), ("vaultlocker", "12")):
            prefix = name.upper()
            args = snap_install_args(
                name,
                variables.get_variable_value(f"${{{prefix}_REVISION}}", revision),
                variables.get_variable_value(f"${{{prefix}_CHANNEL}}", ""),
            )
            harness.run_in_vm_with_snap_retry(shlex.join(args), 300)
        harness.run_in_vm_and_check("snap refresh --hold=forever vault vaultlocker", 60)
        for interface in ("block-devices", "dm-crypt"):
            harness.run_in_vm_and_check(f"snap connect vaultlocker:{interface}", 60)
        harness._copy_files_to_vm(
            [
                ("tests/scripts/vaultlocker_fixture.py", GUEST_FIXTURE, False),
            ]
        )

    def start_vaultlocker_test_vault(self):
        """Start the private Vault and poll its API through the shared waiter."""
        self.run_vaultlocker_fixture("start")
        self.wait_for_vaultlocker_test_vault()

    def wait_for_vaultlocker_test_vault(self):
        """Wait for the private Vault API, which remains sealed after reboot."""
        self._harness._poll_until(
            lambda: self.run_vaultlocker_fixture("ready")["ready"],
            attempts=30,
            interval=2,
            fail_msg="Private Vault API did not become ready",
        )

    def run_vaultlocker_fixture(self, operation, name="fresh"):
        """Run an operation without shell tracing; only sanitized JSON is returned."""
        command = shlex.join(["python3", GUEST_FIXTURE, operation, name])
        result = self._harness.run_in_vm_and_check(command, 600)
        return json.loads(result.stdout)

    def sample_locked_vaultlocker_mapper(self):
        """Return mapper-state samples over at least 90 seconds, without unlocking."""
        samples = []
        started = time.monotonic()

        def sample():
            samples.append(self.run_vaultlocker_fixture("locked")["locked"])
            return len(samples) >= 19

        self._harness._poll_until(
            sample, attempts=20, interval=5, fail_msg="Mapper sampling did not complete"
        )
        return {"samples": samples, "seconds": time.monotonic() - started}

    def wait_for_vaultlocker_object(self):
        """Poll a read of the original object; status flags alone are insufficient."""

        result = {}

        def read():
            try:
                result.update(self.run_vaultlocker_fixture("read"))
                return True
            except AssertionError:
                return False

        self._harness._poll_until(
            read, attempts=12, interval=5, fail_msg="Original object unavailable after unlock"
        )
        return result

    def teardown_vaultlocker_test_vm(self):
        """Collect allowlisted diagnostics, then delete only this suite's VM."""
        try:
            logger.info(json.dumps(self.run_vaultlocker_fixture("diagnostics"), indent=2))
        except Exception as error:
            logger.warn(f"Vaultlocker diagnostics unavailable: {type(error).__name__}")
        finally:
            keep = BuiltIn().get_variable_value("${KEEP_VAULTLOCKER_VM}", False)
            if str(keep).upper() in ("TRUE", "YES", "1"):
                logger.warn("Keeping the test VM: its root-only fixture contains test credentials")
            else:
                self._harness.destroy_lxd_instances()

"""Exercise the real OSD wrapper with controlled startup and admin-socket probes."""

import os
from pathlib import Path
import signal
import subprocess
import sys
import time

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "snapcraft/commands/osd.start"


def eventually(predicate, timeout=3):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.01)
    raise AssertionError("OSD wrapper did not reach the expected state")


@pytest.fixture
def wrapper(tmp_path):
    snap = tmp_path / "snap"
    data = tmp_path / "data" / "1"
    common = tmp_path / "common"
    binaries = tmp_path / "bin"
    for path in (snap / "commands", data / "run", common / "data/osd", binaries):
        path.mkdir(parents=True)
    (data.parent / "current").symlink_to(data)
    (snap / "commands/common").write_text("wait_for_config() { :; }\nlimits() { :; }\n")
    scripts = {
        "ceph-osd": """import os, pathlib, sys
root = pathlib.Path(os.environ["TEST_ROOT"])
nr = sys.argv[sys.argv.index("--id") + 1]
with (root / "attempts").open("a") as stream:
    stream.write(nr + "\\n")
if not (root / ("allow-" + nr)).exists():
    sys.exit(1)
(pathlib.Path(os.environ["SNAP_DATA"]) / "run" / ("ceph-osd." + nr + ".asok")).touch()
""",
        "nc": """import pathlib, sys
sys.exit(0 if pathlib.Path(sys.argv[-1]).exists() else 1)
""",
        "sleep": """import os, pathlib, sys
root = pathlib.Path(os.environ["TEST_ROOT"])
with (root / "sleeps").open("a") as stream:
    stream.write(sys.argv[1] + "\\n")
os.execv("/bin/sleep", ["sleep", "infinity" if sys.argv[1] == "infinity" else "0.03"])
""",
    }
    for name, source in scripts.items():
        path = binaries / name
        path.write_text(f"#!{sys.executable}\n" + source)
        path.chmod(0o755)
    env = dict(
        os.environ,
        SNAP=str(snap),
        SNAP_DATA=str(data),
        SNAP_COMMON=str(common),
        TEST_ROOT=str(tmp_path),
        PATH=str(binaries) + os.pathsep + os.environ["PATH"],
    )
    processes = []

    class Wrapper:
        def add(self, nr, available=True):
            directory = common / "data/osd" / f"ceph-{nr}"
            directory.mkdir()
            (directory / "ready").touch()
            if available:
                self.allow(nr)

        def allow(self, nr):
            (tmp_path / f"allow-{nr}").touch()

        def running(self, nr):
            return (data / "run" / f"ceph-osd.{nr}.asok").exists()

        def attempts(self):
            path = tmp_path / "attempts"
            return path.read_text().splitlines() if path.exists() else []

        def sleeps(self):
            path = tmp_path / "sleeps"
            return path.read_text().splitlines() if path.exists() else []

        def start(self):
            output = (tmp_path / "output").open("w")
            process = subprocess.Popen(
                ["bash", str(SCRIPT)],
                env=env,
                stdout=output,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            processes.append((process, output))
            self.process = process

        def reload(self):
            self.process.send_signal(signal.SIGHUP)

    fixture = Wrapper()
    try:
        yield fixture
    finally:
        for process, output in processes:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            process.wait(timeout=5)
            output.close()


def test_osd_start_retries_when_backing_mapping_becomes_available(wrapper):
    wrapper.add(1, available=False)
    wrapper.start()
    eventually(lambda: "1" in wrapper.attempts())
    wrapper.allow(1)
    eventually(lambda: wrapper.running(1))
    assert wrapper.process.poll() is None


def test_failed_osd_does_not_block_other_osds_or_restart_running_osds(wrapper):
    wrapper.add(1, available=False)
    wrapper.add(2)
    wrapper.start()
    eventually(lambda: wrapper.running(2))
    wrapper.allow(1)
    eventually(lambda: wrapper.running(1))
    assert wrapper.attempts().count("2") == 1


def test_healthy_wrapper_waits_for_reload_without_periodic_restarts(wrapper):
    wrapper.add(1)
    wrapper.start()
    eventually(lambda: "infinity" in wrapper.sleeps())
    assert wrapper.attempts() == ["1"]
    assert wrapper.sleeps() == ["infinity"]
    wrapper.add(2)
    wrapper.reload()
    eventually(lambda: wrapper.running(2))
    assert wrapper.attempts().count("1") == 1


def test_reload_during_failed_start_wait_does_not_disable_retry(wrapper):
    wrapper.add(1, available=False)
    wrapper.start()
    eventually(lambda: bool(wrapper.sleeps()))
    wrapper.reload()
    eventually(lambda: wrapper.attempts().count("1") >= 2)
    wrapper.allow(1)
    eventually(lambda: wrapper.running(1))

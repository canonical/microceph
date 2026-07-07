"""Robot Framework library: core execution primitives for the MicroCeph harness.

Class-based library exposing the keywords that run commands inside the outer LXD
test VM and the inner LXD containers. Keeping these as Python methods lets the
higher-level harness logic compose them natively (self.run_in_vm(...)) without
BuiltIn().run_keyword() boilerplate.
"""

import glob
import ipaddress
import json
import re
import subprocess
import tempfile
import time
import os
import uuid
from collections import namedtuple

from robot.api import logger
from robot.libraries.BuiltIn import BuiltIn
from robot.utils import timestr_to_secs

import placement_status
from cephfs_replication import cephfs_replication_list_has_volume
from rbd_replication import (
    rbd_mirror_health,
    rbd_primary_image_count,
    rbd_synced_image_count,
)
from snap_services import enabled_active_services
from streaming_process import run_streaming_process

# Attribute names are load-bearing: Robot suites read ${result.rc}, ${result.stdout},
# ${result.stderr} via extended-variable syntax, so these must be ATTRIBUTES (namedtuple),
# and the rc field must be named `rc` (NOT `returncode`).
ExecResult = namedtuple("ExecResult", ["rc", "stdout", "stderr"])


# ===========================================================================
# Harness configuration constants
#
# Magic values that were previously hardcoded mid-method, surfaced here so the
# topology, paths, and tool lists are defined once. These are byte-identical to
# the literals they replace -- this block only names/relocates them.
# ===========================================================================

# --- topology ---
NODES = ("node-wrk0", "node-wrk1", "node-wrk2", "node-wrk3")
HEAD_NODE = NODES[0]

# --- microceph paths ---
MICROCEPH_DATA = "/var/snap/microceph"
CEPH_CONF = f"{MICROCEPH_DATA}/current/conf/ceph.conf"
MICROCEPH_CONTROL_SOCKET = f"{MICROCEPH_DATA}/common/state/control.socket"
SNAP_META_PATH = "/snap/microceph/current/meta/snap.yaml"
SNAP_REVISION_DIR = "/snap/microceph/x1"
SNAP_MOUNT_UNIT = "snap-microceph-x1.mount"
SNAP_APPARMOR_PROFILE = "/var/lib/snapd/apparmor/profiles/snap.microceph.daemon"

# --- snap interfaces / tools ---
SNAP_INTERFACES = ("block-devices", "hardware-observe", "mount-observe", "load-rbd",
                   "microceph-support", "network-bind", "process-control")
SNAP_INTERFACES_MINIMAL = ("block-devices", "hardware-observe", "mount-observe")
VM_APT_TOOLS = ("s3cmd", "jq")

# --- images / builder ---
IMG_BUILDER_NAME = "microceph-img-builder"
BASE_IMAGE_ALIAS = "ubuntu-22.04"
MICROCEPH_IMAGE_ALIAS = "ubuntu-22.04-microceph"
# raw.lxc device-allow block; the \n is a LITERAL backslash-n for the remote printf.
RAW_LXC_DEVICE_ALLOW = "lxc.cgroup2.devices.allow = b 7:* rwm\\nlxc.cgroup2.devices.allow = c 10:237 rwm"

# --- snap artefact ---
SNAP_DEST_NAME = "microceph_0_amd64.snap"
LOCAL_SNAP_GLOB = "~/microceph_*.snap"
MNT_SNAP_GLOB = "/mnt/microceph_*.snap"

# --- loop devices ---
LOOP_DEV_PREFIX = "/dev/sdi"
DEFAULT_LOOP_SUFFIXES = ("a", "b", "c")

# --- hurl fixtures ---
# Hurl fixtures copied into ~/tests/hurl on the outer VM by copy_hurl_files_to_vm.
HURL_FILES = (
    "disks-delete.hurl",
    "disks-encryption-support-supported.hurl",
    "disks-encryption-support-unsupported.hurl",
    "disks-list.hurl",
    "disks-post-dryrun.hurl",
    "maintenance-put-failed.hurl",
    "services-mon.hurl",
)

# --- file-copy manifests: (src_rel_to_repo, dest_on_vm, chmod_x) ---
HARNESS_SCRIPTS = (
    ("tests/scripts/actionutils.sh", "/root/actionutils.sh", True),
    ("tests/scripts/adoptutils.sh",  "/root/adoptutils.sh",  True),
)
DSL_SCRIPTS = (
    ("tests/scripts/test_dsl_functest.sh", "/root/test_dsl_functest.sh", True),
)


# The class name intentionally matches the module name (microceph_harness) so that
# Robot Framework's "Library microceph_harness.py" path import auto-selects this class
# as the library. A differently-named class (e.g. MicroCephHarness) would require the
# resources/ dir on PYTHONPATH for the dotted "module.ClassName" import form, which the
# path-based import does not provide.
class microceph_harness:
    """Core execution primitives for the MicroCeph Robot Framework harness.

    Runs commands inside the outer LXD test VM and the inner LXD containers,
    mirroring the keyword bodies previously defined in microceph_harness.resource.
    """

    ROBOT_LIBRARY_SCOPE = "SUITE"

    # -----------------------------------------------------------------------
    # Private helpers
    # -----------------------------------------------------------------------

    def _outer_vm(self):
        """Returns the current outer VM name from the Robot ${OUTER_VM} variable.

        Read lazily on every call rather than cached in __init__: calling BuiltIn()
        during library import raises RobotNotRunningError (the class is instantiated
        for keyword discovery before a run context exists), and the still-in-Robot
        Launch Outer Test VM keyword updates ${OUTER_VM} at runtime via
        Set Suite Variable, so the primitives must observe the current value.
        """
        return BuiltIn().get_variable_value("${OUTER_VM}", "microceph-test-vm")

    def _vm_argv(self, *rest):
        """Builds the argv that runs *rest* inside the outer VM via lxc exec."""
        return ["lxc", "exec", "-n", self._outer_vm(), "--", *rest]

    def _ct_argv(self, container, *rest):
        """Builds the argv that runs *rest* inside *container* via the outer VM."""
        return ["lxc", "exec", "-n", self._outer_vm(), "--", "lxc", "exec", "-n", container, "--", *rest]

    def _exec(self, argv, timeout):
        """Runs *argv* with shell=False and returns an ExecResult. No logging.

        On timeout the child (and, since it is not a new session, only the child)
        is terminated and a non-zero ExecResult is returned rather than raising.
        This mirrors Robot Framework's ``Run Process`` default ``on_timeout=terminate``
        behaviour that the original keywords relied on -- a timed-out command yields
        a result with a non-zero rc, so callers polling via Run In VM keep looping
        instead of crashing.
        """
        try:
            cp = subprocess.run(argv, capture_output=True, text=True, timeout=int(timeout))
            return ExecResult(cp.returncode, cp.stdout, cp.stderr)
        except subprocess.TimeoutExpired as exc:
            out = exc.stdout.decode() if isinstance(exc.stdout, bytes) else (exc.stdout or "")
            err = exc.stderr.decode() if isinstance(exc.stderr, bytes) else (exc.stderr or "")
            return ExecResult(124, out, f"{err}\nCommand timed out after {timeout}s")

    @staticmethod
    def _coerce_xtrace(value):
        """Returns True when *value* represents a truthy XTRACE setting.

        Pure helper so the truthiness rule can be unit-tested without a running
        Robot context. Handles both the Robot bool default ${False} and a CLI
        string like 'True'.
        """
        return str(value).upper() in ("TRUE", "YES", "1")

    def _xtrace(self):
        """Returns True when ${XTRACE} is truthy.

        Handles both the Robot bool default ${False} and a CLI string 'True'.
        """
        return self._coerce_xtrace(BuiltIn().get_variable_value("${XTRACE}", False))

    # -----------------------------------------------------------------------
    # Host dependency checking
    # -----------------------------------------------------------------------

    def require_host_commands(self, *commands):
        """Fails immediately if any listed command is absent from the host PATH.

        Call this in Suite Setup for tests that run directly on the host runner.
        For VM-based tests, lxc is checked automatically inside Launch Outer Test VM.
        """
        for cmd in commands:
            # `command -v` is a shell builtin, so a shell is required; pass cmd as a positional
            # ($1) rather than interpolating it into the script string (no quoting/injection risk).
            res = subprocess.run(["bash", "-c", 'command -v "$1" >/dev/null 2>&1', "--", cmd])
            if res.returncode != 0:
                raise AssertionError(
                    f"Missing host dependency: '{cmd}' not found in PATH. "
                    "Install it before running this suite."
                )

    # -----------------------------------------------------------------------
    # Core execution helpers
    # -----------------------------------------------------------------------

    def run_in_vm(self, bash_cmd, timeout=300):
        """Runs an arbitrary bash command inside the outer VM (no fail on non-zero).

        bash -eo pipefail: pipe failures and early command failures propagate to the exit code,
        mirroring the set -e behaviour of the original bash CI steps.
        lxc exec -n (--disable-stdin) wires the command's stdin to /dev/null. Without it the
        command inherits Robot's stdin (a tty on interactive runs, Robot Framework >= 7.0), and
        commands that read stdin to EOF when it is not a tty -- notably lxc init / lxc launch,
        which slurp instance config YAML from stdin -- block forever on a tty that never EOFs.
        """
        res = self._exec(self._vm_argv("bash", "-eo", "pipefail", "-c", bash_cmd), timeout)
        logger.info(f"VM cmd rc={res.rc}: {res.stdout}")
        logger.info(f"STDERR: {res.stderr}")
        return res

    def run_in_vm_and_check(self, bash_cmd, timeout=300):
        """Runs a bash command inside the outer VM and fails on non-zero rc."""
        res = self.run_in_vm(bash_cmd, timeout)
        if res.rc != 0:
            raise AssertionError(
                f"Command failed (rc={res.rc}):\nSTDERR: {res.stderr}\nSTDOUT: {res.stdout}"
            )
        return res

    def run_in_vm_must_fail(self, bash_cmd, timeout=120):
        """Runs a bash command inside the outer VM and fails if it SUCCEEDS (expects non-zero)."""
        res = self.run_in_vm(bash_cmd, timeout)
        if res.rc == 0:
            raise AssertionError(f"Expected failure but command succeeded: {bash_cmd}")
        return res

    def run_in_container(self, container, cmd, timeout=300):
        """Runs cmd inside an inner LXD container via the outer VM.

        ${cmd} is written to a temp file by the local runner using Python file I/O
        and pushed into the container with lxc file push, so it is never interpreted
        by any intermediate shell regardless of what characters it contains.
        bash -eo pipefail: mirrors set -e semantics so any failing command or pipe stage
        inside the container fails the keyword immediately.
        """
        logger.console(f"[{container}] {cmd[:80]}")
        name = f"rf_cmd_{uuid.uuid4().hex[:8]}.sh"
        remote = f"/tmp/{name}"
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as f:
            f.write(cmd)
            local = f.name
        try:
            push = self._exec(["lxc", "file", "push", local, f"{self._outer_vm()}{remote}"], 30)
            if push.rc != 0:
                raise AssertionError(f"Failed to push script to outer VM: {push.stderr}")
            push = self._exec(self._vm_argv("lxc", "file", "push", remote, f"{container}{remote}"), 30)
            if push.rc != 0:
                raise AssertionError(f"Failed to push script to {container}: {push.stderr}")
            res = self._exec(self._ct_argv(container, "bash", "-eo", "pipefail", remote), timeout)
            logger.info(f"Container cmd rc={res.rc}: {res.stdout}")
            logger.info(f"STDERR: {res.stderr}")
        finally:
            try:
                self._exec(self._ct_argv(container, "rm", "-f", remote), 10)
            except Exception:
                pass
            try:
                self._exec(self._vm_argv("rm", "-f", remote), 10)
            except Exception:
                pass
            try:
                os.unlink(local)
            except OSError:
                pass
        if res.rc != 0:
            raise AssertionError(
                f"Command failed (rc={res.rc}):\nSTDERR: {res.stderr}\nSTDOUT: {res.stdout}"
            )
        return res

    def exec_in_container(self, container, *argv, timeout=300, check=False):
        """Runs a single command (no inner shell) inside *container* via the outer VM.

        Direct argv through _ct_argv (one round-trip, shell=False). Non-raising by
        default; pass check=True to fail on non-zero rc. Use this instead of
        hand-building 'lxc exec <node> -- <cmd>' strings.
        """
        res = self._exec(self._ct_argv(container, *argv), timeout)
        logger.info(f"[{container}] cmd rc={res.rc}: {res.stdout}")
        logger.info(f"STDERR: {res.stderr}")
        if check and res.rc != 0:
            raise AssertionError(f"Command failed (rc={res.rc}):\nSTDERR: {res.stderr}\nSTDOUT: {res.stdout}")
        return res

    def run_in_container_unchecked(self, container, cmd, timeout=300, shell="sh"):
        """Runs *cmd* under <shell> -c inside *container*; returns the result WITHOUT raising.

        For inner pipelines whose non-zero rc is a VALID outcome -- grep -c, '... || echo 0',
        '... && echo yes || echo no', getters. Default shell is plain 'sh' (NOT bash -eo pipefail):
        errexit/pipefail would abort a 'grep -c' that finds nothing before the trailing '|| echo ...'
        and change the captured stdout. One round-trip (no temp-file push), so it is safe in poll loops.
        """
        res = self._exec(self._ct_argv(container, shell, "-c", cmd), timeout)
        logger.info(f"[{container}] cmd rc={res.rc}: {res.stdout}")
        logger.info(f"STDERR: {res.stderr}")
        return res

    def run_in_container_and_check(self, container, cmd, timeout=300, shell="sh"):
        """Runs *cmd* under <shell> -c inside *container* and fails on non-zero rc.

        A lightweight alternative to run_in_container (no temp-file push) for simple
        'sh -c "A && B && C"' commands that must succeed.
        """
        res = self.run_in_container_unchecked(container, cmd, timeout, shell)
        if res.rc != 0:
            raise AssertionError(f"Command failed (rc={res.rc}):\nSTDERR: {res.stderr}\nSTDOUT: {res.stdout}")
        return res

    def run_in_head_node(self, cmd, timeout=300):
        """Runs cmd inside node-wrk0 container."""
        return self.run_in_container(HEAD_NODE, cmd, timeout)

    def run_script_in_vm_with_trace(self, script, args="", timeout=3600):
        """Runs a script inside the outer VM, honouring ${XTRACE}.

        Output streams in real time via streaming_process.py. When ${XTRACE}
        is truthy the script runs under bash -x, tracing the whole script
        body. The -x must sit on the bash that executes the script file --
        a wrapping "bash -x -c '...'" would only trace the single dispatch
        line because the script then runs as an untraced child process.
        No bash -c wrapper is used, so ${script} must be an absolute path
        (lxc exec spawns no shell, hence no tilde expansion).

        The command is built as an argv list and run with shell=False (see
        run_streaming_process's list form) so script/args/VM-name components are
        never reinterpreted by a host shell. The -x, when tracing, sits on the
        bash that executes the script file -- "lxc exec VM -- bash -x SCRIPT" --
        exactly as the previous string form produced.
        """
        runner = ["bash", "-x"] if self._xtrace() else ["bash"]
        argv = ["lxc", "exec", self._outer_vm(), "--", *runner, script]
        if args:
            argv.extend(str(args).split())
        return run_streaming_process(argv, timeout=timeout, xtrace=False)

    def get_public_network_cidr(self):
        """Returns the CIDR of the LXD public network (e.g. 10.0.0.0/24) from the outer VM."""
        return self._network_cidr("public")

    def _network_cidr(self, network_type):
        """Returns the CIDR of the LXD network of *network_type* from the outer VM.

        Fetches the raw CSV once and parses the matching row in Python (via
        _parse_network_cidr), replacing the repeated remote grep/cut pipeline used
        by the multi-node bootstrap/join keywords.
        """
        return self._parse_network_cidr(
            self.run_in_vm("lxc network list --format=csv", 30).stdout, network_type
        )

    def get_ceph_conf_value(self, node, key):
        """Returns the value of a ``key = value`` line in *node*'s ceph.conf, or "".

        Fetches the raw ceph.conf from the inner container and extracts the value
        in Python via the pure _ceph_conf_value parser, so the remote command only
        does I/O. Used by the multi-subnet suite to read public_network / mon host.
        """
        text = self.run_in_container_unchecked(node, f"cat {CEPH_CONF}", 30).stdout
        return self._ceph_conf_value(text, key)

    def get_vm_hostname(self):
        """Returns the hostname of the outer VM."""
        return self.run_in_vm("hostname").stdout.strip()

    def get_vm_ip(self):
        """Returns the primary IP of the outer VM (first address from hostname -I)."""
        return self.run_in_vm("hostname -I | cut -d ' ' -f1", 10).stdout.strip()

    # -----------------------------------------------------------------------
    # Pure parsers
    #
    # All @staticmethod with no self / BuiltIn use, so they can be unit-tested
    # without a running Robot context. Each replaces a jq/grep/sed pipeline that
    # previously computed a value inside the remote command; the remote command
    # is reduced to fetching raw output, and the decision is made here in Python.
    # -----------------------------------------------------------------------

    @staticmethod
    def _safe_int(value):
        """Returns int(value) for a digit-only string, else 0.

        Mirrors the original ``int('...') if '...'.isdigit() else 0`` guard so a
        blank or non-numeric remote output yields 0 rather than raising.
        """
        s = str(value).strip()
        return int(s) if s.isdigit() else 0

    @staticmethod
    def _ceph_osd_counts(status_json):
        """Returns (num_up_osds, num_in_osds) parsed from ``ceph -s -f json`` text.

        Replaces the ``... -f json | jq -r '.osdmap.num_up_osds // 0'`` and the
        matching num_in_osds pipelines. Returns (0, 0) on any parse error or a
        missing osdmap, so a poller keeps waiting instead of crashing.
        """
        try:
            data = json.loads(status_json)
        except (ValueError, TypeError):
            return (0, 0)
        osdmap = data.get("osdmap", {})
        return (int(osdmap.get("num_up_osds", 0)), int(osdmap.get("num_in_osds", 0)))

    @staticmethod
    def _rgw_daemon_count(ceph_status_text):
        """Returns the RGW daemon count from human-readable ``ceph -s`` output.

        Replaces ``grep -F "rgw:" | sed -E "s/.* ([0-9]+) daemon.*/\\1/" || echo 0``.
        Finds the services line containing ``rgw:`` and extracts the leading
        daemon count; returns 0 when there is no rgw line.
        """
        for line in ceph_status_text.splitlines():
            if "rgw:" in line:
                m = re.search(r"(\d+)\s+daemon", line)
                return int(m.group(1)) if m else 0
        return 0

    @staticmethod
    def _cephfs_snaps_synced_total(status_json):
        """Returns the total snaps_synced across all peers' mirror_status entries.

        Replaces ``jq '[.peers[].mirror_status | .[] | .snaps_synced // 0] | add // 0'``.
        Both ``peers`` (Go type ``map[string]CephFsReplicationResponsePeerItem``) and
        ``mirror_status`` (Go type ``map[string]CephFsReplicationDirMirrorStatus``) marshal
        to JSON OBJECTS, so each is iterated by VALUE -- matching jq's ``.[]`` -- rather than
        by key. A list form is still accepted for robustness. A null peers/mirror_status map
        (nil Go map) and a null snaps_synced are coerced to 0, mirroring jq's ``// 0``.
        Returns 0 on any parse error.
        """
        try:
            data = json.loads(status_json)
        except (ValueError, TypeError):
            return 0
        total = 0
        peers = data.get("peers", {}) or {}
        peer_items = peers.values() if isinstance(peers, dict) else peers
        for peer in peer_items:
            mirror_status = peer.get("mirror_status", {}) or {}
            if isinstance(mirror_status, dict):
                entries = mirror_status.values()
            else:
                entries = mirror_status
            for entry in (entries or []):
                total += entry.get("snaps_synced") or 0
        return total

    @staticmethod
    def _parse_network_cidr(csv_text, network_type):
        """Returns the CIDR (4th comma-field) of the first LXD network row matching *network_type*.

        Mirrors ``lxc network list --format=csv | grep '<type>' | cut -d, -f4``:
        scan lines for the first one containing network_type, split on commas, and
        return the 4th field (index 3) stripped. Returns "" when no line matches or
        the matching line has fewer than 4 fields.
        """
        for line in csv_text.splitlines():
            if network_type in line:
                fields = line.split(",")
                if len(fields) >= 4:
                    return fields[3].strip()
                return ""
        return ""

    @staticmethod
    def _host_address(cidr, host_offset):
        """Returns the address at *host_offset* hosts into *cidr*'s network, as a string.

        Does arithmetic on an ipaddress network rather than string-appending to the
        gateway (which could overflow an octet, e.g. yield an invalid '10.0.0.1000').
        For '10.0.0.1/24' with host_offset=10 this returns '10.0.0.10' -- matching the
        '.10'-based host convention the suite assigns to the head node. strict=False
        tolerates the host bits set on the LXD gateway CIDR.
        """
        network = ipaddress.ip_network(cidr, strict=False)
        return str(network.network_address + host_offset)

    @staticmethod
    def _ceph_conf_value(conf_text, key):
        """Returns the value of the ``key = value`` line in ceph.conf text, else "".

        ceph.conf carries ``name = value`` lines under section headers; this returns
        the right-hand side of the first line whose key -- the text left of the first
        ``=``, stripped -- equals *key*. Section headers and blank lines have no ``=``
        and are skipped. Returns "" when *key* is absent. Keeps the comma-delimited
        public_network value intact for the multi-subnet suite to compare verbatim.
        """
        for line in conf_text.splitlines():
            if "=" not in line:
                continue
            name, _, value = line.partition("=")
            if name.strip() == key:
                return value.strip()
        return ""

    @staticmethod
    def _last_eth_interface(ip_addr_text):
        """Returns the name of the last eth* interface in ``ip a`` output, else "".

        Replaces the ``ip a | grep ': eth' | tail -n 1 | cut -d@ -f1 | cut -d ' ' -f2``
        pipeline used to find the most recently attached container NIC. Interface
        header lines read ``<index>: <name>[@peer]: <flags> ...`` and are listed by
        ascending ifindex, so the last eth* match is the newest NIC. The veth
        ``@peer`` suffix is dropped, and indented address/link sub-lines are skipped.
        """
        name = ""
        for line in ip_addr_text.splitlines():
            m = re.match(r"\s*\d+:\s+(eth\w*)[@:]", line)
            if m:
                name = m.group(1)
        return name

    @staticmethod
    def _count_configured_disks(disk_list_json, *substrings):
        """Counts ConfiguredDisks whose path contains any of *substrings.

        Mirrors ``microceph disk list --json | jq -r '.ConfiguredDisks[].path' |
        grep -e A -e B -c``: parse the JSON, iterate ``data["ConfiguredDisks"]``,
        and count entries whose ``path`` contains at least one of the substrings.
        Returns 0 on any parse error or when the key is absent.
        """
        try:
            data = json.loads(disk_list_json)
        except (ValueError, TypeError):
            return 0
        count = 0
        for disk in data.get("ConfiguredDisks", []):
            path = disk.get("path", "")
            if any(sub in path for sub in substrings):
                count += 1
        return count

    @staticmethod
    def _remote_list_has(remote_list_json, field, value):
        """Returns True if any `microceph remote list --json` entry has *field* == *value*.

        Replaces the remote ``... | jq -e 'any(.[]; .<field> == "<value>")'`` decision with a
        pure Python check, so the parse is unit-testable and no jq is needed on the VM/container.
        Returns False on any parse error or a non-list payload.
        """
        try:
            data = json.loads(remote_list_json)
        except (ValueError, TypeError):
            return False
        return any(isinstance(e, dict) and e.get(field) == value for e in (data or []))

    # -----------------------------------------------------------------------
    # Generic poller
    # -----------------------------------------------------------------------

    @staticmethod
    def _poll_until(predicate, attempts, interval, fail_msg, on_fail=None, between=None, raise_on_timeout=True):
        """Call predicate() up to `attempts` times; return on the first truthy result.

        Between probes, run the optional `between` side-effect (a repair step) then
        sleep `interval` (seconds, or a Robot time string like '3s'). On exhaustion run
        the optional `on_fail` diagnostic and, unless raise_on_timeout is False, raise
        AssertionError(fail_msg).
        """
        secs = timestr_to_secs(interval) if isinstance(interval, str) else interval
        for _ in range(int(attempts)):
            if predicate():
                return
            if between is not None:
                between()
            time.sleep(secs)
        if on_fail is not None:
            on_fail()
        if raise_on_timeout:
            raise AssertionError(fail_msg)

    # -----------------------------------------------------------------------
    # VM / cluster pollers (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def wait_for_vm_agent(self, vm_name):
        """Polls lxc exec until the LXD VM agent responds (60 x 5 s = 5 min max)."""
        logger.info(f"Waiting for VM agent in {vm_name}")
        self._poll_until(
            lambda: self._exec(["lxc", "exec", "-n", vm_name, "--", "true"], 15).rc == 0,
            attempts=60,
            interval=5,
            fail_msg=f"VM agent for {vm_name} did not become ready within 5 minutes",
        )

    def wait_for_cluster_health_ok(self, node="", tries=100, interval="3s"):
        """Polls microceph.ceph health until HEALTH_OK (tries x interval).

        Pass node= (e.g. node-wrk0) to poll inside that LXD container; omit to run
        directly on the outer VM with sudo.
        """
        label = "outer VM" if node == "" else node
        logger.console(f"[health] Waiting for HEALTH_OK ({label})...")

        def predicate():
            if node == "":
                out = self.run_in_vm("sudo microceph.ceph health", 30).stdout
            else:
                out = self.exec_in_container(node, "microceph.ceph", "health", timeout=30).stdout
            return out.strip() == "HEALTH_OK"

        def on_fail():
            if node == "":
                self.run_in_vm_and_check("sudo microceph.ceph -s", 30)
            else:
                self.run_in_container(node, "microceph.ceph -s", 30)

        def succeeded():
            logger.console("[health] HEALTH_OK")

        # _poll_until does not signal success vs return, so emit the success
        # console line from a wrapping predicate when the check first passes.
        def predicate_with_log():
            ok = predicate()
            if ok:
                succeeded()
            return ok

        self._poll_until(
            predicate_with_log,
            attempts=tries,
            interval=interval,
            fail_msg="Cluster did not reach HEALTH_OK",
            on_fail=on_fail,
        )

    def poll_ceph_status_contains(self, substring, tries=16, sleep="15s"):
        """Polls ceph status on the outer VM until the output contains *substring*."""
        attempt = [0]

        def predicate():
            out = self.run_in_vm("sudo microceph.ceph status", 30).stdout
            logger.info(f"Attempt {attempt[0]}: {out}")
            if substring in out:
                logger.console(f"[status] PASS: '{substring}' found (attempt {attempt[0]})")
                attempt[0] += 1
                return True
            attempt[0] += 1
            return False

        self._poll_until(
            predicate,
            attempts=tries,
            interval=sleep,
            fail_msg=f"ceph status never contained '{substring}' after {tries} attempts",
        )

    def wait_for_n_nodes_in_cluster(self, n, head_node=HEAD_NODE):
        """Polls microceph status on *head_node* until at least *n* nodes appear (8 x 2 s)."""
        def predicate():
            status = self.exec_in_container(head_node, "microceph", "status", timeout=30).stdout
            count = len(re.findall(r"^- node", status, re.M))
            return count >= int(n)

        self._poll_until(
            predicate,
            attempts=8,
            interval=2,
            fail_msg=f"Cluster did not reach {n} node(s) after 16 s",
        )

    def wait_for_pool_crush_rule(self, rule_id, tries=30):
        """Polls osd pool ls detail until at least one pool carries crush_rule *rule_id* (30 x 2 s)."""
        logger.console(f"[crush] Waiting for pool with crush_rule {rule_id}...")
        ls_cmd = "microceph.ceph osd pool ls detail 2>/dev/null || true"

        def predicate():
            if f"crush_rule {rule_id}" in self.run_in_container_unchecked(HEAD_NODE, ls_cmd, 30).stdout:
                logger.console(f"[crush] Found pool with crush_rule {rule_id}")
                return True
            return False

        def on_fail():
            # Diagnostic on timeout: omit 2>/dev/null (which the predicate's ls_cmd carries)
            # so ceph's error output is visible in the failure log.
            self.run_in_container_unchecked(HEAD_NODE, "microceph.ceph osd pool ls detail || true", 30)

        self._poll_until(
            predicate,
            attempts=tries,
            interval=2,
            fail_msg=f"No pool reached crush_rule {rule_id} after {tries} tries",
            on_fail=on_fail,
        )

    def node_is_in_mon_list(self, node, head_node=HEAD_NODE):
        """Returns "yes" if *node* appears in the mon daemons line of ceph -s via *head_node*.

        Callers compare the result string against "yes", so the literal "yes"/"no"
        return contract is preserved.
        """
        status = self.exec_in_container(head_node, "microceph.ceph", "-s", timeout=30).stdout
        if re.search(rf"mon: .*daemons.*{re.escape(node)}", status):
            return "yes"
        return "no"

    # -----------------------------------------------------------------------
    # RGW pollers
    # -----------------------------------------------------------------------

    def wait_for_rgw(self, expect, tries=8):
        """Polls until at least *expect* RGW daemons are running on the outer VM."""
        logger.console(f"[rgw] Waiting for {expect} RGW daemon(s)...")

        def predicate():
            text = self.run_in_vm("sudo microceph.ceph -s", 30).stdout
            count = self._rgw_daemon_count(text)
            if count >= int(expect):
                logger.console(f"[rgw] Found {count} RGW daemon(s)")
                return True
            return False

        def on_fail():
            self.run_in_vm_and_check("sudo microceph.ceph -s", 30)

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"Never reached {expect} RGW daemon(s)",
            on_fail=on_fail,
        )

    def wait_for_rgw_on_head_node(self, expect, tries=20):
        """Polls until at least *expect* RGW daemons are running on node-wrk0."""
        logger.console(f"[rgw] Waiting for {expect} RGW daemon(s) on node-wrk0...")

        def predicate():
            text = self.exec_in_container(HEAD_NODE, "microceph.ceph", "-s", timeout=30).stdout
            return self._rgw_daemon_count(text) >= int(expect)

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"Never reached {expect} RGW daemon(s) on head node",
        )

    def wait_for_rgw_ssl_port(self, host="localhost", port=443, tries=60):
        """Polls until the RGW SSL endpoint on *host*:*port* serves a certificate."""
        logger.console(f"[rgw] Waiting for RGW SSL on {host}:{port}...")

        def predicate():
            out = self.run_in_vm(f"echo | openssl s_client -connect {host}:{port} 2>/dev/null", 15).stdout
            return "BEGIN CERTIFICATE" in out

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"RGW SSL never started on {host}:{port}",
        )

    def get_rgw_ssl_cn(self, host="localhost", port=443):
        """Returns the certificate CN served by the RGW SSL endpoint on *host*:*port*."""
        subject = self.run_in_vm(
            f"echo | openssl s_client -connect {host}:{port} 2>/dev/null | "
            f"openssl x509 -noout -subject 2>/dev/null",
            30,
        ).stdout
        m = re.search(r"CN\s*=\s*(.+)", subject)
        return m.group(1).strip() if m else ""

    def read_base64_file_from_container(self, container, path):
        """Returns the base64-encoded (no line wrapping) contents of *path* inside *container*."""
        # argv via exec_in_container (shell=False): *path* is passed as a positional, so a
        # space or shell metacharacter in it cannot be reinterpreted by an inner shell.
        # Non-raising (check=False), matching the previous run_in_container_unchecked call.
        return self.exec_in_container(container, "sudo", "base64", "-w0", path, timeout=30).stdout.strip()

    # -----------------------------------------------------------------------
    # OSD pollers
    # -----------------------------------------------------------------------

    def wait_for_osd_count(self, expected_count, tries=10):
        """Polls until num_in_osds >= *expected_count* on the outer VM."""
        logger.console(f"[osd] Waiting for {expected_count} OSD(s) on outer VM...")

        def predicate():
            out = self.run_in_vm("sudo microceph.ceph -s -f json 2>/dev/null", 30).stdout
            _, num_in = self._ceph_osd_counts(out)
            if num_in >= int(expected_count):
                logger.console(f"[osd] Found {num_in} OSD(s)")
                return True
            return False

        def on_fail():
            self.run_in_vm_and_check("sudo microceph.ceph -s", 30)

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"Never reached {expected_count} OSD(s) on outer VM",
            on_fail=on_fail,
        )
        # Original logs ceph -s on the success path too.
        self.run_in_vm_and_check("sudo microceph.ceph -s", 30)

    def wait_for_osd_count_up_in(self, expected_count, tries=24):
        """Polls until BOTH num_up_osds AND num_in_osds >= *expected_count* on the outer VM.

        Mirrors bash wait_for_osds_up_in: an OSD that is "in" but not "up" (e.g. a
        LUKS volume that failed to reopen after a restart) must NOT satisfy this gate.
        """
        logger.console(f"[osd] Waiting for {expected_count} OSD(s) up AND in on outer VM...")

        def predicate():
            out = self.run_in_vm("sudo microceph.ceph -s -f json 2>/dev/null", 30).stdout
            up, num_in = self._ceph_osd_counts(out)
            if up >= int(expected_count) and num_in >= int(expected_count):
                logger.console(f"[osd] Found {up} up / {num_in} in OSD(s)")
                return True
            return False

        def on_fail():
            self.run_in_vm_and_check("sudo microceph.ceph -s", 30)

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=(
                f"Never reached {expected_count} OSD(s) up AND in on outer VM "
                f"(up<{expected_count} or in<{expected_count})"
            ),
            on_fail=on_fail,
        )
        self.run_in_vm_and_check("sudo microceph.ceph -s", 30)

    def wait_for_osd_count_head(self, expected_count, tries=20):
        """Polls until num_in_osds >= *expected_count* via node-wrk0.

        The JSON is fetched through the outer VM's lxc exec (jq is not used inside
        the container) so it works regardless of container tool availability.
        """
        logger.console(f"[osd] Waiting for {expected_count} OSD(s) on node-wrk0...")

        def predicate():
            out = self.exec_in_container(HEAD_NODE, "microceph.ceph", "-s", "-f", "json", timeout=30).stdout
            _, num_in = self._ceph_osd_counts(out)
            if num_in >= int(expected_count):
                logger.console(f"[osd] Found {num_in} OSD(s)")
                return True
            return False

        def on_fail():
            self.run_in_container(HEAD_NODE, "microceph.ceph -s", 30)

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"Never reached {expected_count} OSD(s) on node-wrk0",
            on_fail=on_fail,
        )
        self.run_in_container(HEAD_NODE, "microceph.ceph -s", 30)

    # -----------------------------------------------------------------------
    # CephFS replication pollers
    # -----------------------------------------------------------------------

    def wait_for_cephfs_replication_list_non_empty(self, node, vol, attempts=50):
        """Polls until the CephFS replication list for *vol* on *node* has a non-empty entry.

        JSON parsing and the present-and-non-empty check are delegated to
        cephfs_replication.py, so an absent volume key counts as "not present yet"
        (keep polling) rather than success.
        """
        def predicate():
            out = self.exec_in_container(node, "sudo", "microceph", "replication", "list", "cephfs", "--json", timeout=30).stdout
            return cephfs_replication_list_has_volume(out, vol)

        self._poll_until(
            predicate,
            attempts=attempts,
            interval=5,
            fail_msg=f"CephFS replication list for {vol} still empty or absent after {attempts} attempts",
        )

    def wait_for_cephfs_snaps_synced(self, node, vol, threshold, attempts=100):
        """Polls until total snaps_synced for volume *vol* on *node* reaches *threshold*."""
        def predicate():
            out = self.exec_in_container(node, "microceph", "replication", "status", "cephfs", vol, "--json", timeout=30).stdout
            return self._cephfs_snaps_synced_total(out) >= int(threshold)

        self._poll_until(
            predicate,
            attempts=attempts,
            interval=5,
            fail_msg=f"CephFS snaps_synced for {vol} never reached {threshold} after {attempts} attempts",
        )

    # -----------------------------------------------------------------------
    # File / snap-mount helpers
    # -----------------------------------------------------------------------

    def read_file_in_vm(self, path):
        """Returns the ExecResult of running cat *path* on the outer VM.

        Returns the result OBJECT (not just stdout): callers read ${result.stdout.strip()}.
        """
        return self.run_in_vm(f"cat {path}", 10)

    def ensure_snap_mount_healthy(self, container):
        """Verifies the pre-baked microceph snap squashfs mount is alive in *container*, repairing it if not.

        Containers cloned from the pre-baked image mount /snap/microceph/x1 via
        squashfuse at boot, and that FUSE mount intermittently comes up dead
        ("transport endpoint is not connected"), which breaks every subsequent
        snap command. Restarting the mount unit re-establishes it. _poll_until
        checks first, then runs the repair (between) and sleeps, matching the
        original check-then-repair-then-sleep ordering.
        """
        def predicate():
            return self.run_in_container_unchecked(container, f"test -r {SNAP_META_PATH}", 15).rc == 0

        attempt = [0]

        def between():
            attempt[0] += 1
            logger.console(
                f"[install] microceph snap mount broken on {container} (attempt {attempt[0]}); "
                "restarting mount unit"
            )
            self.run_in_container_unchecked(
                container,
                f"umount -l {SNAP_REVISION_DIR} 2>/dev/null; systemctl restart {SNAP_MOUNT_UNIT}",
                30,
            )

        self._poll_until(
            predicate,
            attempts=6,
            interval=3,
            fail_msg=f"microceph snap mount never became healthy on {container}",
            between=between,
        )

    # -----------------------------------------------------------------------
    # Lifecycle / distribution / teardown (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def _repo_root(self):
        """Returns the repository root from the Robot ${REPO_ROOT} variable."""
        return BuiltIn().get_variable_value("${REPO_ROOT}")

    def _snap_path(self):
        """Returns the configured snap path from ${SNAP_PATH}, or "" when unset."""
        return BuiltIn().get_variable_value("${SNAP_PATH}", "") or ""

    def _lxc_file_push(self, src, dest, timeout, errlabel):
        """Pushes *src* to *dest* via lxc file push, failing on non-zero rc."""
        res = self._exec(["lxc", "file", "push", src, dest], timeout)
        if res.rc != 0:
            raise AssertionError(f"Failed to {errlabel}: {res.stderr}")
        return res

    def launch_outer_test_vm(self, vm_name=None, disk_size=None, enable_nesting=False):
        """Launches the LXD VM used as the test boundary, deleting any pre-existing instance."""
        vm_name = vm_name or BuiltIn().get_variable_value("${OUTER_VM}", "microceph-test-vm")
        disk_size = disk_size or BuiltIn().get_variable_value("${OUTER_VM_DISK}", "50GiB")
        # enable_nesting is accepted for API parity but is currently unused (the
        # original keyword body ignores it).
        self.require_host_commands("lxc")
        logger.console(f"\n[setup] Deleting pre-existing VM {vm_name} (if any)...")
        self._exec(["lxc", "delete", "--force", vm_name], 60)
        logger.console(f"[setup] Launching VM {vm_name} (disk={disk_size})...")
        cpu = BuiltIn().get_variable_value("${OUTER_VM_CPU}", "4")
        memory = BuiltIn().get_variable_value("${OUTER_VM_MEMORY}", "6GiB")
        image = BuiltIn().get_variable_value("${OUTER_VM_IMAGE}", "ubuntu:24.04")
        argv = [
            "lxc", "launch", image, vm_name, "--vm",
            "-c", f"limits.cpu={cpu}",
            "-c", f"limits.memory={memory}",
            "-d", f"root,size={disk_size}",
        ]
        for attempt in range(3):
            res = self._exec(argv, 300)
            if res.rc == 0:
                break
            logger.console(f"[setup] Launch attempt {attempt} failed (rc={res.rc}), retrying in 30s...")
            self._exec(["lxc", "delete", "--force", vm_name], 60)
            if attempt == 2:
                raise AssertionError(f"Failed to launch VM {vm_name} after 3 attempts: {res.stderr}")
            time.sleep(30)
        # Bridge: keep the still-in-Robot keywords and _outer_vm() in sync (replaces
        # the original Set Suite Variable).
        BuiltIn().set_suite_variable("${OUTER_VM}", vm_name)
        logger.console(f"[setup] Waiting for VM agent in {vm_name}...")
        self.wait_for_vm_agent(vm_name)
        logger.console(f"[setup] Waiting for cloud-init in {vm_name}...")
        res = self._exec(["lxc", "exec", "-n", vm_name, "--", "cloud-init", "status", "--wait"], 300)
        if res.rc != 0:
            raise AssertionError(f"cloud-init failed in {vm_name}: {res.stderr}")
        logger.console(f"[setup] VM {vm_name} ready.")

    def _copy_files_to_vm(self, manifest):
        """Pushes each (src_rel_to_repo, dest_on_vm, chmod_x) entry of *manifest* into the outer VM."""
        repo, vm = self._repo_root(), self._outer_vm()
        exec_paths = []
        for src_rel, dest, make_exec in manifest:
            self._lxc_file_push(f"{repo}/{src_rel}", f"{vm}{dest}", 60, f"copy {os.path.basename(src_rel)}")
            if make_exec:
                exec_paths.append(dest.replace("/root", "~", 1))
        # Single combined chmod (mirrors the pre-refactor `chmod +x ~/a ~/b`) instead of one per file.
        if exec_paths:
            self.run_in_vm_and_check(f"chmod +x {' '.join(exec_paths)}")

    def copy_scripts_to_vm(self):
        """Copies actionutils.sh and adoptutils.sh to ~/ in the outer VM."""
        logger.console(f"[setup] Copying scripts to {self._outer_vm()}...")
        self._copy_files_to_vm(HARNESS_SCRIPTS)
        logger.info(f"Scripts copied to {self._outer_vm()}")

    def copy_snap_to_vm(self, snap_path=None):
        """Copies the snap to ~/microceph_0_amd64.snap inside the outer VM."""
        snap_path = snap_path or self._snap_path()
        if not snap_path:
            logger.warn("SNAP_PATH not set - skipping snap copy")
            return
        # SNAP_PATH may be a glob (CI passes '/home/runner/*.snap'). lxc file push runs
        # with shell=False, so it cannot expand a glob
        # `Run Process lxc file push ${snap_path}` keyword did -- resolve it here.
        # sorted() mirrors shell glob ordering when more than one snap matches.
        matches = sorted(glob.glob(os.path.expanduser(snap_path)))
        if matches:
            snap_path = matches[0]
        vm = self._outer_vm()
        logger.console(f"[setup] Copying snap to {vm} (this may take a minute)...")
        self._lxc_file_push(
            snap_path, f"{vm}/root/{SNAP_DEST_NAME}",
            120, "push snap",
        )
        logger.info(f"Snap pushed to {vm}:/root/{SNAP_DEST_NAME}")

    def copy_source_to_vm(self):
        """Copies the repository source tree into ~/microceph/ inside the outer VM via git archive."""
        # The inner `bash -c` runs INSIDE the VM for ~ expansion + the tar pipe;
        # that part is irreducible. The host side uses a Popen pipeline so no host
        # shell interprets the command.
        git = subprocess.Popen(["git", "archive", "HEAD"], cwd=self._repo_root(), stdout=subprocess.PIPE)
        try:
            res = subprocess.run(
                ["lxc", "exec", self._outer_vm(), "--", "bash", "-c", "mkdir -p ~/microceph && tar -xf - -C ~/microceph"],
                stdin=git.stdout, capture_output=True, text=True, timeout=120,
            )
        finally:
            # Always close our read end and reap git, even if the tar side raised
            # (e.g. TimeoutExpired): closing the pipe makes git see EPIPE and exit,
            # so it cannot leak as an orphan with an open pipe. Bound the wait so a
            # wedged git cannot hang the teardown.
            git.stdout.close()
            try:
                git.wait(timeout=10)
            except subprocess.TimeoutExpired:
                git.kill()
                git.wait()
        # Check git before tar: a non-zero git archive can write a partial stream that
        # tar still unpacks "successfully", silently copying an incomplete source tree.
        if git.returncode != 0:
            raise AssertionError(f"git archive failed (rc={git.returncode}) while copying source to VM")
        if res.returncode != 0:
            raise AssertionError(f"Failed to copy source: {res.stderr}")
        logger.info(f"Source code copied to {self._outer_vm()}:/root/microceph")

    def copy_dsl_test_script_to_vm(self):
        """Copies the DSL functional test script (test_dsl_functest.sh) into the outer VM."""
        self._copy_files_to_vm(DSL_SCRIPTS)
        logger.info(f"test_dsl_functest.sh copied to {self._outer_vm()}")

    def copy_hurl_files_to_vm(self):
        """Copies all hurl test files from tests/hurl/ into ~/tests/hurl/ on the outer VM."""
        repo = self._repo_root()
        vm = self._outer_vm()
        self.run_in_vm_and_check("mkdir -p ~/tests/hurl")
        for f in HURL_FILES:
            self._lxc_file_push(
                f"{repo}/tests/hurl/{f}", f"{vm}/root/tests/hurl/{f}",
                60, f"copy {f}",
            )
        logger.info(f"Hurl files copied to {vm}:~/tests/hurl/")

    def collect_microceph_diagnostics(self):
        """Collects diagnostics from the outer VM and any inner nodes; errors are ignored."""
        r = self.run_in_vm("sudo microceph status 2>/dev/null || true")
        logger.info(f"microceph status: {r.stdout}")
        r = self.run_in_vm("sudo microceph.ceph -s 2>/dev/null || true")
        logger.info(f"ceph -s: {r.stdout}")
        r = self.run_in_vm("sudo snap logs microceph -n 200 2>/dev/null || true")
        logger.info(f"snap logs: {r.stdout}")
        nodes = self.run_in_vm("lxc ls -c n --format csv 2>/dev/null || true", 30)
        for line in nodes.stdout.strip().split("\n"):
            node = line.strip()
            if not node:
                continue
            r = self.run_in_container_unchecked(
                node,
                "microceph status; microceph.ceph -s; snap logs microceph -n 200",
                60,
            )
            logger.info(f"[{node}] diagnostics: {r.stdout}")

    def destroy_lxd_instances(self):
        """Force-stops and force-deletes the outer VM."""
        vm = self._outer_vm()
        logger.info(f"Destroying outer VM: {vm}")
        self._exec(["lxc", "stop", vm, "--force"], 60)
        self._exec(["lxc", "delete", vm, "--force"], 60)
        logger.info(f"Outer VM {vm} destroyed")

    def detach_loop_devices(self):
        """Detaches leftover mctest- loop devices on the host (best-effort)."""
        self._exec(
            ["bash", "-c", "losetup -a | grep -E 'mctest-' | cut -d: -f1 | xargs -r losetup -d 2>/dev/null || true"],
            60,
        )

    def teardown_microceph_environment(self):
        """Always-run suite teardown: collect diagnostics then destroy VM."""
        for step in (self.collect_microceph_diagnostics, self.destroy_lxd_instances, self.detach_loop_devices):
            try:
                step()
            except Exception:
                pass

    # -----------------------------------------------------------------------
    # Single-node MicroCeph setup (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def install_tools(self):
        """Installs s3cmd and jq on the outer VM."""
        logger.console("[setup] Installing tools (s3cmd, jq)...")
        self.run_in_vm_and_check("sudo apt-get update -qq", 120)
        self.run_in_vm_and_check(f"sudo apt-get -qq -y install {' '.join(VM_APT_TOOLS)}", 120)

    def install_microceph_from_local_snap(self, snap_path=None):
        """Installs the locally-built snap and connects all interfaces (except dm-crypt)."""
        snap_path = snap_path or self._snap_path()
        if not snap_path:
            logger.warn("SNAP_PATH not set - skipping MicroCeph snap installation")
            return
        # snap_path only gates the skip above; the install uses the ~/microceph_*.snap
        # glob below, so the argument value is otherwise unused.
        logger.console("[install] Installing MicroCeph snap...")
        self.run_in_vm_and_check("sudo snap install core24 || true", 120)
        self.run_in_vm_and_check(f"sudo snap install --dangerous {LOCAL_SNAP_GLOB}", 600)
        for iface in SNAP_INTERFACES:
            self.run_in_vm_and_check(f"sudo snap connect microceph:{iface}", 30)

    def bootstrap_microceph_cluster(self, mon_ip=""):
        """Runs microceph cluster bootstrap and waits 30 s for stabilisation."""
        logger.console("[bootstrap] Bootstrapping MicroCeph cluster...")
        # Wait for the snap daemon to open the control socket before bootstrapping.
        # The original FOR loop falls through after 24 tries without failing, so
        # raise_on_timeout=False: on exhaustion we proceed to bootstrap anyway.
        socket_wait = [0]

        def log_socket_wait():
            socket_wait[0] += 1
            logger.console(f"[bootstrap] Waiting for MicroCeph control socket ({socket_wait[0]})...")

        self._poll_until(
            lambda: self.run_in_vm(f"test -S {MICROCEPH_CONTROL_SOCKET}", 15).rc == 0,
            attempts=24,
            interval=5,
            fail_msg="",
            raise_on_timeout=False,
            between=log_socket_wait,
        )
        if mon_ip != "":
            self.run_in_vm_and_check(f"sudo microceph cluster bootstrap --mon-ip {mon_ip}", 120)
        else:
            self.run_in_vm_and_check("sudo microceph cluster bootstrap", 120)
        self.run_in_vm_and_check("sudo microceph.ceph version", 30)
        self.run_in_vm_and_check("sudo microceph.ceph status", 30)
        time.sleep(30)
        self.run_in_vm_and_check("sudo microceph.ceph status", 30)
        self.run_in_vm_and_check("sudo microceph.ceph health", 30)

    # -----------------------------------------------------------------------
    # OSD / disk operations (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def add_encrypted_osds(self):
        """Enables dm-crypt, creates loop devices, adds two encrypted OSDs."""
        logger.console("[osd] Adding encrypted OSDs with dm-crypt...")
        self.run_in_vm_and_check("sudo snap connect microceph:dm-crypt", 30)
        self.run_in_vm_and_check("sudo snap restart microceph.daemon", 60)
        self.create_loop_devices()
        self.run_in_vm_and_check("sudo microceph disk add /dev/sdia /dev/sdib --wipe --encrypt", 120)
        time.sleep(30)
        out = self.run_in_vm("sudo microceph disk list --json", 30).stdout
        count = self._count_configured_disks(out, "/dev/sdia", "/dev/sdib")
        if count != 2:
            raise AssertionError(f"Expected 2 encrypted disks, got {count}")

    def add_lvm_volume_osd(self):
        """Creates an LVM volume on a loop device and adds it as an OSD."""
        logger.console("[osd] Adding LVM volume OSD...")
        lf = self.run_in_vm("mktemp /tmp/mctestXXXXXX", 30).stdout.strip()
        self.run_in_vm_and_check(f"sudo truncate -s 4G {lf}", 30)
        ld = self.run_in_vm(f"sudo losetup --show -f {lf}", 30).stdout.strip()
        self.run_in_vm_and_check(f"sudo pvcreate {ld}", 30)
        self.run_in_vm_and_check(f"sudo vgcreate vgtst {ld}", 30)
        self.run_in_vm_and_check("sudo lvcreate -l100%FREE --name lvtest vgtst", 30)
        self.run_in_vm_and_check("sudo microceph disk add /dev/vgtst/lvtest --wipe", 120)
        time.sleep(20)
        self.run_in_vm_and_check("sudo microceph.ceph -s", 30)
        count = self._count_configured_disks(
            self.run_in_vm("sudo microceph disk list --json", 30).stdout, "/dev/vgtst/lvtest"
        )
        if count != 1:
            raise AssertionError("LVM OSD not found in disk list")

    def create_loop_device_at(self, device, size="4G"):
        """Creates a single named loop-backed block device at *device* on the outer VM."""
        lf = self.run_in_vm("mktemp /tmp/mctestXXXXXX", 30).stdout.strip()
        self.run_in_vm_and_check(f"sudo truncate -s {size} {lf}", 30)
        ld = self.run_in_vm(f"sudo losetup --show -f {lf}", 30).stdout.strip()
        minor = ld.replace("/dev/loop", "")
        self.run_in_vm_and_check(f"sudo mknod -m 0660 {device} b 7 {minor}", 30)

    def create_loop_devices(self):
        """Creates /dev/sdia, /dev/sdib, /dev/sdic as loop-backed devices."""
        logger.console("[osd] Creating loop devices /dev/sdia, /dev/sdib, /dev/sdic...")
        for l in DEFAULT_LOOP_SUFFIXES:
            self.create_loop_device_at(f"{LOOP_DEV_PREFIX}{l}")

    # -----------------------------------------------------------------------
    # Multi-node LXD container setup (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def build_base_lxd_image(self, home):
        """Builds the ubuntu-22.04-microceph base LXD image with tools and MicroCeph pre-installed."""
        logger.console("[setup] Building base LXD image with tools and MicroCeph...")
        # Namespaced builder instance plus up-front best-effort cleanup (rc ignored).
        builder = IMG_BUILDER_NAME
        self.run_in_vm(f"lxc delete --force {builder}", 30)
        self.run_in_vm(f"lxc image delete {MICROCEPH_IMAGE_ALIAS}", 30)
        self.run_in_vm_and_check(f"lxc init local:{BASE_IMAGE_ALIAS} {builder}", 120)
        self.run_in_vm_and_check(f"lxc config set {builder} security.privileged true", 10)
        self.run_in_vm_and_check(f"lxc config set {builder} security.nesting true", 10)
        # run_in_vm already wraps the command in `bash -eo pipefail -c`, so the
        # original's redundant outer `bash -c "..."` wrapper is dropped and the
        # printf|lxc pipeline is passed directly. RAW_LXC_DEVICE_ALLOW carries the
        # literal backslash-n the remote printf needs.
        self.run_in_vm_and_check(
            f"printf '{RAW_LXC_DEVICE_ALLOW}' | lxc config set {IMG_BUILDER_NAME} raw.lxc -",
            10,
        )
        self.run_in_vm_and_check(f"lxc config device add {builder} homedir disk source={home} path=/mnt", 10)
        self.run_in_vm_and_check(f"lxc start {builder}", 60)
        time.sleep(5)
        # snap-version readiness loop: break as soon as `snap version` succeeds.
        # raise_on_timeout=False mirrors the original break-and-fall-through: if snap never
        # reports ready we proceed anyway and the apt/snap calls below surface any real problem.
        self._poll_until(
            lambda: self.exec_in_container(builder, "snap", "version", timeout=10).rc == 0,
            attempts=20,
            interval=3,
            fail_msg="",
            raise_on_timeout=False,
        )
        self.run_in_container_and_check(
            builder, f"apt-get update -qq && apt-get -qq -y install {' '.join(VM_APT_TOOLS)}", 300
        )
        self.run_in_container_and_check(
            builder, f"snap install --dangerous {MNT_SNAP_GLOB}", 600
        )
        connects = " && ".join(f"snap connect microceph:{iface}" for iface in SNAP_INTERFACES_MINIMAL)
        self.run_in_container_and_check(builder, connects, 120)
        self.exec_in_container(builder, "snap", "alias", "microceph.ceph", "ceph", timeout=30, check=True)
        # The remote sed command must contain a SINGLE literal backslash before each '*'
        # (matching the original .resource cell '\\*\\*', which Robot unescapes to '\*\*').
        # In this Python string each '\\' is one literal backslash, so '\\*\\*' -> '\*\*'.
        self.run_in_container_and_check(
            builder,
            f"sed -e 's|/sys/devices/\\*\\*/ r,|/sys/devices/** r,|' -i.bak {SNAP_APPARMOR_PROFILE}",
            30,
        )
        self.run_in_vm_and_check(f"lxc stop {builder}", 60)
        self.run_in_vm_and_check(f"lxc publish {builder} --alias {MICROCEPH_IMAGE_ALIAS}", 300)
        self.run_in_vm_and_check(f"lxc delete {builder}", 10)
        logger.console("[setup] Base image ubuntu-22.04-microceph ready.")

    def create_lxd_containers_with_loop_devices(self, network_type="public"):
        """Creates 4 privileged LXD containers with loop-back disks."""
        logger.console(f"[setup] Creating LXD containers with loop devices (network={network_type})...")
        self.run_in_vm_and_check(f"lxc network create {network_type}", 60)
        nw = self._network_cidr(network_type)
        gw = nw.split("/")[0]
        mask = nw.split("/")[1]
        home = self.run_in_vm("echo $HOME", 10).stdout.strip()
        inner_image = BuiltIn().get_variable_value("${INNER_NODE_IMAGE}", "ubuntu:22.04")
        self.run_in_vm_and_check(f"lxc image copy {inner_image} local: --alias {BASE_IMAGE_ALIAS}", 600)
        self.build_base_lxd_image(home)
        for i, c in enumerate(NODES):
            self.run_in_vm_and_check(f"lxc init local:{MICROCEPH_IMAGE_ALIAS} {c}", 120)
            self.run_in_vm_and_check(f"lxc config set {c} security.privileged true", 10)
            self.run_in_vm_and_check(f"lxc config set {c} security.nesting true", 10)
            self.run_in_vm_and_check(
                f"printf '{RAW_LXC_DEVICE_ALLOW}' | lxc config set {c} raw.lxc -",
                10,
            )
            self.run_in_vm_and_check(f"lxc config device add {c} homedir disk source={home} path=/mnt", 10)
            self.run_in_vm_and_check(f"lxc network attach {network_type} {c} eth2", 10)
            self.run_in_vm_and_check(f"lxc start {c}", 60)
            time.sleep(2)
            dev = self._last_eth_interface(
                self.run_in_container_unchecked(c, "ip a", 30).stdout
            )
            self.exec_in_container(c, "ip", "addr", "add", f"{gw}{i}/{mask}", "dev", dev, timeout=10, check=True)
            lf = self.run_in_vm(f"sudo mktemp -p /mnt mctest-{i}-XXXX.img", 30).stdout.strip()
            self.run_in_vm_and_check(f"sudo truncate -s 1G {lf}", 30)
            ld = self.run_in_vm(f"sudo losetup --show -f {lf}", 30).stdout.strip()
            minor = ld.replace("/dev/loop", "")
            self.exec_in_container(c, "mknod", "-m", "0660", "/dev/sdia", "b", "7", minor, timeout=10, check=True)
            self.exec_in_container(c, "ln", "-s", "/bin/true", "/usr/local/bin/udevadm", timeout=10, check=True)
        self.install_tools()

    def install_microceph_on_all_nodes(self, snap_path=None):
        """Activates the pre-baked local snap on all 4 inner containers."""
        snap_path = snap_path or self._snap_path()
        if not snap_path:
            logger.warn("SNAP_PATH not set - skipping multi-node snap installation")
            return
        logger.console("[install] Activating MicroCeph on all nodes...")
        for container in NODES:
            logger.console(f"[install] Activating on {container}...")
            self.ensure_snap_mount_healthy(container)
            self.exec_in_container(container, "apparmor_parser", "-r", SNAP_APPARMOR_PROFILE, timeout=60, check=True)
            self.exec_in_container(container, "snap", "restart", "microceph.daemon", timeout=120, check=True)

    def install_microceph_from_store_on_all_nodes(self, channel):
        """Installs microceph from the Snap Store *channel* on all 4 inner containers."""
        logger.console(f"[install] Installing MicroCeph from store ({channel}) on all nodes...")
        for container in NODES:
            self.ensure_snap_mount_healthy(container)
            # Store install needs only s3cmd (not jq), so the apt-get install list is
            # kept literal rather than driven from VM_APT_TOOLS.
            self.run_in_container_and_check(
                container,
                f"sudo snap remove --purge microceph >/dev/null 2>&1 || true; sudo apt-get update -qq && sudo apt-get -qq -y install s3cmd && sudo snap install microceph --channel {channel}",
                600,
            )

    def bootstrap_head_node(self, network_mode="public", extra_flags=""):
        """Bootstraps microceph on node-wrk0."""
        head = HEAD_NODE
        logger.console(f"[cluster] Bootstrapping head node ({head}, network={network_mode})...")
        if network_mode == "public":
            nw = self._network_cidr("public")
            self.run_in_container(
                head, f"microceph cluster bootstrap --public-network={nw} {extra_flags}", 120
            )
            time.sleep(5)
            nw = self._network_cidr("public")
            gw = nw.split("/")[0]
            node_ip = f"{gw}0"
            cnt = self.run_in_container_unchecked(
                head,
                f"grep 'mon host' {CEPH_CONF} | grep -c '{node_ip}'",
                30,
            ).stdout.strip()
            if cnt != "1":
                raise AssertionError(
                    f"IP {node_ip} not exactly-once on mon host line in {head} ceph.conf (mirrors verify_bootstrap_configs)"
                )
            pub_count = self.run_in_container_unchecked(
                head,
                f"grep -c 'public_network = {nw}' {CEPH_CONF}",
                30,
            ).stdout.strip()
            if pub_count != "1":
                raise AssertionError(
                    f"public_network = {nw} not exactly-once in {head} ceph.conf (mirrors verify_bootstrap_configs)"
                )
        elif network_mode == "internal":
            nw = self._network_cidr("internal")
            gw = nw.split("/")[0]
            node_ip = f"{gw}0"
            self.run_in_container(
                head, f"microceph cluster bootstrap --microceph-ip={node_ip} {extra_flags}", 120
            )
            time.sleep(10)
            self.run_in_container(head, f"microceph status | grep {head} | grep {node_ip}", 30)
        else:
            self.run_in_container(head, f"microceph cluster bootstrap {extra_flags}", 120)
        self.run_in_container(head, "microceph status", 30)
        time.sleep(4)
        self.run_in_container(
            head, f'microceph.ceph -s | grep "mon: 1 daemons, quorum {head}"', 30
        )

    def bootstrap_multi_subnet_head(self):
        """Bootstraps node-wrk0 across two subnets, mon-ip on the SECOND listed one.

        Reproduces canonical/microceph#734: the head already has an address on the
        "public" network (eth2, from create_lxd_containers_with_loop_devices). This
        creates a second LXD network ("subnetb"), attaches it as an extra NIC,
        assigns the head an address on it, and bootstraps with a comma-delimited
        --public-network / --cluster-network plus --mon-ip set to the address on the
        SECOND listed subnet -- the exact case that failed before multi-subnet
        support. Returns [public_networks, mon_ip] for the suite to assert against.
        """
        head = HEAD_NODE
        logger.console("[cluster] Bootstrapping head node across two subnets...")
        self.run_in_vm_and_check("lxc network create subnetb", 60)
        public_cidr = self._network_cidr("public")
        second_cidr = self._network_cidr("subnetb")
        _, mask2 = second_cidr.split("/")
        mon_ip = self._host_address(second_cidr, 10)
        self.run_in_vm_and_check(f"lxc network attach subnetb {head} eth3", 10)
        time.sleep(2)
        dev = self._last_eth_interface(
            self.run_in_container_unchecked(head, "ip a", 30).stdout
        )
        self.exec_in_container(
            head, "ip", "addr", "add", f"{mon_ip}/{mask2}", "dev", dev, timeout=10, check=True
        )
        public_networks = f"{public_cidr},{second_cidr}"
        self.run_in_container(
            head,
            f"microceph cluster bootstrap --public-network={public_networks} "
            f"--cluster-network={public_networks} --mon-ip={mon_ip}",
            120,
        )
        time.sleep(5)
        self.run_in_container(head, "microceph status", 30)
        return [public_networks, mon_ip]

    def join_worker_nodes_to_cluster(self, network_mode="public"):
        """Joins node-wrk1..3 to the cluster."""
        logger.console(f"[cluster] Joining worker nodes to cluster ({network_mode})...")
        head = HEAD_NODE
        nw = self._network_cidr(network_mode)
        gw, mask = nw.split("/")
        mon_ips = [f"{gw}0"]
        for i in range(1, len(NODES)):
            node = NODES[i]
            logger.console(f"[cluster] Joining {node}...")
            tok = self.exec_in_container(head, "microceph", "cluster", "add", node, timeout=60).stdout.strip()
            if network_mode == "internal":
                node_ip = f"{gw}{i}"
                self.run_in_container(
                    node, f"microceph cluster join {tok} --microceph-ip={node_ip}", 120
                )
                time.sleep(10)
                self.run_in_container(
                    head, f"microceph status | grep {node} | grep {node_ip}", 30
                )
            else:
                self.run_in_container(node, f"microceph cluster join {tok}", 120)
            if network_mode == "public":
                for ip in mon_ips:
                    ip_count = self.run_in_container_unchecked(
                        node,
                        f"grep 'mon host' {CEPH_CONF} | grep -c '{ip}'",
                        30,
                    ).stdout.strip()
                    if ip_count != "1":
                        raise AssertionError(
                            f"IP {ip} not exactly-once on mon host line of {node} (mirrors verify_bootstrap_configs)"
                        )
                pub_count = self.run_in_container_unchecked(
                    node,
                    f"grep -c 'public_network = {nw}' {CEPH_CONF}",
                    30,
                ).stdout.strip()
                if pub_count != "1":
                    raise AssertionError(
                        f"public_network = {nw} not exactly-once in {node} ceph.conf (mirrors verify_bootstrap_configs)"
                    )
                mon_ips.append(f"{gw}{i}")
        self.wait_for_n_nodes_in_cluster(len(NODES))
        self.run_in_container(head, "microceph status", 30)
        self.run_in_container(head, "microceph.ceph -s", 30)

    # -----------------------------------------------------------------------
    # Multi-node specific helpers (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def enable_services_on_node(self, node):
        """Enables mon/mds/mgr services on *node*.

        DEAD KEYWORD (pre-existing): no suite calls "Enable Services On Node"; the multi-node
        suite uses its own "Enable Services On Head Node For" (a different, node-wrk0-driven
        implementation). Ported from the pre-refactor harness as-is; flagged for a maintainer to
        confirm before removing rather than silently dropped.
        """
        logger.console(f"[cluster] Enabling mon/mds/mgr on {node}...")
        for svc in ("mon", "mds", "mgr"):
            self.run_in_vm_and_check(f"sudo microceph enable {svc} --target {node}", 120)
        for _ in range(8):
            result = self.run_in_vm(
                f'sudo microceph.ceph -s | grep -q "mon: .*daemons.*{node}" && echo yes || echo no', 30
            )
            if result.stdout.strip() == "yes":
                break
            time.sleep(2)
        self.run_in_vm_and_check("sudo microceph.ceph -s", 30)

    def remove_node(self, node):
        """Removes *node* from the cluster.

        DEAD KEYWORD (pre-existing): no suite calls "Remove Node"; the multi-node suite uses its
        own "Remove Node Head Node" (node-wrk0-driven, with health-wait and retry). Ported from
        the pre-refactor harness as-is; flagged for a maintainer to confirm before removing.
        """
        logger.console(f"[cluster] Removing node {node}...")
        self.run_in_vm_and_check(f"sudo microceph cluster remove {node}", 120)
        for _ in range(8):
            result = self.run_in_vm(
                f'sudo microceph.ceph -s | grep -q "mon: .*daemons.*{node}" && echo yes || echo no', 30
            )
            if result.stdout.strip() != "yes":
                break
            time.sleep(5)
        time.sleep(1)
        self.run_in_vm_and_check("sudo microceph.ceph -s", 30)
        self.run_in_vm_and_check("sudo microceph status", 30)

    def get_node_ip(self, container):
        """Returns the primary IP of *container* (first address from hostname -I), or "" if none.

        Mirrors the pre-refactor ``hostname -I | cut -d ' ' -f1`` + ``.strip()`` which yielded
        an empty string (not an IndexError) when the network was not yet up, so the single NFS
        caller surfaces an informative mount failure rather than masking it with an IndexError.
        """
        parts = self.exec_in_container(container, "hostname", "-I", timeout=30).stdout.split()
        return parts[0] if parts else ""

    # -----------------------------------------------------------------------
    # Upgrade helpers (migrated from microceph_harness.resource)
    #
    # Note: the pre-refactor harness also defined "Refresh Multi Node Snap", which had no suite
    # caller (dead) and was intentionally not ported. Recorded here so the omission is a
    # documented decision rather than a silent drop.
    # -----------------------------------------------------------------------

    def upgrade_multi_node(self):
        """Upgrades all 4 inner containers to the local snap build."""
        logger.console("[upgrade] Upgrading all nodes to local snap build...")
        connects = " && ".join(f"snap connect microceph:{iface}" for iface in SNAP_INTERFACES_MINIMAL)
        for container in NODES:
            logger.console(f"[upgrade] Upgrading {container}...")
            self.run_in_container_and_check(
                container, f"sudo snap install --dangerous {MNT_SNAP_GLOB}", 600
            )
            self.run_in_container_and_check(container, connects, 60)
            time.sleep(15)
            osd_up_cmd = "microceph.ceph osd status 2>/dev/null | grep -c 'exists,up' || echo 0"

            def osd_up_count():
                return self._safe_int(
                    self.run_in_container_unchecked(container, osd_up_cmd, 30).stdout.strip()
                )

            # Poll up to 36 x 10 s for >= 3 OSDs up; raise_on_timeout=False keeps the original
            # break-and-fall-through so the exact-count assertion below stays the failure gate.
            self._poll_until(
                lambda: osd_up_count() >= 3,
                attempts=36,
                interval=10,
                fail_msg="",
                raise_on_timeout=False,
            )
            count = osd_up_count()
            if count != 3:
                raise AssertionError(f"Expected 3 OSD up after upgrading {container}")

    # -----------------------------------------------------------------------
    # Multi-site replication getters (migrated from microceph_harness.resource)
    # -----------------------------------------------------------------------

    def get_synced_image_count_on_node(self, node):
        """Returns string: total images across all RBD replication entries on *node*."""
        return str(
            rbd_synced_image_count(
                self.exec_in_container(node, "sudo", "microceph", "replication", "list", "rbd", "--json", timeout=30).stdout
            )
        )

    def get_primary_image_count_on_node(self, node):
        """Returns string: count of RBD images where is_primary==true on *node*."""
        return str(
            rbd_primary_image_count(
                self.exec_in_container(node, "sudo", "microceph", "replication", "list", "rbd", "--json", timeout=30).stdout
            )
        )

    def get_rbd_mirror_pool_health(self, node, pool):
        """Returns the summary health string (OK/WARNING/ERROR/UNKNOWN) for *pool* on *node*."""
        return rbd_mirror_health(
            self.exec_in_container(
                node, "sudo", "microceph.rbd", "mirror", "pool", "status", pool, "--verbose", timeout=30
            ).stdout
        )

    def assert_remote_list_has(self, node, field, value):
        """Asserts microceph remote list on *node* has an entry whose *field* == *value*.

        Mirrors ``lxc exec <node> -- microceph remote list --json | jq -e 'any(.[]; .<field> ==
        "<value>")'``: fetches the raw JSON through the container-exec helper and decides in
        Python (no jq), raising AssertionError when no matching remote is present.
        """
        out = self.exec_in_container(node, "microceph", "remote", "list", "--json", timeout=30).stdout
        if not self._remote_list_has(out, field, value):
            raise AssertionError(f"remote list on {node} has no entry with {field}={value}")

    # -----------------------------------------------------------------------
    # Deferred-Ceph / role-managed placement keywords
    #
    # Keywords fetch raw JSON from the MicroCeph control socket (minimum
    # remote I/O) and decide locally via the pure parsers in
    # placement_status.py (unit-tested in test_harness_helpers.py).
    # -----------------------------------------------------------------------

    def microceph_api_get(self, path):
        """GETs a path from the MicroCeph control socket on the outer VM (single-node)
        and returns the raw JSON stdout."""
        res = self.run_in_vm_and_check(
            f"sudo curl -s --unix-socket {MICROCEPH_CONTROL_SOCKET} http://localhost/1.0/{path}", 30
        )
        return res.stdout

    def microceph_api_put(self, path, body, timeout=120):
        """PUTs a JSON body to a path on the MicroCeph control socket on the outer VM.

        Returns the raw response body; decide on it with `Response Status Code`.
        """
        res = self.run_in_vm_and_check(
            f"sudo curl -s -X PUT --unix-socket {MICROCEPH_CONTROL_SOCKET}"
            f" -H 'Content-Type: application/json' -d '{body}' http://localhost/1.0/{path}",
            float(timeout),
        )
        return res.stdout

    def microceph_api_get_in_container(self, container, path):
        """GETs a path from the MicroCeph control socket inside an inner container."""
        res = self.exec_in_container(
            container, "curl", "-s", "--unix-socket", MICROCEPH_CONTROL_SOCKET,
            f"http://localhost/1.0/{path}", timeout=30, check=True,
        )
        return res.stdout

    def microceph_api_put_in_container(self, container, path, body, query="", timeout=300):
        """PUTs a JSON body to a path on the MicroCeph control socket inside an inner container.

        Returns the raw response body, which embeds the outcome ("status_code" 200 on
        success, "error_code" on failure); decide on it with `Response Status Code`.
        The default timeout is 300s because placement PUTs with pending removals poll
        Ceph readiness (MON quorum, MGR, MDS) which can take minutes. The body goes
        through the argv-based container exec helper, so no shell ever interprets it.
        """
        res = self.exec_in_container(
            container, "curl", "-s", "-X", "PUT", "--unix-socket", MICROCEPH_CONTROL_SOCKET,
            "-H", "Content-Type: application/json", "-d", body,
            f"http://localhost/1.0/{path}{query}", timeout=float(timeout), check=True,
        )
        return res.stdout

    def microceph_api_delete_in_container(self, container, path):
        """DELETEs a path on the MicroCeph control socket inside an inner container.

        Returns the raw response body JSON.
        """
        res = self.exec_in_container(
            container, "curl", "-s", "-X", "DELETE", "--unix-socket", MICROCEPH_CONTROL_SOCKET,
            f"http://localhost/1.0/{path}", timeout=60, check=True,
        )
        return res.stdout

    def response_status_code(self, raw):
        """Returns the code embedded in a microcluster API response body (int).

        200 for a sync success, the error_code (e.g. 400) for an error body, 0
        for unparseable bodies (so comparisons against 200 fail closed).
        """
        return placement_status.response_code(raw)

    def get_capabilities_json(self):
        """Returns the capabilities JSON from the outer VM snap."""
        return self.microceph_api_get("cluster/capabilities")

    def get_supported_capabilities(self):
        """Returns the list of deferred-Ceph / placement capability markers advertised by the outer VM snap."""
        return placement_status.supported_capabilities(self.get_capabilities_json())

    def get_placement_status_json(self):
        """Returns the placement status JSON from the outer VM snap."""
        return self.microceph_api_get("placement")

    def get_placement_status_json_in_container(self, container):
        """Returns the placement status JSON from an inner container."""
        return self.microceph_api_get_in_container(container, "placement")

    def placement_policy_active(self):
        """Returns True when the outer VM snap reports an active placement policy."""
        return placement_status.placement_active(self.get_placement_status_json())

    def assert_lifecycle_state(self, expected_state, container=""):
        """Asserts the ceph bootstrap lifecycle state in the placement status JSON.

        Runs on the outer VM by default; pass container= to assert inside an
        inner container.
        """
        if container == "":
            raw = self.get_placement_status_json()
            where = "outer VM"
        else:
            raw = self.get_placement_status_json_in_container(container)
            where = container
        state = placement_status.bootstrap_state(raw)
        if state != expected_state:
            raise AssertionError(
                f"Expected bootstrap_state={expected_state} on {where}, got {state or '<absent>'}: {raw}"
            )

    def assert_bootstrap_state_in_container(self, container, expected_state):
        """Asserts the lifecycle bootstrap_state in the placement status JSON on a container."""
        self.assert_lifecycle_state(expected_state, container)

    def wait_for_microceph_control_socket(self, tries=24):
        """Polls until the microceph control socket exists in the outer VM."""
        self._poll_until(
            lambda: self.run_in_vm(f"test -S {MICROCEPH_CONTROL_SOCKET}", 15).rc == 0,
            attempts=tries,
            interval=5,
            fail_msg="MicroCeph control socket never appeared",
        )

    def wait_for_ceph_healthy_on_container(self, container, tries=40):
        """Polls ceph health on *container* until it reports non-empty health
        that is not HEALTH_ERR."""
        logger.console(f"[health] Waiting for Ceph on {container}...")

        def predicate():
            res = self.exec_in_container(container, "microceph.ceph", "health", timeout=30)
            health = res.stdout.strip() if res.rc == 0 else ""
            if health and health != "HEALTH_ERR":
                logger.console(f"[health] Ceph health on {container}: {health}")
                return True
            return False

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"Ceph never came up on {container}",
        )

    def get_mon_count(self, head_node=HEAD_NODE):
        """Returns the current monmap daemon count reported by ceph -s on *head_node*.

        Returns 0 when the cluster is unreachable, so callers polling for a
        count treat an unready cluster as zero mons.
        """
        res = self.exec_in_container(head_node, "microceph.ceph", "-s", "-f", "json", timeout=30)
        if res.rc != 0:
            return 0
        return placement_status.mon_count(res.stdout)

    def wait_for_mon_count(self, n, tries=30):
        """Polls ceph -s on the head node until at least *n* mon daemons are reported."""

        def predicate():
            count = self.get_mon_count()
            if count >= int(n):
                logger.console(f"[deferred-ceph] {count} mon daemons up")
                return True
            return False

        self._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"Never reached {n} mon daemons",
        )

    def assert_member_has_control_services(self, member, expected="yes"):
        """Asserts that mon/mgr/mds are present on *member* via ceph -s on the head node.

        *expected* is 'yes' or 'no'.
        """
        res = self.exec_in_container(HEAD_NODE, "microceph.ceph", "-s", timeout=30)
        status = res.stdout if res.rc == 0 else ""
        present = placement_status.member_in_ceph_status(status, member)
        if str(expected).strip().lower() == "yes":
            if not present:
                raise AssertionError(
                    f"{member} not found in ceph status; expected control services there. Status: {status}"
                )
        else:
            if present:
                raise AssertionError(
                    f"{member} unexpectedly in ceph status; expected no control services there. Status: {status}"
                )

    def assert_no_ceph_cluster_on_container(self, container):
        """Asserts that *container* has NOT bootstrapped Ceph: ceph status fails
        and no ceph.conf exists."""
        res = self.exec_in_container(container, "microceph.ceph", "status", timeout=30)
        if res.rc == 0:
            raise AssertionError(
                f"microceph.ceph status unexpectedly succeeded on {container}; Ceph should not be bootstrapped"
            )
        conf = self.run_in_container_unchecked(
            container, f"test -f {CEPH_CONF} && echo yes || echo no", 15
        ).stdout.strip()
        if conf != "no":
            raise AssertionError(
                f"Ceph config exists on {container} but Ceph should not be bootstrapped"
            )

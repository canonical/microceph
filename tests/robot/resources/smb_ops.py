"""Owned singleton SMB lab I/O; feature actions and assertions stay in Robot."""
import glob
import hashlib
import json
import math
import re
import shlex
import tempfile
import time
import uuid
from dataclasses import dataclass
from pathlib import Path

from robot.api import logger
from robot.libraries.BuiltIn import BuiltIn
from microceph_harness import microceph_harness as Harness, SNAP_DEST_NAME
from smb_guest import PAYLOAD

OWNER_KEY = "user.microceph-test-owner"
OSD_SERIAL = "lxd_test--data"


def select_owned_osd(instance, volume, inventory, node, volume_name, pool, owner):
    """Select only the exact tagged, unmounted, childless 4GiB data disk."""
    devices = instance.get("expanded_devices", {})
    attachment = devices.get("test-data", {})
    if (not owner or instance.get("name") != node
            or instance.get("expanded_config", {}).get(OWNER_KEY) != owner
            or volume.get("name") != volume_name or volume.get("content_type") != "block"
            or volume.get("config", {}).get(OWNER_KEY) != owner
            or attachment.get("type") != "disk" or attachment.get("pool") != pool
            or attachment.get("source") != volume_name or "path" in attachment
            or not any(d.get("type") == "disk" and d.get("path") == "/" for name, d in devices.items() if name != "test-data")):
        raise AssertionError("Refusing OSD: exact tagged data-volume attachment is not established")
    records = []
    def visit(rows):
        for row in rows:
            records.append(row)
            visit(row.get("children", []))
    visit(inventory.get("blockdevices", []))
    matches = [row for row in records if row.get("serial") == OSD_SERIAL]
    if len(matches) != 1:
        raise AssertionError("Refusing OSD: missing or ambiguous attached-device serial")
    disk = matches[0]
    if (disk.get("type") != "disk" or "pkname" not in disk or disk["pkname"] is not None
            or not any(disk is row for row in inventory["blockdevices"])
            or disk.get("size") != 4 * 1024 ** 3
            or "mountpoints" not in disk or any(disk["mountpoints"])
            or disk.get("children") or not re.fullmatch(r"/dev/[A-Za-z0-9_-]+", disk.get("name", ""))
            or not any("/" in (row.get("mountpoints") or []) for row in records)
            or sum(row.get("name") == disk["name"] for row in records) != 1
            or any(row.get("pkname") == disk["name"] for row in records)):
        raise AssertionError("Refusing OSD: not a dedicated unmounted, childless 4GiB disk with visible root")
    return disk["name"]


def owned_resources(rows, names, owner):
    """Select explicit names only; an unknown/foreign owner forbids deletion."""
    if not isinstance(rows, list) or not owner:
        raise AssertionError("Invalid owned-resource inventory")
    selected = []
    for row in rows:
        if row["name"] not in names:
            continue
        if row.get("config", {}).get(OWNER_KEY) != owner:
            raise AssertionError(f"Refusing foreign resource {row['name']}")
        selected.append(row["name"])
    return selected


def parse_snapshot(raw, node):
    """Unavailable API is never readiness; duplicate role observations reject."""
    value = json.loads(raw)
    value["node"] = node
    try:
        value["status"] = json.loads(value.pop("api"))["metadata"]
    except (ValueError, TypeError, KeyError):
        value["status"] = None
    value["roles"] = {}
    for block in value.pop("services", "").strip().split("\n\n"):
        fields = dict(line.split("=", 1) for line in block.splitlines() if "=" in line)
        name = fields.pop("Id", "").removeprefix("snap.microceph.").removesuffix(".service")
        if name:
            if name in value["roles"]:
                raise AssertionError("Duplicate systemd service observation")
            value["roles"][name] = fields
    return value


def snapshot_ready(snapshot, cluster, port=None):
    """Require owner, clean API status, prepared context and live smbd."""
    status = snapshot.get("status") or {}
    context = snapshot.get("context") or {}
    state = snapshot.get("roles", {}).get("smbd", {})
    pid = state.get("MainPID", "")
    return (snapshot.get("owner") == cluster and status.get("cluster_id") == cluster
            and status.get("ready") is True and not status.get("error") and not status.get("stop_requested")
            and context.get("startup", {}).get("smbd", {}).get("prepared") is True
            and (port is None or context.get("smb_port") == int(port))
            and state.get("ActiveState") == "active" and pid.isdigit() and int(pid) > 0)


def role_tokens(snapshots):
    """Process identities for the installed serving role, never inferred defaults."""
    if not snapshots:
        raise AssertionError("No SMB observations supplied")
    result = {}
    for snapshot in snapshots:
        node = snapshot["node"]
        state = snapshot["roles"]["smbd"]
        pid, start = state["MainPID"], state["ExecMainStartTimestampMonotonic"]
        if node in result or state["ActiveState"] != "active" or not pid.isdigit() or int(pid) <= 0 or not start or start == "0":
            raise AssertionError("Invalid or duplicate SMB process observation")
        result[node] = (pid, start)
    return result


def observations_unchanged(samples, tokens, cluster, port):
    """Every collected sample must remain ready with the same process identity."""
    return bool(samples) and all(snapshot_ready(sample, cluster, port) and role_tokens([sample]) == tokens
                                 for sample in samples)


def observations_disabled(samples, cluster, port):
    """No background publication/start while snapd startup intent is disabled."""
    return bool(samples) and all(sample.get("owner") == cluster
                                 and sample.get("context", {}).get("smb_port") == int(port)
                                 and sample.get("roles", {}).get("smbd", {}).get("ActiveState") == "inactive"
                                 for sample in samples)


def parse_user_sid(raw):
    """Accept exactly one native local user SID, never diagnostic output."""
    value = raw.strip()
    if not re.fullmatch(r"S-1-5-21-\d+-\d+-\d+-\d+", value):
        raise AssertionError("Native SMB user SID is unavailable")
    return value


@dataclass(repr=False)
class _SharedState:
    resources: object
    auth: object

    def __repr__(self):
        return "<SMB shared state; authentication material redacted>"


def shared_state(resources_json, auth_json):
    """Private equality object: Robot must never print actual Ceph keys."""
    document = json.loads(resources_json)
    resources = document.get("resources", [document])
    if not resources or any(row.get("resource_type") not in ("ceph.smb.cluster", "ceph.smb.share") for row in resources):
        raise AssertionError("Missing SMB shared resource inventory")
    resources = sorted(resources, key=lambda row: (row["resource_type"], row["cluster_id"], row.get("share_id", "")))
    records = json.loads(auth_json)["auth_dump"]
    if not records:
        raise AssertionError("Empty Ceph authentication inventory")
    return _SharedState(resources, sorted((row["entity"], row["key"], sorted(row["caps"].items())) for row in records))


class _VM(Harness):
    def __init__(self, name):
        self.name = name

    def _outer_vm(self):
        return "local:" + self.name

    def _vm_argv(self, *rest, vm=None):
        return ["lxc", "exec", "-n", vm or self._outer_vm(), "--project", "default", "--mode=non-interactive", "--", "env", "PATH=/snap/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", *rest]

    def command(self, *argv, timeout=120, check=False, quiet=False):
        method = self.run_in_vm_and_check if check else self.run_in_vm
        return method(shlex.join(str(arg) for arg in argv), timeout, quiet)


class smb_ops:
    """Singleton-only topology, exec and observation primitives."""
    ROBOT_LIBRARY_SCOPE = "SUITE"
    _base = "/var/snap/microceph/common/robot-smb"

    def __init__(self):
        self.nodes, self.volumes = [], []
        self.owner, self.pool, self.manifest = "", "default", ""
        self.harness = Harness()

    def _variable(self, name, default):
        return BuiltIn().get_variable_value("${" + name + "}", default)

    def _host(self, *argv, timeout=120):
        result = self.harness._exec(list(argv), timeout)
        if result.rc != 0:
            raise AssertionError(f"SMB lab operation failed: {result.stderr}")
        return result.stdout

    def _persist_smb_lab(self):
        target = Path(self.manifest)
        document = {"owner": self.owner, "pool": self.pool, "nodes": [node.name for node in self.nodes], "volumes": self.volumes}
        if target.exists() and json.loads(target.read_text()).get("owner") != self.owner:
            raise AssertionError("Refusing to overwrite another SMB lab manifest")
        temporary = target.with_suffix(".tmp")
        with temporary.open("w") as stream:
            temporary.chmod(0o600)
            json.dump(document, stream)
        temporary.replace(target)

    def use_smb_lab(self, path=""):
        self.manifest = path or self._variable("SMB_LAB_MANIFEST", "")
        data = json.loads(Path(self.manifest).read_text())
        owner = data["owner"]
        if not re.fullmatch(r"[0-9a-f]{32}", owner):
            raise AssertionError("Invalid SMB lab owner")
        expected = [f"smb-ci-{owner[:8]}-n{index}" for index in range(3)]
        if (len(data["nodes"]) != len(set(data["nodes"])) or any(name not in expected for name in data["nodes"])
                or any(volume not in [name + "-osd" for name in expected] for volume in data["volumes"])
                or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", data["pool"])):
            raise AssertionError("Invalid or foreign SMB lab names")
        self.owner, self.pool, self.volumes = owner, data["pool"], data["volumes"]
        self.nodes = [_VM(name) for name in data["nodes"]]

    def verify_smb_lab(self):
        """Recheck host tags before a scenario may retire gateway state."""
        rows = json.loads(self._host("lxc", "query", "local:/1.0/instances?recursion=1&project=default"))
        names = [node.name for node in self.nodes]
        if set(owned_resources(rows, names, self.owner)) != set(names):
            raise AssertionError("SMB lab is missing an owned VM")
        if any(row["status"] != "Running" for row in rows if row["name"] in names):
            raise AssertionError("SMB lab VM is not running")

    def _capacity(self, remaining):
        headroom = int(self._variable("SMB_DISK_HEADROOM_GIB", 50))
        reserve = int(self._variable("SMB_DISK_RESERVE_PER_VM_GIB", 24))
        if headroom <= 0 or reserve < 0:
            raise ValueError("SMB disk capacity limits require positive headroom and nonnegative reservation")
        space = json.loads(self._host("lxc", "query", f"local:/1.0/storage-pools/{self.pool}/resources"))["space"]
        memory = next(int(line.split()[1]) * 1024 for line in Path("/proc/meminfo").read_text().splitlines() if line.startswith("MemAvailable:"))
        gib = 1024 ** 3
        disk = space["total"] - space["used"]
        required_disk, required_memory = (headroom + remaining * reserve) * gib, (4 + remaining * 3) * gib
        observation = f"SMB lab capacity: disk free={disk / gib:.1f} GiB, required={required_disk / gib:.1f} GiB; memory available={memory / gib:.1f} GiB, required={required_memory / gib:.1f} GiB"
        logger.info(observation)
        if disk < required_disk or memory < required_memory:
            raise AssertionError(observation)

    def provision_smb_lab(self):
        """Three backing-Ceph VMs retain quorum/replicas during gateway reboot.

        There are no clustered SMB or directory VMs. Each OSD has a separately
        tagged block volume; intent is persisted before any allocation.
        """
        matches = glob.glob(str(self._variable("SNAP_PATH", "")))
        if len(matches) != 1 or not Path(matches[0]).is_file():
            raise AssertionError("SMB integration requires exactly one built SNAP_PATH")
        snap = matches[0]
        with open(snap, "rb") as source:
            expected = hashlib.file_digest(source, "sha256").hexdigest()
        self.pool = str(self._variable("SMB_STORAGE_POOL", "default"))
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", self.pool):
            raise AssertionError("Invalid test storage pool")
        remotes = json.loads(self._host("lxc", "remote", "list", "--format", "json"))
        if self._host("lxc", "remote", "get-default").strip() != "local" or remotes["local"].get("Project", "") not in ("", "default"):
            raise AssertionError("SMB lab requires local LXD/default project")
        self.manifest = self._variable("SMB_LAB_MANIFEST", "")
        if not self.manifest:
            raise AssertionError("Use the singleton coordinator to allocate an owned lab")
        self.owner = uuid.uuid4().hex
        image = str(self._variable("SMB_VM_IMAGE", "ubuntu:26.04"))
        channel = str(self._variable("SMB_SNAPD_CHANNEL", "latest/stable"))
        for number in range(3):
            # Capacity is checked only at actual allocation, never as a host-unit gate.
            self._capacity(3 - number)
            name = f"smb-ci-{self.owner[:8]}-n{number}"
            volume = name + "-osd"
            node = _VM(name)
            self.nodes.append(node)
            self.volumes.append(volume)
            self._persist_smb_lab()
            self._host("lxc", "storage", "volume", "create", "local:" + self.pool, volume, "--project", "default", "--type=block", "size=4GiB", f"{OWNER_KEY}={self.owner}")
            self.harness.launch_owned_test_vm(name, self.owner, image=image, storage_pool=self.pool, data_volume=volume)
            node.probe_instance_network()
            node.copy_snap_to_vm(snap)
            if node.command("sha256sum", "/root/" + SNAP_DEST_NAME, check=True).stdout.split()[0] != expected:
                raise AssertionError("Guest candidate snap differs from the supplied artifact")
            node.install_microceph_from_local_snap(snap, snapd_channel=channel)
            for plug in ("network", "network-control", "dm-crypt", "smb-identity", "smb-mounts", "smb-control-mounts"):
                node.command("snap", "connect", "microceph:" + plug, check=True)
            node.command("snap", "restart", "microceph.daemon", check=True)
            node.wait_for_microceph_control_socket()
            node.command("grep", "-Fx", "confinement: strict", "/snap/microceph/current/meta/snap.yaml", check=True)
            if node.command("snap", "debug", "confinement", check=True).stdout.strip() != "strict":
                raise AssertionError("SMB suite requires actual strict confinement")
            node.command("mkdir", "-p", self._base, check=True)
            node.command("chmod", "0700", self._base, check=True)
            self.harness._lxc_file_push(str(Path(__file__).with_name("smb_guest.py")), f"local:{name}{self._base}/smb_guest.py", 60, "copy SMB guest fixture", private=True)
        self.nodes[0].apt_update()
        self.nodes[0].apt_install(["smbclient"])
        addresses = [json.loads(node.command("ip", "-4", "-j", "route", "get", "1.1.1.1", check=True, quiet=True).stdout)[0]["prefsrc"] for node in self.nodes]
        self.nodes[0].command("python3", self._base + "/smb_guest.py", "init", json.dumps(addresses), check=True)

    def _node(self, index):
        index = int(index)
        if not 0 <= index < len(self.nodes):
            raise AssertionError("SMB node index is outside this suite's manifest")
        return self.nodes[index]

    def smb_node_command(self, node, *argv):
        return self._node(node).command(*argv, timeout=180)

    def join_smb_member(self, node):
        target = self._node(node)
        token = self._base + "/join.token"
        self.nodes[0].command("sh", "-ec", 'umask 077; microceph cluster add "$1" > "$2"', "join", target.name, token, check=True)
        with tempfile.TemporaryDirectory(prefix="smb-token-") as directory:
            local = str(Path(directory) / "token")
            self._host("lxc", "file", "pull", f"local:{self.nodes[0].name}{token}", local, "--project", "default")
            self.harness._lxc_file_push(local, f"local:{target.name}{token}", 60, "copy join token", private=True)
        self.nodes[0].command("rm", "-f", token, check=True, quiet=True)
        return target.command("sh", "-c", 'microceph cluster join "$(cat "$1")" > "$2" 2>&1; rc=$?; rm -f "$1"; exit "$rc"', "join", token, self._base + "/join.result", timeout=180)

    def smb_osd_path(self, node):
        index = int(node)
        vm = self._node(index)
        volume_name = self.volumes[index]
        instance = json.loads(self._host("lxc", "query", f"local:/1.0/instances/{vm.name}?project=default"))
        volume = json.loads(self._host("lxc", "query", f"local:/1.0/storage-pools/{self.pool}/volumes/custom/{volume_name}?project=default"))
        inventory = json.loads(vm.command("lsblk", "--json", "--bytes", "--paths", "--output", "NAME,TYPE,SIZE,SERIAL,PKNAME,MOUNTPOINTS", check=True).stdout)
        return select_owned_osd(instance, volume, inventory, vm.name, volume_name, self.pool, self.owner)

    def _smb_ceph_json(self, deadline, *args):
        remaining = math.floor(deadline - time.monotonic())
        if remaining <= 0:
            raise AssertionError("SMB Ceph readiness did not settle within 300 seconds")
        result = self.nodes[0].command("microceph.ceph", *args, timeout=min(30, remaining), check=True, quiet=True)
        try:
            return json.loads(result.stdout)
        except (TypeError, ValueError) as error:
            raise AssertionError(f"SMB Ceph readiness received malformed JSON from {args}") from error

    def wait_for_smb_ceph_health(self):
        """Require two coherent settled PG/monitor observations within 300s."""
        deadline = time.monotonic() + 300
        previous = None
        accepted = None
        def probe():
            nonlocal previous, accepted
            before = self._smb_ceph_json(deadline, "osd", "dump", "-f", "json")
            pgs = self._smb_ceph_json(deadline, "pg", "dump", "pgs_brief", "-f", "json")
            health = self._smb_ceph_json(deadline, "health", "detail", "-f", "json")
            clock = self._smb_ceph_json(deadline, "time-sync-status", "-f", "json")
            after = self._smb_ceph_json(deadline, "osd", "dump", "-f", "json")
            signature = smb_ceph_readiness_signature(health, clock, before, pgs, after)
            if time.monotonic() >= deadline:
                raise AssertionError("SMB Ceph readiness did not settle within 300 seconds")
            ready = signature is not None and signature == previous
            previous = signature
            if ready:
                accepted = health
            return ready
        def pause():
            time.sleep(min(3, max(0, deadline - time.monotonic())))
        self.harness._poll_until(probe, attempts=100, interval=0, between=pause, fail_msg="SMB Ceph readiness did not settle within 300 seconds")
        if "MON_CLOCK_SKEW" in accepted["checks"]:
            logger.warn("Ceph MON_CLOCK_SKEW (non-fatal for SMB tests): " + json.dumps(accepted["checks"]["MON_CLOCK_SKEW"]))
        return accepted

    def prepare_smb_data_paths(self):
        return self.nodes[0].command("timeout", "--kill-after=5", "60", "snap", "run", "--shell", "microceph.ceph", "-c", 'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_guest.py" prepare-fs', timeout=80)

    def apply_smb_resource(self, kind):
        return self.nodes[0].command("python3", self._base + "/smb_guest.py", "apply", kind, timeout=150)

    def smb_snapshot(self, node, cluster="robot-never"):
        vm = self._node(node)
        result = vm.command("python3", self._base + "/smb_guest.py", "snapshot", cluster, timeout=120, check=True, quiet=True)
        return parse_snapshot(result.stdout, vm.name)

    def smb_snapshot_is_ready(self, snapshot, cluster="robot-never", port=None):
        return snapshot_ready(snapshot, cluster, port)

    def wait_for_smb_ready(self, node, cluster="robot-never", port=None):
        self.harness._poll_until(lambda: snapshot_ready(self.smb_snapshot(node, cluster), cluster, port), attempts=40, interval=3, fail_msg="SMB did not become ready without intervention")
        return self.smb_snapshot(node, cluster)

    def smb_role_tokens(self, snapshot):
        return role_tokens([snapshot])

    def observe_smb_interval(self, node, cluster="robot-never"):
        samples = []
        deadline = time.monotonic() + 65  # More than two production updater periods.
        def collect():
            samples.append(self.smb_snapshot(node, cluster))
            return time.monotonic() >= deadline
        self.harness._poll_until(collect, attempts=30, interval=3, fail_msg="SMB observation window did not finish")
        return samples

    def smb_observations_unchanged(self, samples, tokens, cluster="robot-never", port=3445):
        return observations_unchanged(samples, tokens, cluster, port)

    def smb_observations_disabled(self, samples, cluster="robot-never", port=3445):
        return observations_disabled(samples, cluster, port)

    def smb_user_sid(self, node):
        body = ('exec "$SNAP/bin/unshare" --mount --propagation slave -- /bin/bash -euc '
                + shlex.quote('. "$SNAP/commands/smb-startup"; smb_load_context; smb_setup_nss; '
                              'smb_assert_prepared smbd; smb_check_start_intent; export SMB_ROOT; '
                              'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_guest.py" identity'))
        result = self._node(node).command("snap", "run", "--shell", "microceph.smbd", "-c", body,
                                          timeout=120, check=True, quiet=True)
        return parse_user_sid(result.stdout)

    def smb_client_transfer(self, node, filename, upload=True, auth="alice", cluster="robot-never"):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", filename) or auth not in ("alice", "wrong", "refreshed"):
            raise AssertionError("Invalid SMB test filename/auth selector")
        endpoint = self.smb_snapshot(node, cluster)["context"]
        commands = (f"put {self._base}/payload.bin {filename}; " if upload else "") + f"get {filename} {self._base}/readback.bin"
        self.nodes[0].command("rm", "-f", self._base + "/readback.bin", check=True, quiet=True)
        return self.nodes[0].command("smbclient", "//" + endpoint["frontend_address"] + "/data", "-p", endpoint["smb_port"], "-A", self._base + "/" + auth + ".auth", "-c", commands, timeout=60)

    def compare_smb_payload(self):
        return self.nodes[0].command("cmp", self._base + "/payload.bin", self._base + "/readback.bin", timeout=20)

    def reboot_smb_vm(self, node):
        vm = self._node(node)
        self._host("lxc", "restart", "local:" + vm.name, "--project", "default", "--timeout", "120", timeout=180)
        self.harness.wait_for_vm_agent("local:" + vm.name)

    def capture_smb_shared_state(self, *clusters):
        if not clusters or any(cluster not in ("robot-never", "robot-other") for cluster in clusters):
            raise AssertionError("Unknown logical SMB fixture")
        vm = self.nodes[0]
        names = [name for cluster in clusters for name in ("ceph.smb.cluster." + cluster, "ceph.smb.share." + cluster + ".data")]
        resources = vm._exec(vm._vm_argv("microceph.ceph", "smb", "show", *names, "-f", "json"), 30)
        auth = vm._exec(vm._vm_argv("microceph.ceph", "auth", "ls", "-f", "json"), 30)
        if resources.rc != 0 or auth.rc != 0:
            raise AssertionError("Could not capture shared SMB resources/authentication")
        return shared_state(resources.stdout, auth.stdout)

    def smb_backing_file_digest(self, filename, cluster="robot-never"):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", filename) or cluster not in ("robot-never", "robot-other"):
            raise AssertionError("Invalid SMB fixture file/cluster")
        result = self.nodes[0].command("timeout", "--kill-after=5", "60", "snap", "run", "--shell", "microceph.ceph", "-c", 'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_guest.py" read-data "$1" "$2"', "fixture", cluster, filename, timeout=80, check=True)
        digest = result.stdout.strip()
        if not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise AssertionError("Backing-data read returned no digest")
        return digest

    def smb_payload_digest(self):
        return hashlib.sha256(PAYLOAD).hexdigest()

    def retire_smb_scenario(self):
        for index, node in enumerate(self.nodes):
            owner = self.smb_snapshot(index)["owner"]
            if owner and owner not in ("robot-never", "robot-other"):
                raise AssertionError("Refusing to retire a foreign SMB owner")
            if owner:
                node.command("microceph", "disable", "smb", "--cluster-id", owner, timeout=180, check=True)
        self.assert_smb_scenario_idle()

    def assert_smb_scenario_idle(self):
        for index in range(len(self.nodes)):
            state = self.smb_snapshot(index)
            if state["owner"] or state["roles"]["smbd"]["ActiveState"] != "inactive":
                raise AssertionError("SMB scenario did not release its gateway roles")

    def collect_smb_diagnostics(self):
        for node in self.nodes:
            try:
                node.command("snap", "services", "microceph", timeout=20)
                node.command("journalctl", "-b", "-u", "snap.microceph.smbd", "-u", "snap.microceph.daemon", "--no-pager", "-n", "100", timeout=20)
                node.command("journalctl", "-k", "-b", "-g", 'apparmor="DENIED"', "--no-pager", "-n", "100", timeout=20)
            except Exception as error:
                logger.warn(f"SMB diagnostics unavailable for {node.name}: {error}")

    def destroy_smb_lab(self):
        """Delete only exact named/tagged VMs and unattached owned volumes."""
        if not self.owner:
            return
        rows = json.loads(self._host("lxc", "query", "local:/1.0/instances?recursion=1&project=default"))
        for name in owned_resources(rows, [node.name for node in self.nodes], self.owner):
            self._host("lxc", "delete", "local:" + name, "--project", "default", "--force", timeout=180)
            self.harness._poll_until(lambda: name not in [row["name"] for row in json.loads(self._host("lxc", "query", "local:/1.0/instances?recursion=1&project=default"))], attempts=10, interval=3, fail_msg=f"Deletion of {name} was not confirmed")
        rows = json.loads(self._host("lxc", "query", f"local:/1.0/storage-pools/{self.pool}/volumes/custom?recursion=1&project=default"))
        names = owned_resources(rows, self.volumes, self.owner)
        for row in rows:
            if row["name"] in names:
                if row.get("used_by"):
                    raise AssertionError("Refusing to delete an attached SMB test volume")
                self._host("lxc", "storage", "volume", "delete", "local:" + self.pool, row["name"], "--project", "default")


def smb_ceph_readiness_signature(health, clock, before, pgs, after):
    """Require settled complete PG mappings on one unchanged OSD epoch."""
    try:
        checks = health["checks"]
        clock_only = health["status"] == "HEALTH_WARN" and set(checks) == {"MON_CLOCK_SKEW"} and checks["MON_CLOCK_SKEW"]["severity"] == "HEALTH_WARN"
        if not clock_only and (health["status"] != "HEALTH_OK" or checks != {}):
            return None
        monitor_health = ("HEALTH_OK", "HEALTH_WARN") if clock_only else ("HEALTH_OK",)
        monitors = clock["time_skew_status"]
        if (not isinstance(monitors, dict) or not monitors
                or any(not isinstance(row, dict) or row.get("health") not in monitor_health for row in monitors.values())
                or clock["timechecks"]["round_status"] != "finished"):
            return None
        if before != after or str(uuid.UUID(before["fsid"])) != before["fsid"] or type(before["epoch"]) is not int or before["epoch"] <= 0:
            return None
        available = set()
        for osd in before["osds"]:
            if type(osd["osd"]) is not int or osd["osd"] < 0 or osd["osd"] in available or type(osd["up"]) is not int or osd["up"] != 1 or type(osd["in"]) is not int or osd["in"] != 1:
                return None
            available.add(osd["osd"])
        expected, pools = {}, []
        fields = ("pool", "size", "pg_num", "pg_num_target", "pg_num_pending", "pg_placement_num", "pg_placement_num_target")
        for pool in before["pools"]:
            if any(type(pool[field]) is not int or pool[field] <= 0 for field in fields):
                return None
            if any(pool[field] != pool["pg_num"] for field in fields[3:]):
                return None
            pools.append(tuple(pool[field] for field in fields))
            for number in range(pool["pg_num"]):
                pgid = f'{pool["pool"]}.{number:x}'
                if pgid in expected:
                    return None
                expected[pgid] = pool["size"]
        if not expected or not available or pgs.get("pg_ready") is not True or len(pgs["pg_stats"]) != len(expected):
            return None
        observed = {}
        for row in pgs["pg_stats"]:
            pgid, up, acting = row["pgid"], row["up"], row["acting"]
            if (pgid not in expected or pgid in observed or row["state"] != "active+clean" or up != acting
                    or len(acting) != expected[pgid] or len(set(acting)) != len(acting)
                    or any(type(osd) is not int or osd not in available for osd in acting)):
                return None
            observed[pgid] = tuple(acting)
        return before["fsid"], before["epoch"], tuple(sorted(pools)), tuple(sorted(observed.items()))
    except (KeyError, TypeError, ValueError, AttributeError):
        return None

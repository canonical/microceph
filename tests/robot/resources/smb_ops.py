"""SMB integration I/O and observations; scenario assertions stay in Robot."""
import glob
import hashlib
import json
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

ROLES = ("smbd", "smbmetrics", "ctdbd", "ctdb-nodes")
OWNER_KEY = "user.microceph-test-owner"


def parse_snapshot(raw, node):
    """Parse raw control/systemd observations; unavailable API is not readiness."""
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


def snapshot_ready(snapshot, cluster, port=None, clustered=True):
    """Require an owned/applied/error-free gateway, not just CLI acceptance."""
    status = snapshot.get("status") or {}
    context = snapshot.get("context") or {}
    roles = snapshot.get("roles") or {}
    if (snapshot.get("owner") != cluster or status.get("cluster_id") != cluster
            or not status.get("ready") or status.get("error") or status.get("stop_requested")
            or not snapshot.get("applied") or snapshot.get("pending")
            or context.get("clustered") is not bool(clustered)
            or (port is not None and context.get("smb_port") != int(port))):
        return False
    for role in ROLES + (("winbindd",) if context.get("ad_member") else ()):
        state = roles.get(role, {})
        pid = state.get("MainPID", "")
        active = state.get("ActiveState") == "active" and pid.isdigit() and int(pid) > 0
        if role in ("ctdbd", "ctdb-nodes") and not clustered:
            if state.get("ActiveState") != "inactive":
                return False
        elif not active:
            return False
    return True


def role_tokens(snapshots, roles=ROLES):
    """Return node-keyed PID/start identities, refusing empty or inactive data."""
    if not snapshots:
        raise AssertionError("No SMB observations supplied")
    result = {}
    for snapshot in snapshots:
        node = snapshot["node"]
        if node in result:
            raise AssertionError("Duplicate SMB node observation")
        result[node] = {}
        for role in roles:
            state = snapshot["roles"][role]
            pid, start = state["MainPID"], state["ExecMainStartTimestampMonotonic"]
            if state["ActiveState"] != "active" or not pid.isdigit() or int(pid) <= 0 or not start or start == "0":
                raise AssertionError(f"{node}/{role} is not an active process")
            result[node][role] = (pid, start)
    return result


def observations_stable(before, samples):
    """All samples must match; a later healthy sample never erases a failure."""
    if not before or not samples:
        return False
    try:
        tokens = role_tokens(before)
        baseline = {s["node"]: s for s in before}
        for sample in samples:
            if role_tokens(sample) != tokens:
                return False
            for state in sample:
                old = baseline[state["node"]]
                if (not snapshot_ready(state, old["owner"], old["context"]["smb_port"], True)
                        or state["applied_mtime"] != old["applied_mtime"]
                        or state["boot_id"] != old["boot_id"]):
                    return False
    except (KeyError, TypeError, AssertionError):
        return False
    return True


def owned_resources(rows, names, owner):
    """Select only explicit names, refusing any present foreign owner."""
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


@dataclass(repr=False)
class _SharedState:
    resources: object
    auth: object

    def __repr__(self):
        return "<SMB shared state; authentication material redacted>"


def shared_state(resources_json, auth_json):
    """Private equality object; Robot must never print actual Ceph keys."""
    document = json.loads(resources_json)
    resources = document.get("resources", [document])
    if not resources or any(r.get("resource_type") not in ("ceph.smb.cluster", "ceph.smb.share") for r in resources):
        raise AssertionError("Missing SMB shared resource inventory")
    resources = sorted(resources, key=lambda r: (r["resource_type"], r["cluster_id"], r.get("share_id", "")))
    records = json.loads(auth_json)["auth_dump"]
    if not records:
        raise AssertionError("Empty Ceph authentication inventory")
    auth = sorted((r["entity"], r["key"], sorted(r["caps"].items())) for r in records)
    return _SharedState(resources, auth)


class _VM(Harness):
    """Bind existing execution/install/polling helpers to one explicit VM."""
    def __init__(self, name):
        self.name = name

    def _outer_vm(self):
        return "local:" + self.name

    def _vm_argv(self, *rest):
        return ["lxc", "exec", "-n", self._outer_vm(), "--project", "default",
                "--mode=non-interactive", "--", "env",
                "PATH=/snap/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", *rest]

    def command(self, *argv, timeout=120, check=False, quiet=False):
        method = self.run_in_vm_and_check if check else self.run_in_vm
        return method(shlex.join(str(arg) for arg in argv), timeout, quiet)


class smb_ops:
    """Suite-scoped SMB I/O/topology primitives, not test-case bodies."""
    ROBOT_LIBRARY_SCOPE = "SUITE"
    _base = "/var/snap/microceph/common/robot-smb"

    def __init__(self):
        self.nodes = []
        self.owner = ""
        self.volumes = []
        self.pool = "default"
        self.ad_dc = None
        self.harness = Harness()

    @staticmethod
    def _variable(name, default):
        return BuiltIn().get_variable_value("${" + name + "}", default)

    def _host(self, *argv, timeout=120):
        result = self.harness._exec(list(argv), timeout)
        if result.rc != 0:
            raise AssertionError(f"SMB lab operation failed: {result.stderr}")
        return result.stdout

    def _node(self, index):
        index = int(index)
        if not 0 <= index < len(self.nodes):
            raise AssertionError("SMB node index is outside this suite's manifest")
        return self.nodes[index]

    def _capacity(self, remaining):
        space = json.loads(self._host("lxc", "query", f"local:/1.0/storage-pools/{self.pool}/resources"))["space"]
        memory = next(int(line.split()[1]) * 1024 for line in Path("/proc/meminfo").read_text().splitlines() if line.startswith("MemAvailable:"))
        gib = 1024 ** 3
        disk = space["total"] - space["used"]
        required_disk, required_memory = (50 + remaining * 24) * gib, (4 + remaining * 3) * gib
        if disk < required_disk or memory < required_memory:
            raise AssertionError(
                f"SMB lab capacity: disk free={disk / gib:.1f} GiB, required={required_disk / gib:.1f} GiB; "
                f"memory available={memory / gib:.1f} GiB, required={required_memory / gib:.1f} GiB "
                "(includes 50 GiB disk/4 GiB memory headroom)")

    def provision_smb_lab(self):
        """Create three tagged real VMs; no existing instance/profile is modified."""
        matches = glob.glob(str(self._variable("SNAP_PATH", "")))
        if len(matches) != 1 or not Path(matches[0]).is_file():
            raise AssertionError("SMB integration requires exactly one built SNAP_PATH")
        snap = matches[0]
        digest = hashlib.sha256()
        with open(snap, "rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
        expected = digest.hexdigest()
        self.pool = str(self._variable("SMB_STORAGE_POOL", "default"))
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", self.pool):
            raise AssertionError("Invalid test storage pool")
        remotes = json.loads(self._host("lxc", "remote", "list", "--format", "json"))
        if self._host("lxc", "remote", "get-default").strip() != "local" or remotes["local"].get("Project", "") not in ("", "default"):
            raise AssertionError("SMB lab requires local LXD/default project")
        self.owner = uuid.uuid4().hex
        self._capacity(3)
        image = str(self._variable("SMB_VM_IMAGE", "ubuntu:26.04"))
        channel = str(self._variable("SMB_SNAPD_CHANNEL", "latest/stable"))
        for number in range(3):
            self._capacity(3 - number)
            name = f"smb-ci-{self.owner[:8]}-n{number}"
            volume = name + "-osd"
            node = _VM(name)
            # Record intent before creation so teardown also sees partial failures.
            self.nodes.append(node)
            self.volumes.append(volume)
            logger.info("SMB allocation manifest: " + json.dumps({"owner": self.owner, "pool": self.pool,
                        "nodes": [n.name for n in self.nodes], "volumes": self.volumes, "snap_sha256": expected}))
            self._host("lxc", "storage", "volume", "create", "local:" + self.pool, volume,
                       "--project", "default", "--type=block", "size=4GiB", f"{OWNER_KEY}={self.owner}")
            self.harness.launch_owned_test_vm(name, self.owner, image=image, storage_pool=self.pool, data_volume=volume)
            node.probe_instance_network()
            node.copy_snap_to_vm(snap)
            digest = node.command("sha256sum", "/root/" + SNAP_DEST_NAME, check=True).stdout.split()[0]
            if digest != expected:
                raise AssertionError("Guest candidate snap differs from the supplied artifact")
            node.install_microceph_from_local_snap(snap, snapd_channel=channel)
            for plug in ("network", "network-control", "dm-crypt", "smb-identity", "smb-mounts", "ctdb-run", "ctdb-socket", "winbind-run"):
                node.command("snap", "connect", "microceph:" + plug, check=True)
            node.command("snap", "restart", "microceph.daemon", check=True)
            node.wait_for_microceph_control_socket()
            node.command("grep", "-Fx", "confinement: strict", "/snap/microceph/current/meta/snap.yaml", check=True)
            if node.command("snap", "debug", "confinement", check=True).stdout.strip() != "strict":
                raise AssertionError("SMB suite requires actual strict confinement")
            if node.command("blockdev", "--getsize64", "/dev/sdb", check=True).stdout.strip() != str(4 * 1024 ** 3):
                raise AssertionError("Refusing to use a disk other than the dedicated 4GiB OSD")
            node.command("mkdir", "-p", self._base, check=True)
            node.command("chmod", "0700", self._base, check=True)
            self.harness._lxc_file_push(str(Path(__file__).with_name("smb_guest.py")), f"local:{name}{self._base}/smb_guest.py", 60, "copy SMB guest fixture")
            node.command("chmod", "0600", self._base + "/smb_guest.py", check=True)
        self.nodes[0].apt_update()
        self.nodes[0].apt_install(["smbclient"])
        addresses = [json.loads(n.command("ip", "-4", "-j", "route", "get", "1.1.1.1", check=True, quiet=True).stdout)[0]["prefsrc"] for n in self.nodes]
        self.nodes[0].command("python3", self._base + "/smb_guest.py", "init", json.dumps(addresses), check=True)

    def smb_node_command(self, node, *argv):
        """Execute one request once; Robot asserts rc and postconditions."""
        return self._node(node).command(*argv, timeout=180)

    def join_smb_member(self, node):
        target = self._node(node)
        token = self._base + "/join.token"
        self.nodes[0].command("sh", "-ec", 'umask 077; microceph cluster add "$1" > "$2"',
                              "join", target.name, token, check=True)
        with tempfile.TemporaryDirectory(prefix="smb-token-") as directory:
            local = str(Path(directory) / "token")
            self._host("lxc", "file", "pull", f"local:{self.nodes[0].name}{token}", local, "--project", "default")
            self.harness._lxc_file_push(local, f"local:{target.name}{token}", 60, "copy join token")
        self.nodes[0].command("rm", "-f", token, check=True, quiet=True)
        # Keep both the token and any token-bearing join error private.
        return target.command("sh", "-c", 'microceph cluster join "$(cat "$1")" > "$2" 2>&1; rc=$?; rm -f "$1"; exit "$rc"',
                              "join", token, self._base + "/join.result", timeout=180)

    def wait_for_smb_ceph_health(self):
        self.nodes[0].wait_for_cluster_health_ok()

    def prepare_smb_data_paths(self):
        return self.nodes[0].command("timeout", "--kill-after=5", "60", "snap", "run", "--shell", "microceph.ceph", "-c",
                                     'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_guest.py" prepare-fs', timeout=80)

    def apply_smb_resource(self, kind):
        return self.nodes[0].command("python3", self._base + "/smb_guest.py", "apply", kind, timeout=150)

    def smb_snapshot(self, node, cluster="robot-smb"):
        vm = self._node(node)
        result = vm.command("python3", self._base + "/smb_guest.py", "snapshot", cluster, timeout=25, quiet=True)
        if result.rc != 0:
            raise AssertionError(f"SMB observation failed on {vm.name}: {result.stderr}")
        return parse_snapshot(result.stdout, vm.name)

    def smb_snapshots(self, cluster, *nodes):
        return [self.smb_snapshot(node, cluster) for node in nodes]

    def smb_snapshot_is_ready(self, snapshot, cluster="robot-smb", port=None, clustered=True):
        return snapshot_ready(snapshot, cluster, port, clustered)

    def wait_for_smb_ready(self, node, cluster="robot-smb", port=None, clustered=True):
        self.harness._poll_until(lambda: snapshot_ready(self.smb_snapshot(node, cluster), cluster, port, clustered),
                                 attempts=40, interval=3, fail_msg=f"{self._node(node).name}: SMB did not become ready without intervention")
        return self.smb_snapshot(node, cluster)

    def smb_role_tokens(self, snapshots, *roles):
        return role_tokens(snapshots, roles or ROLES)

    def collect_smb_observations(self, seconds=35, cluster="robot-smb", *nodes):
        samples = []
        until = time.monotonic() + int(seconds)
        def sample():
            samples.append(self.smb_snapshots(cluster, *(nodes or (0, 1, 2))))
            return time.monotonic() >= until
        self.harness._poll_until(sample, attempts=int(seconds) + 2, interval=3, fail_msg="SMB observation window did not complete")
        return samples

    def smb_observations_are_stable(self, before, samples):
        return observations_stable(before, samples)

    def smb_client_transfer(self, node, filename, upload=True, auth="alice", cluster="robot-smb"):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", filename) or auth not in ("alice", "wrong"):
            raise AssertionError("Invalid SMB test filename/auth selector")
        state = self.smb_snapshot(node, cluster)
        endpoint = state["context"]
        commands = (f"put {self._base}/payload.bin {filename}; " if upload else "") + f"get {filename} {self._base}/readback.bin"
        self.nodes[0].command("rm", "-f", self._base + "/readback.bin", check=True, quiet=True)
        return self.nodes[0].command("smbclient", "//" + endpoint["frontend_address"] + "/data", "-p", endpoint["smb_port"],
                                     "-A", self._base + "/" + auth + ".auth", "-c", commands, timeout=60)

    def compare_smb_payload(self):
        return self.nodes[0].command("cmp", self._base + "/payload.bin", self._base + "/readback.bin", timeout=20)

    def reboot_smb_vm(self, node):
        vm = self._node(node)
        self._host("lxc", "restart", "local:" + vm.name, "--project", "default", "--timeout", "120", timeout=180)
        self.harness.wait_for_vm_agent("local:" + vm.name)

    def retire_smb_fixture_placements(self):
        """Release the prior disposable fixture, without treating recovery as a pass."""
        for index, node in enumerate(self.nodes):
            owner = self.smb_snapshot(index)["owner"]
            if owner and owner != "robot-smb":
                raise AssertionError("Refusing to retire a different logical SMB fixture")
            if owner:
                node.command("microceph", "disable", "smb", "--cluster-id", owner, timeout=180, check=True)

    def smb_payload_digest(self):
        from smb_guest import PAYLOAD
        return hashlib.sha256(PAYLOAD).hexdigest()

    def capture_smb_shared_state(self, *clusters):
        # No _log_exec: auth ls contains keys, even for this disposable cluster.
        vm = self.nodes[0]
        clusters = clusters or ("robot-smb",)
        if any(cluster not in ("robot-smb", "robot-never", "robot-other", "robot-ad") for cluster in clusters):
            raise AssertionError("Unknown logical SMB fixture")
        names = [name for cluster in clusters for name in ["ceph.smb.cluster." + cluster] +
                 ["ceph.smb.share." + cluster + "." + share for share in (("data", "read", "auth") if cluster == "robot-ad" else ("data",))]]
        resources = vm._exec(vm._vm_argv("microceph.ceph", "smb", "show", *names, "-f", "json"), 30)
        auth = vm._exec(vm._vm_argv("microceph.ceph", "auth", "ls", "-f", "json"), 30)
        if resources.rc != 0 or auth.rc != 0:
            raise AssertionError("Could not capture shared SMB resources/authentication")
        return shared_state(resources.stdout, auth.stdout)

    def smb_backing_file_digest(self, filename, cluster="robot-never"):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", filename):
            raise AssertionError("Invalid SMB test filename")
        if cluster not in ("robot-smb", "robot-never", "robot-other"):
            raise AssertionError("Unknown logical SMB fixture")
        result = self.nodes[0].command("timeout", "--kill-after=5", "60", "snap", "run", "--shell", "microceph.ceph", "-c",
                                      'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_guest.py" read-data "$1" "$2"',
                                      "fixture", cluster, filename, timeout=80, check=True)
        digest = result.stdout.strip()
        if not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise AssertionError("Backing-data read returned no digest")
        return digest

    def collect_smb_diagnostics(self):
        """Capture public status/journal only, never private source/key files."""
        for node in self.nodes[:3]:
            try:
                node.command("snap", "services", "microceph", timeout=20)
                node.command("journalctl", "-b", "-u", "snap.microceph.smbd", "-u", "snap.microceph.ctdbd",
                             "-u", "snap.microceph.daemon", "-u", "snap.microceph.winbindd", "--no-pager", "-n", "100", timeout=20)
                node.command("journalctl", "-k", "-b", "-g", 'apparmor="DENIED"', "--no-pager", "-n", "100", timeout=20)
            except Exception as error:
                logger.warn(f"SMB diagnostics unavailable for {node.name}: {error}")

    def require_smb_ad_capacity(self):
        """Account for three gateways/client plus one dedicated directory VM."""
        self.pool = str(self._variable("SMB_STORAGE_POOL", "default"))
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", self.pool):
            raise AssertionError("Invalid test storage pool")
        self._capacity(4)

    def provision_smb_ad_directory(self):
        """Extend the owned lab, reusing its manifest, exec and cleanup machinery."""
        self._capacity(1)
        dc = _VM(f"smb-ci-{self.owner[:8]}-dc")
        self.nodes.append(dc)  # Record partial allocation before launch.
        self.ad_dc = dc
        logger.info("SMB allocation manifest: " + json.dumps({"owner": self.owner, "pool": self.pool,
                    "nodes": [n.name for n in self.nodes], "volumes": self.volumes}))
        self.harness.launch_owned_test_vm(dc.name, self.owner, image=str(self._variable("SMB_VM_IMAGE", "ubuntu:26.04")), storage_pool=self.pool)
        dc.probe_instance_network()
        dc.apt_update()
        dc.command("env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "samba", "samba-ad-dc", "python3-samba", "krb5-user", "bind9-host", check=True, timeout=300)
        self.nodes[0].command("env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "krb5-user", "samba-common-bin", check=True, timeout=300)
        for node in self.nodes:
            node.command("mkdir", "-p", self._base, check=True)
            node.command("chmod", "0700", self._base, check=True)
            for script in (("smb_guest.py", "smb_ad_guest.py") if node is dc else ("smb_ad_guest.py",)):
                self.harness._lxc_file_push(str(Path(__file__).with_name(script)), f"local:{node.name}{self._base}/{script}", 60, "copy AD fixture")
                node.command("chmod", "0600", self._base + "/" + script, check=True)
        address = lambda n: json.loads(n.command("ip", "-4", "-j", "route", "get", "1.1.1.1", check=True).stdout)[0]["prefsrc"]
        addresses, dc_address = [address(n) for n in self.nodes[:3]], address(dc)
        # Stop packaged default roles/resolved only inside this disposable DC.
        dc.command("systemctl", "disable", "--now", "smbd", "nmbd", "winbind", "samba-ad-dc", "systemd-resolved", check=True)
        dc.command("sh", "-ec", 'umask 077; python3 "$1/smb_ad_guest.py" dc "$2" "$3" > "$1/dc-provision.log" 2>&1',
                   "fixture", self._base, dc_address, addresses[0], check=True, timeout=300)
        dc.command("systemctl", "daemon-reload", check=True)
        dc.command("systemctl", "enable", "--now", "robot-ad-dc", check=True)
        # Credentials never cross Robot variables, argv, public logs or artifacts.
        with tempfile.TemporaryDirectory(prefix="smb-ad-secret-") as directory:
            local = str(Path(directory) / "secrets")
            self._host("lxc", "file", "pull", f"local:{dc.name}{self._base}/ad-secrets.json", local, "--project", "default")
            Path(local).chmod(0o600)
            self.harness._lxc_file_push(local, f"local:{self.nodes[0].name}{self._base}/ad-secrets.json", 60, "copy private AD inputs")
        self.nodes[0].command("chmod", "0600", self._base + "/ad-secrets.json", check=True)
        self.nodes[0].command("python3", self._base + "/smb_ad_guest.py", "init", json.dumps(addresses), dc_address, check=True)
        for node in (dc, self.nodes[0]):
            node.command("dpkg-query", "-W", "samba-common-bin", "krb5-user", check=True)
        self.nodes[0].command("smbclient", "--version", check=True)
        self.nodes[0].command("testparm", "-s", "--parameter-name=client ntlmv2 auth", check=True)
        dc.command("testparm", "-s", "--parameter-name=ntlm auth", "/root/robot-ad-dc/etc/smb.conf", check=True)
        # Observation polling only; provisioning/start are never retried.
        self.harness._poll_until(lambda: dc.command("host", "-t", "SRV", "_kerberos._udp.robot.test", dc_address).rc == 0,
                                 attempts=10, interval=2, fail_msg="DC Kerberos DNS discovery unavailable")
        self.harness._poll_until(lambda: self.nodes[0].command("python3", self._base + "/smb_ad_guest.py", "probe-directory", timeout=75).rc == 0,
                                 attempts=5, interval=2, fail_msg="Fixture KDC is not ready for fresh client tickets")

    def smb_ad_apply(self, kind):
        return self.nodes[0].command("python3", self._base + "/smb_ad_guest.py", "apply", kind, timeout=90)

    def smb_ad_identity(self, node):
        result = self._node(node).command("python3", self._base + "/smb_ad_guest.py", "mappings", timeout=90, check=True)
        return json.loads(result.stdout)

    def prepare_smb_ad_data(self, uid, gid):
        return self.nodes[0].command("timeout", "--kill-after=5", "60", "snap", "run", "--shell", "microceph.ceph", "-c",
                                    'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_ad_guest.py" prepare-data "$1" "$2"',
                                    "fixture", uid, gid, timeout=80)

    def smb_ad_backing_stat(self, filename):
        result = self.nodes[0].command("timeout", "--kill-after=5", "60", "snap", "run", "--shell", "microceph.ceph", "-c",
                                      'exec "$SNAP/bin/python3" "$SNAP_COMMON/robot-smb/smb_ad_guest.py" stat "$1"',
                                      "fixture", filename, timeout=80, check=True)
        return json.loads(result.stdout)

    def smb_ad_client(self, node, share, user, protocol, action, filename):
        address = self.smb_snapshot(node, "robot-ad")["context"]["frontend_address"]
        start = time.monotonic()
        result = self.nodes[0].command("python3", self._base + "/smb_ad_guest.py", "client", address, share, user, protocol, action, filename, timeout=150, check=True)
        value = json.loads(result.stdout)
        value["elapsed"] = time.monotonic() - start
        return value

    def wait_for_smb_ad_refresh(self, before):
        def refreshed():
            states = self.smb_snapshots("robot-ad", 0, 1)
            return all(snapshot_ready(state, "robot-ad", 1445, True) and
                       state["applied_mtime"] != old["applied_mtime"] for state, old in zip(states, before))
        self.harness._poll_until(refreshed, attempts=30, interval=3, fail_msg="AD join-input refresh was not confirmed applied")
        return self.smb_snapshots("robot-ad", 0, 1)

    def smb_ad_join_error(self):
        return self.nodes[0].command("python3", self._base + "/smb_ad_guest.py", "join-error", check=True).stdout

    def smb_ad_account(self):
        result = self.ad_dc.command("python3", self._base + "/smb_ad_guest.py", "account", check=True)
        return json.loads(result.stdout)

    def smb_ad_diagnostics(self):
        for node in self.nodes[:1] + ([self.ad_dc] if self.ad_dc is not None else []):
            if node is not None:
                node.command("python3", self._base + "/smb_ad_guest.py", "diagnostics", timeout=20)

    def smb_ad_dc_command(self, *argv):
        return self.ad_dc.command(*argv, timeout=90)

    def destroy_smb_lab(self):
        """Delete only this suite's named/tagged VMs and unattached block volumes."""
        if not self.owner:
            return
        rows = json.loads(self._host("lxc", "query", "local:/1.0/instances?recursion=1&project=default"))
        names = owned_resources(rows, [node.name for node in self.nodes], self.owner)
        for name in names:
            self._host("lxc", "delete", "local:" + name, "--project", "default", "--force", timeout=180)
            self.harness._poll_until(
                lambda: name not in [row["name"] for row in json.loads(self._host("lxc", "query", "local:/1.0/instances?recursion=1&project=default"))],
                attempts=10, interval=3, fail_msg=f"Deletion of {name} was not confirmed")
        rows = json.loads(self._host("lxc", "query", f"local:/1.0/storage-pools/{self.pool}/volumes/custom?recursion=1&project=default"))
        names = owned_resources(rows, self.volumes, self.owner)
        for row in rows:
            if row["name"] in names:
                if row.get("used_by"):
                    raise AssertionError("Refusing to delete an attached SMB test volume")
                self._host("lxc", "storage", "volume", "delete", "local:" + self.pool, row["name"], "--project", "default")

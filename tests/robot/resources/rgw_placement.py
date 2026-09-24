"""RGW placement helpers for virtual-machine tests."""

import ipaddress
import json
from pathlib import Path
import re
import shlex

from robot.api import logger

import placement_status
from microceph_harness import MICROCEPH_CONTROL_SOCKET, microceph_harness

# Runs inside the coordinating guest so samples are taken every 100 ms
# regardless of the lxc exec round trip; see start_rgw_migration_sampler_in_vm.
RGW_MIGRATION_SAMPLER = """\
import os, sys, time, urllib.request
tag, old, new, port, path, expected = sys.argv[1:7]
def url(host):
    host = f"[{host}]" if ":" in host else host
    return f"http://{host}:{port}{path}"
def serves(host):
    try:
        with urllib.request.urlopen(url(host), timeout=1) as response:
            return response.read().decode().strip() == expected
    except Exception:
        return False
with open(f"/tmp/{tag}.samples", "w") as out:
    while not os.path.exists(f"/tmp/{tag}.done"):
        started = os.path.exists(f"/tmp/{tag}.started")
        new_ok = serves(new)
        old_ok = False if new_ok else serves(old)
        out.write(f"{int(started)} {int(new_ok)} {int(old_ok)}\\n")
        out.flush()
        time.sleep(0.1)
    out.write("END\\n")
"""


class rgw_placement:
    """Keep gateway-specific operations out of the shared execution harness."""

    ROBOT_LIBRARY_SCOPE = "SUITE"

    def __init__(self):
        self._harness = microceph_harness()

    def _exec(self, arguments, vm_name=None, timeout=30, check=True):
        result = self._harness._exec(self._harness._vm_argv(*arguments, vm=vm_name), timeout)
        if check and result.rc != 0:
            raise AssertionError(f"RGW command failed ({result.rc}): {result.stderr}")
        return result

    # -----------------------------------------------------------------------
    # RGW placement observability (named-VM aware, default outer VM)
    # -----------------------------------------------------------------------

    def rgw_service_invocation_id_in_vm(self, vm_name=None):
        """Returns the systemd InvocationID of the snap rgw unit ('' when absent).

        A stable InvocationID across a re-apply proves idempotent reconciliation
        did not restart the daemon; a changed one proves a frontend change or
        recovery restarted it.
        """
        out = self._harness.run_in_vm(
            "systemctl show snap.microceph.rgw -p InvocationID --value", 30, quiet=True, vm_name=vm_name,
        ).stdout
        return out.strip()

    def wait_for_rgw_unit_state_in_vm(self, inactive=False, tries=36, vm_name=None):
        """Polls the snap rgw systemd unit until active (or inactive when inactive=True).

        systemctl is-active is the reliable signal here: ceph -s keeps the rgw
        service-map line for a lag after the daemon stops.
        """
        want = "inactive" if inactive else "active"
        # A gateway that ignored SIGTERM is killed by systemd and reported as
        # "failed"; for this check that is as stopped as "inactive".
        accepted = {"inactive", "failed"} if inactive else {"active"}
        where = vm_name or self._harness._outer_vm()
        logger.console(f"[rgw] Waiting for rgw unit to be {want} on {where}...")

        def predicate():
            res = self._harness.run_in_vm("systemctl is-active snap.microceph.rgw", 15, quiet=True, vm_name=vm_name)
            return res.stdout.strip() in accepted

        self._harness._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=f"rgw systemd unit on {where} never became {want}",
        )

    def get_rgw_daemon_count_in_vm(self, vm_name=None):
        """Returns the RGW daemon count from ceph -s inside *vm_name* (None on failure)."""
        res = self._harness.run_in_vm("sudo microceph.ceph -s", 30, quiet=True, vm_name=vm_name)
        if res.rc != 0:
            return None
        return self._harness._rgw_daemon_count(res.stdout)

    def wait_for_rgw_count_in_vm(self, expect, tries=20, vm_name=None):
        """Polls ceph -s inside *vm_name* until at least *expect* RGW daemons run."""
        last = [0]

        def predicate():
            count = self.get_rgw_daemon_count_in_vm(vm_name)
            last[0] = count if count is not None else 0
            return last[0] >= int(expect)

        def on_fail():
            self._harness.run_in_vm("sudo microceph.ceph -s", 30, vm_name=vm_name)

        self._harness._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=lambda: f"Never reached {expect} RGW daemon(s) (last saw {last[0]})",
            on_fail=on_fail,
        )

    def get_rgw_frontend_conf_ports(self, vm_name=None):
        """Returns the rendered beast frontend listeners from radosgw.conf, parsed in Python."""
        text = self._harness.run_in_vm(
            "cat /var/snap/microceph/current/conf/radosgw.conf", 30, quiet=True, vm_name=vm_name,
        ).stdout
        return placement_status.rgw_frontend_conf_ports(text)

    def rgw_tls_paths_in_vm(self, vm_name=None):
        """Returns the exact certificate/key pair radosgw.conf currently references."""
        text = self._exec(["cat", "/var/snap/microceph/current/conf/radosgw.conf"], vm_name).stdout
        return placement_status.rgw_frontend_tls_paths(text)

    def rgw_tls_file_modes_in_vm(self, vm_name=None):
        """Returns the octal mode of each file radosgw.conf currently references for TLS."""
        paths = self.rgw_tls_paths_in_vm(vm_name)
        if not paths:
            return []
        return self._exec(["stat", "-c", "%a", *paths], vm_name).stdout.splitlines()

    def rgw_tls_material_paths_in_vm(self, vm_name=None):
        """Returns every generated (rgw-tls/*) or legacy (server.crt/server.key) TLS path present."""
        script = (
            "from pathlib import Path; import json; "
            "p=Path('/var/snap/microceph/common'); "
            "files=[x for x in (p/'rgw-tls').rglob('*') if x.is_file()]; "
            "files += [p/n for n in ('server.crt','server.key') if (p/n).exists()]; "
            "print(json.dumps([str(x) for x in files]))"
        )
        return json.loads(self._exec(["python3", "-c", script], vm_name).stdout)

    def wait_for_member_rgw_frontend(self, member, port=None, ssl=None, present=True, tries=48, vm_name=None):
        """Polls GET /placement until *member*'s observed rgw_frontend matches.

        port/ssl are compared when not None; for plaintext (ssl=False) any
        reported ssl_port must be absent/zero. present=False waits for the
        frontend to drop away (member disable / scale-to-zero). The frontend is
        the member's last successfully applied configuration as served from
        the database, so matching it proves the apply landed.
        """
        vm = vm_name or self._harness._outer_vm()
        last = [{}]

        def predicate():
            raw = self._harness.get_placement_status_json_in_vm(vm)
            try:
                last[0] = placement_status.member_rgw_frontend(raw, member)
            except ValueError:
                return False
            if not present:
                return not last[0]
            if not last[0]:
                return False
            if port is not None and int(last[0].get("port", 0)) != int(port):
                return False
            if ssl is not None and bool(last[0].get("ssl", False)) != bool(ssl):
                return False
            if ssl is False and int(last[0].get("ssl_port", 0)) != 0:
                return False
            return True

        self._harness._poll_until(
            predicate,
            attempts=tries,
            interval=5,
            fail_msg=lambda: (
                f"observed rgw_frontend for {member} never reached "
                f"(port={port}, ssl={ssl}, present={present}); last seen: {last[0]}"
            ),
        )

    def member_rgw_frontend_status(self, raw, member):
        """Returns *member*'s observed rgw_frontend dict from a GET /placement body ({} when absent)."""
        return placement_status.member_rgw_frontend(raw, member)

    # -----------------------------------------------------------------------
    # TLS fixtures
    # -----------------------------------------------------------------------

    def generate_tls_pair_in_vm(self, prefix, cn, san_ip="", vm_name=None):
        """Generates a disposable CA + server certificate pair inside the VM.

        Material stays at /tmp/<prefix>/ (ca.{key,crt}, server.{key,crt}); only
        the prefix and CA path are returned, so the certificate and key never
        cross the Robot boundary or land in output.xml. san_ip adds an IP SAN
        so a client on another VM can validate the handshake against the
        gateway VM's address.
        """
        for value in (prefix, cn):
            if not re.fullmatch(r"[A-Za-z0-9.-]+", value):
                raise AssertionError("unsafe TLS fixture name")
        san = f", IP:{ipaddress.ip_address(san_ip)}" if san_ip else ""
        cmd = (
            f"umask 077; mkdir -p /tmp/{prefix} && cd /tmp/{prefix} && "
            "openssl genrsa -out ca.key 2048 && "
            "openssl req -x509 -new -nodes -key ca.key -days 1024 -out ca.crt -outform PEM "
            f"-subj '/C=US/ST=Denial/L=Springfield/O=Dis/CN={cn}' && "
            "openssl genrsa -out server.key 2048 && "
            f"openssl req -new -key server.key -out server.csr -subj '/C=US/ST=Denial/L=Springfield/O=Dis/CN={cn}' && "
            f"printf 'subjectAltName = DNS:localhost{san}\\n' > extfile.cnf && "
            "openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial "
            "-out server.crt -days 365 -extfile extfile.cnf"
        )
        self._harness.run_in_vm_and_check(cmd, 120, vm_name=vm_name)
        return {"prefix": prefix, "ca": f"/tmp/{prefix}/ca.crt"}

    def rgw_tls_fingerprint_in_vm(self, host, port, vm_name=None):
        """Returns the sha256 fingerprint of the certificate served on host:port (or '')."""
        out = self._harness.run_in_vm(
            f"echo | openssl s_client -connect {host}:{port} 2>/dev/null | "
            "openssl x509 -noout -fingerprint -sha256 2>/dev/null",
            30, quiet=True, vm_name=vm_name,
        ).stdout
        m = re.search(r"Fingerprint=(\S+)", out)
        return m.group(1) if m else ""

    # -----------------------------------------------------------------------
    # S3 / HTTP fetch primitives
    # -----------------------------------------------------------------------

    def fetch_rgw_object_in_vm(self, host, port, path, ca_cert="", vm_name=None):
        """Read an object over the requested frontend; return the real command result."""
        scheme = "https" if ca_cert else "http"
        address = f"[{host}]" if ":" in host else host
        args = ["curl", "--fail", "--silent", "--show-error", "--max-time", "3"]
        if ca_cert:
            args.extend(["--cacert", ca_cert])
        args.append(f"{scheme}://{address}:{int(port)}{path}")
        return self._exec(args, vm_name, check=False)

    def wait_for_rgw_fetch_in_vm(self, host, port, path, ca_cert="", vm_name=None, attempts=10, interval=1):
        """Polls a trusted fetch until it succeeds; returns the LAST result either way.

        Listener readiness can lag a preceding PUT or upload by a beat, so this
        absorbs that short lag. The retry budget is deliberately small: it is
        not a substitute for `Wait For Member RGW Frontend` (which proves the
        apply landed) -- the equality assertion against the expected body stays
        in Robot.
        """
        last = [None]

        def predicate():
            last[0] = self.fetch_rgw_object_in_vm(host, port, path, ca_cert=ca_cert, vm_name=vm_name)
            return last[0].rc == 0

        self._harness._poll_until(
            predicate,
            attempts=attempts,
            interval=interval,
            fail_msg="unused: raise_on_timeout is False",
            raise_on_timeout=False,
        )
        return last[0]

    def rgw_endpoint_reachable_in_vm(self, host, port, vm_name=None):
        """Check TCP reachability, including TLS listeners that reject plaintext HTTP."""
        script = "import socket,sys; s=socket.create_connection((sys.argv[1],int(sys.argv[2])),2); s.close()"
        return self._exec(["python3", "-c", script, host, str(port)], vm_name, check=False).rc == 0

    def wait_for_rgw_endpoint_closed_in_vm(self, host, port, vm_name=None, attempts=15, interval=1):
        """Polls until host:port stops accepting TCP connections; returns the final reachability.

        A listener can take a moment to unbind after a policy apply restarts or
        stops RGW; the retry budget stays short and bounded because a genuine
        failure to close should surface promptly.
        """
        last = [True]

        def predicate():
            last[0] = self.rgw_endpoint_reachable_in_vm(host, port, vm_name=vm_name)
            return not last[0]

        self._harness._poll_until(
            predicate,
            attempts=attempts,
            interval=interval,
            fail_msg="unused: raise_on_timeout is False",
            raise_on_timeout=False,
        )
        return last[0]

    def upload_rgw_object_in_vm(self, host, port, bucket, name, content, ssl=False, ca_cert="", vm_name=None):
        """Create the test user and upload an object; assertions stay in Robot."""
        user = self._exec(["microceph.radosgw-admin", "user", "info", "--uid=test"], vm_name, check=False)
        if user.rc != 0:
            self._exec(
                [
                    "microceph.radosgw-admin", "user", "create", "--uid=test", "--display-name=test",
                    "--access-key=fooAccessKey", "--secret-key=fooSecretKey",
                ],
                vm_name,
            )
        address = f"[{host}]" if ":" in host else host
        flags = [
            "s3cmd", "--ssl" if ssl else "--no-ssl", "--host", f"{address}:{int(port)}",
            "--host-bucket", f"{address}:{int(port)}/%(bucket)",
            "--access_key=fooAccessKey", "--secret_key=fooSecretKey",
        ]
        if ca_cert:
            flags.append(f"--ca-certs={ca_cert}")
        existing = self._exec([*flags, "ls", f"s3://{bucket}"], vm_name, check=False)
        if existing.rc != 0:
            self._exec([*flags, "mb", f"s3://{bucket}"], vm_name)
        path = f"/tmp/{name}"
        self._exec(["python3", "-c", "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text(sys.argv[2])", path, content], vm_name)
        self._exec([*flags, "put", "-P", path, f"s3://{bucket}/{name}"], vm_name, timeout=120)

    def find_rgw_material_leaks_in_vm(self, prefix, vm_name=None):
        """Return leak locations without bringing the private key into Robot."""
        vm = vm_name or self._harness._outer_vm()
        self._exec(["mkdir", "-p", "/root/rgw-probe"], vm)
        for name in ("rgw_probe.py", "placement_status.py"):
            self._harness._lxc_file_push(str(Path(__file__).with_name(name)), f"{vm}/root/rgw-probe/{name}", 30, "copy RGW probe")
        arguments = ["python3", "/root/rgw-probe/rgw_probe.py", f"/tmp/{prefix}"]
        for path in self.rgw_tls_paths_in_vm(vm):
            arguments.extend(["--allowed", path])
        return json.loads(self._exec(arguments, vm, timeout=120).stdout)

    # -----------------------------------------------------------------------
    # TLS policy preparation
    # -----------------------------------------------------------------------

    def write_rgw_tls_policy_in_vm(self, member, port, ssl_port, prefix, key_prefix=None, vm_name=None):
        """Builds a placement policy carrying TLS material entirely inside the VM.

        jq reads /tmp/<prefix>/server.crt and /tmp/<key_prefix or prefix>/server.key
        and base64-encodes them into the policy, so the submitted material never
        appears in a command line, the journal, or the Robot log. key_prefix
        differs from prefix only to build a deliberately mismatched (cert, key)
        pair for rejection tests. Returns the body file path.
        """
        key_prefix = key_prefix or prefix
        body_path = f"/tmp/rgw-tls-policy-{prefix}-{key_prefix}.json"
        jq = (
            '{mode:"reconcile",members:{($member):{rgw:{enabled:true,ssl:true,'
            'port:$port,ssl_port:$sslport,ssl_certificate:($cert|@base64),'
            'ssl_private_key:($key|@base64)}}}}'
        )
        cmd = (
            f"umask 077; jq -n --rawfile cert {shlex.quote(f'/tmp/{prefix}/server.crt')} "
            f"--rawfile key {shlex.quote(f'/tmp/{key_prefix}/server.key')} "
            f"--arg member {shlex.quote(member)} --argjson port {int(port)} --argjson sslport {int(ssl_port)} "
            f"{shlex.quote(jq)} > {shlex.quote(body_path)}"
        )
        self._harness.run_in_vm_and_check(cmd, 30, vm_name=vm_name)
        return body_path

    # -----------------------------------------------------------------------
    # In-flight placement requests
    # -----------------------------------------------------------------------

    def start_placement_put_in_vm(self, vm_name, body, tag, timeout=120):
        """Starts a detached placement PUT inside *vm_name* and returns immediately.

        nohup + disown keeps curl running after the lxc exec session closes;
        the response body lands in /tmp/<tag>.json and /tmp/<tag>.done appears
        once it finishes. Background PUTs are how the suites observe in-flight
        apply behavior (migration add-before-remove sampling, lock conflicts)
        that a blocking PUT cannot show.
        """
        body_path = f"/tmp/{tag}-body.json"
        # argv, not shell interpolation: the body must reach the file verbatim.
        self._exec(
            ["python3", "-c", "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text(sys.argv[2])", body_path, body],
            vm_name,
        )
        self._harness.run_in_vm_and_check(
            f"rm -f /tmp/{tag}.json /tmp/{tag}.done && "
            f"nohup sh -c 'touch /tmp/{tag}.started; curl -s -X PUT --unix-socket {MICROCEPH_CONTROL_SOCKET} "
            f"-H \"Content-Type: application/json\" -d @{body_path} -o /tmp/{tag}.json "
            f"http://localhost/1.0/placement; touch /tmp/{tag}.done' >/dev/null 2>&1 & disown",
            timeout, vm_name=vm_name,
        )

    def start_concurrent_placement_puts_in_vm(self, vm_name, body_a, tag_a, body_b, tag_b, target_b):
        """Launches two detached placement PUTs from one shell, milliseconds apart.

        The second request carries ?target=<target_b>, so the daemon in
        *vm_name* forwards it to that member and the apply runs there, under
        that member's daemon, while the first apply runs locally. Launching
        both from one shell keeps the gap far below the shortest real apply,
        so the pair genuinely overlaps on the cluster-wide apply lock. Both
        results land in /tmp/<tag>.json with a /tmp/<tag>.done marker.
        """
        for tag, body in ((tag_a, body_a), (tag_b, body_b)):
            self._exec(
                ["python3", "-c", "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text(sys.argv[2])",
                 f"/tmp/{tag}-body.json", body],
                vm_name,
            )
        curl = (f"curl -s -X PUT --unix-socket {MICROCEPH_CONTROL_SOCKET} "
                f"-H \"Content-Type: application/json\"")
        self._harness.run_in_vm_and_check(
            f"rm -f /tmp/{tag_a}.json /tmp/{tag_a}.done /tmp/{tag_b}.json /tmp/{tag_b}.done && "
            f"nohup sh -c '{curl} -d @/tmp/{tag_a}-body.json -o /tmp/{tag_a}.json http://localhost/1.0/placement; "
            f"touch /tmp/{tag_a}.done' >/dev/null 2>&1 & "
            f"nohup sh -c '{curl} -d @/tmp/{tag_b}-body.json -o /tmp/{tag_b}.json "
            f"\"http://localhost/1.0/placement?target={shlex.quote(target_b)}\"; touch /tmp/{tag_b}.done' >/dev/null 2>&1 & disown",
            30, vm_name=vm_name,
        )

    def background_put_done_in_vm(self, vm_name, tag):
        """Returns True when the detached PUT *tag* inside *vm_name* has finished."""
        return self._harness.run_in_vm(f"test -f /tmp/{tag}.done", 15, quiet=True, vm_name=vm_name).rc == 0

    def wait_for_background_put_in_vm(self, vm_name, tag, timeout=600):
        """Waits for the detached PUT *tag* and returns its response body."""
        self._harness._poll_until(
            lambda: self.background_put_done_in_vm(vm_name, tag),
            attempts=max(1, int(timeout / 5)),
            interval=5,
            fail_msg=f"background placement PUT {tag} did not finish within {timeout}s",
        )
        return self._harness.run_in_vm(f"cat /tmp/{tag}.json", 30, vm_name=vm_name).stdout

    def start_rgw_migration_sampler_in_vm(self, vm_name, tag, old_host, new_host, port, path, expected):
        """Starts an in-guest sampler that reads the object every 100 ms until the PUT *tag* finishes.

        The sampler runs inside *vm_name* so its cadence does not depend on the
        lxc exec round trip; a fast apply still yields in-flight samples. Start
        it before Start Placement Put In VM with the same tag; the PUT's
        .started marker separates in-flight samples from the warm-up.
        """
        self._exec(["rm", "-f", f"/tmp/{tag}.started", f"/tmp/{tag}.done", f"/tmp/{tag}.samples"], vm_name)
        self._exec(
            ["python3", "-c", "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text(sys.argv[2])",
             f"/tmp/{tag}-sampler.py", RGW_MIGRATION_SAMPLER],
            vm_name,
        )
        self._harness.run_in_vm_and_check(
            f"nohup python3 /tmp/{tag}-sampler.py {shlex.quote(tag)} {shlex.quote(old_host)} {shlex.quote(new_host)} "
            f"{int(port)} {shlex.quote(path)} {shlex.quote(expected)} >/tmp/{tag}-sampler.log 2>&1 & disown",
            30, vm_name=vm_name,
        )

    def collect_rgw_migration_samples_in_vm(self, vm_name, tag, attempts=60):
        """Waits for the sampler started for *tag* to finish and returns its verdict.

        Returns {"samples": in-flight sample count, "available": every in-flight
        sample read the object from the old or the new gateway,
        "replacement_ready": the new gateway served the object at least once}.
        """
        last = [""]

        def predicate():
            last[0] = self._harness.run_in_vm(f"cat /tmp/{tag}.samples", 30, quiet=True, vm_name=vm_name).stdout
            return placement_status.migration_samples(last[0])["complete"]

        self._harness._poll_until(
            predicate,
            attempts=attempts,
            interval=1,
            fail_msg=f"migration sampler for {tag} never finished",
        )
        verdict = placement_status.migration_samples(last[0])
        del verdict["complete"]
        return verdict

    # -----------------------------------------------------------------------
    # Stored-policy / observed-state parsers (Python decisions for Robot asserts)
    # -----------------------------------------------------------------------

    def placement_refusal_text(self, raw):
        """Returns the recorded placement_refusal from a GET /placement body (strict parse)."""
        return placement_status.placement_refusal(raw)

    def stored_rgw_intent(self, raw, member):
        """Returns the stored policy's rgw intent for *member* ({} when absent, strict parse)."""
        return placement_status.stored_policy_rgw(raw, member)

    def observed_rgw_flags(self, raw):
        """Returns {member: rgw-running flag} from a GET /placement body (strict parse)."""
        return placement_status.observed_rgw_members(raw)

    def rgw_policy_leaks_secrets(self, raw):
        """Returns True when the stored placement policy carries RGW SSL material (strict parse)."""
        return placement_status.placement_leaks_rgw_secrets(raw)

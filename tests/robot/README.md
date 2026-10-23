# MicroCeph Robot Framework Tests

Integration tests for MicroCeph using [Robot Framework](https://robotframework.org/).

## Prerequisites

### Host-only suites (no LXD, no snap required)

Use `tox`, which installs the dependencies into an isolated venv instead of the
system Python (this is also how CI runs them):

```bash
tox -e robot -- --test-suite static-checks   # golangci-lint + go vet
tox -e robot -- --test-suite unit-tests       # go test ./...
```

In CI, the host-only suites and vulnerability scans depend on the shared `dqlite-preflight.yml` workflow: it checks that the `dqlite/dev` PPA index and both `libdqlite1.18` packages can be downloaded before those jobs start. An outage produces a `kind=preflight` infrastructure annotation in each invoking workflow and skips its dependent jobs.

### Integration suites

1. **LXD initialised** — if not already done:
   ```bash
   sudo snap install lxd
   sudo lxd init --auto
   ```

2. **Internet access from LXD VMs** — suite setup installs packages (`s3cmd`, `jq`,
   `ceph-common`, `nfs-common`, etc.) inside the outer VM via `apt-get`.  The LXD
   bridge must have outbound internet access; if it does not, package downloads will
   fail during suite setup.  Check with:
   ```bash
   lxc launch ubuntu:24.04 probe
   lxc exec probe -- bash -c "apt-get update -qq && apt-get install -y s3cmd"
   lxc delete probe --force
   ```

3. **A built snap**:
   ```bash
   snapcraft pack -v           # produces microceph_*.snap in the repo root
   ```

## Running tests

Run a single suite:

```bash
tox -e robot -- --snap-path /path/to/microceph_*.snap \
    --test-suite cluster-tests
```

Run all suites sequentially (omitting `--test-suite` defaults to the full tree):

```bash
tox -e robot -- --snap-path /path/to/microceph_*.snap
```

The current native-SMB artifact requires snapd >= 2.78. CI sets
`MICROCEPH_TEST_SNAPD_CHANNEL=latest/candidate` to prepare snapd in every VM or
nested container before installing/upgrading the **local artifact**, including
DSL, adoption and wiping guests. Set the same variable for local runs when needed. The
runner host snapd and MicroCeph store-channel selection are unchanged; this does
not qualify installation on stable snapd. Without the variable, existing guest snapd is kept.

Results land in `output.xml`, `log.html`, and `report.html` in the working directory.
Each suite creates and destroys its own LXD VM; a failed suite teardown also cleans up.

### Vaultlocker mapper regression

```bash
tox -e robot -- --snap-path /path/to/microceph_*.snap \
    --test-suite vaultlocker-test
```

This snap-only suite installs the real Vaultlocker snap (amd64 revision **12**)
and Vault (revision **2487**, 2.0), with a private, loopback-only Vault server,
KV v2 and a scoped AppRole. Vaultlocker encrypts owned loop-backed scratch disks
inside the disposable VM. MicroCeph receives the returned mapper path without
`--wipe` or `--encrypt`; the suite verifies OSD up/in, a 1 MiB object round-trip,
and refusal of ext4, GPT, BlueStore and ordinary non-zero data. Refusal checks
compare signatures and the first 4 MiB, not every byte of each disk.

The suite then cold-boots the VM with its file-backed Vault sealed. A fixture-only
unit restores owned loop associations and stable aliases, but does not unlock
mappers. The test samples the locked mapper for at least 90 seconds, unseals Vault
using private guest state, and requires Vaultlocker and MicroCeph to recover the
original 1 MiB object automatically. No OSD reload or manual unlock is issued.

Override a pin with `--variable VAULTLOCKER_REVISION:<revision>` (or
`VAULT_REVISION`). To select a channel instead, explicitly clear the revision:
`--variable VAULTLOCKER_REVISION: --variable VAULTLOCKER_CHANNEL:latest/edge`.
Pins must remain available in the Snap Store; channel overrides are not reproducible.

Vault credentials/configuration and private failure details never leave the VM
or enter CI artifacts. CI uploads only the normal Robot results; teardown logs
allowlisted snap/interface/block-device/Ceph diagnostics. For local debugging,
`--variable KEEP_VAULTLOCKER_VM:True` preserves the VM, including its root-only
test credentials; private failure details are in
`/run/microceph-vaultlocker-ci/failure.log`. Reboot testing also retains root-only
state in `/var/snap/vaultlocker/common/microceph-ci/reboot-state.json` inside that
VM. Delete the VM when finished. This is not a Juju/charm, HA or cross-model test;
the test Vault shares the VM and starts sealed after reboot.

## Shared-lab SMB CI

The `smb-integration` matrix entry is **one CI job** with one verified snap and
one owned four-VM lab (three gateways plus the Samba AD DC). Three ordered
scenario suites run sequentially on the same Ceph cluster: singleton/independent
non-AD (3 cases), clustered non-AD (6), and external AD (9).

```bash
tox -e robot -- --snap-path /path/to/candidate.snap --test-suite smb-integration \
    --variable SMB_SNAPD_CHANNEL:latest/candidate
# Another complete-suite order, repeated without reallocating the lab:
tox -e robot -- --snap-path /path/to/candidate.snap --test-suite smb-integration \
    --smb-suites ad,clustered,singleton --smb-repeat 2 \
    --variable SMB_SNAPD_CHANNEL:latest/candidate
# All six orders: long-running acceptance, not the default PR job:
tox -e robot -- --snap-path /path/to/candidate.snap --test-suite smb-integration \
    --smb-permutations --variable SMB_SNAPD_CHANNEL:latest/candidate
```

`tests/robot/smb_runner.py` provisions once, passes a private owner/name manifest
to each suite and tears down once in finalization, including partial setup failure.
It emits separate `smb-results/<execution>-<suite>/` reports, a combined Robot
report and `smb-summary.json`. An earlier failure never becomes success; an
unconfirmed stop or unhealthy boundary blocks later suites. Lifecycle errors are
infrastructure failures in the combined report, not hidden skips. Private manifests,
inputs and credential caches are not uploaded. `--dryrun` allocates no VMs.
The existing GitHub workflow's optional `smb_permutations` dispatch input enables
all six orders in its single SMB job; default PR runs execute each suite once.

Each scenario supplies its own inputs and starts with idle, owned gateway slots
and coherent Ceph health. Singleton no longer depends on clustered deployment.
Cleanup retires only known fixture owners using the native API and confirms stop.
Logical resources/data and local AD identity remain in the owned lab for repeats
until final VM teardown: no last-identity purge, trust reset or automatic rejoin.
Each AD execution uses a new negative-join ID/path, retaining earlier private
inputs/outcomes. The positive AD identity stays stable. An issued/uncertain DC
stop is restored in finalization. Cases within each scenario remain ordered;
live repeat/order validation is required, not inferred from dry-run success.

Standalone CLI selections are `smb-singleton-tests`, `smb-clustered-tests` and
`smb-ad-tests`. The legacy `smb-tests` alias selects both non-AD suites. All use
the coordinator. Raw Robot invocations retain isolated scenario setup; shared
binding requires an explicit manifest. Clock, strict confinement and the local
50 GiB/4 GiB headroom defaults are unchanged; the smaller CI disk trial is
explicitly configured as described below.

## Native SMB lifecycle

```bash
tox -e robot -- --snap-path /path/to/candidate.snap --test-suite smb-tests \
    --variable SMB_SNAPD_CHANNEL:latest/candidate
```

Run the **whole ordered scenario** (six clustered cases or three singleton/
independent cases), or the legacy alias for both. They cover clustered deployment, real
password-authenticated put/get and cross-gateway reads (plus wrong-password denial),
endpoint refresh/no-op, retirement/rejoin, daemon restart, ordinary VM reboot and
clustered peer-preserving purge, nonclustered second-owner rejection and verified
sequential placement, plus independent logical IDs on disjoint data paths with
scoped purge. The singleton scenario checks existing-file access after returning
to the original gateway with retained Samba state; it does not require local-user
identities or file ACL access to follow placement to another gateway. Purging a
standalone service also loses its local identities; repeat runs cannot assume
access to files protected by those discarded identities. Clustered SMB or external
AD is recommended for identity continuity; native AD also requires clustered mode.
Reboot recovery never starts a role or re-enables a placement. A prior failure
remains failed even if later, independent mode tests can run. Missing deployment
prerequisites are reported as skips, not passes.

Gateways are **real Ubuntu 26.04 VMs**, not nested containers; VM0 also runs
`smbclient`. Each has 2 vCPU, 3 GiB RAM, a 20 GiB root and dedicated 4 GiB data disk.
Local defaults require 146 GiB free pool/16 GiB available RAM initially for the
shared lab, or 122 GiB/13 GiB for isolated non-AD setup. They retain 50 GiB
storage/4 GiB memory headroom and budget 24 GiB per remaining VM.

CI starts on `self-hosted-linux-amd64-noble-xlarge` with its configured 100 GB disk
and the `dir` backend. It explicitly sets `SMB_DISK_HEADROOM_GIB:10` and
`SMB_DISK_RESERVE_PER_VM_GIB:0`: check observed free space against a 10 GiB floor
before allocation and at lab verification boundaries, rather than reserve every
virtual disk's full limit. The initial 16 GiB available-RAM gate and 4 GiB memory
headroom remain. This is a starting resource budget, not proof that 100 GB suffices.
Capacity samples and final disk/RAM/kernel OOM diagnostics are logged; increase
the runner size only after an observed resource-related failure, not an automatic
retry. Local defaults do not change unless explicitly overridden.

`SMB_STORAGE_POOL` selects a pre-existing pool; avoid LXD's small default loop
pool. The local LXD remote must use the default project. No existing VM/profile
is altered.

The SMB fixture discovers each OSD immediately before disk add: the tagged VM's
`test-data` attachment must reference the exact tagged block volume in the selected
pool. Structured guest inventory must contain exactly one `lxd_test--data` serial
(the LXD-escaped device ID, verified on a real Ubuntu 26.04 VM). It must be a
childless, unmounted 4 GiB disk, with a distinct visible root filesystem; mounted
ancestors/holders, missing identity or ambiguous matches fail closed. No `/dev/sdX`
letter or size-only fallback is used. A different LXD device identity contract
requires investigation, not a guessed replacement.

Gateways and the DC retain the Ubuntu 26.04 image's normal time configuration;
the harness does not install or configure a time service, step clocks, or require
public NTP access, PTP devices, or a particular `timedatectl` synchronization flag.
Guest UTC time, time-service status, and available Chrony diagnostics are recorded
with bounded, non-fatal reads. Chrony's remaining system-clock correction is not
an inter-monitor skew measurement or an absolute accuracy bound.

The shared setup accepts `HEALTH_OK` or `HEALTH_WARN` caused only by
`MON_CLOCK_SKEW`. That warning is logged without blocking SMB scenarios; every
other health warning/error remains blocking. A finished Ceph `time-sync-status`
round, nonempty monitor observations without errors, fully up/in OSDs, settled
pool targets and two coherent active+clean placement samples remain required.
Ceph's default 50 ms inter-monitor limit is not changed or muted.

AD directory readiness still uses a real `kinit`, and the AD cases require fresh
tickets and Kerberos-required SMB authentication, without NTLM fallback.
Authentication, SMB readiness and I/O failures remain fatal. No Kerberos policy
change, clock repair or automatic rejoin is performed.

References: [Ceph clock health](https://docs.ceph.com/en/tentacle/rados/operations/health-checks/#mon-clock-skew),
[Chrony tracking fields](https://chrony-project.org/doc/4.8/chronyc.html#tracking),
and [MIT Kerberos clock-skew policy](https://web.mit.edu/kerberos/krb5-1.22/doc/admin/conf_files/krb5_conf.html#libdefaults).

Exactly one supplied snap is required and its SHA256 is checked in every VM; there
is no store-snap fallback. The candidate currently needs snapd >= 2.78, hence the
**guest-only** candidate channel override above. No host MicroCeph installation or
AppArmor profile is changed. Credentials remain in private guest files (a temporary
cluster-join token copy is also private); status and failure diagnostics go into the
normal Robot artifacts. Allocation names, owner tags and the snap hash are logged.
Teardown deletes only the named/tagged resources; foreign tags or attached volumes
fail closed. Installation/network helpers may retry known infrastructure failures,
but SMB placement, resource updates, retirement and reboot are issued only once.

Dry-run checks keyword resolution, not installed behavior:

```bash
tox -e robot -- --dryrun --test-suite smb-tests
```

## Native SMB external AD authentication

```bash
tox -e robot -- --snap-path /path/to/candidate.snap --test-suite smb-ad-tests \
    --variable SMB_SNAPD_CHANNEL:latest/candidate
```

Run the whole ordered suite on an **accepted, human-signed source artifact**.
This is executable qualification code, not a record of an installed AD pass.
It reuses `smb_suite.resource`, the owned-VM/exec/polling/artifact/hash machinery
and tagged cleanup from `smb-tests`; it does not join the host or use enterprise
credentials. The native placement/user-mode suite remains a separate CI job.

The additional, dedicated Ubuntu Samba AD DC is **test infrastructure**, not a
MicroCeph domain-controller feature. VM0 remains the client. The four guests need
8 vCPU / 12 GiB allocated RAM, 92 GiB disks (three 24 GiB gateways, one 20 GiB DC).
The conservative capacity guard budgets **146 GiB free pool / 16 GiB available
RAM initially**, retaining 50 GiB disk/4 GiB RAM headroom and rounding the DC to
24 GiB. CI retains the extra-disk runner and `dir` backend measured for #931;
a 100 GB xlarge runner is not adequate. No guard or disk-size check is weakened.

Prerequisites: outbound guest APT/image access; working local LXD/default project;
the exact strict snap and guest snapd >= 2.78; stable guest clocks and the exact
owned 4 GiB OSD in each gateway, verified by the shared prerequisites above.
Stop at a disk mismatch or blocking readiness failure; report clock skew without
muting it or changing Ceph's tolerance. The DC installs
`samba`, `samba-ad-dc`, `python3-samba`, `krb5-user`, `bind9-host`; the client uses
`smbclient`, `samba-common-bin` and `krb5-user`. Package versions, snap hash and allocation ownership
are logged. Only the newly owned DC's packaged default Samba/resolved services
are disabled; its Samba provision and private client Kerberos config are scoped
to the fixture. Gateways consume the DC through upstream app-private `custom_dns`.
No runner-host DNS/time/account changes are needed.

Nine compact cases cover wrong join credentials with a retained uncertain
attempt, two-gateway SID/UID/GID equality, fresh valid/invalid/unknown domain
logins, authenticated group nonmember denial and read/write versus read-only
shares, cross-gateway exact contents, role restart, ordinary reboot, join-input
refresh, retained-state re-enable/initial-joiner stop, short DC outage and
peer-safe purge. **Kerberos-required** name-based I/O and CIFS ticket checks run
separately from **Kerberos-disabled domain NTLMv2**. Every operation starts a
fresh client; Kerberos obtains a fresh cache/ticket, never `desired` fallback.
Bob first reads through the authenticated reader share before writers-share
denial, distinguishing authorization from bad credentials. The CephFS backing
UID/GID and exact content are checked. App-namespace `wbinfo`/`getent` observations
are real installed data, not a synthetic UID allocator; the root observation is
not proof of unprivileged NSS permissions outside that profile.

Credential refresh waits for applied-file metadata to advance on both gateways,
then checks serving PID/start identities and DC computer SID/password-generation
metadata. Persistence checks reauthenticate and compare mappings. The short
outage spans at least one ordinary updater interval and accepts either bounded failure or exact cached success, not a promise of
offline login. It restores only the intentionally stopped DC in `FINALLY`;
there is no automatic join retry, leave or trust/idmap reset. SQL/process source
regressions separately gate exclusion/ambiguous outcomes; ordered live admission
alone is not a distributed-join-race proof.

Secrets are generated inside the DC and transferred via mode-0600 private files,
never password argv, Robot variables or public artifacts. Provision/operation
first errors stay private in the guest; bounded redacted diagnostics and normal
public observations enter Robot logs before tagged teardown. Do not upload
`ad-secrets.json`, `.ad-auth`, credential caches, resource inputs, CTDB databases
or raw private native diagnostics. A qualification failure remains failed; no
control mutation is retried to green. Missing prerequisites skip dependent cases,
not the original failure. The disposable fixture's VM teardown is not production
AD cleanup or a supported last-replica recovery operation.

Samba-DC evidence qualifies only the tested protocols/policy. No Windows release,
GPO, cross-forest, forced rotation, broad backup/replacement or cache-revocation
matrix is implied. Windows-specific claims need separate Windows infrastructure.

## Host resource guide

Each suite runs sequentially. Peak resource usage per suite (not concurrent):

| Suite category            | vCPU | RAM  | Disk  | Typical duration |
|---------------------------|------|------|-------|-----------------|
| Single-node               | 4    | 6 GB | 50 GB | ~10 min         |
| Multi-node (4 containers) | 4    | 6 GB | 50 GB | ~20 min         |
| Replication (8 containers)| 4    | 6 GB | 50 GB | ~30 min         |
| Native SMB (3 VMs)        | 6    | 9 GiB | 72 GiB + headroom | Not yet measured |
| Native SMB AD (4 VMs)     | 8    | 12 GiB | 92 GiB + headroom | Not yet measured |

## Suite names

Each directory under `tests/robot/` is a suite:

```
api-tests                                    rbd-replication-test
availability-zone-tests                      rgw-placement-multivm-tests
cephadm-adopt-test                           rgw-placement-tests
cephfs-replication-test                      single-system-tests
cluster-tests                                static-checks
dsl-functional-tests                         test-maintenance-modes
loop-file-tests                              test-sequential-mon-host-refresh
messenger-v2-tests                           unit-tests
multi-node-tests                             upgrade-squid-tests
multi-node-tests-with-custom-microceph-ip    vaultlocker-test
nfs-multinode-test                           wal-db-tests
nfs-test                                    wiping-test
smb-clustered-tests                          smb-ad-tests
smb-singleton-tests
```

## Harness conventions

- Shared keywords live in `resources/microceph_harness.resource`.
- Test case bodies and suite-level `*** Keywords ***` sections call named keywords —
  no raw bash in test bodies.
- Keyword bodies in the harness may contain bash; that is implementation detail.
- `Run In VM`, `Run In VM And Check`, `Run In VM Must Fail` and `Run In Container` run the command under `bash -eo pipefail`. Do not pipe a command whose result is checked into a consumer that exits early (`grep -q`, `head -1`): if the producer is still writing it is killed by SIGPIPE and the pipeline fails with rc=141 although the consumer already had what it needed, and a `&& echo yes || echo no` probe answers "no". Run the command on its own and decide in Robot (`Should Contain`) or in a pure Python helper (see "Purify" in `AGENTS.md`).
- New harness keywords go under the relevant section comment in the resource file.
- RGW endpoint-closure probes require connection refusals from every resolved address. Timeouts, DNS or routing failures, guest execution failures, and malformed output fail the check instead of proving closure. The probe requires Python 3.11+ in the guest, provided by the default Ubuntu 24.04 image.
- `Stop Named VM` fails on a failed or timed-out stop and verifies that LXD reports the exact named VM as stopped before an offline-member scenario continues.

### Foreground placement scenarios

Suite setup copies `resources/rgw_scenario.py` to the coordinating guest once. Each scenario then runs that script in the foreground through `lxc exec`, returning one JSON result. There are no detached shells, per-request directories, response files, completion markers, or result-polling keywords. The script sends request bodies to `curl` on standard input and captures responses in memory.

`Run RGW Migration In VM` returns a `response` string and an `observation` object containing the sample count, availability verdict, and replacement-readiness verdict. One sampler thread reads old then new while the PUT runs. A `finally` block signals it to stop, including on request failure, and the runner joins it before returning or raising an error.

`Run Concurrent Placement Puts In VM` releases two request workers from a barrier and returns both responses in A, B order. The runner joins both workers before returning. A barrier reduces the submission gap but does not guarantee overlap at the daemon or decide the lock winner; the Robot test still requires one HTTP 200 and one HTTP 409 in either order. It retries once only when both requests succeed; any other response pair fails immediately and reports both responses.

Each PUT has the scenario's request timeout, enforced by `curl` and its owning subprocess call. The host allows five additional seconds for process cleanup and final sampler reads. HTTP error responses are returned for Robot assertions; transport errors and timeouts fail the runner instead of producing a successful-looking result.

### RGW migration sampling

The migration sampler reads the old gateway before the replacement, then pauses for 100 ms. This follows the add-before-remove handover: once the old gateway has stopped, the replacement must be ready for the next read. Reading in the opposite order can miss both sides of a successful handover. Both gateways are probed on each iteration so replacement readiness is recorded even when the old gateway still serves. Each sample must contain a successful object read; it is not an atomic snapshot of both gateways.

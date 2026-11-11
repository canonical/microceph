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

Results land in `output.xml`, `log.html`, and `report.html` in the working directory.
Integration suites own their disposable LXD resources; host-only suites allocate none.
Default/full runs (including `--all`) route the three dependent SMB singleton cases
through `smb_runner.py`, which owns its lab manifest, exact case accounting and cleanup.
The explicit `--test-suite smb-integration` and `--test-suite smb-singleton-tests`
routes use that same coordinator. Its private evidence remains in `smb-results/`
and its verdict in `smb-summary.json`; do not publish the private lab manifest.

Full-tree runs apply Robot's normal selection flags before execution, without
running suite setup. Selecting no SMB cases skips its coordinator; selecting all
three runs it once; a partial SMB scenario fails before allocating a lab. Ordinary
filtered non-SMB runs remain supported, including qualified suite/test names.
Full output/log/report options apply to the aggregated result; coordinator or
cleanup failure cannot become success merely because its test XML contains PASS.
`--dryrun` validates discovery/keyword resolution only, never installed SMB behavior.

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

### NFS grace-helper cleanup regression

The `nfs-test` suite checks the installed `ganesha-rados-grace` helper against the VM's real Ceph cluster. A test-only `LD_PRELOAD` observer forwards calls to librados and fails with exit status 99 if the helper exits with live handles or shuts down the client before destroying its I/O context. This deterministically catches the lifetime bug behind https://github.com/canonical/microceph/issues/899 without waiting for a segmentation fault. The observer is compiled inside the VM and is never included in the snap.

```bash
tox -e robot -- --snap-path /path/to/microceph.snap \
    --test-suite nfs-test --test 'Test Grace Helper Cleanup*'
```

The three cases cover successful add/remove, a failed grace operation, and configuration failure after client creation; failures must retain exit status 1 after cleanup. They use a separate `microceph-ci-grace-cleanup` namespace. The same tests fail against the archive helper and pass with the V6.5 backport of https://review.gerrithub.io/c/ffilz/nfs-ganesha/+/1256299; no expectations change between the test-only and fix commits.

## Host resource guide

Each suite runs sequentially. Peak resource usage per suite (not concurrent):

| Suite category            | vCPU | RAM  | Disk  | Typical duration |
|---------------------------|------|------|-------|-----------------|
| Single-node               | 4    | 6 GB | 50 GB | ~10 min         |
| Multi-node (4 containers) | 4    | 6 GB | 50 GB | ~20 min         |
| Replication (8 containers)| 4    | 6 GB | 50 GB | ~30 min         |

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

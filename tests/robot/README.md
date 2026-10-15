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
api-tests                          nfs-test
availability-zone-tests            nfs-multinode-test
cephadm-adopt-test                 rbd-replication-test
cephfs-replication-test            single-system-tests
cluster-tests                      static-checks
dsl-functional-tests               test-maintenance-modes
loop-file-tests                    test-sequential-mon-host-refresh
messenger-v2-tests                 unit-tests
multi-node-tests                   upgrade-squid-tests
multi-node-tests-with-custom-microceph-ip
                                   vaultlocker-test
                                   wal-db-tests
                                   wiping-test
```

## Harness conventions

- Shared keywords live in `resources/microceph_harness.resource`.
- Test case bodies and suite-level `*** Keywords ***` sections call named keywords —
  no raw bash in test bodies.
- Keyword bodies in the harness may contain bash; that is implementation detail.
- `Run In VM`, `Run In VM And Check`, `Run In VM Must Fail` and `Run In Container` run the command under `bash -eo pipefail`. Do not pipe a command whose result is checked into a consumer that exits early (`grep -q`, `head -1`): if the producer is still writing it is killed by SIGPIPE and the pipeline fails with rc=141 although the consumer already had what it needed, and a `&& echo yes || echo no` probe answers "no". Run the command on its own and decide in Robot (`Should Contain`) or in a pure Python helper (see "Purify" in `AGENTS.md`).
- New harness keywords go under the relevant section comment in the resource file.

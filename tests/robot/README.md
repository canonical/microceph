# MicroCeph Robot Framework Tests

Integration tests for MicroCeph using [Robot Framework](https://robotframework.org/).

## Prerequisites

### Host-only suites (no LXD, no snap required)

```bash
pip3 install -r tests/robot/requirements.txt
python3 tests/robot/robot.py --test-suite static-checks   # golangci-lint + go vet
python3 tests/robot/robot.py --test-suite unit-tests       # go test ./...
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
python3 tests/robot/robot.py --snap-path /path/to/microceph_*.snap \
    --test-suite cluster-tests
```

Run all suites sequentially (omitting `--test-suite` defaults to the full tree):

```bash
python3 tests/robot/robot.py --snap-path /path/to/microceph_*.snap
```

Via tox:

```bash
tox -e robot -- --snap-path /path/to/microceph_*.snap --test-suite cluster-tests
```

Results land in `output.xml`, `log.html`, and `report.html` in the working directory.
Each suite creates and destroys its own LXD VM; a failed suite teardown also cleans up.

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
multi-node-tests                   upgrade-reef-tests
multi-node-tests-with-custom-microceph-ip
                                   wal-db-tests
                                   wiping-test
```

## Harness conventions

- Shared keywords live in `resources/microceph_harness.resource`.
- Test case bodies and suite-level `*** Keywords ***` sections call named keywords —
  no raw bash in test bodies.
- Keyword bodies in the harness may contain bash; that is implementation detail.
- New harness keywords go under the relevant section comment in the resource file.

# MicroCeph Robot Framework Tests

Integration tests for MicroCeph using [Robot Framework](https://robotframework.org/).

## Prerequisites

- A built MicroCeph snap (`.snap` file)
- Python 3 with tox: `pip install tox`
- LXD initialised on the host

## Running tests

Run a single suite:

```bash
python3 tests/robot/robot.py --snap-path /path/to/microceph.snap --test-suite cluster-tests
```

Run all suites:

```bash
python3 tests/robot/robot.py --snap-path /path/to/microceph.snap --all
```

Via tox:

```bash
tox -e robot -- --snap-path /path/to/microceph.snap --test-suite cluster-tests
```

Results are written to `output.xml`, `log.html`, and `report.html` in the working directory.

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

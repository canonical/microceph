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
multi-node-tests-with-custom-microceph-ip    wal-db-tests
nfs-multinode-test                           wiping-test
nfs-test
```

## Harness conventions

- Shared keywords live in `resources/microceph_harness.resource`.
- Test case bodies and suite-level `*** Keywords ***` sections call named keywords —
  no raw bash in test bodies.
- Keyword bodies in the harness may contain bash; that is implementation detail.
- `Run In VM`, `Run In VM And Check`, `Run In VM Must Fail` and `Run In Container` run the command under `bash -eo pipefail`. Do not pipe a command whose result is checked into a consumer that exits early (`grep -q`, `head -1`): if the producer is still writing it is killed by SIGPIPE and the pipeline fails with rc=141 although the consumer already had what it needed, and a `&& echo yes || echo no` probe answers "no". Run the command on its own and decide in Robot (`Should Contain`) or in a pure Python helper (see "Purify" in `AGENTS.md`).
- New harness keywords go under the relevant section comment in the resource file.
- RGW endpoint-closure probes require connection refusals from every resolved address. Timeouts, DNS or routing failures, guest execution failures, and malformed output fail the check instead of proving closure. The probe requires Python 3.11+ in the guest, provided by the default Ubuntu 24.04 image.

### Foreground placement scenarios

Suite setup copies `resources/rgw_scenario.py` to the coordinating guest once. Each scenario then runs that script in the foreground through `lxc exec`, returning one JSON result. There are no detached shells, per-request directories, response files, completion markers, or result-polling keywords. The script sends request bodies to `curl` on standard input and captures responses in memory.

`Run RGW Migration In VM` returns a `response` string and an `observation` object containing the sample count, availability verdict, and replacement-readiness verdict. One sampler thread reads old then new while the PUT runs. A `finally` block signals it to stop, including on request failure, and the runner joins it before returning or raising an error.

`Run Concurrent Placement Puts In VM` releases two request workers from a barrier and returns both responses in A, B order. The runner joins both workers before returning. A barrier reduces the submission gap but does not guarantee overlap at the daemon or decide the lock winner; the Robot test still requires one HTTP 200 and one HTTP 409 in either order and retains its bounded retry.

Each PUT has the scenario's request timeout, enforced by `curl` and its owning subprocess call. The host allows five additional seconds for process cleanup and final sampler reads. HTTP error responses are returned for Robot assertions; transport errors and timeouts fail the runner instead of producing a successful-looking result.

### RGW migration sampling

The migration sampler reads the old gateway before the replacement, then pauses for 100 ms. This follows the add-before-remove handover: once the old gateway has stopped, the replacement must be ready for the next read. Reading in the opposite order can miss both sides of a successful handover. Both gateways are probed on each iteration so replacement readiness is recorded even when the old gateway still serves. Each sample must contain a successful object read; it is not an atomic snapshot of both gateways.

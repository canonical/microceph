# Robot Framework CI Migration — Specification

## Goal

Replace MicroCeph's existing bash-driven CI test execution layer with Robot Framework
7.x suites that wrap the same underlying test logic. The result is a uniform,
structured test layer that produces HTML/XML logs, supports selective execution
(`--test`), and makes CI failures easier to diagnose — without changing what the
tests actually do.

This work lives on the `megademo-robot` branch and is tracked as a PR against `main`.

---

## What Changes, What Stays the Same

**Changes:**
- Each CI job (currently a set of raw bash `run:` steps) gains a corresponding
  `.robot` suite under `tests/robot/<suite-name>/`.
- CI jobs call `python3 tests/robot/robot.py --test-suite <name>` instead of
  inline bash scripts.
- Robot Framework produces `output.xml`, `log.html`, `report.html` which are
  uploaded as CI artifacts.

**Stays the same:**
- The MicroCeph Go source and snap build are untouched by the Robot work.
- CI hardware topology (LXD VMs, loop devices, LXD containers) is unchanged.
- **DSL tests only:** `dsl-functional-tests` and `api-tests` delegate to
  `tests/scripts/test_dsl_functest.sh` — the DSL harness is large, well-structured,
  and has its own internal test tracking, so it stays in bash.
- **Wiping test:** delegates to `actionutils.sh verify_pristine_check` inside the
  outer VM — the snap remove/reinstall cycle is long and streaming, making it
  impractical to inline.
- **cephadm adopt:** delegates to `adoptutils.sh` on the host runner — same reason
  as DSL (existing structured bash harness).

**Changes (everything else):**
- All other test logic is translated **inline** into Robot Framework keywords and
  test cases. No shell script is called; commands are issued directly via
  `Run In VM`, `Run In Container`, or `Run Process` keywords in the `.robot` files
  and `microceph_harness.resource`. This covers ~20 of the 23 suites.

---

## Repository Layout

```
tests/robot/
├── robot.py                        # Thin CLI: finds & runs a named suite
├── requirements.txt                # robot framework + dependencies
├── resources/
│   ├── microceph_harness.resource  # Shared RF keywords (VM lifecycle, OSD, RGW, …)
│   └── streaming_process.py        # Python library: run long commands with live stdout
│
├── <suite-name>/
│   └── <suite_name>.robot          # One .robot file per CI job
│
└── SPEC.md                         # This file
```

There are 23 `.robot` suites — one per CI job in `.github/workflows/tests.yml`
(excluding the snap-build job which has no tests).

---

## Execution Model

### 1. GitHub CI

```yaml
- name: Run <suite>
  run: python3 tests/robot/robot.py --test-suite <suite-name> \
         --snap-path '/home/runner/*.snap' \
         [--variable XTRACE:True] \
         [--test "<specific test case>"]
```

`robot.py` finds the suite directory, resolves the snap glob, and invokes
`robot --variable SNAP_PATH:...` on the suite.

### 2. VM Topology

Three distinct execution patterns exist, each with strict constraints:

| Pattern | Example suites | How it works |
|---|---|---|
| **Outer-VM** | single-node, multi-node, wiping, upgrade-reef, … | RF runs on the host; launches one LXD VM (`lxc launch --vm`); all test commands run inside that VM via `lxc exec`. The VM has internet access (host Docker iptables FORWARD chain must be cleared first). |
| **Host-direct** | dsl-functional-tests, cephadm-adopt | RF runs on host; test script creates its own LXD VMs internally. No outer VM. Constraint: no packages may be installed on the host runner — only `lxc` and already-present tools may be used. |
| **LXD-containers inside VM** | multi-node-tests, availability-zone-tests | RF launches an outer VM; inside that VM the suite creates LXD containers (`node-wrk0`, `node-wrk1`, …) to simulate a multi-node cluster. |

> **Hard constraint:** KVM VMs cannot be nested inside LXD VMs on GitHub runners
> (nested virtualisation is unavailable). Any test that requires KVM VMs must run
> directly on the host. Any test that requires only containers may run inside a VM.

### 3. Long-running Commands

For operations that take many minutes (snap install, snap remove, full test scripts),
the `streaming_process.py` library is used instead of Robot's built-in `Run Process`.
It streams stdout line-by-line so the CI console shows live progress.

```robotframework
${rc}    ${out}=    Run Streaming Process
...    lxc exec ${OUTER_VM} -- bash -c "~/actionutils.sh verify_pristine_check"
...    timeout=3600
```

`xtrace=True` prepends `bash -x` to the command, enabling shell tracing. This only
works correctly when the command is a **script file**, not a `lxc exec` invocation
(because `bash -x lxc exec …` tries to execute `lxc` as a bash script file).

---

## Shared Resource: `microceph_harness.resource`

Provides keywords used across multiple suites:

| Category | Keywords |
|---|---|
| VM lifecycle | `Launch Outer Test VM`, `Teardown MicroCeph Environment`, `Free Runner Disk`, `Setup LXD In VM` |
| Execution | `Run In VM`, `Run In VM And Check`, `Run In VM Must Fail`, `Run In Container` |
| OSD | `Add OSD To Node`, `Wait For OSD Count Head`, `Wait For OSD Count Container` |
| Cluster | `Bootstrap Head Node`, `Join Worker Nodes To Cluster`, `Install MicroCeph On All Nodes` |
| Snap | `Copy Snap To VM`, `Copy Scripts To VM` |
| RGW | (inline in multi-node suite) |
| Upgrade | `Upgrade Multi Node`, `Verify Cluster Health Head Node` |
| Firewall | `Clear IPTables` |
| Host checks | `Require Host Commands` |

Variables injected by `robot.py` at launch:
- `SNAP_PATH` — resolved path to the `.snap` file
- `OUTER_VM` — name of the outer LXD VM (suite-specific default, e.g. `microceph-test-vm`)
- `XTRACE` — `True`/`False` to enable bash -x in streaming commands

---

## CI Job → Suite Mapping

| CI job name | Suite directory | Pattern |
|---|---|---|
| DSL baseline/validation/dry-run/provision/cleanup/consistency | `dsl-functional-tests` | Host-direct |
| API tests | `api-tests` | Outer-VM |
| Single node with encryption | `single-system-tests` | Outer-VM |
| Multi node testing | `multi-node-tests` | Containers-in-VM |
| Availability zone crush rule | `availability-zone-tests` | Containers-in-VM |
| Multi node with custom IP | `multi-node-tests-with-custom-microceph-ip` | Containers-in-VM |
| Sequential mon host refresh | `test-sequential-mon-host-refresh` | Outer-VM |
| Maintenance mode | `test-maintenance-modes` | Outer-VM |
| Loopback file OSDs | `loop-file-tests` | Outer-VM |
| WAL/DB device usage | `wal-db-tests` | Outer-VM |
| Reef upgrades | `upgrade-reef-tests` | Containers-in-VM |
| Cluster features | `cluster-tests` | Outer-VM |
| RBD replication | `rbd-replication-test` | Outer-VM |
| CephFS replication | `cephfs-replication-test` | Outer-VM |
| NFS (single) | `nfs-test` | Outer-VM |
| NFS (multinode) | `nfs-multinode-test` | Containers-in-VM |
| Messenger v2 | `messenger-v2-tests` | Outer-VM |
| Pristine/wipe check | `wiping-test` | Outer-VM |
| cephadm adopt | `cephadm-adopt-test` | Host-direct |

---

## Known Constraints and Gotchas

### Docker FORWARD DROP
Docker sets `iptables -P FORWARD DROP` on startup. Every CI job that launches a
LXD VM **must** clear these rules first — otherwise the VM has no internet access
and snap install / apt-get fail. The step:
```yaml
- name: Clear FORWARD firewall rules
  run: |
    sudo iptables -P FORWARD ACCEPT || true
    sudo ip6tables -P FORWARD ACCEPT || true
    sudo iptables -F FORWARD || true
    sudo ip6tables -F FORWARD || true
```
must appear in every job that uses an outer VM.

### CRUSH Rule Propagation Race
After `microceph disk add` causes the cluster to scale to 3 OSDs across 3 hosts,
the CRUSH rule config value updates before both the crush map (`osd crush rule ls`)
and the pool detail (`osd pool ls detail`) reflect the change. Waits on the config
value alone are not sufficient; all three must be polled with retry loops.

### OSD Recovery Timing (upgrade-reef)
After a rolling upgrade, OSDs may take several minutes to return to `up+in` state
on loaded CI runners. All OSD-ready waits must use retry loops of ≥ 60 attempts ×
10 s = 600 s budget, not single-shot checks.

### RGW Daemon Startup
Enabling RGW on a second node takes longer than on the first (Ceph needs to replicate
keys). Wait budget of 20 × 5 s = 100 s (not 40 s) is needed.

### `install_tools` in `actionutils.sh`
`wait_for_osds` calls `install_tools` unconditionally; `verify_pristine_check` calls
`wait_for_osds` twice. The resulting double `apt-get update` in the wiping test VM
can fail due to network timeouts. `install_tools` must guard with `command -v` before
running apt.

### Transient udevd Race in `disk add`
`resources.GetStorage()` (LXD library) scans `/dev/disk/by-id/` and can encounter a
`.#<name>` temp entry created by udevd's atomic rename. If `lstat` is called after
the rename completes, ENOENT is returned and `disk add` fails. See
`docs/bugs/udevd-getStorage-race.md` for full analysis and a candidate fix.

---

## Acceptance Criteria

1. All 23 CI jobs in `tests.yml` pass consistently (green) for three consecutive runs.
2. Robot Framework `output.xml` artifacts are uploaded for every job.
3. No packages are installed on the GitHub runner host (only inside VMs).
4. Snap artifact from the build job is passed via `actions/upload-artifact` /
   `actions/download-artifact` to all test jobs; no redundant builds.
5. The existing bash test scripts (`tests/scripts/`) are not modified beyond
   necessary bug-fixes that would also apply on `main`.

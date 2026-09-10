# Private Pebble adapter

This package backs `cmd/microceph-pebble`. It is an internal Snap launcher, not
an additional public Snap app or MicroCeph API. It uses the bundled Pebble CLI
and standard-library Go code; MicroCeph does not import Pebble's Go module.

The nine long-running Snap apps use this adapter. Snapcraft builds Pebble
v1.32.1 from a pinned commit with Go 1.26.7. Go lifecycle callers check child
status and require verified OSD exit before removal or failed-add cleanup.
Full-package and real-Ceph qualification remain release gates.

## Contract

- `run <app>` renders a deterministic JSON-as-YAML layer and execs
  `pebble run --verbose`, with private paths and persistence disabled.
- Singleton Pebble names match their Snap app names. Existing child wrappers
  retain Ceph daemon IDs and configuration. `log-rotate` is explicitly rejected.
- `status <service>` requires the child to be active. It is a process-status
  check, not a Ceph readiness check. OSD service names are `osd-<id>`.
- `wait-ready <service>` bounds the initial startup wait before sustained
  placement checks. Backoff/error is a failure, not successful startup.
- `osd-ready <id>` publishes the ready marker under the controller lock and
  refuses publication over an existing removal fence.
- `reload osd` adds/starts eligible OSDs individually, without replanning healthy
  siblings or resetting their backoff loops. A failed start does not strand
  later OSDs.
- `osd-run <id>` records boot ID, PID, and process start time before checking
  eligibility and execing the foreground `commands/osd.run` wrapper. The
  wrapper retains configuration waits, limits, and primary/WAL/DB LUKS handling.
- `osd-stop <id>` requires autostart suppression, disables the named service,
  waits for its stop, and checks process-group exit. A successful repeated stop
  or an inactive status alone cannot authorize storage deletion. Uncertain
  results must leave storage and the suppression marker intact.

Controller operations share a cross-process lock. Each OSD has a separate
process-registration lock, so startup can register while a controller waits
for Pebble. Process receipts live outside OSD data directories and survive
revision changes; boot ID and start time prevent confusing stale identities
with reused PIDs. No credentials are stored in layers or receipts.

## Tests

From `microceph/`:

```bash
go test ./internal/pebble ./cmd/microceph-pebble
go vet ./internal/pebble ./cmd/microceph-pebble
```

The tests build the private executable, exercise its command interface, and
use harmless process groups for exit-verification cases. No Ceph, root, or LXD
is required. To include the real-Pebble lifecycle test:

```bash
PEBBLE_TEST_BINARY=/absolute/path/to/pebble \
  go test ./internal/pebble -count=1
```

The supplied binary must be trusted; use Pebble v1.32.1 built with Go 1.26.7
to match the packaged supervisor. The optional test is skipped when no binary
is supplied.

Harmless-process tests, including a strict core26 fixture, exercise supervisor
boundaries but do not qualify real Ceph, encryption, or destructive disk cleanup.
Before release, validate the built Snap with the full integration suites and
explicit direct-launch/Pebble refresh and revert, five-minute OSD shutdown,
failed-add rollback, and unchanged log-rotation scheduling checks.

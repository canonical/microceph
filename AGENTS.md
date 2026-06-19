# AGENTS.md

## Layout

All Go source lives under `microceph/`. There is no top-level `Makefile` — use `microceph/Makefile`.

## Commit conventions

- Commits must be signed off (`Signed-off-by:` trailer) **by the human**. Agents must never add a `Signed-off-by:` trailer on the human's behalf — the DCO sign-off is an attestation only the human can make.
- Agents must include an `Assisted-by:` trailer identifying the agent and model.
- Order trailers as: `Assisted-by:` first, then the human's `Signed-off-by:` last (added by the human).

Format:

```
Assisted-by: AGENT_NAME:MODEL_VERSION
```

- `AGENT_NAME` — the AI tool or framework (e.g. `claude-code`, `opencode`, `codex`, `pi`, …).
- `MODEL_VERSION` — the specific model version used (e.g. `claude-sonnet-4-6`, `gpt-5.5`).

Example:

```
Assisted-by: opencode:gpt-5.5
```

Other commit rules:

- Commit messages must be ASCII only.
- Keep PRs small and focused; don't mix trivial and controversial changes.
- Squash into logical commits (API / docs / CLI / daemon / tests / CI) for non-trivial PRs.
- Maintain a linear git history.

## Coding conventions

Follow the [Go Style Guide](https://google.github.io/styleguide/go/guide), plus:

### Imports

Three groups, alphabetised (run `go fmt`): standard library, third-party, MicroCeph.

```go
import (
    "fmt"
    "os"

    "github.com/pborman/uuid"

    "github.com/canonical/microceph/microceph/common"
    "github.com/canonical/microceph/microceph/database"
)
```

### Avoid one-line assign/test

Use:

```go
err := doStuff()
if err != nil {
    return err
}
```

Not:

```go
if err := doStuff(); err != nil {
    return err
}
```

### Doc comments

Every exported (capitalised) name needs a doc comment immediately preceding the declaration with no intervening blank lines.

### Injectable function variables

When extracting a function as a package-level `var` so tests can override it, suffix the variable name with `Func` (e.g. `getMonitorCountFunc`). This makes it obvious at the call site that the symbol is an injectable variable, not a plain function.

### Atomic file writes

When writing config files, write to a `.tmp` path and `os.Rename` into place so a failed write can't leave partial state on disk:

```go
tmpFile := destPath + ".tmp"
err := os.WriteFile(tmpFile, data, 0644)
if err != nil {
    return err
}
err = os.Rename(tmpFile, destPath)
if err != nil {
    os.Remove(tmpFile)
    return err
}
```

## Building and installing locally

Build the snap:

```bash
snapcraft pack -v
```

Install the locally built snap (the `--dangerous` flag is required for unsigned local builds):

```bash
sudo snap install --dangerous microceph_*.snap
```

Locally built snaps do **not** auto-connect plugs. Connect them manually:

```bash
sudo snap connect microceph:block-devices
sudo snap connect microceph:hardware-observe
sudo snap connect microceph:mount-observe
sudo snap connect microceph:load-rbd
sudo snap connect microceph:microceph-support
sudo snap connect microceph:network-bind
sudo snap connect microceph:process-control
sudo snap connect microceph:dm-crypt
sudo snap restart microceph.daemon
```

## Unit tests and lint

From `microceph/`:

```bash
make check-unit      # unit tests
make check-static    # lint / static checks
```

## Robot Framework integration tests

See [tests/robot/README.md](tests/robot/README.md) for the full suite layout and
harness conventions, and [Designing Robot Framework tests](#designing-robot-framework-tests)
below for how to structure new suites and harness keywords.

Two suites run on the host with no extra dependencies. Use `tox`, which installs
the dependencies into an isolated venv rather than the system Python (matches CI):

```bash
tox -e robot -- --test-suite static-checks   # golangci-lint + go vet
tox -e robot -- --test-suite unit-tests       # go test ./...
```

All other suites are integration tests that launch LXD VMs.  To run them locally
you need:

1. **LXD initialised** on the host (`lxd init --auto` if not already done).
2. **Internet access from LXD VMs** — suite setup runs `apt-get install s3cmd jq`
   and other package installs inside the VMs.  If the LXD bridge has no outbound
   route, package downloads will fail.
3. **A built snap** — produce one with `snapcraft pack -v` at the repo root.

Run a single suite:

```bash
tox -e robot -- --snap-path /path/to/microceph_*.snap \
    --test-suite cluster-tests
```

Run every suite sequentially:

```bash
tox -e robot -- --snap-path /path/to/microceph_*.snap
```

Results land in `output.xml`, `log.html`, and `report.html` in the working
directory.  Each suite tears down its own LXD VM on completion (or failure).

## Designing Robot Framework tests

The harness is the class library `tests/robot/resources/microceph_harness.py`, holding the shared
keywords as Python methods. The companion `tests/robot/resources/microceph_harness.resource` carries
the `Library` imports and `*** Variables ***` that suites consume, plus some keywords still written in
Robot (RGW/NFS helpers, a few setup wrappers). Robot maps a Python method `run_in_vm_and_check` to the
keyword `Run In VM And Check` (case/space/underscore-insensitive), so moving a keyword body between
Robot and Python never touches a suite as long as the name is preserved.

### Where does my code go?

| Code | Location |
|------|----------|
| Test body: actions **plus** the assertions for one feature | The suite's `*** Keywords ***` or the test case itself — keep it readable Robot |
| Area-agnostic primitives: exec helpers, `_poll_until`, lifecycle, parsers | `microceph_harness.py` |
| Area-coupled logic shared across suites | An area module sibling (`rbd_replication.py`, `cluster_ops.py`, `snap_services.py`, …) |

**Rule of thumb:** if a keyword does work *and* asserts the outcome, it is a test body — it belongs
in the suite, not the shared harness. Move loops, branching, parsing, and polling to Python; keep
linear "do X, check Y" in Robot.

### Purify: fetch raw, decide in Python

The remote command does the **minimum I/O** (`microceph.ceph -s -f json`, `snap services microceph`).
The **parse/decision** lives in a pure Python helper (`@staticmethod` or module-level — no `self`, no
`BuiltIn`) using `json.loads`/regex. This keeps the helper unit-testable and removes fragile
`jq`/`grep` pipelines from the shell command. Preserve the resulting *value*, not the `jq`/`grep`
string.

### Library rules (breaking if ignored)

- **Class name == module name (lowercase).** `Library microceph_harness.py` auto-selects the class
  by filename. A `CamelCase` class silently registers **zero** keywords.
- **`ROBOT_LIBRARY_SCOPE = "SUITE"`** must be set on the class.
- **Read Robot variables lazily** via `BuiltIn().get_variable_value(...)` inside methods. Never read
  `${OUTER_VM}`, `${SNAP_PATH}`, etc. in `__init__` — the class is instantiated for keyword discovery
  before a run context exists, which raises `RobotNotRunningError`.
- **`subprocess` with `shell=False`** — run commands as arg lists, never as host shell strings.
- **Result namedtuple exposes `.rc` / `.stdout` / `.stderr`.** The name `rc` is load-bearing: suites
  read `${result.rc}` via extended-variable syntax. Renaming it `returncode` breaks every caller
  without an error.
- **Use `_poll_until(predicate, attempts, interval, fail_msg, ...)` for every poll loop** — never
  re-implement `FOR`/`Sleep`.
- **Never call `BuiltIn().run_keyword()`.** Compose Python directly (`self.run_in_vm(...)`). The only
  allowed `BuiltIn` calls are `get_variable_value` and `set_suite_variable`. Replace `Log` with
  `robot.api.logger.info`, `Sleep` with `time.sleep`, and `Should *` / `Fail` with
  `raise AssertionError(msg)` (keep the message text).
- **Container commands go through the exec helpers** — never hand-build `lxc exec <node> -- sh -c "..."`
  and pass it to `run_in_vm`. Sites where non-zero rc is a valid outcome (`grep -c`, `cmd || echo 0`)
  need a **non-raising** variant and must **not** run under `bash -eo pipefail` (errexit aborts before
  the `|| echo` fallback and changes the captured output).
- **Keep helper modules separate.** The class imports `snap_services.py`, `rbd_replication.py`, etc.
  but must never re-export their keyword names — two imported libraries exposing the same keyword name
  is a Robot error.

### Migrating an existing keyword

Preserve **byte-for-byte**: the keyword name, return shape (bare string vs. result namedtuple — match
what callers consume), timeouts, sleeps, and assertion-message text. Only rewrite the extraction
pipeline. If you find a dead argument or an unreachable keyword, **flag it** for a maintainer — do not
silently fix it.

### Verify

- Add pytest tests for every pure helper in `tests/robot/resources/test_harness_helpers.py`; they run
  from the `unit-tests` suite and need no LXD.
- Run `tox -e robot -- --dryrun ...` to prove keyword resolution across all suites before opening a PR.

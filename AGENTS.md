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

Integration tests run in GitHub Actions only — do not try to run them locally.

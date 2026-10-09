package pebble_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWaitReady tolerates startup's not-yet-published service, not child failure.
func TestWaitReady(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.snap, "bin", "pebble"), `#!/bin/sh
if [ ! -e "$SNAP_COMMON/queried" ]; then
    touch "$SNAP_COMMON/queried"
    printf '{"services":{}}'
else
    printf '{"services":{"mon":{"name":"mon","startup":"enabled","current":"active"}}}'
fi
`, 0755)
	err := os.MkdirAll(f.common, 0700)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.command(t, "wait-ready", "mon").CombinedOutput()
	if err != nil {
		t.Fatalf("did not await child startup: %v: %s", err, out)
	}
}

// TestReloadWaitsForBootstrap covers reload reaching a freshly execed supervisor
// before its API is listening. It must not lose a just-published OSD inventory.
func TestReloadWaitsForBootstrap(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.snap, "bin", "pebble"), `#!/bin/sh
if [ ! -e "$SNAP_COMMON/queried" ]; then
    touch "$SNAP_COMMON/queried"
    exit 1
fi
printf '{"services":{}}'
`, 0755)
	out, err := f.command(t, "reload", "osd").CombinedOutput()
	if err != nil {
		t.Fatalf("reload raced supervisor startup: %v: %s", err, out)
	}
}

// TestOSDOuterEntryPointHasNoLegacySupervisor keeps the old private script
// usable, but forbids a parallel HUP/daemonizing implementation.
func TestOSDOuterEntryPointHasNoLegacySupervisor(t *testing.T) {
	f := newFixture(t)
	wrapper, err := os.ReadFile("../../../snapcraft/commands/osd.start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.snap, "commands", "osd.start"), string(wrapper), 0755)
	writeFile(t, filepath.Join(f.snap, "bin", "microceph-pebble"), "#!/bin/sh\nprintf '%s\\n' \"$@\"\n", 0755)
	cmd := f.command(t, "unused", "unused")
	cmd.Path = filepath.Join(f.snap, "commands", "osd.start")
	cmd.Args = []string{cmd.Path}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "run\nosd\n" {
		t.Fatalf("legacy outer launcher still in use: %v: %s", err, out)
	}
}

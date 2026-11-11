package ceph

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var newSMBPrivateCommandFunc = newSMBPrivateCommand
var runSMBPrivateCommandFunc = runSMBPrivateCommand

// newSMBPrivateCommand creates a bounded native client, never a serving process.
// Setup uses the same mechanical library as service startup, in a child mount
// namespace. There is no additional command/script family or MicroCeph entrypoint.
func newSMBPrivateCommand(ctx context.Context, root string, argv []string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("missing SMB native command")
	}
	snap := os.Getenv("SNAP")
	setup := ""
	switch argv[0] {
	case filepath.Join(snap, "bin/ctdb"):
		if len(argv) < 2 || len(argv) > 3 {
			return nil, fmt.Errorf("invalid CTDB client arguments")
		}
		switch argv[1] {
		case "pnn", "status", "reloadnodes":
			if len(argv) != 2 {
				return nil, fmt.Errorf("invalid CTDB client arguments")
			}
		case "nodestatus":
			if len(argv) != 3 {
				return nil, fmt.Errorf("invalid CTDB node argument")
			}
			_, err := strconv.ParseUint(argv[2], 10, 64)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported CTDB client command")
		}
		setup = "smb_setup_ctdb;"
	case filepath.Join(snap, "bin/python3"):
		if len(argv) != 5 || argv[1] != "-B" || argv[2] != filepath.Join(snap, "smb/native.py") {
			return nil, fmt.Errorf("unsupported SMB binding command")
		}
		switch argv[3] {
		case "accounts":
			if filepath.Dir(argv[4]) != filepath.Join(root, "config") || !strings.HasPrefix(filepath.Base(argv[4]), ".native-input-") {
				return nil, fmt.Errorf("invalid private account input")
			}
			setup = "smb_setup_nss; if [[ \"$SMB_CLUSTERED\" == 1 ]]; then smb_setup_ctdb; fi;"
		default:
			return nil, fmt.Errorf("unsupported SMB binding operation")
		}
	default:
		return nil, fmt.Errorf("unsupported SMB native client")
	}
	active, err := activeSMBRoot(filepath.Dir(root))
	if err != nil || active != root {
		return nil, fmt.Errorf("SMB command owner is unavailable or changed")
	}
	err = checkSMBStartIntent(root)
	if err != nil {
		return nil, err
	}
	body := `. "$SNAP/commands/smb-startup"; smb_load_context; ` + setup + ` smb_check_start_intent; exec "$@"`
	args := append([]string{"-euc", body, "smb-native-client"}, argv...)
	cmd := exec.CommandContext(ctx, "/bin/bash", args...)
	cmd.Env = append(os.Environ(), "SMB_EXPECTED_OWNER="+filepath.Base(root), "SMB_EXPECTED_ROOT="+root)
	// Cloneflags confines only the child. Never unshare, chdir, setenv or mount
	// in the daemon, and never fall back to the daemon's ordinary namespace.
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS, Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	// systemd's daemon control-group cleanup covers descendants after daemon
	// failure. Pdeathsig is thread-bound and is not process-tree cleanup.
	return cmd, nil
}

func runSMBPrivateCommand(ctx context.Context, root string, argv []string, input io.Reader) ([]byte, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}
	cmd, err := newSMBPrivateCommandFunc(ctx, root, argv)
	if err != nil {
		return nil, err
	}
	var out smbNativeOutput
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &out, input
	err = cmd.Run()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		active, ownerErr := activeSMBRoot(filepath.Dir(root))
		if ownerErr != nil || active != root {
			err = fmt.Errorf("SMB owner changed during native command")
		}
	}
	if err == nil && out.overflow {
		err = fmt.Errorf("SMB native output exceeded its limit")
	}
	if err != nil {
		_ = writeSMBNativeFile(filepath.Join(root, "diagnostics/native-process-error"), out.data)
		return nil, fmt.Errorf("SMB native command failed; inspect private diagnostics; external outcome may be uncertain")
	}
	return out.data, nil
}

type smbNativeOutput struct {
	data     []byte
	overflow bool
}

func (b *smbNativeOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 32*1024*1024 - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

package common

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/canonical/lxd/shared"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The backing file also exists on the host so the pre-fix zero-byte check can
// obtain its size. Only the kernel metadata is simulated: no root is required.
type luksTestFS struct {
	afero.Fs
	device        string
	metadataError error
}

func (f luksTestFS) Open(name string) (afero.File, error) {
	if name == "/sys/dev/block/253:0/dm/uuid" && f.metadataError != nil {
		return nil, f.metadataError
	}
	return f.Fs.Open(name)
}

func (f luksTestFS) Stat(name string) (os.FileInfo, error) {
	info, err := f.Fs.Stat(name)
	if err != nil || name != f.device {
		return info, err
	}
	return luksTestBlockInfo{info}, nil
}

type luksTestBlockInfo struct{ os.FileInfo }

func (luksTestBlockInfo) Mode() os.FileMode { return os.ModeDevice | 0600 }
func (luksTestBlockInfo) Sys() any {
	return &syscall.Stat_t{Rdev: unix.Mkdev(253, 0)}
}

func TestPristineLUKSMapping(t *testing.T) {
	for _, tc := range []struct {
		name          string
		uuid          string
		probe         string
		probeError    error
		labels        bool
		labelError    error
		labelOutput   string
		labelStderr   string
		metadataError error
		want          bool
		wantError     bool
	}{
		{name: "fresh LUKS2", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, want: true},
		{name: "fresh LUKS1", uuid: "CRYPT-LUKS1-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":null}`, want: true},
		{name: "filesystem", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[{"type":"ext4"}]}`},
		{name: "partition table", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[{"type":"gpt"}]}`},
		{name: "bluestore signature", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[{"type":"ceph_bluestore"}]}`},
		{name: "bluestore label", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labels: true},
		{name: "nested LUKS signature", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[{"type":"crypto_LUKS"}]}`},
		{name: "label tool unexpected exit", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelError: NewMockExitError(2), wantError: true},
		{name: "label tool permission denied", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelStderr: "unable to read label for %s: (13) Permission denied\n", wantError: true},
		{name: "label tool read failure", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelStderr: "2026-10-17T15:48:12.830+0530 715069a29bc0 -1 bluestore _read_bdev_label failed to read from %s at 0x0: (5) Input/output error\n2026-10-17T15:48:12.830+0530 715069a29bc0 -1 bluestore(%s) No valid bdev label found\nunable to read label for %s: (2) No such file or directory\n", wantError: true},
		{name: "label tool missing diagnostics", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelError: NewMockExitError(1), wantError: true},
		{name: "label tool unavailable", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelError: os.ErrNotExist, wantError: true},
		{name: "label tool invalid output", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelOutput: "invalid", wantError: true},
		{name: "label tool missing device", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":[]}`, labelOutput: `{}`, wantError: true},
		{name: "probe failure", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probeError: fmt.Errorf("permission denied"), wantError: true},
		{name: "invalid probe JSON", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: "invalid", wantError: true},
		{name: "missing signatures", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{}`, wantError: true},
		{name: "invalid signatures type", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", probe: `{"signatures":{}}`, wantError: true},
		{name: "mapper UUID inaccessible", uuid: "CRYPT-LUKS2-0123456789abcdef0123456789abcdef-volume", metadataError: os.ErrPermission, wantError: true},
		{name: "no mapper UUID"},
		{name: "plain crypt mapper", uuid: "CRYPT-PLAIN-volume"},
		{name: "LVM mapper", uuid: "LVM-volume"},
		{name: "malformed LUKS UUID", uuid: "CRYPT-LUKS2-invalid-volume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := filepath.Join(t.TempDir(), "device")
			data := bytes.Repeat([]byte{0x5a}, 2*1024*1024)
			require.NoError(t, os.WriteFile(device, data, 0600))
			fs := luksTestFS{Fs: afero.NewMemMapFs(), device: device, metadataError: tc.metadataError}
			require.NoError(t, afero.WriteFile(fs, device, data, 0600))
			uuidPath := "/sys/dev/block/253:0/dm/uuid"
			require.NoError(t, fs.MkdirAll(filepath.Dir(uuidPath), 0755))
			require.NoError(t, afero.WriteFile(fs, uuidPath, []byte(tc.uuid+"\n"), 0400))

			runner := &MockRunner{}
			previous := ProcessExec
			ProcessExec = runner
			t.Cleanup(func() { ProcessExec = previous })
			if tc.probe != "" || tc.probeError != nil {
				runner.On("RunCommand", "wipefs", "--no-act", "--json", device).Return(tc.probe, tc.probeError).Once()
			}
			if tc.want || tc.labels || tc.labelError != nil || tc.labelOutput != "" || tc.labelStderr != "" {
				stderr := fmt.Sprintf("2026-10-17T15:48:12.830+0530 715069a29bc0 -1 bluestore(%s) No valid bdev label found\nunable to read label for %s: (2) No such file or directory\n", device, device)
				if tc.labelStderr != "" {
					stderr = strings.ReplaceAll(tc.labelStderr, "%s", device)
				}
				var labelError error = shared.NewRunError("ceph-bluestore-tool", nil, NewMockExitError(1), nil, bytes.NewBufferString(stderr))
				if tc.labels {
					labelError = nil
				} else if tc.labelError != nil {
					labelError = tc.labelError
				}
				labelOutput := tc.labelOutput
				if labelOutput == "" {
					labelOutput = fmt.Sprintf(`{%q:{}}`, device)
				}
				runner.On("RunCommand", "ceph-bluestore-tool", "show-label", "--dev", device).Return(labelOutput, labelError).Once()
			}

			pristine, err := IsPristineDiskWithFs(device, fs)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, pristine)
			runner.AssertExpectations(t)
		})
	}
}

// Exercise production stderr capture, not just a bare mocked exit status.
func TestLUKSLabelProbeDiagnostics(t *testing.T) {
	binDir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$TEST_LABEL_STDOUT\"\nprintf '%s' \"$TEST_LABEL_STDERR\" >&2\nexit 1\n"
	err := os.WriteFile(filepath.Join(binDir, "ceph-bluestore-tool"), []byte(script), 0700)
	require.NoError(t, err)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := ProcessExec
	ProcessExec = RunnerImpl{}
	t.Cleanup(func() { ProcessExec = previous })

	device := "/dev/mapper/test-luks"
	absent := "2026-10-17T15:48:12.830+0530 715069a29bc0 -1 bluestore(" + device + ") No valid bdev label found\n" +
		"unable to read label for " + device + ": (2) No such file or directory\n"
	for _, tc := range []struct {
		name   string
		stderr string
		want   bool
	}{
		{name: "confirmed absence", stderr: absent, want: true},
		{name: "permission denied", stderr: "unable to read label for " + device + ": (13) Permission denied\n"},
		{name: "read error then ENOENT", stderr: "failed to read from " + device + ": (5) Input/output error\n" + absent},
		{name: "unknown diagnostic", stderr: "unexpected probe failure\n" + absent},
		{name: "missing diagnostics"},
		{name: "ENOENT alone", stderr: "unable to read label for " + device + ": (2) No such file or directory\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_LABEL_STDOUT", fmt.Sprintf(`{%q:{}}`, device))
			t.Setenv("TEST_LABEL_STDERR", tc.stderr)
			pristine, err := checkLUKSDeviceLabels(device)
			if tc.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tc.want, pristine)
		})
	}
}

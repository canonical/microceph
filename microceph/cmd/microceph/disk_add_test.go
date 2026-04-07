package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestCmdDiskAddValidateFlags(t *testing.T) {
	tests := []struct {
		name        string
		cmd         cmdDiskAdd
		args        []string
		errorSubstr string
	}{
		{
			name:        "wal-match requires osd-match",
			cmd:         cmdDiskAdd{flagWALMatch: "eq(@size, 20GiB)"},
			errorSubstr: "--wal-match requires --osd-match",
		},
		{
			name:        "db-match requires osd-match",
			cmd:         cmdDiskAdd{flagDBMatch: "eq(@size, 30GiB)"},
			errorSubstr: "--db-match requires --osd-match",
		},
		{
			name:        "wal-match requires wal-size",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagWALMatch: "eq(@size, 20GiB)"},
			errorSubstr: "--wal-match requires --wal-size",
		},
		{
			name:        "db-match requires db-size",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagDBMatch: "eq(@size, 30GiB)"},
			errorSubstr: "--db-match requires --db-size",
		},
		{
			name:        "wal-size requires wal-match",
			cmd:         cmdDiskAdd{flagWALSize: "1GiB"},
			errorSubstr: "--wal-size requires --wal-match",
		},
		{
			name:        "db-size requires db-match",
			cmd:         cmdDiskAdd{flagDBSize: "4GiB"},
			errorSubstr: "--db-size requires --db-match",
		},
		{
			name:        "dsl and positional args are exclusive",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)"},
			args:        []string{"/dev/sdb"},
			errorSubstr: "cannot be used with positional device arguments",
		},
		{
			name:        "dsl and all-available are exclusive",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagAllDevices: true},
			errorSubstr: "cannot be used with --all-available",
		},
		{
			name:        "wal-device with dsl is unsupported",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", walDevice: "/dev/sdb"},
			errorSubstr: "--wal-device and --db-device are not supported with DSL matching",
		},
		{
			name:        "dry-run requires osd-match",
			cmd:         cmdDiskAdd{flagDryRun: true},
			errorSubstr: "--dry-run requires --osd-match",
		},
		{
			name:        "json requires dry-run",
			cmd:         cmdDiskAdd{flagJSON: true, flagOSDMatch: "eq(@size, 10GiB)"},
			errorSubstr: "--json requires --dry-run",
		},
		{
			name:        "wal-encrypt requires wal-match",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", walEncrypt: true},
			errorSubstr: "--wal-encrypt requires --wal-match",
		},
		{
			name:        "wal-wipe requires wal-match",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", walWipe: true},
			errorSubstr: "--wal-wipe requires --wal-match",
		},
		{
			name:        "db-encrypt requires db-match",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", dbEncrypt: true},
			errorSubstr: "--db-encrypt requires --db-match",
		},
		{
			name:        "db-wipe requires db-match",
			cmd:         cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", dbWipe: true},
			errorSubstr: "--db-wipe requires --db-match",
		},
		{
			name: "waldb execution is accepted",
			cmd:  cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagWALMatch: "eq(@size, 20GiB)", flagWALSize: "1GiB"},
		},
		{
			name: "db waldb execution is accepted",
			cmd:  cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagDBMatch: "eq(@size, 30GiB)", flagDBSize: "4GiB"},
		},
		{
			name: "waldb dry-run is accepted",
			cmd:  cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagWALMatch: "eq(@size, 20GiB)", flagWALSize: "1GiB", walEncrypt: true, walWipe: true, flagDryRun: true},
		},
		{
			name: "db dry-run is accepted",
			cmd:  cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagDBMatch: "eq(@size, 30GiB)", flagDBSize: "4GiB", dbEncrypt: true, dbWipe: true, flagDryRun: true},
		},
		{
			name: "legacy wal-device with wal-encrypt remains valid",
			cmd:  cmdDiskAdd{walDevice: "/dev/sdb", walEncrypt: true},
		},
		{
			name: "legacy wal-device with wal-wipe remains valid",
			cmd:  cmdDiskAdd{walDevice: "/dev/sdb", walWipe: true},
		},
		{
			name: "legacy db-device with db-encrypt remains valid",
			cmd:  cmdDiskAdd{dbDevice: "/dev/sdc", dbEncrypt: true},
		},
		{
			name: "legacy db-device with db-wipe remains valid",
			cmd:  cmdDiskAdd{dbDevice: "/dev/sdc", dbWipe: true},
		},
		{
			name: "plain osd-match remains valid",
			cmd:  cmdDiskAdd{flagOSDMatch: "eq(@size, 10GiB)", flagDryRun: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.validateFlags(tt.args)
			if tt.errorSubstr == "" {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			assert.ErrorContains(t, err, tt.errorSubstr)
		})
	}
}

func TestPrintDryRunOutputShowsResetActions(t *testing.T) {
	cmd := cmdDiskAdd{}
	resp := types.DiskAddResponse{
		Warnings: []string{"WAL carrier /dev/disk/by-path/pci-0000:00:03.0 will be wiped/reset before partitioning"},
		DryRunPlan: []types.DryRunOSDPlan{{
			OSDPath: "/dev/disk/by-path/pci-0000:00:01.0",
			WAL: &types.DryRunPartitionPlan{
				ParentPath:     "/dev/disk/by-path/pci-0000:00:03.0",
				Partition:      1,
				Size:           "1.00 GiB",
				ResetBeforeUse: true,
			},
			DB: &types.DryRunPartitionPlan{
				ParentPath: "/dev/disk/by-path/pci-0000:00:04.0",
				Partition:  2,
				Size:       "2.00 GiB",
			},
		}},
	}

	output := captureStdout(t, func() {
		err := cmd.printDryRunOutput(resp)
		require.NoError(t, err)
	})

	assert.True(t, strings.Contains(output, "wiped/reset before partitioning"))
	assert.True(t, strings.Contains(output, "WAL ACTION"))
	assert.True(t, strings.Contains(output, "DB ACTION"))
	assert.True(t, strings.Contains(output, "reset"))
	assert.True(t, strings.Contains(output, "append"))
}

func TestPrintDryRunOutputJSON(t *testing.T) {
	cmd := cmdDiskAdd{flagJSON: true}
	resp := types.DiskAddResponse{
		Warnings: []string{"dry-run warning"},
		DryRunPlan: []types.DryRunOSDPlan{{
			OSDPath: "/dev/disk/by-path/osd0",
			WAL: &types.DryRunPartitionPlan{
				Kind:           "wal",
				ParentPath:     "/dev/disk/by-path/wal0",
				Partition:      1,
				Size:           "1.00 GiB",
				ResetBeforeUse: true,
			},
		}},
	}

	output := captureStdout(t, func() {
		err := cmd.printDryRunOutput(resp)
		require.NoError(t, err)
	})

	var decoded types.DiskAddResponse
	require.NoError(t, json.Unmarshal([]byte(output), &decoded))
	assert.Equal(t, resp, decoded)
}

func TestPrintDryRunOutputJSONValidationErrorReturnsError(t *testing.T) {
	cmd := cmdDiskAdd{flagJSON: true}
	resp := types.DiskAddResponse{
		ValidationError: "--wal-size must be greater than 0",
	}

	var gotErr error
	output := captureStdout(t, func() {
		gotErr = cmd.printDryRunOutput(resp)
	})

	require.Error(t, gotErr)
	assert.EqualError(t, gotErr, resp.ValidationError)

	var decoded types.DiskAddResponse
	require.NoError(t, json.Unmarshal([]byte(output), &decoded))
	assert.Equal(t, resp, decoded)
}

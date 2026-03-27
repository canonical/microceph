package api

import (
	"io"
	"strings"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAndPatchDiskPostParams(t *testing.T) {
	tests := []struct {
		name               string
		body               string
		expectedPaths      []string
		expectedOSDMatch   string
		expectedWALMatch   string
		expectedWALSize    string
		expectedDBMatch    string
		expectedDBSize     string
		expectedWALWipe    bool
		expectedWALEncrypt bool
		expectedDBWipe     bool
		expectedDBEncrypt  bool
		description        string
	}{
		{
			name:          "legacy single string path",
			body:          `{"path":"/dev/sdb","wipe":false,"encrypt":false}`,
			expectedPaths: []string{"/dev/sdb"},
			description:   "Legacy clients send path as a single string; should be wrapped into an array",
		},
		{
			name:          "current array of paths",
			body:          `{"path":["/dev/sdb","/dev/sdc"],"wipe":false,"encrypt":false}`,
			expectedPaths: []string{"/dev/sdb", "/dev/sdc"},
			description:   "Current clients send path as an array; should be preserved as-is",
		},
		{
			name:          "current single element array",
			body:          `{"path":["/dev/sdb"],"wipe":false,"encrypt":false}`,
			expectedPaths: []string{"/dev/sdb"},
			description:   "Current clients with a single disk send a one-element array",
		},
		{
			name:          "empty array (no available disks)",
			body:          `{"path":[],"wipe":false,"encrypt":false}`,
			expectedPaths: []string{},
			description:   "Empty array from --all-available with no free disks should stay empty",
		},
		{
			name:          "null path field",
			body:          `{"path":null,"wipe":false,"encrypt":false}`,
			expectedPaths: []string{},
			description:   "Null path should result in empty path slice, not [\"\"]",
		},
		{
			name:          "missing path field",
			body:          `{"wipe":false,"encrypt":false}`,
			expectedPaths: []string{},
			description:   "Missing path should result in empty path slice, not [\"\"]",
		},
		{
			name:             "osd_match only",
			body:             `{"osd_match":"eq(@type,'nvme')","dry_run":true}`,
			expectedPaths:    []string{},
			expectedOSDMatch: "eq(@type,'nvme')",
			description:      "DSL-only requests should preserve osd_match while path stays empty",
		},
		{
			name:             "osd plus wal match",
			body:             `{"osd_match":"eq(@type,'ssd')","wal_match":"eq(@type,'nvme')","wal_size":"4GiB"}`,
			expectedPaths:    []string{},
			expectedOSDMatch: "eq(@type,'ssd')",
			expectedWALMatch: "eq(@type,'nvme')",
			expectedWALSize:  "4GiB",
			description:      "Request plumbing should preserve wal DSL fields",
		},
		{
			name:             "osd plus db match",
			body:             `{"osd_match":"eq(@type,'ssd')","db_match":"eq(@type,'nvme')","db_size":"8GiB"}`,
			expectedPaths:    []string{},
			expectedOSDMatch: "eq(@type,'ssd')",
			expectedDBMatch:  "eq(@type,'nvme')",
			expectedDBSize:   "8GiB",
			description:      "Request plumbing should preserve db DSL fields",
		},
		{
			name:               "dsl wal and db aux flags",
			body:               `{"osd_match":"eq(@type,'ssd')","wal_match":"eq(@type,'nvme')","wal_size":"4GiB","walwipe":true,"walencrypt":true,"db_match":"eq(@type,'sata')","db_size":"8GiB","dbwipe":true,"dbencrypt":true}`,
			expectedPaths:      []string{},
			expectedOSDMatch:   "eq(@type,'ssd')",
			expectedWALMatch:   "eq(@type,'nvme')",
			expectedWALSize:    "4GiB",
			expectedWALWipe:    true,
			expectedWALEncrypt: true,
			expectedDBMatch:    "eq(@type,'sata')",
			expectedDBSize:     "8GiB",
			expectedDBWipe:     true,
			expectedDBEncrypt:  true,
			description:        "Request plumbing should preserve wal/db DSL auxiliary flags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := io.NopCloser(strings.NewReader(tt.body))
			result, err := parseAndPatchDiskPostParams(reader)
			require.NoError(t, err, tt.description)

			assert.Equal(t, tt.expectedPaths, result.Path, tt.description)
			assert.Equal(t, tt.expectedOSDMatch, result.OSDMatch, tt.description)
			assert.Equal(t, tt.expectedWALMatch, result.WALMatch, tt.description)
			assert.Equal(t, tt.expectedWALSize, result.WALSize, tt.description)
			assert.Equal(t, tt.expectedWALWipe, result.WALWipe, tt.description)
			assert.Equal(t, tt.expectedWALEncrypt, result.WALEncrypt, tt.description)
			assert.Equal(t, tt.expectedDBMatch, result.DBMatch, tt.description)
			assert.Equal(t, tt.expectedDBSize, result.DBSize, tt.description)
			assert.Equal(t, tt.expectedDBWipe, result.DBWipe, tt.description)
			assert.Equal(t, tt.expectedDBEncrypt, result.DBEncrypt, tt.description)
		})
	}
}

func TestValidateDiskPostRequest(t *testing.T) {
	tests := []struct {
		name        string
		req         types.DisksPost
		errorSubstr string
	}{
		{
			name:        "wal-match requires osd-match",
			req:         types.DisksPost{WALMatch: "eq(@type,'nvme')", WALSize: "1GiB"},
			errorSubstr: "--wal-match requires --osd-match",
		},
		{
			name:        "db-match requires osd-match",
			req:         types.DisksPost{DBMatch: "eq(@type,'nvme')", DBSize: "4GiB"},
			errorSubstr: "--db-match requires --osd-match",
		},
		{
			name:        "dry-run requires osd-match",
			req:         types.DisksPost{DryRun: true},
			errorSubstr: "--dry-run requires --osd-match",
		},
		{
			name:        "wal-match requires wal-size",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", WALMatch: "eq(@type,'nvme')"},
			errorSubstr: "--wal-match requires --wal-size",
		},
		{
			name:        "db-match requires db-size",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", DBMatch: "eq(@type,'nvme')"},
			errorSubstr: "--db-match requires --db-size",
		},
		{
			name:        "wal-size requires wal-match",
			req:         types.DisksPost{WALSize: "1GiB"},
			errorSubstr: "--wal-size requires --wal-match",
		},
		{
			name:        "db-size requires db-match",
			req:         types.DisksPost{DBSize: "4GiB"},
			errorSubstr: "--db-size requires --db-match",
		},
		{
			name:        "dsl and path are exclusive",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", Path: []string{"/dev/sdb"}},
			errorSubstr: "cannot be used with positional device arguments",
		},
		{
			name:        "dsl and waldev are exclusive",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", WALDev: stringPtr("/dev/sdc")},
			errorSubstr: "--wal-device and --db-device are not supported with DSL matching",
		},
		{
			name:        "wal-encrypt requires wal-match or wal-device",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", WALEncrypt: true},
			errorSubstr: "--wal-encrypt requires --wal-match or --wal-device",
		},
		{
			name:        "db-wipe requires db-match or db-device",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", DBWipe: true},
			errorSubstr: "--db-wipe requires --db-match or --db-device",
		},
		{
			name:        "wal-size must be positive",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", WALMatch: "eq(@type,'nvme')", WALSize: "0B"},
			errorSubstr: "--wal-size must be greater than 0",
		},
		{
			name:        "db-size must be positive",
			req:         types.DisksPost{OSDMatch: "eq(@type,'ssd')", DBMatch: "eq(@type,'nvme')", DBSize: "0B"},
			errorSubstr: "--db-size must be greater than 0",
		},
		{
			name: "legacy waldev with walencrypt remains valid",
			req:  types.DisksPost{Path: []string{"/dev/sdb"}, WALDev: stringPtr("/dev/sdc"), WALEncrypt: true},
		},
		{
			name: "legacy dbdev with dbwipe remains valid",
			req:  types.DisksPost{Path: []string{"/dev/sdb"}, DBDev: stringPtr("/dev/sdd"), DBWipe: true},
		},
		{
			name: "plain osd-match dry-run remains valid",
			req:  types.DisksPost{OSDMatch: "eq(@type,'ssd')", DryRun: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDiskPostRequest(tt.req)
			if tt.errorSubstr == "" {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			assert.ErrorContains(t, err, tt.errorSubstr)
		})
	}
}

func stringPtr(v string) *string {
	return &v
}

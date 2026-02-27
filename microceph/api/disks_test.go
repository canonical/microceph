package api

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAndPatchDiskPostParams(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		expectedPaths []string
		description   string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := io.NopCloser(strings.NewReader(tt.body))
			result, err := parseAndPatchDiskPostParams(reader)
			require.NoError(t, err, tt.description)

			assert.Equal(t, tt.expectedPaths, result.Path, tt.description)
		})
	}
}

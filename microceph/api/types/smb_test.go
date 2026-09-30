package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBClusterIDMatchesUpstreamSyntax(t *testing.T) {
	for _, id := range []string{"a", "hr", "files", "A1-b2", strings.Repeat("a", 18)} {
		require.True(t, SMBClusterIDRegex.MatchString(id), "valid upstream ID: %q", id)
	}
	for _, id := range []string{"", "files_prod", "files.prod", "-files", "files-", "files\n", "f iles", strings.Repeat("a", 19)} {
		require.False(t, SMBClusterIDRegex.MatchString(id), "invalid upstream ID: %q", id)
	}
}

package types

import (
	"encoding/json"
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

func TestManagedSMBRemovalJSONAndSelectors(t *testing.T) {
	request := ManagedSMBRemoval{ClusterID: "files", Target: "node-a"}
	data, err := json.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{"cluster_id":"files","target":"node-a"}`, string(data))

	for _, removal := range []ManagedSMBRemoval{
		{ClusterID: "files"},
		{ClusterID: "files", Target: "node-a"},
		{ClusterID: "files", Force: true},
	} {
		require.NoError(t, removal.Validate())
	}
	for _, removal := range []ManagedSMBRemoval{
		{ClusterID: "files", Target: "node/a"},
		{ClusterID: "files", Target: "node-a", Force: true},
		{ClusterID: "?"},
	} {
		require.Error(t, removal.Validate())
	}
}

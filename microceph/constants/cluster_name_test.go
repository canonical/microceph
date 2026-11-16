package constants

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateClusterNameAcceptsValidNames(t *testing.T) {
	for _, name := range []string{"a", "simple", "site2", "123", strings.Repeat("a", 63)} {
		assert.NoError(t, ValidateClusterName("remote", name), "expected %q to be valid", name)
	}
}

// The error must name the rejected value and state the whole rule, so that a
// user who typed a hyphen, an uppercase letter or a long name can tell which
// argument was refused and why.
func TestValidateClusterNameExplainsRejection(t *testing.T) {
	for _, name := range []string{"", "with-hyphens", "UPPERCASE", "primary_cluster", strings.Repeat("a", 64)} {
		t.Run(name, func(t *testing.T) {
			err := ValidateClusterName("local", name)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `invalid local name "`+name+`"`)
			assert.Contains(t, err.Error(), "1 to 63 characters")
			assert.Contains(t, err.Error(), "lowercase letters (a-z) and digits (0-9)")
		})
	}
}

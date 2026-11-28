package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBUsesExistingServiceFamily(t *testing.T) {
	found := false
	for _, r := range Servers["microceph"].Resources {
		for _, e := range r.Endpoints {
			if e.Path == "services/smb" {
				found = true
				require.NotNil(t, e.Get.Handler)
				require.NotNil(t, e.Put.Handler)
				require.NotNil(t, e.Delete.Handler)
				require.True(t, e.Put.ProxyTarget)
			}
			require.NotEqual(t, "managed-services", e.Path)
		}
	}
	require.True(t, found)
}

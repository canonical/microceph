package ceph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBLifecyclePreservesConfigurationMountRoot(t *testing.T) {
	for _, operation := range []string{"remove", "restore"} {
		t.Run(operation, func(t *testing.T) {
			_, confPath, _ := smbTestPaths(t, false)
			configDir := filepath.Join(confPath, "samba")
			require.NoError(t, os.MkdirAll(configDir, 0750))
			config := filepath.Join(configDir, "smb.conf")
			require.NoError(t, os.WriteFile(config, []byte("original"), 0600))
			// Hold the old inode open, like snapd's bind mount, so deletion and
			// recreation cannot accidentally reuse it and make this test pass.
			dir, err := os.Open(configDir)
			require.NoError(t, err)
			defer func() { _ = dir.Close() }()
			before, err := dir.Stat()
			require.NoError(t, err)
			snapshot, err := snapshotSMBLocalState("files")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(config, []byte("changed"), 0600))
			stale := filepath.Join(configDir, "stale")
			require.NoError(t, os.Mkdir(stale, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(stale, "partial"), []byte("partial"), 0600))

			if operation == "remove" {
				require.NoError(t, removeSMBLocalState("files"))
				require.NoFileExists(t, config)
			} else {
				require.NoError(t, snapshot.restore())
				contents, err := os.ReadFile(config)
				require.NoError(t, err)
				require.Equal(t, "original", string(contents))
			}
			after, err := os.Stat(configDir)
			require.NoError(t, err, "the snap layout's bind source must remain present")
			require.True(t, os.SameFile(before, after), "recreating the directory leaves /etc/samba bound to the deleted inode")
			require.Equal(t, before.Mode(), after.Mode())
			require.NoDirExists(t, stale)
		})
	}
}

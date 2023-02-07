package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"os"
	"path/filepath"
)

// EnableRGW enables the RGW service on the cluster and adds initial configuration given a service port number.
func EnableRGW(s common.StateInterface, port int) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	runPath := filepath.Join(os.Getenv("SNAP_DATA"), "run")
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")

	// Create RGW configuration.
	conf := newRadosGWConfig(confPath)
	err := conf.WriteConfig(
		map[string]any{
			"runDir":   runPath,
			"monitors": s.ClusterState().Address().Hostname(),
			"rgwPort":  port,
		},
	)
	if err != nil {
		return err
	}
	// Create RGW keyring.
	path := filepath.Join(dataPath, "radosgw", "ceph-radosgw.gateway")
	if err = createRGWKeyring(path); err != nil {
		return err
	}
	// Symlink the keyring to the conf directory for usage with the radosgw-admin command.
	if err = symlinkRGWKeyring(path, confPath); err != nil {
		return err
	}
	// Record the changes to the database.
	if err = rgwUpdateDatabase(s); err != nil {
		return err
	}
	if err = startRGW(); err != nil {
		return err
	}
	return nil
}

// rgwUpdateDatabase records changes to the database.
func rgwUpdateDatabase(s common.StateInterface) error {
	if s.ClusterState().Database == nil {
		return fmt.Errorf("no database")
	}
	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Record the role.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "rgw"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}
		return nil
	})
	return err
}

// startRGW starts the RGW service.
func startRGW() error {
	err := snapStart("rgw", true)
	if err != nil {
		return fmt.Errorf("Failed to start RGW service: %w", err)
	}
	return nil
}

// createRGWKeyring creates the RGW keyring.
func createRGWKeyring(path string) error {
	if err := os.MkdirAll(path, 0770); err != nil {
		return err
	}
	// Create the keyring.
	keyringPath := filepath.Join(path, "keyring")
	if _, err := os.Stat(keyringPath); err == nil {
		return nil
	}
	err := genAuth(
		keyringPath,
		"client.radosgw.gateway",
		[]string{"mon", "allow rw"},
		[]string{"osd", "allow rwx"})
	if err != nil {
		return err
	}

	return nil
}

// symlinkRGWKeyring creates a symlink to the RGW keyring in the conf directory for use with the radosgw-admin command.
func symlinkRGWKeyring(keyPath, confPath string) error {
	if err := os.Symlink(
		filepath.Join(keyPath, "keyring"),
		filepath.Join(confPath, "ceph.client.radosgw.gateway.keyring")); err != nil {
		return fmt.Errorf("Failed to create symlink to RGW keyring: %w", err)
	}
	return nil
}

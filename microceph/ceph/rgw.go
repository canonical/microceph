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
	if err = rgwCreateServiceDatabase(s); err != nil {
		return err
	}

	if err = startRGW(); err != nil {
		return err
	}

	return nil
}

// DisableRGW disables the RGW service on the cluster.
func DisableRGW(s common.StateInterface) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")

	err := stopRGW()
	if err != nil {
		return fmt.Errorf("Failed to stop RGW service: %w", err)
	}

	err = rgwRemoveServiceDatabase(s)
	if err != nil {
		return err
	}

	// Remove the keyring symlink.
	err = os.Remove(filepath.Join(confPath, "ceph.client.radosgw.gateway.keyring"))
	if err != nil {
		return fmt.Errorf("Failed to remove RGW keyring symlink: %w", err)
	}

	// Remove the keyring.
	err = os.Remove(filepath.Join(dataPath, "radosgw", "ceph-radosgw.gateway", "keyring"))
	if err != nil {
		return fmt.Errorf("Failed to remove RGW keyring: %w", err)
	}

	// Remove the configuration.
	err = os.Remove(filepath.Join(confPath, "radosgw.conf"))
	if err != nil {
		return fmt.Errorf("Failed to remove RGW configuration: %w", err)
	}

	return nil
}

// rgwCreateServiceDatabase creates a rgw service record in the database.
func rgwCreateServiceDatabase(s common.StateInterface) error {
	if s.ClusterState().Database == nil {
		return fmt.Errorf("no database")
	}

	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Create the service.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "rgw"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}

		return nil
	})
	return err
}

// rgwRemoveServiceDatabase removes a rgw service record from the database.
func rgwRemoveServiceDatabase(s common.StateInterface) error {
	if s.ClusterState().Database == nil {
		return fmt.Errorf("no database")
	}

	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Remove the service.
		err := database.DeleteService(ctx, tx, s.ClusterState().Name(), "rgw")
		if err != nil {
			return fmt.Errorf("Failed to remove role: %w", err)
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

// stopRGW stops the RGW service.
func stopRGW() error {
	err := snapStop("rgw", true)
	if err != nil {
		return fmt.Errorf("Failed to stop RGW service: %w", err)
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

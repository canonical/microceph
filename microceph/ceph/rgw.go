package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/database"
)

// EnableRGW enables the RGW service on the cluster and adds initial configuration given a service port number.
func EnableRGW(s interfaces.StateInterface, port int, sslPort int, sslCertificate string, sslPrivateKey string, monitors []string) error {
	pathConsts := constants.GetPathConst()

	sslCertificatePath := ""
	sslPrivateKeyPath := ""
	if sslCertificate != "" && sslPrivateKey != "" {
		err := os.Mkdir(pathConsts.SSLFilesPath, 0600)
		if err != nil {
			return err
		}
		sslCertificatePath = filepath.Join(sslDirectory, "server.crt")
		err = writeFile(sslCertificatePath, sslCertificate, 0600)
		if err != nil {
			return err
		}
		sslPrivateKeyPath = filepath.Join(sslDirectory, "server.key")
		err = writeFile(sslPrivateKeyPath, sslPrivateKey, 0600)
		if err != nil {
			return err
		}
	} else if sslCertificate == "" || sslPrivateKey == "" {
		port = 80
	}
	configs := map[string]any{
		"runDir":             pathConsts.RunPath,
		"monitors":           strings.Join(monitors, ","),
		"rgwPort":            port,
		"sslPort":            sslPort,
		"sslCertificatePath": sslCertificatePath,
		"sslPrivateKeyPath":  sslPrivateKeyPath,
	}

	// Create RGW configuration.
	rgwConf := newRadosGWConfig(pathConsts.ConfPath)
	err := rgwConf.WriteConfig(configs, 0644)
	if err != nil {
		return err
	}
	// Create RGW keyring.
	path := filepath.Join(pathConsts.DataPath, "radosgw", "ceph-radosgw.gateway")
	if err = createRGWKeyring(path); err != nil {
		return err
	}
	// Symlink the keyring to the conf directory for usage with the radosgw-admin command.
	if err = symlinkRGWKeyring(path, pathConsts.ConfPath); err != nil {
		return err
	}

	if err = startRGW(); err != nil {
		return err
	}

	return nil
}

// DisableRGW disables the RGW service on the cluster.
func DisableRGW(s interfaces.StateInterface) error {
	pathConsts := constants.GetPathConst()

	err := stopRGW()
	if err != nil {
		return fmt.Errorf("Failed to stop RGW service: %w", err)
	}

	err = removeServiceDatabase(s, "rgw")
	if err != nil {
		return err
	}

	// Remove the keyring symlink.
	err = os.Remove(filepath.Join(pathConsts.ConfPath, "ceph.client.radosgw.gateway.keyring"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW keyring symlink: %w", err)
	}

	// Remove the keyring.
	err = os.Remove(filepath.Join(pathConsts.DataPath, "radosgw", "ceph-radosgw.gateway", "keyring"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW keyring: %w", err)
	}

	// Remove the SSL files.
	err = os.Remove(pathConsts.SSLFilesPath)
	if err != nil {
		return fmt.Errorf("failed to remove RGW SSL files: %w", err)
	}

	// Remove the configuration.
	err = os.Remove(filepath.Join(pathConsts.ConfPath, "radosgw.conf"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW configuration: %w", err)
	}

	return nil
}

// rgwCreateServiceDatabase creates a rgw service record in the database.
func rgwCreateServiceDatabase(s interfaces.StateInterface) error {
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
func symlinkRGWKeyring(keyPath, ConfPath string) error {
	if err := os.Symlink(
		filepath.Join(keyPath, "keyring"),
		filepath.Join(ConfPath, "ceph.client.radosgw.gateway.keyring")); err != nil {
		return fmt.Errorf("Failed to create symlink to RGW keyring: %w", err)
	}

	return nil
}

func writeFile(path, data string, mode int) error {
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, os.FileMode(mode))
	if err != nil {
		return fmt.Errorf("Couldn't open %s: %w", path, err)
	}
	defer fd.Close()

	_, err = fd.Write([]byte(data))
	if err != nil {
		return fmt.Errorf("Couldn't write to %s: %w", path, err)
	}
	return nil
}

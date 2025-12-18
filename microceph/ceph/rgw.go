package ceph

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/database"
)

// EnableRGW enables the RGW service on the cluster and adds initial configuration given a service port number.
func EnableRGW(s interfaces.StateInterface, port int, sslPort int, sslCertificate string, sslPrivateKey string, monitors []string) error {
	pathConsts := constants.GetPathConst()

	sslCertificatePath := ""
	sslPrivateKeyPath := ""
	if sslCertificate != "" && sslPrivateKey != "" {
		sslCertificatePath = filepath.Join(pathConsts.SSLFilesPath, "server.crt")
		decodedSSLCertificate, err := base64.StdEncoding.DecodeString(sslCertificate)
		if err != nil {
			return err
		}
		err = os.WriteFile(sslCertificatePath, decodedSSLCertificate, 0600)
		if err != nil {
			return err
		}
		sslPrivateKeyPath = filepath.Join(pathConsts.SSLFilesPath, "server.key")
		decodedSSLPrivateKey, err := base64.StdEncoding.DecodeString(sslPrivateKey)
		if err != nil {
			return err
		}
		err = os.WriteFile(sslPrivateKeyPath, decodedSSLPrivateKey, 0600)
		if err != nil {
			return err
		}
	} else if sslCertificate == "" || sslPrivateKey == "" {
		// The default value is in the command line is 0 for the case where
		// both SSL certificates and Private Key are provided, so we handle the
		// default case here.
		if port == 0 {
			port = 80
		}
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
// If groupID is provided, it removes the grouped service; otherwise, it removes the ungrouped service.
func DisableRGW(ctx context.Context, s interfaces.StateInterface, groupID string) error {
	pathConsts := constants.GetPathConst()

	// If GroupID is provided, check if the grouped service exists
	if groupID != "" {
		exists, err := database.GroupedServicesQuery.ExistsOnHost(ctx, s, "rgw", groupID)
		if err != nil {
			return fmt.Errorf("failed to verify the node's RGW service GroupID: %w", err)
		} else if !exists {
			return fmt.Errorf("RGW service with GroupID '%s' not found on node '%s'", groupID, s.ClusterState().Name())
		}
	}

	err := stopRGW()
	if err != nil {
		return fmt.Errorf("Failed to stop RGW service: %w", err)
	}

	// Remove database records based on service type
	if groupID != "" {
		err = database.GroupedServicesQuery.RemoveForHost(ctx, s, "rgw", groupID)
		if err != nil {
			return err
		}
	} else {
		err = removeServiceDatabase(ctx, s, "rgw")
		if err != nil {
			return err
		}
	}

	// Remove the keyring symlink.
	err = os.Remove(filepath.Join(pathConsts.ConfPath, "ceph.client.radosgw.gateway.keyring"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW keyring symlink: %w", err)
	}

	// Remove the keyring.
	err = os.Remove(filepath.Join(pathConsts.DataPath, "radosgw", "ceph-radosgw.gateway", "keyring"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW keyring: %w", err)
	}

	// Remove the SSL files.
	err = os.Remove(filepath.Join(pathConsts.SSLFilesPath, "server.crt"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW SSL Certificate file: %w", err)
	}
	err = os.Remove(filepath.Join(pathConsts.SSLFilesPath, "server.key"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW SSL Private Key file: %w", err)
	}

	// Remove the configuration.
	err = os.Remove(filepath.Join(pathConsts.ConfPath, "radosgw.conf"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW configuration: %w", err)
	}

	return nil
}

// rgwCreateServiceDatabase creates a rgw service record in the database.
func rgwCreateServiceDatabase(ctx context.Context, s interfaces.StateInterface) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
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

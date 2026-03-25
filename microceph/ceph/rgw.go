package ceph

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"os"
	"path/filepath"
	"strings"
)

// writeSSLFiles decodes base64-encoded SSL certificate and key, and writes them to disk.
// Returns the paths to the written certificate and key files.
func writeSSLFiles(sslFilesPath string, sslCertificate string, sslPrivateKey string) (certPath string, keyPath string, err error) {
	if sslCertificate == "" {
		return "", "", fmt.Errorf("SSL certificate cannot be empty")
	}
	if sslPrivateKey == "" {
		return "", "", fmt.Errorf("SSL private key cannot be empty")
	}

	decodedCert, err := base64.StdEncoding.DecodeString(sslCertificate)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode SSL certificate: %w", err)
	}

	decodedKey, err := base64.StdEncoding.DecodeString(sslPrivateKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode SSL private key: %w", err)
	}

	certPath = filepath.Join(sslFilesPath, "server.crt")
	keyPath = filepath.Join(sslFilesPath, "server.key")

	// Write to temporary files first so that a partial failure doesn't
	// leave inconsistent state on disk.
	certTmpPath := certPath + ".tmp"
	keyTmpPath := keyPath + ".tmp"

	if err := os.WriteFile(certTmpPath, decodedCert, 0600); err != nil {
		return "", "", fmt.Errorf("failed to write SSL certificate: %w", err)
	}

	if err := os.WriteFile(keyTmpPath, decodedKey, 0600); err != nil {
		os.Remove(certTmpPath)
		return "", "", fmt.Errorf("failed to write SSL private key: %w", err)
	}

	// Both files written successfully — move them into place.
	if err := os.Rename(certTmpPath, certPath); err != nil {
		os.Remove(certTmpPath)
		os.Remove(keyTmpPath)
		return "", "", fmt.Errorf("failed to install SSL certificate: %w", err)
	}

	if err := os.Rename(keyTmpPath, keyPath); err != nil {
		os.Remove(keyTmpPath)
		return "", "", fmt.Errorf("failed to install SSL private key: %w", err)
	}

	return certPath, keyPath, nil
}

// EnableRGW enables the RGW service on the cluster and adds initial configuration given a service port number.
func EnableRGW(s interfaces.StateInterface, port int, sslPort int, sslCertificate string, sslPrivateKey string, monitors []string) error {
	pathConsts := constants.GetPathConst()

	sslCertificatePath := ""
	sslPrivateKeyPath := ""
	if sslCertificate != "" && sslPrivateKey != "" {
		var err error
		sslCertificatePath, sslPrivateKeyPath, err = writeSSLFiles(pathConsts.SSLFilesPath, sslCertificate, sslPrivateKey)
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

// UpdateRGWCertificates decodes base64 SSL certificate and key, and writes them to disk.
// RGW must be active for this operation.
func UpdateRGWCertificates(s interfaces.StateInterface, sslCertificate string, sslPrivateKey string) error {
	if err := snapCheckActive("rgw"); err != nil {
		return fmt.Errorf("RGW service is not running: %w", err)
	}

	pathConsts := constants.GetPathConst()

	// Verify that RGW was configured with SSL by checking the config file.
	confPath := filepath.Join(pathConsts.ConfPath, "radosgw.conf")
	confData, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("failed to read RGW configuration: %w", err)
	}
	if !strings.Contains(string(confData), "ssl_certificate=") {
		return fmt.Errorf("RGW is not configured with SSL; enable RGW with --ssl-certificate and --ssl-private-key first")
	}

	_, _, err = writeSSLFiles(pathConsts.SSLFilesPath, sslCertificate, sslPrivateKey)
	return err
}

// RestartRGW restarts the RGW service for immediate certificate pickup.
func RestartRGW() error {
	if err := snapRestart("rgw", false); err != nil {
		return fmt.Errorf("failed to restart RGW service: %w", err)
	}
	return nil
}

// DisableRGW disables the RGW service on the cluster.
func DisableRGW(ctx context.Context, s interfaces.StateInterface) error {
	pathConsts := constants.GetPathConst()

	err := stopRGW()
	if err != nil {
		return fmt.Errorf("Failed to stop RGW service: %w", err)
	}

	err = removeServiceDatabase(ctx, s, "rgw")
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
	if err != nil {
		return fmt.Errorf("failed to remove RGW configuration: %w", err)
	}

	return nil
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

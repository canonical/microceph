package ceph

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
)

// Injectable primitives so applyRGWFrontend is unit-testable without a running
// snap or Ceph cluster (AGENTS.md injectable-function-variable convention). The
// defaults delegate to the real snapctl/ceph helpers; tests override these to
// record calls and avoid external commands.
var (
	startRGWFunc         = startRGW
	restartRGWFunc       = RestartRGW
	createRGWKeyringFunc = createRGWKeyring
)

// effectiveRGWPorts applies the default-port-80 rule for a plaintext frontend
// (no SSL material) and returns the port/sslPort that will actually be rendered
// to radosgw.conf and recorded as observed state. When SSL is not configured
// sslPort is forced to 0 so the observed frontend never reports a stray SSL
// port (e.g. the enable-rgw CLI's --ssl-port default of 443) for a plaintext
// gateway. Centralizing the rule keeps the on-disk render and the rgw_frontends
// DB record consistent, so observed state matches what is running.
func effectiveRGWPorts(port, sslPort int, sslCert, sslKey string) (int, int) {
	if sslCert != "" && sslKey != "" {
		return port, sslPort
	}
	// Plaintext: no SSL port, default the plain port to 80.
	if port == 0 {
		port = 80
	}
	return port, 0
}

// rgwFrontendLine extracts the single `rgw frontends = ...` line from a rendered
// radosgw.conf. It is the only line whose change (port/ssl_port/ssl paths)
// requires an RGW restart; the `mon host` and `run dir` lines are owned by
// UpdateConfig/migrateStaleRunDir and must not drive restart decisions here.
// Returns "" when no such line is present (e.g. a missing file on first enable).
func rgwFrontendLine(conf []byte) string {
	for _, line := range strings.Split(string(conf), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "rgw frontends = ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// applyRGWFrontend renders radosgw.conf and SSL material for the desired RGW
// beast frontend and (re)starts RGW only when the on-disk state changed. It is
// idempotent: re-applying the same port/TLS is a no-op with no restart, so
// frequent placement reconcile PUTs do not churn RGW. It returns changed=true
// when it wrote files and (re)started RGW.
//
// The member decides whether a restart is needed because radosgw.conf and the
// SSL files are local to it; the engine only declares desired state. This is
// the single primitive shared by the enable rgw CLI path and the placement
// path.
func applyRGWFrontend(s interfaces.StateInterface, port, sslPort int, sslCert, sslKey string, monitors []string) (bool, error) {
	pathConsts := constants.GetPathConst()

	certPath := filepath.Join(pathConsts.SSLFilesPath, "server.crt")
	keyPath := filepath.Join(pathConsts.SSLFilesPath, "server.key")

	var decodedCert, decodedKey []byte
	sslCertificatePath := ""
	sslPrivateKeyPath := ""

	sslConfigured := sslCert != "" && sslKey != ""
	if sslConfigured {
		var err error
		decodedCert, err = base64.StdEncoding.DecodeString(sslCert)
		if err != nil {
			return false, fmt.Errorf("%w: failed to decode SSL certificate: %v", ErrRgwFrontendInvalid, err)
		}
		decodedKey, err = base64.StdEncoding.DecodeString(sslKey)
		if err != nil {
			return false, fmt.Errorf("%w: failed to decode SSL private key: %v", ErrRgwFrontendInvalid, err)
		}
		sslCertificatePath = certPath
		sslPrivateKeyPath = keyPath
	}

	port, sslPort = effectiveRGWPorts(port, sslPort, sslCert, sslKey)

	// Normalize monitors (IPv6-bracket + sort) so the rendered `mon host` line
	// is deterministic and byte-identical to what the periodic UpdateConfig ->
	// updateRadosGWMonHost writer produces. getMonitorsFromConfig iterates a map
	// (randomized order), so without this the two writers would flip-flop the
	// line. Restart decisions do not depend on this line (see rgwFrontendLine),
	// but keeping it stable avoids a pointless re-render race between writers.
	monitors = formatIPv6(monitors)
	sort.Strings(monitors)

	configs := map[string]any{
		"runDir":             pathConsts.RunPath,
		"monitors":           strings.Join(monitors, ","),
		"rgwPort":            port,
		"sslPort":            sslPort,
		"sslCertificatePath": sslCertificatePath,
		"sslPrivateKeyPath":  sslPrivateKeyPath,
	}

	rgwConf := newRadosGWConfig(pathConsts.ConfPath)
	desiredConf, err := rgwConf.RenderConfig(configs)
	if err != nil {
		return false, err
	}
	confPath := rgwConf.GetPath()

	// Detect a frontend change by comparing ONLY the rendered `rgw frontends`
	// line against the on-disk one, not the whole file. The `mon host` and
	// `run dir` lines are owned by UpdateConfig/migrateStaleRunDir and are
	// rewritten in place there; comparing the whole file would make every
	// reconcile look changed once those writers touch it, restarting RGW
	// needlessly (idempotency defeat in multi-mon / IPv6 clusters). A missing
	// file is a first enable.
	currentConf, confErr := os.ReadFile(confPath)
	confExists := false
	if confErr == nil {
		confExists = true
	} else if !os.IsNotExist(confErr) {
		return false, fmt.Errorf("failed to read radosgw.conf: %w", confErr)
	}
	frontendChanged := !confExists || rgwFrontendLine(desiredConf) != rgwFrontendLine(currentConf)

	// Detect an SSL change by comparing the decoded material against the
	// on-disk files. When moving to plaintext, any leftover SSL files from a
	// prior TLS config are removed, which is also a change.
	sslChanged := false
	if sslConfigured {
		curCert, e1 := os.ReadFile(certPath)
		curKey, e2 := os.ReadFile(keyPath)
		if os.IsNotExist(e1) || os.IsNotExist(e2) || !bytes.Equal(curCert, decodedCert) || !bytes.Equal(curKey, decodedKey) {
			sslChanged = true
		}
	} else if fileExists(certPath) || fileExists(keyPath) {
		sslChanged = true
	}

	if !frontendChanged && !sslChanged {
		return false, nil
	}

	// Apply SSL: write if configured, remove leftovers if moving to plaintext.
	if sslChanged {
		if sslConfigured {
			_, _, err = writeSSLFiles(pathConsts.SSLFilesPath, sslCert, sslKey)
			if err != nil {
				return false, err
			}
		} else {
			if err = removeIgnoreMissing(certPath); err != nil {
				return false, fmt.Errorf("failed to remove leftover SSL certificate: %w", err)
			}
			if err = removeIgnoreMissing(keyPath); err != nil {
				return false, fmt.Errorf("failed to remove leftover SSL private key: %w", err)
			}
		}
	}

	// Rewrite the config only when the frontend line changed (a cert rotation
	// keeps the same paths, so the line is unchanged and only the SSL files are
	// rewritten above). The full file is rendered so `mon host`/`run dir` stay
	// present; UpdateConfig keeps them fresh thereafter.
	if frontendChanged {
		if err = rgwConf.WriteConfig(configs, 0644); err != nil {
			return false, err
		}
	}

	// Ensure the keyring and its conf-dir symlink exist (both idempotent).
	keyringPath := filepath.Join(pathConsts.DataPath, "radosgw", "ceph-radosgw.gateway")
	if err = createRGWKeyringFunc(keyringPath); err != nil {
		return false, err
	}
	if err = symlinkRGWKeyring(keyringPath, pathConsts.ConfPath); err != nil {
		return false, err
	}

	// Start on first enable; restart on a frontend change. Only one of these
	// runs, and only when something actually changed.
	if !confExists {
		if err = startRGWFunc(); err != nil {
			return false, err
		}
	} else {
		if err = restartRGWFunc(); err != nil {
			return false, err
		}
	}
	return true, nil
}

// fileExists reports whether path exists (any type). Errors other than
// IsNotExist are treated as missing to keep the change-detection robust.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// removeIgnoreMissing removes path, treating a missing file as success so the
// disable / TLS->plaintext paths are idempotent (a re-run or partial prior
// state still converges).
func removeIgnoreMissing(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

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

// EnableRGW enables the RGW service on the cluster and adds initial
// configuration given a service port number. It delegates to applyRGWFrontend so
// the enable rgw CLI path and the placement path share one idempotent,
// restart-on-change primitive.
func EnableRGW(s interfaces.StateInterface, port int, sslPort int, sslCertificate string, sslPrivateKey string, monitors []string) error {
	_, err := applyRGWFrontend(s, port, sslPort, sslCertificate, sslPrivateKey, monitors)
	return err
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
	err = removeIgnoreMissing(filepath.Join(pathConsts.ConfPath, "ceph.client.radosgw.gateway.keyring"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW keyring symlink: %w", err)
	}

	// Remove the keyring.
	err = removeIgnoreMissing(filepath.Join(pathConsts.DataPath, "radosgw", "ceph-radosgw.gateway", "keyring"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW keyring: %w", err)
	}

	// Remove the SSL files.
	err = removeIgnoreMissing(filepath.Join(pathConsts.SSLFilesPath, "server.crt"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW SSL Certificate file: %w", err)
	}
	err = removeIgnoreMissing(filepath.Join(pathConsts.SSLFilesPath, "server.key"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW SSL Private Key file: %w", err)
	}

	// Remove the configuration.
	err = removeIgnoreMissing(filepath.Join(pathConsts.ConfPath, "radosgw.conf"))
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

// symlinkRGWKeyring creates a symlink to the RGW keyring in the conf directory
// for use with the radosgw-admin command. It is idempotent: if a symlink
// already points at the keyring, it is a no-op; a stale or non-symlink entry at
// the link path is replaced so re-applies and recoveries converge.
func symlinkRGWKeyring(keyPath, confPath string) error {
	linkPath := filepath.Join(confPath, "ceph.client.radosgw.gateway.keyring")
	target := filepath.Join(keyPath, "keyring")

	if fi, err := os.Lstat(linkPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if existing, rErr := os.Readlink(linkPath); rErr == nil && existing == target {
				return nil
			}
		}
		// A stale symlink or a stray regular file: remove before re-creating.
		_ = removeIgnoreMissing(linkPath)
	}

	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("Failed to create symlink to RGW keyring: %w", err)
	}
	return nil
}

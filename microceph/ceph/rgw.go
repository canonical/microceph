package ceph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

var (
	startRGWFunc         = startRGW
	restartRGWFunc       = restartRGW
	stopRGWFunc          = stopRGW
	checkRGWActiveFunc   = checkRGWActive
	checkRGWReadyFunc    = checkRGWReady
	createRGWKeyringFunc = createRGWKeyring
	getConfigDbFunc      = GetConfigDb
)

var errRGWInactive = errors.New("rgw service is not active")

// A first start creates the gateway's pools before it listens, so readiness
// waits far longer than a restart of an already-initialised gateway needs.
var rgwReadyTimeout = 2 * time.Minute

func rgwConfPath() string {
	return filepath.Join(constants.GetPathConst().ConfPath, "radosgw.conf")
}

func rgwTLSRoot() string {
	return filepath.Join(constants.GetPathConst().SSLFilesPath, "rgw-tls")
}

// rgwFrontendSpec contains validated, effective settings and decoded TLS material.
type rgwFrontendSpec struct {
	port    int
	sslPort int
	ssl     bool
	certPEM []byte
	keyPEM  []byte
}

// TLS material lives in generations: one directory per certificate and key
// pair, named by the hash of both, under rgwTLSRoot. The gateway needs the
// pair to change together, and two files rewritten in place cannot promise
// that: a crash between the writes leaves a certificate beside the wrong key.
// A generation is complete before radosgw.conf refers to it, so switching
// pairs is one line in the config, rolling back is pointing that line at the
// previous directory that is still intact, and the same pair always names the
// same directory so a repeat apply finds it already staged. A rotation lands
// in a fresh directory rather than over the files the running gateway reads,
// and old generations are removed only after the config points elsewhere.
// The digest is hex-encoded because raw bytes are not a valid path component.
func (s rgwFrontendSpec) tlsGenDir() string {
	sum := sha256.New()
	sum.Write(s.certPEM)
	sum.Write(s.keyPEM)
	return filepath.Join(rgwTLSRoot(), hex.EncodeToString(sum.Sum(nil)))
}

func rgwFrontendLine(conf []byte) string {
	for _, line := range strings.Split(string(conf), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "rgw frontends = ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// replaceRGWFrontendLine swaps only the frontend line so newer monitor and
// run-directory lines written by other paths are kept.
func replaceRGWFrontendLine(conf []byte, frontend string) []byte {
	lines := strings.Split(string(conf), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "rgw frontends = ") {
			lines[i] = frontend
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func rgwConfTLSPaths(conf []byte) (string, string) {
	var certPath, keyPath string
	for _, token := range strings.Fields(rgwFrontendLine(conf)) {
		value, ok := strings.CutPrefix(token, "ssl_certificate=")
		if ok {
			certPath = value
		}
		value, ok = strings.CutPrefix(token, "ssl_private_key=")
		if ok {
			keyPath = value
		}
	}
	return certPath, keyPath
}

func rgwConfPorts(conf []byte) (int, int, error) {
	var port, sslPort int
	for _, token := range strings.Fields(rgwFrontendLine(conf)) {
		name, value, found := strings.Cut(token, "=")
		if !found || (name != "port" && name != "ssl_port") {
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil {
			return 0, 0, fmt.Errorf("%w: invalid RGW listener port", ErrPlacementOperationFailed)
		}
		if name == "port" {
			port = number
		} else {
			sslPort = number
		}
	}
	return port, sslPort, nil
}

func pairOnDisk(certPath, keyPath string, certPEM, keyPEM []byte) bool {
	cert, certErr := os.ReadFile(certPath)
	key, keyErr := os.ReadFile(keyPath)
	return certErr == nil && keyErr == nil && bytes.Equal(cert, certPEM) && bytes.Equal(key, keyPEM)
}

// writeRGWFile publishes a complete file, including the directory entry, before returning.
func writeRGWFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	err = os.Rename(file.Name(), path)
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func stageRGWTLSGeneration(genDir string, certPEM, keyPEM []byte) error {
	err := os.MkdirAll(genDir, 0700)
	if err != nil {
		return err
	}
	err = os.Chmod(genDir, 0700)
	if err != nil {
		return err
	}
	certPath := filepath.Join(genDir, "server.crt")
	keyPath := filepath.Join(genDir, "server.key")
	if !pairOnDisk(certPath, keyPath, certPEM, keyPEM) {
		err = writeRGWFile(certPath, certPEM, 0600)
		if err != nil {
			return err
		}
		err = writeRGWFile(keyPath, keyPEM, 0600)
		if err != nil {
			return err
		}
	}
	err = os.Chmod(certPath, 0600)
	if err != nil {
		return err
	}
	return os.Chmod(keyPath, 0600)
}

// The journal contains configuration paths, not certificate or private-key bytes.
// Keep the previous state until readiness and database recording both succeed.
type rgwPendingApply struct {
	PreviousConfig *string `json:"previous_config"`
	PreviousActive *bool   `json:"previously_active"`
}

// rgwPendingApplyPath is the journal's on-disk path, radosgw.conf.pending
// beside radosgw.conf itself.
func rgwPendingApplyPath() string {
	return rgwConfPath() + ".pending"
}

func readPendingApply() (*rgwPendingApply, error) {
	data, err := os.ReadFile(rgwPendingApplyPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending rgwPendingApply
	err = json.Unmarshal(data, &pending)
	if err != nil || pending.PreviousConfig == nil || pending.PreviousActive == nil {
		return nil, errors.New("invalid pending RGW apply journal")
	}
	return &pending, nil
}

func writePendingApplyMarker(previous []byte, active bool) error {
	conf := string(previous)
	data, err := json.Marshal(rgwPendingApply{PreviousConfig: &conf, PreviousActive: &active})
	if err != nil {
		return err
	}
	return writeRGWFile(rgwPendingApplyPath(), data, 0600)
}

type rgwRollback struct {
	confPath        string
	prevConf        []byte
	prevActive      bool
	publishedGenDir string
	prevGenDir      string
	serviceChanged  bool
}

func (rb *rgwRollback) restore() error {
	if rb == nil {
		return nil
	}
	radosgwConfMu.Lock()
	var restoreErr error
	if len(rb.prevConf) == 0 {
		// First enable: no previous config existed, so restoring means
		// removing the file this apply just wrote.
		restoreErr = removeIgnoreMissing(rb.confPath)
	} else {
		previous := rb.prevConf
		current, err := os.ReadFile(rb.confPath)
		if err != nil && !os.IsNotExist(err) {
			restoreErr = err
		} else {
			// Keep monitor/run-directory updates made since this apply began.
			if err == nil && rgwFrontendLine(current) != "" {
				previous = replaceRGWFrontendLine(current, rgwFrontendLine(rb.prevConf))
			}
			if !bytes.Equal(previous, current) {
				restoreErr = writeRGWFile(rb.confPath, previous, 0644)
			}
		}
	}
	radosgwConfMu.Unlock()
	if restoreErr != nil {
		// The published config may still reference the new pair: do not delete it.
		return fmt.Errorf("failed to restore RGW configuration: %w", restoreErr)
	}
	if rb.serviceChanged {
		if rb.prevActive {
			restoreErr = restartRGWFunc()
		} else {
			restoreErr = stopRGWFunc()
		}
		if restoreErr != nil {
			return fmt.Errorf("failed to restore RGW service state: %w", restoreErr)
		}
	}
	// Delete the newly staged generation only if the restored config no
	// longer names it; an apply that never changed the pair leaves
	// publishedGenDir empty and skips this.
	if rb.publishedGenDir != "" && rb.publishedGenDir != rb.prevGenDir {
		restoreErr = os.RemoveAll(rb.publishedGenDir)
		if restoreErr != nil {
			return restoreErr
		}
	}
	// The rollback is itself the completed corrective action, so the marker
	// is cleared here rather than left for a later apply to redrive.
	restoreErr = removeIgnoreMissing(rgwPendingApplyPath())
	if restoreErr != nil {
		return restoreErr
	}
	// Material staged by an earlier unfinished update is unreferenced now.
	pruneRGWTLSGenerations()
	return nil
}

func readRGWConf() ([]byte, error) {
	conf, err := os.ReadFile(rgwConfPath())
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read RGW configuration: %w", ErrPlacementOperationFailed, err)
	}
	return conf, nil
}

func pruneRGWTLSGenerations() {
	conf, err := os.ReadFile(rgwConfPath())
	if err != nil {
		return
	}
	certPath, keyPath := rgwConfTLSPaths(conf)
	keep := filepath.Dir(certPath)
	entries, err := os.ReadDir(rgwTLSRoot())
	if err != nil && !os.IsNotExist(err) {
		logger.Warnf("cannot inspect old RGW TLS generations: %v", err)
		return
	}
	for _, entry := range entries {
		dir := filepath.Join(rgwTLSRoot(), entry.Name())
		if dir != keep {
			err = os.RemoveAll(dir)
			if err != nil {
				logger.Warnf("cannot remove old RGW TLS generation: %v", err)
			}
		}
	}
	removeLegacyRGWTLSFiles(certPath, keyPath)
}

// removeLegacyRGWTLSFiles deletes the pre-generation flat server.crt/server.key
// pair, but only once radosgw.conf points elsewhere: "feat(rgw): reuse the
// member's own pair when TLS is asked for without one" reads this pair before
// the first apply migrates it into a generation directory, so removing it any
// earlier would break reuse.
func removeLegacyRGWTLSFiles(certPath, keyPath string) {
	for _, path := range []string{
		filepath.Join(constants.GetPathConst().SSLFilesPath, "server.crt"),
		filepath.Join(constants.GetPathConst().SSLFilesPath, "server.key"),
	} {
		if path == certPath || path == keyPath {
			continue
		}
		err := removeIgnoreMissing(path)
		if err != nil {
			logger.Warnf("cannot remove old RGW TLS file: %v", err)
		}
	}
}

// refreshPendingApplyMarker makes a journal that could not be cleared describe
// the configuration now confirmed, so a later rollback restores this state.
func refreshPendingApplyMarker() {
	radosgwConfMu.Lock()
	defer radosgwConfMu.Unlock()
	conf, err := os.ReadFile(rgwConfPath())
	if err == nil {
		err = writePendingApplyMarker(conf, true)
	}
	if err != nil {
		logger.Warnf("cannot refresh the pending RGW apply journal: %v", err)
	}
}

func finishRGWApply() error {
	err := removeIgnoreMissing(rgwPendingApplyPath())
	if err != nil {
		return fmt.Errorf("%w: cannot clear pending RGW apply: %w", ErrPlacementOperationFailed, err)
	}
	pruneRGWTLSGenerations()
	return nil
}

// applyRGWFrontend publishes complete settings and starts or updates RGW.
// serviceStartMu is held by the caller; file writers take radosgwConfMu second.
// The returned rollback remains valid until the service record is committed.
func applyRGWFrontend(spec rgwFrontendSpec, monitors []string, restartOnChange bool) (*rgwRollback, error) {
	// Is the gateway running now? A rollback normally returns it to this state.
	activeErr := checkRGWActiveFunc()
	if activeErr != nil && !errors.Is(activeErr, errRGWInactive) {
		return nil, fmt.Errorf("%w: cannot inspect RGW service: %w", ErrPlacementOperationFailed, activeErr)
	}
	wasActive := activeErr == nil

	// Every gateway needs the keyring and its symlink. Both calls are safe to repeat.
	paths := constants.GetPathConst()
	keyringPath := filepath.Join(paths.DataPath, "radosgw", "ceph-radosgw.gateway")
	err := createRGWKeyringFunc(keyringPath)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot create RGW keyring: %w", ErrPlacementOperationFailed, err)
	}
	err = symlinkRGWKeyring(keyringPath, paths.ConfPath)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot link RGW keyring: %w", ErrPlacementOperationFailed, err)
	}

	// Render the config we want. With TLS it points at the pair's hashed directory.
	monitors = formatIPv6(monitors)
	sort.Strings(monitors)
	var certPath, keyPath, genDir string
	if spec.ssl {
		genDir = spec.tlsGenDir()
		certPath = filepath.Join(genDir, "server.crt")
		keyPath = filepath.Join(genDir, "server.key")
	}
	config := map[string]any{
		"runDir": paths.RunPath, "monitors": strings.Join(monitors, ","),
		"rgwPort": spec.port, "sslPort": spec.sslPort,
		"sslCertificatePath": certPath, "sslPrivateKeyPath": keyPath,
	}
	desired, err := newRadosGWConfig(paths.ConfPath).RenderConfig(config)
	if err != nil {
		return nil, err
	}

	// Lock radosgw.conf, then read it and any marker an unfinished apply left.
	radosgwConfMu.Lock()
	current, readErr := os.ReadFile(rgwConfPath())
	if readErr != nil && !os.IsNotExist(readErr) {
		radosgwConfMu.Unlock()
		return nil, fmt.Errorf("%w: cannot read RGW configuration: %w", ErrPlacementOperationFailed, readErr)
	}
	pending, err := readPendingApply()
	if err != nil {
		radosgwConfMu.Unlock()
		return nil, fmt.Errorf("%w: %w", ErrPlacementOperationFailed, err)
	}

	// What differs: the frontend line, the pair on disk, or an unfinished apply?
	changed := readErr != nil || rgwFrontendLine(current) != rgwFrontendLine(desired)
	materialChanged := spec.ssl && !pairOnDisk(certPath, keyPath, spec.certPEM, spec.keyPEM)
	var rb *rgwRollback
	if changed || materialChanged || pending != nil {
		// Record how to get back. A leftover marker, not the half-applied file,
		// is the rollback target.
		rb = &rgwRollback{confPath: rgwConfPath(), prevConf: current, prevActive: wasActive}
		if pending != nil {
			// Override the just-read default: the marker is what an earlier,
			// unfinished attempt actually started from.
			rb.prevConf = []byte(*pending.PreviousConfig)
			rb.prevActive = *pending.PreviousActive
		}
		// The old TLS generation isn't stored as its own field; recover its
		// directory by parsing the frontends line out of this rollback's own
		// config text.
		previousCert, _ := rgwConfTLSPaths(rb.prevConf)
		if previousCert != "" {
			rb.prevGenDir = filepath.Dir(previousCert)
		}
		// What this attempt is about to publish, so restore can tell it apart
		// from prevGenDir and know which one is safe to delete.
		rb.publishedGenDir = genDir

		// Publish in a safe order: the pair, then the marker, then the config.
		if spec.ssl {
			// Idempotent: a repeat apply, or a generation a crash left
			// partial, is restaged safely rather than skipped.
			err = stageRGWTLSGeneration(genDir, spec.certPEM, spec.keyPEM)
		}
		if err == nil && pending == nil {
			// Only when no marker exists yet: one from an earlier interrupted
			// attempt is left as the true rollback target, not overwritten.
			err = writePendingApplyMarker(current, wasActive)
		}
		if err == nil && changed {
			// The monitor line on disk may be newer than the one rendered
			// before the lock was taken, so only the frontend line changes.
			if rgwFrontendLine(current) != "" {
				desired = replaceRGWFrontendLine(current, rgwFrontendLine(desired))
			}
			err = writeRGWFile(rgwConfPath(), desired, 0644)
		}
	}
	// Unlock before the restart and the readiness wait, which can take minutes.
	radosgwConfMu.Unlock()
	if err != nil {
		// A failed publish undoes whatever was written.
		return nil, fmt.Errorf("%w: cannot publish RGW frontend: %w", ErrPlacementOperationFailed, errors.Join(err, rb.restore()))
	}

	// Without a restart the marker stays, so the next apply restarts.
	if !restartOnChange {
		return rb, nil
	}

	// Start a stopped gateway, restart a changed one, leave the rest alone.
	if !wasActive || rb != nil {
		if rb != nil {
			rb.serviceChanged = true
		}
		if wasActive {
			err = restartRGWFunc()
		} else {
			err = startRGWFunc()
		}
	}

	// If the start or the readiness wait fails, put the previous config and
	// gateway back.
	if err == nil {
		err = checkRGWReadyFunc(spec)
	}
	if err != nil {
		restoreErr := rb.restore()
		if restoreErr == nil && rb != nil {
			// Any material this attempt was activating is gone with it.
			restoreErr = errors.New("previous configuration restored")
		}
		return nil, fmt.Errorf("%w: RGW frontend did not become ready: %w", ErrPlacementOperationFailed, errors.Join(err, restoreErr))
	}
	return rb, nil
}

func checkRGWActive() error {
	out, err := common.ProcessExec.RunCommand("snapctl", "services", "microceph.rgw")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "microceph.rgw" {
			if fields[2] == "active" {
				return nil
			}
			if fields[2] == "inactive" {
				return errRGWInactive
			}
		}
	}
	return errors.New("cannot determine the RGW service state")
}

func checkRGWReady(spec rgwFrontendSpec) error {
	deadline := time.Now().Add(rgwReadyTimeout)
	for {
		err := checkRGWActiveFunc()
		if err == nil {
			err = checkRGWFrontend(spec)
		}
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Local readiness pins the supplied certificate, not a public DNS name or CA.
func checkRGWFrontend(spec rgwFrontendSpec) error {
	dialer := &net.Dialer{Timeout: time.Second}
	if spec.port != 0 {
		conn, err := dialer.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.port)))
		if err != nil {
			return err
		}
		conn.Close()
	}
	if !spec.ssl {
		return nil
	}
	certificate, _ := pem.Decode(spec.certPEM)
	if certificate == nil {
		return errors.New("expected RGW certificate is unavailable")
	}
	conn, err := dialer.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.sslPort)))
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err != nil {
		return err
	}
	client := tls.Client(conn, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, certificate.Bytes) {
				return errors.New("RGW did not load the expected certificate")
			}
			return nil
		},
	})
	return client.Handshake()
}

func removeIgnoreMissing(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// UpdateRGWCertificates saves a new pair and optionally restarts the gateway.
// A deferred restart leaves the journal so later reconciliation loads the pair.
func UpdateRGWCertificates(ctx context.Context, s interfaces.StateInterface, certificate, privateKey string, restart bool) error {
	cert, key, err := types.DecodeRGWCertificate(certificate, privateKey)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRgwFrontendInvalid, err)
	}
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	err = checkRGWActiveFunc()
	if err != nil {
		return fmt.Errorf("%w: RGW is not running: %w", ErrPlacementOperationFailed, err)
	}
	conf, err := readRGWConf()
	if err != nil {
		return err
	}
	certPath, keyPath := rgwConfTLSPaths(conf)
	if certPath == "" || keyPath == "" {
		return fmt.Errorf("%w: RGW is not configured with TLS", ErrPlacementOperationFailed)
	}
	port, sslPort, err := rgwConfPorts(conf)
	if err != nil {
		return err
	}
	config, err := getConfigDbFunc(ctx, s)
	if err != nil {
		return err
	}
	_, err = applyRGWFrontend(rgwFrontendSpec{port: port, sslPort: sslPort, ssl: true, certPEM: cert, keyPEM: key}, getMonitorsFromConfig(config), restart)
	if err != nil || !restart {
		return err
	}
	return finishRGWApply()
}

// DisableRGW disables the RGW service on the cluster.
// [[NOTE: intentionally minimal; only removes the files this branch adds
// and is not yet convergent on a repeat or interrupted call, replaced in
// "fix(rgw): make RGW disable converge when repeated or interrupted".]]
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

	// Remove the SSL files, in the old flat layout and in generations.
	err = os.Remove(filepath.Join(pathConsts.SSLFilesPath, "server.crt"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW SSL Certificate file: %w", err)
	}
	err = os.Remove(filepath.Join(pathConsts.SSLFilesPath, "server.key"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove RGW SSL Private Key file: %w", err)
	}
	err = os.RemoveAll(rgwTLSRoot())
	if err != nil {
		return fmt.Errorf("failed to remove RGW TLS generations: %w", err)
	}

	// Remove the configuration and any unfinished apply marker.
	err = removeIgnoreMissing(rgwPendingApplyPath())
	if err != nil {
		return fmt.Errorf("failed to remove RGW pending apply marker: %w", err)
	}
	err = os.Remove(filepath.Join(pathConsts.ConfPath, "radosgw.conf"))
	if err != nil {
		return fmt.Errorf("failed to remove RGW configuration: %w", err)
	}

	return nil
}

func startRGW() error {
	return snapStart("rgw", true)
}

func stopRGW() error {
	return snapStop("rgw", true)
}

func restartRGW() error {
	return snapRestart("rgw", false)
}

func createRGWKeyring(path string) error {
	err := os.MkdirAll(path, 0770)
	if err != nil {
		return err
	}
	keyring := filepath.Join(path, "keyring")
	_, err = os.Stat(keyring)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return genAuth(keyring, "client.radosgw.gateway", []string{"mon", "allow rw"}, []string{"osd", "allow rwx"})
}

func symlinkRGWKeyring(keyPath, confPath string) error {
	path := filepath.Join(confPath, "ceph.client.radosgw.gateway.keyring")
	target := filepath.Join(keyPath, "keyring")
	existing, err := os.Readlink(path)
	if err == nil && existing == target {
		return nil
	}
	err = removeIgnoreMissing(path)
	if err != nil {
		return err
	}
	return os.Symlink(target, path)
}

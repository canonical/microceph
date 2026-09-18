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

func resolveRGWTLSReuse(conf []byte) ([]byte, []byte, error) {
	certPath, keyPath := rgwConfTLSPaths(conf)
	if certPath == "" || keyPath == "" {
		return nil, nil, fmt.Errorf("%w: TLS material must be supplied for this member", ErrPlacementOperationFailed)
	}
	cert, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: cannot read the local RGW certificate: %w", ErrPlacementOperationFailed, err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: cannot read the local RGW private key: %w", ErrPlacementOperationFailed, err)
	}
	_, err = tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: the local RGW TLS pair is unusable", ErrPlacementOperationFailed)
	}
	return cert, key, nil
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
	if rb.publishedGenDir != "" && rb.publishedGenDir != rb.prevGenDir {
		restoreErr = os.RemoveAll(rb.publishedGenDir)
		if restoreErr != nil {
			return restoreErr
		}
	}
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
	// Remove the old layout only after the config points elsewhere.
	for _, path := range []string{
		filepath.Join(constants.GetPathConst().SSLFilesPath, "server.crt"),
		filepath.Join(constants.GetPathConst().SSLFilesPath, "server.key"),
	} {
		if path == certPath || path == keyPath {
			continue
		}
		err = removeIgnoreMissing(path)
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
	activeErr := checkRGWActiveFunc()
	if activeErr != nil && !errors.Is(activeErr, errRGWInactive) {
		return nil, fmt.Errorf("%w: cannot inspect RGW service: %w", ErrPlacementOperationFailed, activeErr)
	}
	wasActive := activeErr == nil
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
	changed := readErr != nil || rgwFrontendLine(current) != rgwFrontendLine(desired)
	materialChanged := spec.ssl && !pairOnDisk(certPath, keyPath, spec.certPEM, spec.keyPEM)
	var rb *rgwRollback
	if changed || materialChanged || pending != nil {
		rb = &rgwRollback{confPath: rgwConfPath(), prevConf: current, prevActive: wasActive}
		if pending != nil {
			rb.prevConf = []byte(*pending.PreviousConfig)
			rb.prevActive = *pending.PreviousActive
		}
		previousCert, _ := rgwConfTLSPaths(rb.prevConf)
		if previousCert != "" {
			rb.prevGenDir = filepath.Dir(previousCert)
		}
		rb.publishedGenDir = genDir
		if spec.ssl {
			err = stageRGWTLSGeneration(genDir, spec.certPEM, spec.keyPEM)
		}
		if err == nil && pending == nil {
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
	radosgwConfMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot publish RGW frontend: %w", ErrPlacementOperationFailed, errors.Join(err, rb.restore()))
	}
	if !restartOnChange {
		return rb, nil
	}
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

// DisableRGW stops the gateway, drops its records, then deletes local files.
// The record is dropped first so a failure leaves files and record consistent,
// and the file cleanup tolerates leftovers so a retry converges.
func DisableRGW(ctx context.Context, s interfaces.StateInterface) error {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	err := stopRGWFunc()
	if err != nil {
		return fmt.Errorf("%w: cannot stop RGW: %w", ErrPlacementOperationFailed, err)
	}
	err = removeServiceDatabase(ctx, s, "rgw")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPlacementOperationFailed, err)
	}
	paths := constants.GetPathConst()
	var failures []error
	err = removeIgnoreMissing(filepath.Join(paths.ConfPath, "ceph.client.radosgw.gateway.keyring"))
	if err != nil {
		failures = append(failures, err)
	}
	for _, dir := range []string{
		filepath.Join(paths.DataPath, "radosgw", "ceph-radosgw.gateway"),
		rgwTLSRoot(),
	} {
		err = os.RemoveAll(dir)
		if err != nil {
			failures = append(failures, err)
		}
	}
	for _, path := range []string{
		filepath.Join(paths.SSLFilesPath, "server.crt"),
		filepath.Join(paths.SSLFilesPath, "server.key"),
	} {
		err = removeIgnoreMissing(path)
		if err != nil {
			failures = append(failures, err)
		}
	}
	radosgwConfMu.Lock()
	for _, path := range []string{rgwConfPath(), rgwPendingApplyPath()} {
		err = removeIgnoreMissing(path)
		if err != nil {
			failures = append(failures, err)
		}
	}
	radosgwConfMu.Unlock()
	if len(failures) != 0 {
		return fmt.Errorf("%w: RGW cleanup failed: %w", ErrPlacementOperationFailed, errors.Join(failures...))
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

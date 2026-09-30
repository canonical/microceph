package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestParseMonCiphers(t *testing.T) {
	jsonSample := `{
		"auth_allowed_ciphers": [
			{"name": "aes", "type": 1},
			{"name": "aes256k", "type": 2}
		],
		"auth_preferred_cipher": {
			"name": "aes256k",
			"type": 2
		},
		"auth_service_cipher": {
			"name": "aes256k",
			"type": 2
		}
	}`

	ciphers := parseMonCiphers(jsonSample)
	assert.Equal(t, []string{"aes", "aes256k"}, ciphers.AuthAllowedCiphers)
	assert.Equal(t, "aes256k", ciphers.AuthPreferredCipher)
	assert.Equal(t, "aes256k", ciphers.AuthServiceCipher)

	// String fallback format
	jsonFallback := `{
		"auth_allowed_ciphers": "aes, aes256k",
		"auth_preferred_cipher": "aes",
		"auth_service_cipher": "aes"
	}`
	ciphersFallback := parseMonCiphers(jsonFallback)
	assert.Equal(t, []string{"aes", "aes256k"}, ciphersFallback.AuthAllowedCiphers)
	assert.Equal(t, "aes", ciphersFallback.AuthPreferredCipher)
	assert.Equal(t, "aes", ciphersFallback.AuthServiceCipher)
}

func TestGetMonCiphers(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	mockOutput := `{"auth_allowed_ciphers": [{"name": "aes256k"}], "auth_preferred_cipher": {"name": "aes256k"}}`
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").Return(mockOutput, nil).Once()

	ciphers, err := GetMonCiphers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"aes256k"}, ciphers.AuthAllowedCiphers)
	assert.Equal(t, "aes256k", ciphers.AuthPreferredCipher)
}

func TestSetMonCiphers(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "set", "auth_allowed_ciphers", "aes,aes256k").Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "set", "auth_preferred_cipher", "aes256k").Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "set", "auth_service_cipher", "aes256k").Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "config", "set", "mon", "mon_auth_allow_insecure_key", "false").Return("", nil).Once()

	err := SetMonAllowedCiphers(context.Background(), []string{"aes", "aes256k"})
	require.NoError(t, err)

	err = SetMonPreferredCipher(context.Background(), "aes256k")
	require.NoError(t, err)

	err = SetMonServiceCipher(context.Background(), "aes256k")
	require.NoError(t, err)

	err = SetMonAllowInsecureKey(context.Background(), false)
	require.NoError(t, err)
}

func TestRotateEntityKey(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "rotate", "client.admin", "--key-type", "aes256k").
		Return("[client.admin]\n\tkey = AQB...==\n", nil).Once()

	keyring, err := RotateEntityKey(context.Background(), "client.admin", "aes256k")
	require.NoError(t, err)
	assert.Contains(t, keyring, "client.admin")
	assert.Contains(t, keyring, "key = AQB")

	// Without key-type
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "rotate", "osd.0").
		Return("[osd.0]\n\tkey = AQB...==\n", nil).Once()

	keyringOsd, err := RotateEntityKey(context.Background(), "osd.0", "")
	require.NoError(t, err)
	assert.Contains(t, keyringOsd, "osd.0")
}

func TestRotateEntityKeyToFile(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "ceph.keyring")

	origRotate := rotateEntityKeyFunc
	defer func() { rotateEntityKeyFunc = origRotate }()

	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		return "[client.admin]\n\tkey = TESTKEY==\n", nil
	}

	err := RotateEntityKeyToFile(context.Background(), "client.admin", "aes256k", destPath, 0600)
	require.NoError(t, err)

	content, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, "[client.admin]\n\tkey = TESTKEY==\n", string(content))
}

func TestPendingKeyOperations(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create-pending", "client.rgw", "-f", "json").
		Return(`{"auth": {"client.rgw": {"key": "ACTIVE", "pending_key": "PENDING"}}}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "commit-pending", "client.rgw").
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "clear-pending", "client.rgw").
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "wipe-rotating-service-keys").
		Return("wiped rotating service keys!", nil).Once()

	pendingKey, err := GetOrCreatePendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)
	assert.Equal(t, "PENDING", pendingKey)

	err = CommitPendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)

	err = ClearPendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)

	err = WipeRotatingServiceKeys(context.Background())
	require.NoError(t, err)
}

func TestGetOrCreatePendingKey(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	// 1. KeyRing::encode_formatted wraps the entity section; the pending key sits
	//    beside the active key.
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create-pending", "client.rgw", "-f", "json").
		Return(`{"auth": {"client.rgw": {"key": "AQOLD==", "pending_key": "AQNEW=="}}}`, nil).Once()

	pendingKey, err := GetOrCreatePendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)
	assert.Equal(t, "AQNEW==", pendingKey)

	// 2. A flat entity section is tolerated too.
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create-pending", "client.flat", "-f", "json").
		Return(`{"key": "AQOLD==", "pending_key": "AQNEW=="}`, nil).Once()

	pendingKey, err = GetOrCreatePendingKey(context.Background(), "client.flat")
	require.NoError(t, err)
	assert.Equal(t, "AQNEW==", pendingKey)

	// 3. A missing pending_key is an error that must not leak the secrets from
	//    the output.
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create-pending", "client.missing", "-f", "json").
		Return(`{"key": "AQOLD=="}`, nil).Once()

	_, err = GetOrCreatePendingKey(context.Background(), "client.missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pending_key")
	assert.NotContains(t, err.Error(), "AQOLD==")
}

func TestParseAuthDumpKeys(t *testing.T) {
	jsonSample := `{
		"data": {
			"version": 15,
			"secrets": [
				{
					"entity": {"type": 2, "type_str": "mds", "id": "a"},
					"auth": {
						"key": {"type": 1, "type_str": "aes", "created": "2025-07-29T21:53:30.978646-0400"},
						"pending_key": {"type": 0, "type_str": "none", "created": "0.000000"}
					}
				},
				{
					"entity": {"type": 1, "type_str": "mon", "id": ""},
					"auth": {
						"key": {"type": 2, "type_str": "aes256k", "created": "2025-07-29T21:53:30.978646-0400"},
						"pending_key": {"type": 0, "type_str": "none", "created": "0.000000"}
					}
				},
				{
					"entity": {"type": 8, "type_str": "client", "id": "admin"},
					"auth": {
						"key": {"type": 2, "type_str": "aes256k", "created": "2025-07-29T21:53:30.978646-0400"},
						"pending_key": {"type": 2, "type_str": "aes256k", "created": "2025-07-29T22:00:00.000000-0400"}
					}
				}
			]
		}
	}`

	entries := parseAuthDumpKeys(jsonSample)
	require.Len(t, entries, 3)

	assert.Equal(t, "mds.a", entries[0].EntityName)
	assert.Equal(t, "aes", entries[0].KeyType)
	assert.Equal(t, "none", entries[0].PendingKeyType)

	assert.Equal(t, "mon.", entries[1].EntityName)
	assert.Equal(t, "aes256k", entries[1].KeyType)

	assert.Equal(t, "client.admin", entries[2].EntityName)
	assert.Equal(t, "aes256k", entries[2].KeyType)
	assert.Equal(t, "aes256k", entries[2].PendingKeyType)
}

func TestParseAuthHealthWarnings(t *testing.T) {
	jsonSample := `{
		"status": "HEALTH_WARN",
		"checks": {
			"AUTH_INSECURE_CLIENT_KEY_TYPE": {
				"severity": "HEALTH_WARN",
				"detail": [
					{"message": "entity client.admin using insecure key type: aes"},
					{"message": "entity client.fs using insecure key type: aes"}
				]
			},
			"AUTH_INSECURE_SERVICE_KEY_TYPE": {
				"severity": "HEALTH_ERR",
				"detail": [
					{"message": "entity osd.0 using insecure key type: aes"}
				]
			},
			"AUTH_INSECURE_SERVICE_TICKETS": {
				"severity": "HEALTH_ERR"
			},
			"AUTH_INSECURE_KEYS_ALLOWED": {
				"severity": "HEALTH_WARN"
			}
		}
	}`

	hw := parseAuthHealthWarnings(jsonSample)
	assert.True(t, hw.InsecureClientKeyType)
	assert.True(t, hw.InsecureServiceKeyType)
	assert.True(t, hw.InsecureServiceTickets)
	assert.True(t, hw.InsecureKeysAllowed)
	assert.False(t, hw.InsecureKeysCreatable)
	assert.False(t, hw.InsecureRotatingKeyType)

	assert.Len(t, hw.InsecureClientDetails, 2)
	assert.Contains(t, hw.InsecureClientDetails[0], "client.admin")
	assert.Len(t, hw.InsecureServiceDetails, 1)
	assert.Contains(t, hw.InsecureServiceDetails[0], "osd.0")
}

func TestParseClientSessionsAndCompatibility(t *testing.T) {
	jsonSample := `[
		{
			"name": "client.admin",
			"entity_name": "client.admin",
			"con_features_release": "squid",
			"con_features": 4611686018427387903,
			"remote_host": "node1",
			"open": true
		},
		{
			"name": "client.legacy",
			"entity_name": "client.legacy",
			"con_features_release": "octopus",
			"con_features": 1152921504606846975,
			"remote_host": "node2",
			"open": true
		}
	]`

	sessions := parseClientSessions(jsonSample)
	require.Len(t, sessions, 2)

	assert.Equal(t, "client.admin", sessions[0].EntityName)
	assert.Equal(t, "squid", sessions[0].ConFeaturesRelease)
	assert.True(t, ClientSupportsKeyType(sessions[0], "aes256k"))

	assert.Equal(t, "client.legacy", sessions[1].EntityName)
	assert.Equal(t, "octopus", sessions[1].ConFeaturesRelease)
	assert.False(t, ClientSupportsKeyType(sessions[1], "aes256k"))

	// aes is supported by both
	assert.True(t, ClientSupportsKeyType(sessions[0], "aes"))
	assert.True(t, ClientSupportsKeyType(sessions[1], "aes"))
}

func TestResolveTargetKeyType(t *testing.T) {
	// Explicit requested type
	resolved, err := ResolveTargetKeyType(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Equal(t, "aes256k", resolved)

	// Fallback to cluster preferred cipher
	origGetMonCiphers := getMonCiphersFunc
	defer func() { getMonCiphersFunc = origGetMonCiphers }()

	getMonCiphersFunc = func(ctx context.Context) (MonCiphers, error) {
		return MonCiphers{AuthPreferredCipher: "aes"}, nil
	}
	resolved, err = ResolveTargetKeyType(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "aes", resolved)

	// Default fallback if preferred cipher is empty
	getMonCiphersFunc = func(ctx context.Context) (MonCiphers, error) {
		return MonCiphers{}, nil
	}
	resolved, err = ResolveTargetKeyType(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "aes256k", resolved)
}

func TestCheckAuthRotationReadiness(t *testing.T) {
	origCipherCompat := checkCipherCompatibilityFunc
	origMonQuorum := checkMonQuorumReadyFunc
	origMembersReachable := checkClusterMembersReachableFunc
	defer func() {
		checkCipherCompatibilityFunc = origCipherCompat
		checkMonQuorumReadyFunc = origMonQuorum
		checkClusterMembersReachableFunc = origMembersReachable
	}()

	// 1. Success case
	checkCipherCompatibilityFunc = func(ctx context.Context, targetKeyType string) error { return nil }
	checkMonQuorumReadyFunc = func(ctx context.Context) error { return nil }
	checkClusterMembersReachableFunc = func(ctx context.Context, s interfaces.StateInterface) error { return nil }

	err := CheckAuthRotationReadiness(context.Background(), nil, "aes256k")
	require.NoError(t, err)

	// 2. Incompatible cipher failure
	checkCipherCompatibilityFunc = func(ctx context.Context, targetKeyType string) error {
		return fmt.Errorf("incompatible")
	}
	err = CheckAuthRotationReadiness(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cipher compatibility check failed")

	// 3. Monitor quorum failure
	checkCipherCompatibilityFunc = func(ctx context.Context, targetKeyType string) error { return nil }
	checkMonQuorumReadyFunc = func(ctx context.Context) error {
		return fmt.Errorf("mon.b is out of quorum")
	}
	err = CheckAuthRotationReadiness(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor quorum check failed")

	// 4. Cluster member reachability failure
	checkMonQuorumReadyFunc = func(ctx context.Context) error { return nil }
	checkClusterMembersReachableFunc = func(ctx context.Context, s interfaces.StateInterface) error {
		return fmt.Errorf("node-b unreachable")
	}
	err = CheckAuthRotationReadiness(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster member reachability check failed")
}

func TestCheckCipherCompatibilityAndQuorum(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	// Unsupported key type
	err := checkCipherCompatibility(context.Background(), "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported key type")

	// aes is always supported
	err = checkCipherCompatibility(context.Background(), "aes")
	require.NoError(t, err)

	// aes256k with modern release (e.g. 19 for Squid)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"min_mon_release": 19}`, nil).Once()
	err = checkCipherCompatibility(context.Background(), "aes256k")
	require.NoError(t, err)

	// aes256k with ancient release (< 17)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"min_mon_release": 16}`, nil).Once()
	err = checkCipherCompatibility(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "older than Squid")

	// Mon quorum check: mon.b is out of quorum
	// mon dump
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons": [{"name": "mon.a"}, {"name": "mon.b"}]}`, nil).Once()
	// mon stat
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum": [{"name": "mon.a"}]}`, nil).Once()
	err = checkMonQuorumReady(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of quorum")

	// Mon quorum check: all in quorum
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons": [{"name": "mon.a"}, {"name": "mon.b"}]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum": [{"name": "mon.a"}, {"name": "mon.b"}]}`, nil).Once()
	err = checkMonQuorumReady(context.Background())
	require.NoError(t, err)
}

func TestPrepareAuthRotation(t *testing.T) {
	origGetMonCiphers := getMonCiphersFunc
	origSetAllowed := setMonAllowedCiphersFunc
	origSetPreferred := setMonPreferredCipherFunc
	defer func() {
		getMonCiphersFunc = origGetMonCiphers
		setMonAllowedCiphersFunc = origSetAllowed
		setMonPreferredCipherFunc = origSetPreferred
	}()

	// 1. Initial migration: aes -> aes256k
	allowedCalls := 0
	preferredCalls := 0

	stateCiphers := MonCiphers{
		AuthAllowedCiphers:  []string{"aes"},
		AuthPreferredCipher: "aes",
		AuthServiceCipher:   "aes",
	}

	getMonCiphersFunc = func(ctx context.Context) (MonCiphers, error) {
		return stateCiphers, nil
	}
	setMonAllowedCiphersFunc = func(ctx context.Context, ciphers []string) error {
		allowedCalls++
		stateCiphers.AuthAllowedCiphers = ciphers
		return nil
	}
	setMonPreferredCipherFunc = func(ctx context.Context, cipher string) error {
		preferredCalls++
		stateCiphers.AuthPreferredCipher = cipher
		return nil
	}

	err := PrepareAuthRotation(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, allowedCalls)
	assert.Equal(t, 1, preferredCalls)
	assert.Equal(t, []string{"aes", "aes256k"}, stateCiphers.AuthAllowedCiphers)
	assert.Equal(t, "aes256k", stateCiphers.AuthPreferredCipher)

	// 2. Routine same-type renewal: already has aes256k
	err = PrepareAuthRotation(context.Background(), "aes256k")
	require.NoError(t, err)
	// Should not have made any more calls
	assert.Equal(t, 1, allowedCalls)
	assert.Equal(t, 1, preferredCalls)
}

func TestRotateMonKeyAuth(t *testing.T) {
	origRotate := rotateEntityKeyFunc
	defer func() { rotateEntityKeyFunc = origRotate }()

	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		assert.Equal(t, "mon.", entity)
		assert.Equal(t, "aes256k", keyType)
		return "[mon.]\n\tkey = AQB...==\n", nil
	}

	keyring, err := RotateMonKeyAuth(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Contains(t, keyring, "key = AQB")

	// Empty output is an error.
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		return "", nil
	}
	_, err = RotateMonKeyAuth(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestDeployMonKeyringAndRestart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)

	origRestart := snapRestartFunc
	origQuorumWait := waitForMonQuorumFunc
	defer func() {
		snapRestartFunc = origRestart
		waitForMonQuorumFunc = origQuorumWait
	}()

	restarted := false
	quorumWaited := false
	snapRestartFunc = func(service string, isReload bool) error {
		assert.Equal(t, "mon", service)
		restarted = true
		return nil
	}
	waitForMonQuorumFunc = func(ctx context.Context, monName string, timeout time.Duration) error {
		assert.Equal(t, "node-a", monName)
		quorumWaited = true
		return nil
	}

	// 1. Member runs a mon: keyring written, restarted, quorum verified.
	monDataDir := filepath.Join(tmpDir, "data", "mon", "ceph-node-a")
	require.NoError(t, os.MkdirAll(monDataDir, 0700))

	restartedFlag, err := deployMonKeyringAndRestart(context.Background(), "node-a", "[mon.]\n\tkey = MONKEY==\n")
	require.NoError(t, err)
	assert.True(t, restartedFlag)
	assert.True(t, restarted)
	assert.True(t, quorumWaited)

	content, err := os.ReadFile(filepath.Join(monDataDir, "keyring"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "MONKEY")

	// 2. Member runs no mon: no restart, no error.
	restarted = false
	quorumWaited = false
	restartedFlag, err = deployMonKeyringAndRestart(context.Background(), "node-b", "[mon.]\n\tkey = MONKEY==\n")
	require.NoError(t, err)
	assert.False(t, restartedFlag)
	assert.False(t, restarted)
	assert.False(t, quorumWaited)
}

func TestRotateLocalMGRKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)

	origRotate := rotateEntityKeyFunc
	origStop := snapStopFunc
	origStart := snapStartFunc
	origMGRWait := waitForMGRReadyFunc
	defer func() {
		rotateEntityKeyFunc = origRotate
		snapStopFunc = origStop
		snapStartFunc = origStart
		waitForMGRReadyFunc = origMGRWait
	}()

	stopped := false
	started := false
	verified := false

	snapStopFunc = func(service string, disable bool) error {
		if service == "mgr" {
			stopped = true
		}
		return nil
	}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		assert.Equal(t, "mgr.node-a", entity)
		assert.True(t, stopped)
		return "[mgr.node-a]\n\tkey = MGRKEY==\n", nil
	}
	snapStartFunc = func(service string, enable bool) error {
		if service == "mgr" {
			started = true
		}
		return nil
	}
	waitForMGRReadyFunc = func(ctx context.Context, mgrName string, timeout time.Duration) error {
		assert.Equal(t, "node-a", mgrName)
		verified = true
		return nil
	}

	err := RotateLocalMGRKey(context.Background(), "aes256k", "node-a")
	require.NoError(t, err)
	assert.True(t, stopped)
	assert.True(t, started)
	assert.True(t, verified)

	content, err := os.ReadFile(filepath.Join(tmpDir, "data", "mgr", "ceph-node-a", "keyring"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "MGRKEY")

	// Failure case: rotation error restarts the daemon best-effort and still returns the error.
	startCalls := 0
	started = false
	snapStartFunc = func(service string, enable bool) error {
		if service == "mgr" {
			startCalls++
			started = true
		}
		return nil
	}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		return "", fmt.Errorf("auth rotate failed")
	}
	err = RotateLocalMGRKey(context.Background(), "aes256k", "node-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rotate mgr.node-a key")
	assert.True(t, started, "daemon must be restarted on failure")
	assert.Equal(t, 1, startCalls)

	// Failure case: restart itself fails is logged, not masked.
	startCalls = 0
	snapStartFunc = func(service string, enable bool) error {
		startCalls++
		return fmt.Errorf("snapctl start failed")
	}
	err = RotateLocalMGRKey(context.Background(), "aes256k", "node-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rotate mgr.node-a key")
	assert.Equal(t, 1, startCalls)
}

func TestRotateLocalMDSKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)

	origRotate := rotateEntityKeyFunc
	origStop := snapStopFunc
	origStart := snapStartFunc
	origMDSWait := waitForMDSReadyFunc
	defer func() {
		rotateEntityKeyFunc = origRotate
		snapStopFunc = origStop
		snapStartFunc = origStart
		waitForMDSReadyFunc = origMDSWait
	}()

	stopped := false
	started := false
	verified := false

	snapStopFunc = func(service string, disable bool) error {
		if service == "mds" {
			stopped = true
		}
		return nil
	}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		assert.Equal(t, "mds.node-a", entity)
		assert.True(t, stopped)
		return "[mds.node-a]\n\tkey = MDSKEY==\n", nil
	}
	snapStartFunc = func(service string, enable bool) error {
		if service == "mds" {
			started = true
		}
		return nil
	}
	waitForMDSReadyFunc = func(ctx context.Context, mdsName string, timeout time.Duration) error {
		assert.Equal(t, "node-a", mdsName)
		verified = true
		return nil
	}

	err := RotateLocalMDSKey(context.Background(), "aes256k", "node-a")
	require.NoError(t, err)
	assert.True(t, stopped)
	assert.True(t, started)
	assert.True(t, verified)

	// Failure case: rotation error restarts the daemon best-effort and still returns the error.
	startCalls := 0
	started = false
	snapStartFunc = func(service string, enable bool) error {
		if service == "mds" {
			startCalls++
			started = true
		}
		return nil
	}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		return "", fmt.Errorf("auth rotate failed")
	}
	err = RotateLocalMDSKey(context.Background(), "aes256k", "node-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rotate mds.node-a key")
	assert.True(t, started, "daemon must be restarted on failure")
	assert.Equal(t, 1, startCalls)
}

func TestGetLocalOSDIDs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)

	osdRoot := filepath.Join(tmpDir, "data", "osd")
	for _, dir := range []string{"ceph-0", "ceph-2", "ceph-10"} {
		require.NoError(t, os.MkdirAll(filepath.Join(osdRoot, dir, "ready"), 0700))
	}
	// No ready marker: the osd service never spawns it.
	require.NoError(t, os.MkdirAll(filepath.Join(osdRoot, "ceph-3"), 0700))
	// Not an OSD dir.
	require.NoError(t, os.MkdirAll(filepath.Join(osdRoot, "junk"), 0700))

	ids, err := getLocalOSDIDs()
	require.NoError(t, err)
	assert.Equal(t, []int64{0, 2, 10}, ids)

	// No osd root at all: empty, no error.
	t.Setenv("SNAP_COMMON", filepath.Join(tmpDir, "other"))
	ids, err = getLocalOSDIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestRotateLocalOSDKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)

	r := mocks.NewRunner(t)
	common.ProcessExec = r

	origRotate := rotateEntityKeyFunc
	origStop := snapStopFunc
	origStart := snapStartFunc
	origOSDWait := waitForOSDUpFunc
	defer func() {
		rotateEntityKeyFunc = origRotate
		snapStopFunc = origStop
		snapStartFunc = origStart
		waitForOSDUpFunc = origOSDWait
	}()

	// One osd down call covering both OSDs.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "down", "0", "2").Return("", nil).Once()
	// The failure-case retry below marks only osd.0 down.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "down", "0").Return("", nil).Once()

	stopCount := 0
	startCount := 0
	rotatedEntities := []string{}
	verifiedOSDs := []int64{}

	snapStopFunc = func(service string, disable bool) error {
		assert.Equal(t, "osd", service)
		stopCount++
		return nil
	}
	snapStartFunc = func(service string, enable bool) error {
		assert.Equal(t, "osd", service)
		startCount++
		return nil
	}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		rotatedEntities = append(rotatedEntities, entity)
		return fmt.Sprintf("[%s]\n\tkey = OSDKEY==\n", entity), nil
	}
	waitForOSDUpFunc = func(ctx context.Context, osdID int64, timeout time.Duration) error {
		verifiedOSDs = append(verifiedOSDs, osdID)
		return nil
	}

	err := RotateLocalOSDKeys(context.Background(), "aes256k", []int64{0, 2})
	require.NoError(t, err)

	// Single stop/start for the whole batch.
	assert.Equal(t, 1, stopCount)
	assert.Equal(t, 1, startCount)
	assert.Equal(t, []string{"osd.0", "osd.2"}, rotatedEntities)
	assert.Equal(t, []int64{0, 2}, verifiedOSDs)

	// Keyrings written on this member for both OSDs.
	for _, id := range []int64{0, 2} {
		content, err := os.ReadFile(filepath.Join(tmpDir, "data", "osd", fmt.Sprintf("ceph-%d", id), "keyring"))
		require.NoError(t, err)
		assert.Contains(t, string(content), "OSDKEY")
	}

	// Failure case: rotate failure after stop propagates, and the service is
	// restarted best-effort so the OSDs are not left stopped.
	stopCount = 0
	startCount = 0
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		return "", fmt.Errorf("auth rotate failed")
	}
	err = RotateLocalOSDKeys(context.Background(), "aes256k", []int64{0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rotate osd.0 key")
	assert.Equal(t, 1, stopCount)
	assert.Equal(t, 1, startCount, "osd service must be restarted on failure")
}

func TestRotateMemberDaemons(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)

	origDeploy := deployMonKeyringAndRestartFunc
	origMGR := rotateLocalMGRKeyFunc
	origOSD := rotateLocalOSDKeysFunc
	origMDS := rotateLocalMDSKeyFunc
	defer func() {
		deployMonKeyringAndRestartFunc = origDeploy
		rotateLocalMGRKeyFunc = origMGR
		rotateLocalOSDKeysFunc = origOSD
		rotateLocalMDSKeyFunc = origMDS
	}()

	// Local layout: mon + mgr + one OSD, no mds.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "data", "mon", "ceph-node-a"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "data", "mgr", "ceph-node-a"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "data", "osd", "ceph-0", "ready"), 0700))

	state := interfaces.CephState{State: &mocks.MockState{ClusterName: "node-a"}}

	deployCalled := false
	mgrRotated := false
	mdsRotated := false
	osdIDsSeen := []int64{}

	deployMonKeyringAndRestartFunc = func(ctx context.Context, hostname string, monKeyring string) (bool, error) {
		deployCalled = true
		assert.Equal(t, "node-a", hostname)
		assert.Equal(t, "[mon.]\n\tkey = MONKEY==\n", monKeyring)
		return true, nil
	}
	rotateLocalMGRKeyFunc = func(ctx context.Context, keyType string, hostname string) error {
		mgrRotated = true
		assert.Equal(t, "aes256k", keyType)
		return nil
	}
	rotateLocalOSDKeysFunc = func(ctx context.Context, keyType string, osdIDs []int64) error {
		osdIDsSeen = osdIDs
		return nil
	}
	rotateLocalMDSKeyFunc = func(ctx context.Context, keyType string, hostname string) error {
		mdsRotated = true
		return nil
	}

	summary, err := RotateMemberDaemons(context.Background(), state, "aes256k", "[mon.]\n\tkey = MONKEY==\n")
	require.NoError(t, err)
	assert.True(t, deployCalled)
	assert.True(t, summary.MonRestarted)
	assert.True(t, mgrRotated)
	assert.Equal(t, []int64{0}, osdIDsSeen)
	assert.Equal(t, summary.RotatedOSDs, []int64{0})
	assert.False(t, mdsRotated)
	assert.Empty(t, summary.RotatedMDSs)
	assert.Equal(t, "node-a", summary.Hostname)

	// Failure in the local mgr phase propagates.
	rotateLocalMGRKeyFunc = func(ctx context.Context, keyType string, hostname string) error {
		return fmt.Errorf("mgr start failed")
	}
	_, err = RotateMemberDaemons(context.Background(), state, "aes256k", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rotate local mgr key")

	// Empty monKeyring skips the mon phase entirely.
	deployCalled = false
	rotateLocalMGRKeyFunc = func(ctx context.Context, keyType string, hostname string) error {
		return nil
	}
	_, err = RotateMemberDaemons(context.Background(), state, "aes256k", "")
	require.NoError(t, err)
	assert.False(t, deployCalled)
}

func TestRotateDaemonsCoordinator(t *testing.T) {
	origRotateMon := rotateMonKeyAuthFunc
	origSend := sendMemberAuthRotateFunc
	origMember := rotateMemberDaemonsFunc
	origGetKeyring := getEntityKeyringFunc
	defer func() {
		rotateMonKeyAuthFunc = origRotateMon
		sendMemberAuthRotateFunc = origSend
		rotateMemberDaemonsFunc = origMember
		getEntityKeyringFunc = origGetKeyring
	}()

	state := interfaces.CephState{State: &mocks.MockState{ClusterName: "node-a"}}

	monKeyringRotated := false
	monKeyringFetched := false
	fanOutRequests := []types.MemberAuthRotateRequest{}
	memberCalls := []string{}

	rotateMonKeyAuthFunc = func(ctx context.Context, keyType string) (string, error) {
		monKeyringRotated = true
		assert.Equal(t, "aes256k", keyType)
		return "[mon.]\n\tkey = MONKEY==\n", nil
	}
	getEntityKeyringFunc = func(ctx context.Context, entity string) (string, error) {
		monKeyringFetched = true
		assert.Equal(t, "mon.", entity)
		return "[mon.]\n\tkey = MONKEY==\n", nil
	}
	sendMemberAuthRotateFunc = func(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) ([]string, error) {
		fanOutRequests = append(fanOutRequests, req)
		return []string{"node-b"}, nil
	}
	rotateMemberDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, keyType string, monKeyring string) (*MemberRotationSummary, error) {
		memberCalls = append(memberCalls, monKeyring)
		return &MemberRotationSummary{Hostname: "node-a"}, nil
	}

	// 1. Success: mon. rotated exactly once, fan-out carries the shared keyring
	//    and the skip list, local member handler receives the same keyring, and
	//    progress records both members.
	progress := &AuthRotationProgress{}
	err := RotateDaemons(context.Background(), state, "aes256k", progress)
	require.NoError(t, err)
	assert.True(t, monKeyringRotated)
	assert.False(t, monKeyringFetched)
	require.Len(t, fanOutRequests, 1)
	assert.Equal(t, "aes256k", fanOutRequests[0].KeyType)
	assert.Equal(t, "[mon.]\n\tkey = MONKEY==\n", fanOutRequests[0].MonKeyring)
	assert.Empty(t, fanOutRequests[0].Skip)
	assert.Equal(t, []string{"[mon.]\n\tkey = MONKEY==\n"}, memberCalls)
	assert.True(t, progress.MonRotated)
	assert.Equal(t, []string{"node-b", "node-a"}, progress.RotatedMembers)

	// 2. Mon rotation failure aborts before any fan-out.
	monKeyringRotated = false
	fanOutRequests = nil
	rotateMonKeyAuthFunc = func(ctx context.Context, keyType string) (string, error) {
		return "", fmt.Errorf("mon rotate failed")
	}
	err = RotateDaemons(context.Background(), state, "aes256k", &AuthRotationProgress{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor key rotation failed")
	assert.Empty(t, fanOutRequests)

	// 3. Remote member failure aborts before local rotation, but the members
	//    that completed before the failure are recorded in progress.
	rotateMonKeyAuthFunc = func(ctx context.Context, keyType string) (string, error) {
		return "[mon.]\n\tkey = MONKEY==\n", nil
	}
	sendMemberAuthRotateFunc = func(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) ([]string, error) {
		return []string{"node-b"}, fmt.Errorf("member unreachable")
	}
	memberCalls = nil
	progress = &AuthRotationProgress{}
	err = RotateDaemons(context.Background(), state, "aes256k", progress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote member daemon rotation failed")
	assert.Empty(t, memberCalls)
	assert.Equal(t, []string{"node-b"}, progress.RotatedMembers)

	// 4. Resume: the mon. key is fetched instead of rotated, already-rotated
	//    members are skipped via the request, and the coordinator's own
	//    rotation is skipped because its name is recorded.
	monKeyringRotated = false
	fanOutRequests = nil
	memberCalls = nil
	sendMemberAuthRotateFunc = func(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) ([]string, error) {
		fanOutRequests = append(fanOutRequests, req)
		return []string{}, nil
	}
	progress = &AuthRotationProgress{MonRotated: true, RotatedMembers: []string{"node-b", "node-a"}}
	err = RotateDaemons(context.Background(), state, "aes256k", progress)
	require.NoError(t, err)
	assert.False(t, monKeyringRotated)
	assert.True(t, monKeyringFetched)
	require.Len(t, fanOutRequests, 1)
	assert.Equal(t, []string{"node-b", "node-a"}, fanOutRequests[0].Skip)
	assert.Empty(t, memberCalls, "coordinator's own rotation must be skipped on resume")
}

func TestActivateAndCheck(t *testing.T) {
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		getAuthHealthWarningsFunc = origHealth
	}()

	// 1. Warnings cleared: the stage passes.
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: false}, nil
	}
	err := ActivateAndCheck(context.Background())
	require.NoError(t, err)

	// 2. AUTH_INSECURE_SERVICE_KEY_TYPE remaining active fails the stage.
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: true, InsecureServiceDetails: []string{"entity osd.1 using insecure key type: aes"}}, nil
	}
	err = ActivateAndCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_INSECURE_SERVICE_KEY_TYPE remains active")
}

func TestSwitchServiceAuthentication(t *testing.T) {
	origGetCiphers := getMonCiphersFunc
	origSetService := setMonServiceCipherFunc
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		getMonCiphersFunc = origGetCiphers
		setMonServiceCipherFunc = origSetService
		getAuthHealthWarningsFunc = origHealth
	}()

	currentCiphers := MonCiphers{
		AuthServiceCipher: "aes",
	}
	setCalls := 0

	getMonCiphersFunc = func(ctx context.Context) (MonCiphers, error) {
		return currentCiphers, nil
	}
	setMonServiceCipherFunc = func(ctx context.Context, cipher string) error {
		setCalls++
		currentCiphers.AuthServiceCipher = cipher
		return nil
	}
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceTickets: false}, nil
	}

	// 1. Success case: switches to aes256k
	err := SwitchServiceAuthentication(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, setCalls)
	assert.Equal(t, "aes256k", currentCiphers.AuthServiceCipher)

	// 2. Already satisfied: doesn't set again
	err = SwitchServiceAuthentication(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, setCalls)

	// 3. Health check warning remains active
	currentCiphers.AuthServiceCipher = "aes"
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceTickets: true}, nil
	}
	err = SwitchServiceAuthentication(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_INSECURE_SERVICE_TICKETS warning remains active")
}

func TestPreventNewInsecureKeys(t *testing.T) {
	origSetInsecure := setMonAllowInsecureKeyFunc
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		setMonAllowInsecureKeyFunc = origSetInsecure
		getAuthHealthWarningsFunc = origHealth
	}()

	setCalls := 0
	setMonAllowInsecureKeyFunc = func(ctx context.Context, allow bool) error {
		setCalls++
		assert.False(t, allow)
		return nil
	}
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureKeysCreatable: false}, nil
	}

	// 1. Success for aes256k
	err := PreventNewInsecureKeys(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, setCalls)

	// 2. Non-aes256k (e.g. aes) is a no-op
	err = PreventNewInsecureKeys(context.Background(), "aes")
	require.NoError(t, err)
	assert.Equal(t, 1, setCalls) // No additional calls

	// 3. Health check remains active
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureKeysCreatable: true}, nil
	}
	err = PreventNewInsecureKeys(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_INSECURE_KEYS_CREATABLE warning remains active")
}

func TestParseKeyringData(t *testing.T) {
	keyringSample := `
[client.admin]
	key = AQB...12345==
	caps mds = "allow *"
	caps mon = "allow *"
	caps osd = "allow *"
`
	secret, err := ParseKeyringData(keyringSample)
	require.NoError(t, err)
	assert.Equal(t, "AQB...12345==", secret)

	// No key
	_, err = ParseKeyringData("[client.none]\n\tcaps mon = \"allow *\"\n")
	require.Error(t, err)
}

func TestCreateAndDeleteAdminBackupKey(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	tmpDir := t.TempDir()
	backupName := "client.admin-backup-a1b2c3d4"
	backupPath := filepath.Join(tmpDir, backupName+".keyring")

	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", backupName,
		"mon", "allow *", "osd", "allow *", "mds", "allow *", "mgr", "allow *", "-o", backupPath).
		Run(func(args mock.Arguments) {
			// ceph writes the keyring file as a side effect of the command.
			require.NoError(t, os.WriteFile(backupPath, []byte("x"), 0644))
		}).
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "del", backupName).
		Return("", nil).Once()

	err := createAdminBackupKey(context.Background(), backupName, backupPath)
	require.NoError(t, err)

	// The durable backup keyring holds a full-caps secret: owner-only.
	info, statErr := os.Stat(backupPath)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	err = deleteAdminBackupKey(context.Background(), backupName)
	require.NoError(t, err)
}

func TestVerifyAdminAccess(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	r.On("RunCommandContext", mock.Anything, "ceph", "-n", "client.admin", "-k", "/path/to/keyring", "auth", "ls").
		Return("installed auth entries...", nil).Once()

	err := verifyAdminAccess(context.Background(), "client.admin", "/path/to/keyring")
	require.NoError(t, err)

	// Failure case
	r.On("RunCommandContext", mock.Anything, "ceph", "-n", "client.admin", "-k", "/path/to/keyring", "auth", "ls").
		Return("", fmt.Errorf("connection refused")).Once()
	err = verifyAdminAccess(context.Background(), "client.admin", "/path/to/keyring")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to verify access")
}

func TestRotateAdminKeyPipeline(t *testing.T) {
	// Sandbox the snap paths: the backup keyring is durable under the conf
	// dir, so without this the write path would land in system directories.
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)
	t.Setenv("SNAP_DATA", filepath.Join(tmpDir, "current"))

	origCreateBackup := createAdminBackupKeyFunc
	origVerify := verifyAdminAccessFunc
	origPending := getOrCreatePendingKeyFunc
	origDump := dumpAuthKeysFunc
	origClear := clearPendingKeyFunc
	origCommit := commitPendingKeyFunc
	origUpdateDB := updateAdminKeyringInDBFunc
	origUpdateFiles := updateAdminKeyringFilesFunc
	origDeleteBackup := deleteAdminBackupKeyFunc
	defer func() {
		createAdminBackupKeyFunc = origCreateBackup
		verifyAdminAccessFunc = origVerify
		getOrCreatePendingKeyFunc = origPending
		dumpAuthKeysFunc = origDump
		clearPendingKeyFunc = origClear
		commitPendingKeyFunc = origCommit
		updateAdminKeyringInDBFunc = origUpdateDB
		updateAdminKeyringFilesFunc = origUpdateFiles
		deleteAdminBackupKeyFunc = origDeleteBackup
	}()

	pendingKey := "AQBwZW5kaW5nAQIDBAUGBwgJCgsMDQ4PAA=="
	remintKey := "AQB0cmVtaW50AQIDBAUGBwgJCgsMDQ4PAA=="

	var backupNames []string
	backupDeletedName := ""
	committed := false
	dbUpdatedWith := ""
	filesUpdatedWith := ""
	clearCalls := 0
	pendingCalls := 0
	var verifyCalls []string

	dumpPendingTypes := []string{"aes256k"}
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		t := "aes256k"
		if len(dumpPendingTypes) > 0 {
			t = dumpPendingTypes[0]
			dumpPendingTypes = dumpPendingTypes[1:]
		}
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes", PendingKeyType: t},
		}, nil
	}

	setupHappy := func() {
		backupNames = nil
		backupDeletedName = ""
		committed = false
		dbUpdatedWith = ""
		filesUpdatedWith = ""
		clearCalls = 0
		pendingCalls = 0
		verifyCalls = nil
		dumpPendingTypes = []string{"aes256k"}

		createAdminBackupKeyFunc = func(ctx context.Context, backupName string, backupKeyringPath string) error {
			backupNames = append(backupNames, backupName)
			// client.admin-backup is the name the upstream procedure tells
			// operators to create by hand: never touch one of those.
			require.NotEqual(t, "client.admin-backup", backupName)
			assert.True(t, strings.HasPrefix(backupName, "client.admin-backup-"))
			require.NoError(t, os.MkdirAll(filepath.Dir(backupKeyringPath), 0700))
			return os.WriteFile(backupKeyringPath, []byte("x"), 0600)
		}
		deleteAdminBackupKeyFunc = func(ctx context.Context, backupName string) error {
			backupDeletedName = backupName
			return nil
		}
		verifyAdminAccessFunc = func(ctx context.Context, entityName string, keyringPath string) error {
			verifyCalls = append(verifyCalls, entityName)
			return nil
		}
		getOrCreatePendingKeyFunc = func(ctx context.Context, entity string) (string, error) {
			pendingCalls++
			assert.Equal(t, "client.admin", entity)
			if pendingCalls == 1 {
				return pendingKey, nil
			}
			return remintKey, nil
		}
		clearPendingKeyFunc = func(ctx context.Context, entity string) error {
			clearCalls++
			return nil
		}
		commitPendingKeyFunc = func(ctx context.Context, entity string) error {
			committed = true
			assert.Equal(t, "client.admin", entity)
			return nil
		}
		updateAdminKeyringInDBFunc = func(ctx context.Context, s interfaces.StateInterface, secretKey string) error {
			dbUpdatedWith = secretKey
			return nil
		}
		updateAdminKeyringFilesFunc = func(ctx context.Context, s interfaces.StateInterface, secretKey string) error {
			filesUpdatedWith = secretKey
			return nil
		}
	}

	// 1. Success: the pending secret is persisted to DB and files BEFORE the
	//    commit makes it active, the backup is verified first and removed
	//    only after the new key verifies.
	setupHappy()
	err := RotateAdminKey(context.Background(), nil, "aes256k")
	require.NoError(t, err)
	require.Len(t, backupNames, 1)
	assert.True(t, committed)
	assert.Equal(t, pendingKey, dbUpdatedWith, "the pending secret must be persisted before it becomes active")
	assert.Equal(t, pendingKey, filesUpdatedWith)
	assert.Equal(t, 0, clearCalls)
	assert.Equal(t, []string{backupNames[0], "client.admin"}, verifyCalls)
	assert.Equal(t, backupNames[0], backupDeletedName)

	// The durable backup keyring was cleaned up with the entity.
	backupPath := filepath.Join(tmpDir, "current", "conf", backupNames[0]+".keyring")
	_, statErr := os.Stat(backupPath)
	assert.True(t, os.IsNotExist(statErr))

	// 2. Run-unique backup names: two runs never share a credential (the mocks
	//    and the name log from case 1 stay installed).
	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.NoError(t, err)
	require.Len(t, backupNames, 2)
	assert.NotEqual(t, backupNames[0], backupNames[1])

	// 3. Backup verification failure: nothing was changed, the unusable
	//    backup is removed, and no pending key is issued.
	setupHappy()
	verifyAdminAccessFunc = func(ctx context.Context, entityName string, keyringPath string) error {
		verifyCalls = append(verifyCalls, entityName)
		if entityName == backupNames[0] {
			return fmt.Errorf("backup failed")
		}
		return nil
	}
	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin recovery credential failed verification")
	assert.Equal(t, 0, pendingCalls)
	assert.False(t, committed)
	assert.Equal(t, backupNames[0], backupDeletedName)

	// 4. Failure after the pending key exists (commit fails): the recovery
	//    credential must NOT be removed; the error points at it.
	setupHappy()
	commitPendingKeyFunc = func(ctx context.Context, entity string) error {
		return fmt.Errorf("commit failed")
	}
	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to commit pending key for client.admin")
	assert.Contains(t, err.Error(), backupNames[0])
	assert.Contains(t, err.Error(), ".keyring")
	assert.Empty(t, backupDeletedName, "the recovery credential must survive failures")

	// 5. New admin key verification failure: same recovery guarantee.
	setupHappy()
	verifyAdminAccessFunc = func(ctx context.Context, entityName string, keyringPath string) error {
		verifyCalls = append(verifyCalls, entityName)
		if entityName == "client.admin" {
			return fmt.Errorf("admin failed auth")
		}
		return nil
	}
	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new client.admin key failed verification")
	assert.Contains(t, err.Error(), backupNames[0])
	assert.Empty(t, backupDeletedName)

	// 6. Stale pending key minted on the old cipher: cleared and reminted
	//    before anything is persisted, and the reminted secret is what gets
	//    written and committed.
	setupHappy()
	dumpPendingTypes = []string{"aes", "aes256k"}
	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, clearCalls)
	assert.Equal(t, 2, pendingCalls)
	assert.Equal(t, remintKey, dbUpdatedWith)
	assert.Equal(t, remintKey, filesUpdatedWith)
	assert.True(t, committed)
}

func TestNormalizeAndManagedClient(t *testing.T) {
	assert.Equal(t, "client.admin", NormalizeClientName("admin"))
	assert.Equal(t, "client.admin", NormalizeClientName("client.admin"))
	assert.Equal(t, "client.radosgw.gateway", NormalizeClientName("radosgw.gateway"))

	assert.True(t, IsMicroCephManagedClient("admin"))
	assert.True(t, IsMicroCephManagedClient("client.admin"))
	assert.True(t, IsMicroCephManagedClient("client.radosgw.gateway"))
	assert.True(t, IsMicroCephManagedClient("client.rbd-mirror.node1"))
	assert.True(t, IsMicroCephManagedClient("client.cephfs-mirror.node1"))
	assert.True(t, IsMicroCephManagedClient("client.nfs.foo.node-a"))
	assert.True(t, IsMicroCephManagedClient("client.bootstrap-osd"))

	// Cross-site credentials: the entity exists in this cluster's auth DB, but
	// the key is held by the peer (cluster export) or the remote site's
	// cephfs-mirror (peer bootstrap). Nothing local can distribute a new key.
	assert.False(t, IsMicroCephManagedClient("client.siteb"))
	assert.False(t, IsMicroCephManagedClient("client.fsmir-vol1-rem1"))

	assert.False(t, IsMicroCephManagedClient("client.cinder"))
	assert.False(t, IsMicroCephManagedClient("client.glance"))
	assert.False(t, IsMicroCephManagedClient("client.external"))
	// The dash form is not an NFS Ganesha client of ours.
	assert.False(t, IsMicroCephManagedClient("client.nfs-ganesha"))
}

// TestManagedClientWithRemoteReplicationDeployed is the regression for the
// remote-keyring misclassification: with two-way replication, cluster export
// creates client.<remote> locally (key held by the peer) and remote import
// writes <remote>.keyring in the conf dir (the remote's key for our identity
// there). The old check classified client.<remote> as managed and, in
// getClientKeyringPaths, wrote its new key over <remote>.keyring — clobbering
// the credential used to reach the remote and retiring the key the peer holds,
// breaking replication in both directions.
func TestManagedClientWithRemoteReplicationDeployed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_DATA", filepath.Join(tmpDir, "current"))

	confDir := filepath.Join(tmpDir, "current", "conf")
	require.NoError(t, os.MkdirAll(confDir, 0744))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "siteb.keyring"), []byte("x"), 0600))

	// The export-created peer user is unmanaged even though siteb.keyring
	// exists, and the fsmir peer user is unmanaged: both are reported as
	// unmanaged credentials instead of being rotated.
	assert.False(t, IsMicroCephManagedClient("client.siteb"))
	assert.False(t, IsMicroCephManagedClient("client.fsmir-vol1-rem1"))

	paths, service := getClientKeyringPaths("client.siteb")
	assert.Empty(t, paths)
	assert.Equal(t, "", service)
}

// TestManagedClientWithNFSDeployed is the regression for the blanket ganesha-dir
// check: on a node with NFS enabled every client used to classify as managed.
func TestManagedClientWithNFSDeployed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_DATA", filepath.Join(tmpDir, "current"))

	ganeshaDir := filepath.Join(tmpDir, "current", "conf", "ganesha")
	require.NoError(t, os.MkdirAll(ganeshaDir, 0744))

	// The NFS Ganesha client of this node is managed.
	assert.True(t, IsMicroCephManagedClient("client.nfs.foo.node-a"))
	// Every other client stays unmanaged even though the ganesha dir exists:
	// with the old check these were all classified managed and, in
	// getClientKeyringPaths, written over ganesha/keyring.
	assert.False(t, IsMicroCephManagedClient("client.cinder"))
	assert.False(t, IsMicroCephManagedClient("client.external-app"))
}

func TestGetClientKeyringPaths(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)
	t.Setenv("SNAP_DATA", filepath.Join(tmpDir, "current"))

	ganeshaKeyring := filepath.Join(tmpDir, "current", "conf", "ganesha", "keyring")

	// 1. Without a local ganesha keyring, an NFS client has no distribution path
	//    and no associated local service (e.g. an entity belonging to another
	//    host must not restart this node's NFS).
	paths, service := getClientKeyringPaths("client.nfs.foo.node-a")
	assert.Empty(t, paths)
	assert.Equal(t, "", service)

	// 2. With the ganesha keyring present, the NFS client resolves to it.
	require.NoError(t, os.MkdirAll(filepath.Dir(ganeshaKeyring), 0744))
	require.NoError(t, os.WriteFile(ganeshaKeyring, []byte("x"), 0600))
	paths, service = getClientKeyringPaths("client.nfs.foo.node-a")
	assert.Equal(t, []string{ganeshaKeyring}, paths)
	assert.Equal(t, "nfs", service)

	// 3. On the same NFS node, other clients must not be written to the ganesha
	//    keyring.
	paths, service = getClientKeyringPaths("client.cinder")
	assert.Empty(t, paths)
	assert.Equal(t, "", service)

	paths, service = getClientKeyringPaths("client.bootstrap-osd")
	assert.Empty(t, paths)
	assert.Equal(t, "", service)

	// 4. A <remote>.keyring in the conf dir (remote import, two-way
	//    replication) is the remote's credential for reaching it — it must
	//    never resolve as a distribution path for client.<remote>, whose key
	//    is held by the peer.
	remoteKeyring := filepath.Join(tmpDir, "current", "conf", "siteb.keyring")
	require.NoError(t, os.WriteFile(remoteKeyring, []byte("x"), 0600))
	paths, service = getClientKeyringPaths("client.siteb")
	assert.Empty(t, paths)
	assert.Equal(t, "", service)
}

func TestInspectClientSessionBlockers(t *testing.T) {
	origSessions := getClientSessionsFunc
	defer func() { getClientSessionsFunc = origSessions }()

	getClientSessionsFunc = func(ctx context.Context) ([]ClientSessionInfo, error) {
		return []ClientSessionInfo{
			{EntityName: "client.ok", ConFeaturesRelease: "squid", Open: true, RemoteHost: "node1"},
			{EntityName: "client.legacy", ConFeaturesRelease: "octopus", Open: true, RemoteHost: "node2"},
			{EntityName: "client.closed", ConFeaturesRelease: "octopus", Open: false, RemoteHost: "node3"},
		}, nil
	}

	blockers, err := InspectClientSessionBlockers(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Len(t, blockers, 1)
	assert.Contains(t, blockers, "client.legacy")
	assert.Contains(t, blockers["client.legacy"], "incompatible client release")
}

func TestRotateSingleClientKey(t *testing.T) {
	// Sandbox the snap paths: getClientKeyringPaths derives both the data and conf
	// locations from the environment, and without them the write path would land
	// in relative directories inside the package source tree.
	tmpDir := t.TempDir()
	t.Setenv("SNAP_COMMON", tmpDir)
	t.Setenv("SNAP_DATA", filepath.Join(tmpDir, "current"))

	origInspect := inspectClientSessionBlockersFunc
	origPending := getOrCreatePendingKeyFunc
	origCommit := commitPendingKeyFunc
	origGetKeyring := getEntityKeyringFunc
	origRestart := snapRestartFunc
	origDumpKeys := dumpAuthKeysFunc
	origClear := clearPendingKeyFunc
	defer func() {
		inspectClientSessionBlockersFunc = origInspect
		getOrCreatePendingKeyFunc = origPending
		commitPendingKeyFunc = origCommit
		getEntityKeyringFunc = origGetKeyring
		snapRestartFunc = origRestart
		dumpAuthKeysFunc = origDumpKeys
		clearPendingKeyFunc = origClear
	}()

	// Valid base64 secrets, as a real keyring must carry for ceph-authtool or a
	// daemon to load it.
	oldKey := "AQB0ZXN0b2xka2V5AQIDBAUGBwgJCgsMDQ4PAA=="
	newKey := "AQB0ZXN0bmV3a2V5AQIDBAUGBwgJCgsMDQ4PAA=="
	remintKey := "AQB0ZXN0cmVtaW50AQIDBAUGBwgJCgsMDQ4PAA=="

	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}
	// The dump reports the minted pending key's cipher; cases consume the queue in
	// order and fall back to the happy-path value once it is exhausted.
	dumpPendingTypes := []string{"aes256k"}
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		t := "aes256k"
		if len(dumpPendingTypes) > 0 {
			t = dumpPendingTypes[0]
			dumpPendingTypes = dumpPendingTypes[1:]
		}
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes", PendingKeyType: t},
		}, nil
	}
	clearCalls := 0
	clearPendingKeyFunc = func(ctx context.Context, entity string) error {
		clearCalls++
		return nil
	}
	pendingCalls := 0
	getOrCreatePendingKeyFunc = func(ctx context.Context, entity string) (string, error) {
		pendingCalls++
		assert.Equal(t, "client.radosgw.gateway", entity)
		// GetOrCreatePendingKey extracts the pending secret from the JSON output;
		// the keyring rendering happens from this secret alone. The remint cases
		// below get a second, different secret on the second call.
		if pendingCalls == 1 {
			return newKey, nil
		}
		return remintKey, nil
	}
	committed := false
	commitPendingKeyFunc = func(ctx context.Context, entity string) error {
		committed = true
		assert.Equal(t, "client.radosgw.gateway", entity)
		return nil
	}
	// After the commit the pending key is the active key: 'ceph auth get' returns
	// a loadable keyring holding only the new key.
	getEntityKeyringFunc = func(ctx context.Context, entity string) (string, error) {
		assert.True(t, committed, "keyring finalization must run after the commit")
		return fmt.Sprintf("[client.radosgw.gateway]\n\tkey = %s\n\tcaps mon = \"allow rw\"\n\tcaps osd = \"allow rwx\"\n", newKey), nil
	}

	rgwKeyringPath := filepath.Join(tmpDir, "data", "radosgw", "ceph-radosgw.gateway", "keyring")
	restarts := 0
	restartTimeContent := ""
	restartTimeReadErr := error(nil)
	snapRestartFunc = func(service string, isReload bool) error {
		restarts++
		assert.Equal(t, "rgw", service)
		// The daemon loads its keyring at this moment: it must already be a
		// loadable keyring whose active key is the pending key. A file holding
		// get-or-create-pending's raw two-key output would be rejected by
		// KeyRing::decode (malformed_input on "pending key") and the daemon
		// would not come back up.
		content, err := os.ReadFile(rgwKeyringPath)
		restartTimeContent = string(content)
		restartTimeReadErr = err
		return nil
	}

	// 1. Success case
	err := RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, pendingCalls)
	assert.True(t, committed)
	assert.Equal(t, 1, restarts)
	require.NoError(t, restartTimeReadErr)

	// The keyring the daemon would have loaded at restart carries the pending
	// key as the active key, with nothing KeyRing::decode would reject.
	parsed, err := ParseKeyring(rgwKeyringPath)
	require.NoError(t, err)
	assert.Equal(t, newKey, parsed)
	assert.NotContains(t, restartTimeContent, "pending")
	assert.NotContains(t, restartTimeContent, oldKey)

	// Feed the final written files back through the keyring parser: the active
	// key must be the committed pending key, with no stale pending entry or
	// retired key left behind.
	for _, p := range []string{
		rgwKeyringPath,
		filepath.Join(tmpDir, "current", "conf", "ceph.client.radosgw.gateway.keyring"),
	} {
		parsed, err := ParseKeyring(p)
		require.NoError(t, err)
		assert.Equal(t, newKey, parsed)

		content, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.NotContains(t, string(content), "pending")
		assert.NotContains(t, string(content), oldKey)
	}

	// 2. Finalization failure surfaces: the commit has happened, so the error must
	//    not be swallowed.
	getEntityKeyringFunc = func(ctx context.Context, entity string) (string, error) {
		return "", fmt.Errorf("auth get failed")
	}
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch committed keyring")

	// 3. Distribution failure aborts before the commit: the old key stays active
	//    and the consumer keeps working on it.
	commitCalls := 0
	getOrCreatePendingKeyFunc = func(ctx context.Context, entity string) (string, error) {
		return newKey, nil
	}
	commitPendingKeyFunc = func(ctx context.Context, entity string) error {
		commitCalls++
		return nil
	}
	// Make the distribution write fail: point SNAP_COMMON at a regular file so
	// creating the keyring directory underneath it fails.
	t.Setenv("SNAP_COMMON", filepath.Join(tmpDir, "not-a-dir"))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "not-a-dir"), []byte("x"), 0400))

	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write client keyring")
	assert.Equal(t, 0, commitCalls, "commit must not run when distribution fails")

	// 4. Blocked case: no pending key is issued at all.
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return map[string]string{"client.radosgw.gateway": "session incompatible"}, nil
	}
	pendingCalls = 0
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session incompatible")
	assert.Equal(t, 0, pendingCalls)

	// 5. Stale pending key remediation: get-or-create-pending reuses an
	//    existing pending key regardless of its cipher, so a pending key
	//    minted before the cluster was prepared is cleared and reminted, and
	//    the reminted secret is what gets distributed and finalized.
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}
	t.Setenv("SNAP_COMMON", tmpDir)
	pendingCalls = 0
	clearCalls = 0
	dumpPendingTypes = []string{"aes", "aes256k"}
	committed = false
	getOrCreatePendingKeyFunc = func(ctx context.Context, entity string) (string, error) {
		pendingCalls++
		if pendingCalls == 1 {
			return newKey, nil
		}
		return remintKey, nil
	}
	commitPendingKeyFunc = func(ctx context.Context, entity string) error {
		committed = true
		return nil
	}
	getEntityKeyringFunc = func(ctx context.Context, entity string) (string, error) {
		assert.True(t, committed, "keyring finalization must run after the commit")
		return fmt.Sprintf("[client.radosgw.gateway]\n\tkey = %s\n", remintKey), nil
	}
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.NoError(t, err)
	assert.Equal(t, 1, clearCalls)
	assert.Equal(t, 2, pendingCalls)
	assert.True(t, committed)

	// The distributed and finalized keyrings carry the reminted secret.
	parsed, err = ParseKeyring(rgwKeyringPath)
	require.NoError(t, err)
	assert.Equal(t, remintKey, parsed)

	// 6. Unprepared cluster: the remint still comes back on the old cipher, so
	//    the rotation fails before the commit, leaving the old key active.
	commitCalls = 0
	commitPendingKeyFunc = func(ctx context.Context, entity string) error {
		commitCalls++
		return nil
	}
	pendingCalls = 0
	clearCalls = 0
	dumpPendingTypes = []string{"aes", "aes"}
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_preferred_cipher")
	assert.Equal(t, 1, clearCalls)
	assert.Equal(t, 2, pendingCalls)
	assert.Equal(t, 0, commitCalls, "commit must not run when the pending key cipher does not match")

	// 7. Verification failure: an auth dump error aborts before distribution.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return nil, fmt.Errorf("mon unreachable")
	}
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to verify pending key cipher")
	assert.Equal(t, 0, commitCalls, "commit must not run when verification fails")
}

func TestRotateManagedClientsPipeline(t *testing.T) {
	origDumpKeys := dumpAuthKeysFunc
	origInspect := inspectClientSessionBlockersFunc
	origRotateSingle := rotateSingleClientKeyFunc
	defer func() {
		dumpAuthKeysFunc = origDumpKeys
		inspectClientSessionBlockersFunc = origInspect
		rotateSingleClientKeyFunc = origRotateSingle
	}()

	rotated := []string{}
	rotateSingleClientKeyFunc = func(ctx context.Context, clientName string, targetKeyType string) error {
		rotated = append(rotated, clientName)
		return nil
	}

	// 1. Success case: managed clients only, no session blockers
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes256k"}, // Should be skipped (admin handled in step 8)
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.rbd-mirror.node1", EntityType: "client", KeyType: "aes"},
		}, nil
	}
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}

	res, err := RotateManagedClients(context.Background(), "aes256k", nil)
	require.NoError(t, err)
	assert.False(t, res.HasBlockers)
	assert.Len(t, res.RotatedClients, 2)
	assert.Contains(t, res.RotatedClients, "client.radosgw.gateway")
	assert.Contains(t, res.RotatedClients, "client.rbd-mirror.node1")

	// 2. Unmanaged client present on insecure cipher
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.cinder", EntityType: "client", KeyType: "aes"},
		}, nil
	}
	res, err = RotateManagedClients(context.Background(), "aes256k", nil)
	require.NoError(t, err)
	assert.True(t, res.HasBlockers)
	assert.Equal(t, []string{"client.cinder"}, res.UnmanagedClients)
	assert.Contains(t, res.BlockerMessage, "Unmanaged credentials must be rotated manually")

	// 3. Unmanaged client already has targetKeyType: not a blocker!
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.cinder", EntityType: "client", KeyType: "aes256k"},
		}, nil
	}
	res, err = RotateManagedClients(context.Background(), "aes256k", nil)
	require.NoError(t, err)
	assert.False(t, res.HasBlockers)
	assert.Empty(t, res.UnmanagedClients)

	// 4. Session blocker present on managed client
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return map[string]string{"client.radosgw.gateway": "incompatible kernel driver"}, nil
	}
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
		}, nil
	}
	res, err = RotateManagedClients(context.Background(), "aes256k", nil)
	require.NoError(t, err)
	assert.True(t, res.HasBlockers)
	assert.Contains(t, res.BlockedClients, "client.radosgw.gateway")
	assert.Contains(t, res.BlockerMessage, "incompatible")

	// 5. Resume progress: clients already rotated on a previous attempt are
	//    skipped and progress is extended as the remaining ones complete.
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.rbd-mirror.node1", EntityType: "client", KeyType: "aes"},
		}, nil
	}
	progress := &AuthRotationProgress{RotatedClients: []string{"client.radosgw.gateway"}}
	res, err = RotateManagedClients(context.Background(), "aes256k", progress)
	require.NoError(t, err)
	assert.False(t, res.HasBlockers)
	assert.Equal(t, []string{"client.rbd-mirror.node1"}, res.RotatedClients)
	assert.Equal(t, []string{"client.radosgw.gateway", "client.rbd-mirror.node1"}, progress.RotatedClients)
}

func TestDisallowInsecureLegacyCiphers(t *testing.T) {
	origGetCiphers := getMonCiphersFunc
	origSetAllowed := setMonAllowedCiphersFunc
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		getMonCiphersFunc = origGetCiphers
		setMonAllowedCiphersFunc = origSetAllowed
		getAuthHealthWarningsFunc = origHealth
	}()

	// 1. Non-aes256k: no-op
	err := DisallowInsecureLegacyCiphers(context.Background(), "aes")
	require.NoError(t, err)

	// 2. Lockout prevention failure: insecure service entities still present
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: true, InsecureServiceDetails: []string{"osd.0 on aes"}}, nil
	}
	err = DisallowInsecureLegacyCiphers(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service entities still use insecure key types")

	// 3. Lockout prevention failure: insecure client entities still present
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureClientKeyType: true, InsecureClientDetails: []string{"client.cinder on aes"}}, nil
	}
	err = DisallowInsecureLegacyCiphers(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client entities still use insecure key types")

	// 4. Lockout prevention failure: insecure service tickets
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceTickets: true}, nil
	}
	err = DisallowInsecureLegacyCiphers(context.Background(), "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service tickets still using insecure cipher")

	// 5. Success case
	healthWarningAllowed := true
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureKeysAllowed: healthWarningAllowed}, nil
	}
	currentAllowed := []string{"aes", "aes256k"}
	setMonAllowedCiphersFunc = func(ctx context.Context, ciphers []string) error {
		currentAllowed = ciphers
		healthWarningAllowed = false
		return nil
	}
	getMonCiphersFunc = func(ctx context.Context) (MonCiphers, error) {
		return MonCiphers{AuthAllowedCiphers: currentAllowed}, nil
	}

	err = DisallowInsecureLegacyCiphers(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.Equal(t, []string{"aes256k"}, currentAllowed)
}

func TestBuildAuthStatus(t *testing.T) {
	origDumpKeys := dumpAuthKeysFunc
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		dumpAuthKeysFunc = origDumpKeys
		getAuthHealthWarningsFunc = origHealth
	}()

	// 1. Mixed ciphers across daemons and clients, with active health warnings.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "mon.", EntityType: "mon", KeyType: "aes256k"},
			{EntityName: "mgr.node-a", EntityType: "mgr", KeyType: "aes"},
			{EntityName: "osd.0", EntityType: "osd", KeyType: "aes"},
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes256k"},
			{EntityName: "client.rgw", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.notype", EntityType: "client", KeyType: ""},
		}, nil
	}
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{
			InsecureRotatingKeyType: true,
			InsecureKeysAllowed:     true,
		}, nil
	}

	resp, err := BuildAuthStatus(context.Background(), nil)
	require.NoError(t, err)

	// Daemon keys are counted in their own distribution.
	assert.Equal(t, map[string][]string{
		"aes256k": {"mon."},
		"aes":     {"mgr.node-a", "osd.0"},
	}, resp.ServiceDistribution)

	// Client keys keep their own distribution; a missing key type buckets as unknown.
	assert.Equal(t, map[string][]string{
		"aes256k": {"client.admin"},
		"aes":     {"client.rgw"},
		"unknown": {"client.notype"},
	}, resp.ClientDistribution)

	// Mixed Status carries counts only; names live in the distributions.
	assert.Equal(t, "1 clients on aes, 1 clients on aes256k, 1 clients on unknown", resp.Status)

	assert.Equal(t, []string{
		"AUTH_INSECURE_ROTATING_SERVICE_KEY_TYPE",
		"AUTH_INSECURE_KEYS_ALLOWED",
	}, resp.HealthWarnings)

	// 2. Uniform clients: spec "All client <cipher>" summary.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "mon.", EntityType: "mon", KeyType: "aes256k"},
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes256k"},
			{EntityName: "client.rgw", EntityType: "client", KeyType: "aes256k"},
		}, nil
	}
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{}, nil
	}

	resp, err = BuildAuthStatus(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "All client aes256k", resp.Status)
	assert.Empty(t, resp.HealthWarnings)

	// 3. Health query failure is best effort and must not fail the status call.
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{}, fmt.Errorf("ceph down")
	}
	resp, err = BuildAuthStatus(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, resp.HealthWarnings)
}

func TestExecuteAuthRotationFullPipeline(t *testing.T) {
	origResolve := resolveTargetKeyTypeFunc
	origReadiness := checkAuthRotationReadinessFunc
	origPrepare := prepareAuthRotationFunc
	origRotateDaemons := rotateDaemonsFunc
	origActivate := activateAndCheckFunc
	origSwitchAuth := switchServiceAuthenticationFunc
	origPrevent := preventNewInsecureKeysFunc
	origRotateAdmin := rotateAdminKeyFunc
	origRotateClients := rotateManagedClientsFunc
	origFinalize := finalizeAuthRotationFunc
	origDump := dumpAuthKeysFunc
	defer func() {
		resolveTargetKeyTypeFunc = origResolve
		checkAuthRotationReadinessFunc = origReadiness
		prepareAuthRotationFunc = origPrepare
		rotateDaemonsFunc = origRotateDaemons
		activateAndCheckFunc = origActivate
		switchServiceAuthenticationFunc = origSwitchAuth
		preventNewInsecureKeysFunc = origPrevent
		rotateAdminKeyFunc = origRotateAdmin
		rotateManagedClientsFunc = origRotateClients
		finalizeAuthRotationFunc = origFinalize
		dumpAuthKeysFunc = origDump
	}()

	stages := []string{}
	// The finish stage verifies client.admin's cipher before disallowing
	// insecure ciphers; the admin key is NOT rotated by the pipeline anymore.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes256k"},
		}, nil
	}
	resolveTargetKeyTypeFunc = func(ctx context.Context, requestedType string) (string, error) {
		return "aes256k", nil
	}
	checkAuthRotationReadinessFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "readiness")
		return nil
	}
	prepareAuthRotationFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "prepare_auth")
		return nil
	}
	rotateDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string, progress *AuthRotationProgress) error {
		stages = append(stages, "rotate_daemons")
		return nil
	}
	activateAndCheckFunc = func(ctx context.Context) error {
		stages = append(stages, "activate_and_check")
		return nil
	}
	switchServiceAuthenticationFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "switch_service_auth")
		return nil
	}
	preventNewInsecureKeysFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "prevent_insecure_keys")
		return nil
	}
	rotateAdminKeyFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "protect_admin")
		return nil
	}
	finalizeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "finish_safely")
		return nil
	}

	// 1. Success case without blockers
	rotateManagedClientsFunc = func(ctx context.Context, targetKeyType string, progress *AuthRotationProgress) (ClientRotationResult, error) {
		stages = append(stages, "rotate_clients")
		return ClientRotationResult{HasBlockers: false, RotatedClients: []string{"client.rgw"}}, nil
	}

	rec, err := ExecuteAuthRotation(context.Background(), nil, "aes256k", "")
	require.NoError(t, err)
	assert.Equal(t, "completed", rec.State)
	assert.Equal(t, []string{
		"readiness", "prepare_auth", "rotate_daemons", "activate_and_check",
		"switch_service_auth", "prevent_insecure_keys", "rotate_clients",
		"finish_safely",
	}, stages)

	// 2. Blocked case (unmanaged client present)
	stages = nil
	rotateManagedClientsFunc = func(ctx context.Context, targetKeyType string, progress *AuthRotationProgress) (ClientRotationResult, error) {
		stages = append(stages, "rotate_clients")
		return ClientRotationResult{
			HasBlockers:    true,
			BlockerMessage: "Unmanaged credentials must be rotated manually before rotation can proceed",
		}, nil
	}

	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "")
	require.NoError(t, err)
	assert.Equal(t, "blocked", rec.State)
	assert.Contains(t, rec.Blocker, "Unmanaged credentials must be rotated manually")
	// Should not have reached finish_safely
	assert.NotContains(t, stages, "finish_safely")

	// 3. Single-client mode
	origRotateSingle := rotateSingleClientKeyFunc
	defer func() {
		rotateSingleClientKeyFunc = origRotateSingle
	}()

	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client"},
			{EntityName: "client.radosgw.gateway", EntityType: "client"},
			{EntityName: "mon.", EntityType: "mon"},
			{EntityName: "osd.0", EntityType: "osd"},
			{EntityName: "client.cinder", EntityType: "client"},
		}, nil
	}
	singleClientRotated := false
	rotateSingleClientKeyFunc = func(ctx context.Context, clientName string, targetKeyType string) error {
		singleClientRotated = true
		assert.Equal(t, "client.radosgw.gateway", clientName)
		return nil
	}

	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "radosgw.gateway")
	require.NoError(t, err)
	assert.True(t, singleClientRotated)
	assert.Equal(t, "completed", rec.State)
}

func TestExecuteAuthRotationSingleClientValidation(t *testing.T) {
	origResolve := resolveTargetKeyTypeFunc
	origDump := dumpAuthKeysFunc
	origRotateAdmin := rotateAdminKeyFunc
	origRotateSingle := rotateSingleClientKeyFunc
	defer func() {
		resolveTargetKeyTypeFunc = origResolve
		dumpAuthKeysFunc = origDump
		rotateAdminKeyFunc = origRotateAdmin
		rotateSingleClientKeyFunc = origRotateSingle
	}()

	resolveTargetKeyTypeFunc = func(ctx context.Context, requestedType string) (string, error) {
		return "aes256k", nil
	}
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client"},
			{EntityName: "client.radosgw.gateway", EntityType: "client"},
			{EntityName: "client.siteb", EntityType: "client"},
			{EntityName: "client.fsmir-vol1-rem1", EntityType: "client"},
			{EntityName: "mon.", EntityType: "mon"},
			{EntityName: "mgr.node1", EntityType: "mgr"},
			{EntityName: "osd.0", EntityType: "osd"},
			{EntityName: "mds.node1", EntityType: "mds"},
		}, nil
	}

	adminRotated := false
	rotateAdminKeyFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		adminRotated = true
		return nil
	}
	singleClientRotated := false
	rotateSingleClientKeyFunc = func(ctx context.Context, clientName string, targetKeyType string) error {
		singleClientRotated = true
		return nil
	}

	// 1. --client client.admin routes to the protected admin rotation, never
	//    through the generic pending-key path (which matches no admin keyring
	//    paths and would lock MicroCeph out on commit).
	rec, err := ExecuteAuthRotation(context.Background(), nil, "aes256k", "admin")
	require.NoError(t, err)
	assert.True(t, adminRotated)
	assert.False(t, singleClientRotated)
	assert.Equal(t, "completed", rec.State)
	// Single-client completions persist the finish_safely stage, matching the
	// full pipeline's terminal stage.
	assert.Equal(t, database.AuthRotationStageFinishSafely, rec.Stage)

	// 2. Unmanaged clients are refused up front: nothing is rotated and no
	//    rotation record would be persisted on the daemon path.
	adminRotated = false
	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "client.siteb")
	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "not managed by MicroCeph")
	assert.False(t, adminRotated)
	assert.False(t, singleClientRotated)

	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "client.fsmir-vol1-rem1")
	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "not managed by MicroCeph")

	// 3. Daemon entities are refused: they belong to the cluster-wide pipeline.
	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "osd.0")
	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "daemon credential")

	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "mon.")
	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "daemon credential")

	// 4. Unknown entities are refused.
	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "client.nosuch")
	assert.Error(t, err)
	assert.Nil(t, rec)
	assert.Contains(t, err.Error(), "unknown client")

	// 5. Managed clients still pass validation and reach the generic path.
	rec, err = ExecuteAuthRotation(context.Background(), nil, "aes256k", "client.radosgw.gateway")
	require.NoError(t, err)
	assert.True(t, singleClientRotated)
	assert.False(t, adminRotated)
	assert.Equal(t, "completed", rec.State)
}

func TestRunAuthRotationPipelineResume(t *testing.T) {
	origReadiness := checkAuthRotationReadinessFunc
	origPrepare := prepareAuthRotationFunc
	origRotateDaemons := rotateDaemonsFunc
	origActivate := activateAndCheckFunc
	origSwitch := switchServiceAuthenticationFunc
	origPrevent := preventNewInsecureKeysFunc
	origAdmin := rotateAdminKeyFunc
	origClients := rotateManagedClientsFunc
	origFinalize := finalizeAuthRotationFunc
	origDump := dumpAuthKeysFunc
	defer func() {
		checkAuthRotationReadinessFunc = origReadiness
		prepareAuthRotationFunc = origPrepare
		rotateDaemonsFunc = origRotateDaemons
		activateAndCheckFunc = origActivate
		switchServiceAuthenticationFunc = origSwitch
		preventNewInsecureKeysFunc = origPrevent
		rotateAdminKeyFunc = origAdmin
		rotateManagedClientsFunc = origClients
		finalizeAuthRotationFunc = origFinalize
		dumpAuthKeysFunc = origDump
	}()

	stages := []string{}
	checkAuthRotationReadinessFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "readiness")
		return nil
	}
	prepareAuthRotationFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "prepare_auth")
		return nil
	}
	rotateDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string, progress *AuthRotationProgress) error {
		stages = append(stages, "rotate_daemons")
		return nil
	}
	activateAndCheckFunc = func(ctx context.Context) error {
		stages = append(stages, "activate_and_check")
		return nil
	}
	switchServiceAuthenticationFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "switch_service_auth")
		return nil
	}
	preventNewInsecureKeysFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "prevent_insecure_keys")
		return nil
	}
	rotateAdminKeyFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "protect_admin")
		return nil
	}
	finalizeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "finish_safely")
		return nil
	}
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.rbd-mirror.node1", EntityType: "client", KeyType: "aes"},
		}, nil
	}
	rotateManagedClientsFunc = func(ctx context.Context, targetKeyType string, progress *AuthRotationProgress) (ClientRotationResult, error) {
		stages = append(stages, "rotate_clients")
		assert.NotNil(t, progress)
		assert.True(t, progress.MonRotated)
		assert.Equal(t, []string{"node-b", "node-a"}, progress.RotatedMembers)
		return ClientRotationResult{HasBlockers: false}, nil
	}

	// A record blocked/failed mid-run at switch_service_auth carries completed
	// progress; the resume must skip every stage before it and the already
	// rotated client, and must close the record as completed.
	rec := &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateInProgress,
		Stage:         database.AuthRotationStageSwitchServiceAuth,
		StepProgress:  `{"mon_rotated":true,"rotated_members":["node-b","node-a"],"rotated_clients":["client.radosgw.gateway"]}`,
	}

	rec, err := runAuthRotationPipeline(context.Background(), nil, rec, "aes256k")
	require.NoError(t, err)
	assert.Equal(t, "completed", rec.State)
	assert.Equal(t, []string{
		"switch_service_auth", "prevent_insecure_keys", "rotate_clients",
		"finish_safely",
	}, stages)
}

func TestRunAuthRotationPipelineFailedState(t *testing.T) {
	origPrepare := prepareAuthRotationFunc
	origReadiness := checkAuthRotationReadinessFunc
	origClients := rotateManagedClientsFunc
	origDump := dumpAuthKeysFunc
	origSingle := rotateSingleClientKeyFunc
	origInspect := inspectClientSessionBlockersFunc
	defer func() {
		prepareAuthRotationFunc = origPrepare
		checkAuthRotationReadinessFunc = origReadiness
		rotateManagedClientsFunc = origClients
		dumpAuthKeysFunc = origDump
		rotateSingleClientKeyFunc = origSingle
		inspectClientSessionBlockersFunc = origInspect
	}()

	// 1. Stage failure records the failed state with the error detail, and a
	//    dead run no longer looks like a live one.
	checkAuthRotationReadinessFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		return nil
	}
	prepareAuthRotationFunc = func(ctx context.Context, targetKeyType string) error {
		return fmt.Errorf("mon not ready")
	}

	rec := &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateInProgress,
		Stage:         database.AuthRotationStageReadiness,
	}
	rec, err := runAuthRotationPipeline(context.Background(), nil, rec, "aes256k")
	require.Error(t, err)
	assert.Equal(t, database.AuthRotationStateFailed, rec.State)
	assert.Contains(t, rec.Detail, "auth preparation failed")
	assert.Contains(t, rec.Detail, "mon not ready")

	// 2. A mid-stage client failure persists the entities rotated so far in
	//    the record's step progress, so a resume skips them.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.radosgw.gateway", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.rbd-mirror.node1", EntityType: "client", KeyType: "aes"},
		}, nil
	}
	rotateManagedClientsFunc = RotateManagedClients
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}
	rotateSingleClientKeyFunc = func(ctx context.Context, clientName string, targetKeyType string) error {
		if clientName == "client.rbd-mirror.node1" {
			return fmt.Errorf("keyring write failed")
		}
		return nil
	}

	rec = &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateInProgress,
		Stage:         database.AuthRotationStageRotateClients,
		StepProgress:  `{"mon_rotated":true,"rotated_members":["node-a"]}`,
	}
	rec, err = runAuthRotationPipeline(context.Background(), nil, rec, "aes256k")
	require.Error(t, err)
	assert.Equal(t, database.AuthRotationStateFailed, rec.State)
	assert.Contains(t, rec.Detail, "client key rotation failed")
	assert.Contains(t, rec.StepProgress, "client.radosgw.gateway")
	assert.NotContains(t, rec.StepProgress, "client.rbd-mirror.node1")
}

func TestAuthRotationProgressEncoding(t *testing.T) {
	// Empty payload: empty progress, nothing skipped.
	p := parseAuthRotationProgress("")
	assert.False(t, p.MonRotated)
	assert.Empty(t, p.RotatedMembers)
	assert.Equal(t, "{}", p.String())

	// Round trip.
	p = parseAuthRotationProgress(`{"mon_rotated":true,"rotated_members":["node-a"],"rotated_clients":["client.rgw"]}`)
	assert.True(t, p.MonRotated)
	assert.Equal(t, []string{"node-a"}, p.RotatedMembers)
	assert.Equal(t, []string{"client.rgw"}, p.RotatedClients)

	// Garbage payload: empty progress instead of a panic, with a warning.
	garbage := parseAuthRotationProgress("not-json{")
	assert.False(t, garbage.MonRotated)
	assert.Empty(t, garbage.RotatedMembers)
	assert.Equal(t, "{}", garbage.String())
}

func TestRunAuthRotationPipelineAdminOptIn(t *testing.T) {
	origFinalize := finalizeAuthRotationFunc
	origDump := dumpAuthKeysFunc
	defer func() {
		finalizeAuthRotationFunc = origFinalize
		dumpAuthKeysFunc = origDump
	}()

	finalized := false
	finalizeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		finalized = true
		return nil
	}

	// 1. client.admin still on the old cipher: the finish pauses as blocked
	//    with an actionable message instead of rotating the shared admin key
	//    automatically or locking out its holders by disallowing the cipher.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes"},
			{EntityName: "client.rgw", EntityType: "client", KeyType: "aes256k"},
		}, nil
	}

	rec := &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateInProgress,
		Stage:         database.AuthRotationStageFinishSafely,
	}
	rec, err := runAuthRotationPipeline(context.Background(), nil, rec, "aes256k")
	require.NoError(t, err)
	assert.Equal(t, database.AuthRotationStateBlocked, rec.State)
	assert.Contains(t, rec.Blocker, "client.admin still uses cipher")
	assert.Contains(t, rec.Blocker, "microceph auth rotate --client client.admin")
	assert.False(t, finalized, "insecure ciphers must not be disallowed while client.admin holds one")

	// 2. client.admin already rotated (explicit opt-in happened): the finish
	//    proceeds and disallows insecure ciphers.
	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "client.admin", EntityType: "client", KeyType: "aes256k"},
			{EntityName: "client.rgw", EntityType: "client", KeyType: "aes256k"},
		}, nil
	}

	rec = &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateInProgress,
		Stage:         database.AuthRotationStageFinishSafely,
	}
	rec, err = runAuthRotationPipeline(context.Background(), nil, rec, "aes256k")
	require.NoError(t, err)
	assert.Equal(t, "completed", rec.State)
	assert.True(t, finalized)
}

func TestRunSingleClientRotationAdminSubStep(t *testing.T) {
	origAdmin := rotateAdminKeyFunc
	origSingle := rotateSingleClientKeyFunc
	defer func() {
		rotateAdminKeyFunc = origAdmin
		rotateSingleClientKeyFunc = origSingle
	}()

	adminRotated := false
	rotateAdminKeyFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		adminRotated = true
		return nil
	}
	rotateSingleClientKeyFunc = func(ctx context.Context, clientName string, targetKeyType string) error {
		t.Error("admin rotation must route to the protected admin rotation")
		return fmt.Errorf("must not be called")
	}

	// 1. Dedicated run (record filter matches the request): completes.
	rec := &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateInProgress,
		Stage:         database.AuthRotationStageReadiness,
		ClientName:    "client.admin",
	}
	rec, err := runSingleClientRotation(context.Background(), nil, rec, "client.admin", "aes256k")
	require.NoError(t, err)
	assert.True(t, adminRotated)
	assert.Equal(t, database.AuthRotationStateCompleted, rec.State)

	// 2. Sub-step of an incomplete full run (record filter empty because the
	//    pipeline no longer rotates client.admin itself): the record is handed
	//    back to the full pipeline in_progress at its own stage, not completed.
	rec = &database.AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         database.AuthRotationStateBlocked,
		Stage:         database.AuthRotationStageFinishSafely,
		Blocker:       "client.admin still uses cipher",
	}
	rec, err = runSingleClientRotation(context.Background(), nil, rec, "client.admin", "aes256k")
	require.NoError(t, err)
	assert.True(t, adminRotated)
	assert.Equal(t, database.AuthRotationStateInProgress, rec.State)
	assert.Equal(t, database.AuthRotationStageFinishSafely, rec.Stage, "the full run's stage must not be clobbered")
	assert.Empty(t, rec.Blocker)
}

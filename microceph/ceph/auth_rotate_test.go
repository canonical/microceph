package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
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
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		rotateMonKeyAuthFunc = origRotateMon
		sendMemberAuthRotateFunc = origSend
		rotateMemberDaemonsFunc = origMember
		getAuthHealthWarningsFunc = origHealth
	}()

	state := interfaces.CephState{State: &mocks.MockState{ClusterName: "node-a"}}

	monKeyringRotated := false
	fanOutRequests := []types.MemberAuthRotateRequest{}
	memberCalls := []string{}

	rotateMonKeyAuthFunc = func(ctx context.Context, keyType string) (string, error) {
		monKeyringRotated = true
		assert.Equal(t, "aes256k", keyType)
		return "[mon.]\n\tkey = MONKEY==\n", nil
	}
	sendMemberAuthRotateFunc = func(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) error {
		fanOutRequests = append(fanOutRequests, req)
		return nil
	}
	rotateMemberDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, keyType string, monKeyring string) (*MemberRotationSummary, error) {
		memberCalls = append(memberCalls, monKeyring)
		return &MemberRotationSummary{Hostname: "node-a"}, nil
	}
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: false}, nil
	}

	// 1. Success: mon. rotated exactly once, fan-out carries the shared keyring,
	//    local member handler receives the same keyring.
	err := RotateDaemons(context.Background(), state, "aes256k")
	require.NoError(t, err)
	assert.True(t, monKeyringRotated)
	require.Len(t, fanOutRequests, 1)
	assert.Equal(t, "aes256k", fanOutRequests[0].KeyType)
	assert.Equal(t, "[mon.]\n\tkey = MONKEY==\n", fanOutRequests[0].MonKeyring)
	assert.Equal(t, []string{"[mon.]\n\tkey = MONKEY==\n"}, memberCalls)

	// 2. Mon rotation failure aborts before any fan-out.
	monKeyringRotated = false
	fanOutRequests = nil
	rotateMonKeyAuthFunc = func(ctx context.Context, keyType string) (string, error) {
		return "", fmt.Errorf("mon rotate failed")
	}
	err = RotateDaemons(context.Background(), state, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor key rotation failed")
	assert.Empty(t, fanOutRequests)

	// 3. Remote member failure aborts before local rotation.
	rotateMonKeyAuthFunc = func(ctx context.Context, keyType string) (string, error) {
		return "[mon.]\n\tkey = MONKEY==\n", nil
	}
	sendMemberAuthRotateFunc = func(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) error {
		return fmt.Errorf("member unreachable")
	}
	memberCalls = nil
	err = RotateDaemons(context.Background(), state, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote member daemon rotation failed")
	assert.Empty(t, memberCalls)

	// 4. AUTH_INSECURE_SERVICE_KEY_TYPE remaining active fails the stage.
	sendMemberAuthRotateFunc = func(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) error {
		return nil
	}
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: true, InsecureServiceDetails: []string{"entity osd.1 using insecure key type: aes"}}, nil
	}
	err = RotateDaemons(context.Background(), state, "aes256k")
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

func TestSwitchServiceAuthAndPreventInsecurePipeline(t *testing.T) {
	origSwitch := switchServiceAuthenticationFunc
	origPrevent := preventNewInsecureKeysFunc
	defer func() {
		switchServiceAuthenticationFunc = origSwitch
		preventNewInsecureKeysFunc = origPrevent
	}()

	switchCalled := false
	preventCalled := false

	switchServiceAuthenticationFunc = func(ctx context.Context, targetKeyType string) error {
		switchCalled = true
		assert.Equal(t, "aes256k", targetKeyType)
		return nil
	}
	preventNewInsecureKeysFunc = func(ctx context.Context, targetKeyType string) error {
		preventCalled = true
		assert.Equal(t, "aes256k", targetKeyType)
		return nil
	}

	err := SwitchServiceAuthAndPreventInsecure(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.True(t, switchCalled)
	assert.True(t, preventCalled)
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
	backupPath := filepath.Join(tmpDir, "client.admin-backup.keyring")

	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.admin-backup",
		"mon", "allow *", "osd", "allow *", "mds", "allow *", "mgr", "allow *", "-o", backupPath).
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "del", "client.admin-backup").
		Return("", nil).Once()

	err := createAdminBackupKey(context.Background(), backupPath)
	require.NoError(t, err)

	err = deleteAdminBackupKey(context.Background())
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
	origCreateBackup := createAdminBackupKeyFunc
	origVerify := verifyAdminAccessFunc
	origRotate := rotateEntityKeyFunc
	origUpdateDB := updateAdminKeyringInDBFunc
	origUpdateFiles := updateAdminKeyringFilesFunc
	origDeleteBackup := deleteAdminBackupKeyFunc
	defer func() {
		createAdminBackupKeyFunc = origCreateBackup
		verifyAdminAccessFunc = origVerify
		rotateEntityKeyFunc = origRotate
		updateAdminKeyringInDBFunc = origUpdateDB
		updateAdminKeyringFilesFunc = origUpdateFiles
		deleteAdminBackupKeyFunc = origDeleteBackup
	}()

	backupCreated := false
	backupDeleted := false
	dbUpdated := false
	filesUpdated := false
	rotated := false
	verifyCalls := 0

	createAdminBackupKeyFunc = func(ctx context.Context, backupKeyringPath string) error {
		backupCreated = true
		return nil
	}
	deleteAdminBackupKeyFunc = func(ctx context.Context) error {
		backupDeleted = true
		return nil
	}
	verifyAdminAccessFunc = func(ctx context.Context, entityName string, keyringPath string) error {
		verifyCalls++
		return nil
	}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		rotated = true
		assert.Equal(t, "client.admin", entity)
		assert.Equal(t, "aes256k", keyType)
		return "[client.admin]\n\tkey = ROTATEDSECRET==\n", nil
	}
	updateAdminKeyringInDBFunc = func(ctx context.Context, s interfaces.StateInterface, secretKey string) error {
		dbUpdated = true
		assert.Equal(t, "ROTATEDSECRET==", secretKey)
		return nil
	}
	updateAdminKeyringFilesFunc = func(ctx context.Context, s interfaces.StateInterface, secretKey string) error {
		filesUpdated = true
		assert.Equal(t, "ROTATEDSECRET==", secretKey)
		return nil
	}

	// 1. Full successful run
	err := RotateAdminKey(context.Background(), nil, "aes256k")
	require.NoError(t, err)
	assert.True(t, backupCreated)
	assert.True(t, rotated)
	assert.True(t, dbUpdated)
	assert.True(t, filesUpdated)
	assert.True(t, backupDeleted)
	assert.Equal(t, 2, verifyCalls) // 1 for backup, 1 for rotated admin

	// 2. Failure on backup verification: aborts before rotating client.admin
	rotated = false
	backupDeleted = false
	verifyAdminAccessFunc = func(ctx context.Context, entityName string, keyringPath string) error {
		if entityName == "client.admin-backup" {
			return fmt.Errorf("backup failed")
		}
		return nil
	}

	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporary recovery credential failed verification")
	assert.False(t, rotated)
	assert.True(t, backupDeleted) // Must still be cleaned up

	// 3. Failure on new admin key verification
	verifyAdminAccessFunc = func(ctx context.Context, entityName string, keyringPath string) error {
		if entityName == "client.admin" {
			return fmt.Errorf("admin failed auth")
		}
		return nil
	}
	err = RotateAdminKey(context.Background(), nil, "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new client.admin key failed verification")
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
	assert.True(t, IsMicroCephManagedClient("client.fsmir-vol1-rem1"))

	assert.False(t, IsMicroCephManagedClient("client.cinder"))
	assert.False(t, IsMicroCephManagedClient("client.glance"))
	assert.False(t, IsMicroCephManagedClient("client.external"))
	// The dash form is not an NFS Ganesha client of ours.
	assert.False(t, IsMicroCephManagedClient("client.nfs-ganesha"))
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

	// 4. Remote cluster keyring: matched by the entity name.
	remoteKeyring := filepath.Join(tmpDir, "current", "conf", "siteb.keyring")
	require.NoError(t, os.WriteFile(remoteKeyring, []byte("x"), 0600))
	paths, service = getClientKeyringPaths("client.siteb")
	assert.Equal(t, []string{remoteKeyring}, paths)
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
	defer func() {
		inspectClientSessionBlockersFunc = origInspect
		getOrCreatePendingKeyFunc = origPending
		commitPendingKeyFunc = origCommit
		getEntityKeyringFunc = origGetKeyring
		snapRestartFunc = origRestart
	}()

	// Valid base64 secrets, as a real keyring must carry for ceph-authtool or a
	// daemon to load it.
	oldKey := "AQB0ZXN0b2xka2V5AQIDBAUGBwgJCgsMDQ4PAA=="
	newKey := "AQB0ZXN0bmV3a2V5AQIDBAUGBwgJCgsMDQ4PAA=="

	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}
	pendingIssued := false
	getOrCreatePendingKeyFunc = func(ctx context.Context, entity string) (string, error) {
		pendingIssued = true
		assert.Equal(t, "client.radosgw.gateway", entity)
		// GetOrCreatePendingKey extracts the pending secret from the JSON output;
		// the keyring rendering happens from this secret alone.
		return newKey, nil
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
	assert.True(t, pendingIssued)
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
	pendingIssued = false
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session incompatible")
	assert.False(t, pendingIssued)
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

	res, err := RotateManagedClients(context.Background(), "aes256k")
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
	res, err = RotateManagedClients(context.Background(), "aes256k")
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
	res, err = RotateManagedClients(context.Background(), "aes256k")
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
	res, err = RotateManagedClients(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.True(t, res.HasBlockers)
	assert.Contains(t, res.BlockedClients, "client.radosgw.gateway")
	assert.Contains(t, res.BlockerMessage, "incompatible")
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
	origSwitchAuth := switchServiceAuthAndPreventInsecureFunc
	origRotateAdmin := rotateAdminKeyFunc
	origRotateClients := rotateManagedClientsFunc
	origFinalize := finalizeAuthRotationFunc
	defer func() {
		resolveTargetKeyTypeFunc = origResolve
		checkAuthRotationReadinessFunc = origReadiness
		prepareAuthRotationFunc = origPrepare
		rotateDaemonsFunc = origRotateDaemons
		switchServiceAuthAndPreventInsecureFunc = origSwitchAuth
		rotateAdminKeyFunc = origRotateAdmin
		rotateManagedClientsFunc = origRotateClients
		finalizeAuthRotationFunc = origFinalize
	}()

	stages := []string{}
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
	rotateDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
		stages = append(stages, "rotate_daemons")
		return nil
	}
	switchServiceAuthAndPreventInsecureFunc = func(ctx context.Context, targetKeyType string) error {
		stages = append(stages, "switch_service_auth")
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
	rotateManagedClientsFunc = func(ctx context.Context, targetKeyType string) (ClientRotationResult, error) {
		stages = append(stages, "rotate_clients")
		return ClientRotationResult{HasBlockers: false, RotatedClients: []string{"client.rgw"}}, nil
	}

	rec, err := ExecuteAuthRotation(context.Background(), nil, "aes256k", "")
	require.NoError(t, err)
	assert.Equal(t, "completed", rec.State)
	assert.Equal(t, []string{
		"readiness", "prepare_auth", "rotate_daemons", "switch_service_auth",
		"protect_admin", "rotate_clients", "finish_safely",
	}, stages)

	// 2. Blocked case (unmanaged client present)
	stages = nil
	rotateManagedClientsFunc = func(ctx context.Context, targetKeyType string) (ClientRotationResult, error) {
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
	defer func() { rotateSingleClientKeyFunc = origRotateSingle }()

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

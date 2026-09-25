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

	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create-pending", "client.rgw").
		Return("[client.rgw]\n\tkey = ACTIVE\n\tpending_key = PENDING\n", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "commit-pending", "client.rgw").
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "clear-pending", "client.rgw").
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "wipe-rotating-service-keys").
		Return("wiped rotating service keys!", nil).Once()

	out, err := GetOrCreatePendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)
	assert.Contains(t, out, "pending_key")

	err = CommitPendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)

	err = ClearPendingKey(context.Background(), "client.rgw")
	require.NoError(t, err)

	err = WipeRotatingServiceKeys(context.Background())
	require.NoError(t, err)
}

func TestSetOSDBlueStoreLabelKey(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "--dev", "/dev/sdb", "set-label-key", "--key", "osd_key", "-v", "/var/lib/ceph/osd/ceph-0/keyring").
		Return("", nil).Once()

	err := SetOSDBlueStoreLabelKey(context.Background(), "/dev/sdb", "/var/lib/ceph/osd/ceph-0/keyring")
	require.NoError(t, err)
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

func TestRotateMonitorKey(t *testing.T) {
	origRotate := rotateEntityKeyFunc
	origRestart := snapRestartFunc
	origQuorumWait := waitForMonQuorumFunc
	defer func() {
		rotateEntityKeyFunc = origRotate
		snapRestartFunc = origRestart
		waitForMonQuorumFunc = origQuorumWait
	}()

	restartedMons := []string{}
	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		assert.Equal(t, "mon.", entity)
		assert.Equal(t, "aes256k", keyType)
		return "[mon.]\n\tkey = AQB...==\n", nil
	}
	snapRestartFunc = func(service string, isReload bool) error {
		assert.Equal(t, "mon", service)
		return nil
	}
	waitForMonQuorumFunc = func(ctx context.Context, monName string, timeout time.Duration) error {
		restartedMons = append(restartedMons, monName)
		return nil
	}

	err := RotateMonitorKey(context.Background(), "aes256k", []string{"mon-a", "mon-b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"mon-a", "mon-b"}, restartedMons)
}

func TestRotateMGRKey(t *testing.T) {
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

	err := RotateMGRKey(context.Background(), "aes256k", "node-a")
	require.NoError(t, err)
	assert.True(t, stopped)
	assert.True(t, started)
	assert.True(t, verified)
}

func TestRotateMDSKey(t *testing.T) {
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

	err := RotateMDSKey(context.Background(), "aes256k", "node-a")
	require.NoError(t, err)
	assert.True(t, stopped)
	assert.True(t, started)
	assert.True(t, verified)
}

func TestRotateOSDKey(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	origRotate := rotateEntityKeyFunc
	origRestart := snapRestartFunc
	origOSDWait := waitForOSDUpFunc
	origSetLabel := setOSDBlueStoreLabelKeyFunc
	defer func() {
		rotateEntityKeyFunc = origRotate
		snapRestartFunc = origRestart
		waitForOSDUpFunc = origOSDWait
		setOSDBlueStoreLabelKeyFunc = origSetLabel
	}()

	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "down", "0").Return("", nil).Once()

	rotateEntityKeyFunc = func(ctx context.Context, entity string, keyType string) (string, error) {
		assert.Equal(t, "osd.0", entity)
		return "[osd.0]\n\tkey = OSDKEY==\n", nil
	}
	snapRestartFunc = func(service string, isReload bool) error {
		assert.Equal(t, "osd", service)
		return nil
	}
	waitForOSDUpFunc = func(ctx context.Context, osdID int64, timeout time.Duration) error {
		assert.Equal(t, int64(0), osdID)
		return nil
	}

	err := RotateOSDKey(context.Background(), "aes256k", 0)
	require.NoError(t, err)
}

func TestRotateDaemonsPipeline(t *testing.T) {
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	origRotateMon := rotateMonitorKeyFunc
	origRotateMGR := rotateMGRKeyFunc
	origRotateMDS := rotateMDSKeyFunc
	origRotateOSD := rotateOSDKeyFunc
	origDumpKeys := dumpAuthKeysFunc
	origHealth := getAuthHealthWarningsFunc
	defer func() {
		rotateMonitorKeyFunc = origRotateMon
		rotateMGRKeyFunc = origRotateMGR
		rotateMDSKeyFunc = origRotateMDS
		rotateOSDKeyFunc = origRotateOSD
		dumpAuthKeysFunc = origDumpKeys
		getAuthHealthWarningsFunc = origHealth
	}()

	// Mock monitor map names
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons": [{"name": "node-a"}]}`, nil).Once()

	monRotated := false
	mgrRotated := false
	mdsRotated := false
	osdRotated := false

	rotateMonitorKeyFunc = func(ctx context.Context, targetKeyType string, monNames []string) error {
		monRotated = true
		assert.Equal(t, []string{"node-a"}, monNames)
		return nil
	}

	dumpAuthKeysFunc = func(ctx context.Context) ([]AuthKeyEntry, error) {
		return []AuthKeyEntry{
			{EntityName: "mgr.node-a", EntityType: "mgr", EntityID: "node-a"},
			{EntityName: "mds.node-a", EntityType: "mds", EntityID: "node-a"},
			{EntityName: "osd.0", EntityType: "osd", EntityID: "0"},
		}, nil
	}

	rotateMGRKeyFunc = func(ctx context.Context, targetKeyType string, mgrName string) error {
		mgrRotated = true
		assert.Equal(t, "node-a", mgrName)
		return nil
	}

	rotateMDSKeyFunc = func(ctx context.Context, targetKeyType string, mdsName string) error {
		mdsRotated = true
		assert.Equal(t, "node-a", mdsName)
		return nil
	}

	rotateOSDKeyFunc = func(ctx context.Context, targetKeyType string, osdID int64) error {
		osdRotated = true
		assert.Equal(t, int64(0), osdID)
		return nil
	}

	// Health check clears
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: false}, nil
	}

	err := RotateDaemons(context.Background(), "aes256k")
	require.NoError(t, err)
	assert.True(t, monRotated)
	assert.True(t, mgrRotated)
	assert.True(t, mdsRotated)
	assert.True(t, osdRotated)

	// Failure case: AUTH_INSECURE_SERVICE_KEY_TYPE remains active
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons": [{"name": "node-a"}]}`, nil).Once()
	getAuthHealthWarningsFunc = func(ctx context.Context) (AuthHealthWarnings, error) {
		return AuthHealthWarnings{InsecureServiceKeyType: true, InsecureServiceDetails: []string{"entity osd.1 using insecure key type: aes"}}, nil
	}

	err = RotateDaemons(context.Background(), "aes256k")
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
	assert.True(t, IsMicroCephManagedClient("client.bootstrap-osd"))
	assert.True(t, IsMicroCephManagedClient("client.fsmir-vol1-rem1"))

	assert.False(t, IsMicroCephManagedClient("client.cinder"))
	assert.False(t, IsMicroCephManagedClient("client.glance"))
	assert.False(t, IsMicroCephManagedClient("client.external"))
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
	origInspect := inspectClientSessionBlockersFunc
	origPending := getOrCreatePendingKeyFunc
	origCommit := commitPendingKeyFunc
	origRestart := snapRestartFunc
	defer func() {
		inspectClientSessionBlockersFunc = origInspect
		getOrCreatePendingKeyFunc = origPending
		commitPendingKeyFunc = origCommit
		snapRestartFunc = origRestart
	}()

	pendingIssued := false
	committed := false
	reloaded := false

	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return nil, nil
	}
	getOrCreatePendingKeyFunc = func(ctx context.Context, entity string) (string, error) {
		pendingIssued = true
		assert.Equal(t, "client.radosgw.gateway", entity)
		return "[client.radosgw.gateway]\n\tkey = ACTIVE\n\tpending_key = PENDING\n", nil
	}
	snapRestartFunc = func(service string, isReload bool) error {
		reloaded = true
		assert.Equal(t, "rgw", service)
		return nil
	}
	commitPendingKeyFunc = func(ctx context.Context, entity string) error {
		committed = true
		assert.Equal(t, "client.radosgw.gateway", entity)
		return nil
	}

	// 1. Success case
	err := RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.NoError(t, err)
	assert.True(t, pendingIssued)
	assert.True(t, committed)
	assert.True(t, reloaded)

	// 2. Blocked case
	inspectClientSessionBlockersFunc = func(ctx context.Context, targetKeyType string) (map[string]string, error) {
		return map[string]string{"client.radosgw.gateway": "session incompatible"}, nil
	}
	err = RotateSingleClientKey(context.Background(), "radosgw.gateway", "aes256k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session incompatible")
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
	rotateDaemonsFunc = func(ctx context.Context, targetKeyType string) error {
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


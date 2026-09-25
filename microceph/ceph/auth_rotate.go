package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/tidwall/gjson"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// Configuration of the Cephx auth cipher.
type MonCiphers struct {
	AuthAllowedCiphers  []string
	AuthPreferredCipher string
	AuthServiceCipher   string
}

// Auth entity and key cipher status.
type AuthKeyEntry struct {
	EntityName     string
	EntityType     string
	EntityID       string
	KeyType        string
	PendingKeyType string
	Created        string
}

// These are insecure Cephx health warnings raised by Ceph.
type AuthHealthWarnings struct {
	InsecureServiceKeyType  bool
	InsecureServiceTickets  bool
	InsecureRotatingKeyType bool
	InsecureKeysCreatable   bool
	InsecureClientKeyType   bool
	InsecureKeysAllowed     bool
	InsecureClientDetails   []string
	InsecureServiceDetails  []string
}

// Metadata for an active client connection to the mons.
type ClientSessionInfo struct {
	Name               string
	EntityName         string
	ConFeatures        uint64
	ConFeaturesRelease string
	RemoteHost         string
	Open               bool
}

// Functions that can be patched for testing.
var (
	getMonCiphersFunc                = GetMonCiphers
	setMonAllowedCiphersFunc         = SetMonAllowedCiphers
	setMonPreferredCipherFunc        = SetMonPreferredCipher
	setMonServiceCipherFunc          = SetMonServiceCipher
	setMonAllowInsecureKeyFunc       = SetMonAllowInsecureKey
	rotateEntityKeyFunc              = RotateEntityKey
	rotateEntityKeyToFileFunc        = RotateEntityKeyToFile
	getOrCreatePendingKeyFunc        = GetOrCreatePendingKey
	commitPendingKeyFunc             = CommitPendingKey
	clearPendingKeyFunc              = ClearPendingKey
	wipeRotatingServiceKeysFunc      = WipeRotatingServiceKeys
	setOSDBlueStoreLabelKeyFunc      = SetOSDBlueStoreLabelKey
	dumpAuthKeysFunc                 = DumpAuthKeys
	getAuthHealthWarningsFunc        = GetAuthHealthWarnings
	getClientSessionsFunc            = GetClientSessions
	resolveTargetKeyTypeFunc         = ResolveTargetKeyType
	checkAuthRotationReadinessFunc   = CheckAuthRotationReadiness
	checkClusterMembersReachableFunc = checkClusterMembersReachable
	checkMonQuorumReadyFunc          = checkMonQuorumReady
	checkCipherCompatibilityFunc     = checkCipherCompatibility
	prepareAuthRotationFunc          = PrepareAuthRotation
	snapStartFunc                    = snapStart
	snapRestartFunc                  = snapRestart
	waitForMonQuorumFunc             = waitForMonQuorum
	waitForMGRReadyFunc              = waitForMGRReady
	waitForMDSReadyFunc              = waitForMDSReady
	waitForOSDUpFunc                 = waitForOSDUp
	rotateMonitorKeyFunc             = RotateMonitorKey
	rotateMGRKeyFunc                 = RotateMGRKey
	rotateMDSKeyFunc                 = RotateMDSKey
	rotateOSDKeyFunc                 = RotateOSDKey
	rotateDaemonsFunc                = RotateDaemons
	switchServiceAuthenticationFunc         = SwitchServiceAuthentication
	preventNewInsecureKeysFunc              = PreventNewInsecureKeys
	switchServiceAuthAndPreventInsecureFunc = SwitchServiceAuthAndPreventInsecure
	createAdminBackupKeyFunc                = createAdminBackupKey
	verifyAdminAccessFunc                   = verifyAdminAccess
	deleteAdminBackupKeyFunc                = deleteAdminBackupKey
	updateAdminKeyringInDBFunc              = updateAdminKeyringInDB
	updateAdminKeyringFilesFunc             = updateAdminKeyringFiles
	rotateAdminKeyFunc                      = RotateAdminKey
	rotateSingleClientKeyFunc               = RotateSingleClientKey
	rotateManagedClientsFunc                = RotateManagedClients
	inspectClientSessionBlockersFunc        = InspectClientSessionBlockers
	disallowInsecureLegacyCiphersFunc       = DisallowInsecureLegacyCiphers
	finalizeAuthRotationFunc                = FinalizeAuthRotation
	executeAuthRotationFunc                 = ExecuteAuthRotation
	buildAuthStatusFunc                     = BuildAuthStatus
)

// Query the monitor map and return the current cipher config.
func GetMonCiphers(ctx context.Context) (MonCiphers, error) {
	output, err := cephRunContext(ctx, "mon", "dump", "-f", "json")
	if err != nil {
		return MonCiphers{}, fmt.Errorf("failed to fetch monitor dump: %w", err)
	}

	return parseMonCiphers(output), nil
}

func parseMonCiphers(output string) MonCiphers {
	var ciphers MonCiphers

	// Parse allowed ciphers. In Ceph JSON, auth_allowed_ciphers is typically an array of objects
	// [{"name": "aes", "type": 1}, ...], or an array of strings.
	rawAllowed := gjson.Get(output, "auth_allowed_ciphers")
	if rawAllowed.IsArray() {
		for _, item := range rawAllowed.Array() {
			name := item.Get("name").String()
			if name == "" {
				name = item.String()
			}
			if name != "" {
				ciphers.AuthAllowedCiphers = append(ciphers.AuthAllowedCiphers, name)
			}
		}
	} else if rawAllowed.Type == gjson.String && rawAllowed.String() != "" {
		for _, part := range strings.Split(rawAllowed.String(), ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				ciphers.AuthAllowedCiphers = append(ciphers.AuthAllowedCiphers, trimmed)
			}
		}
	}

	// Now parse preferred and service ciphers.
	pref := gjson.Get(output, "auth_preferred_cipher.name")
	if pref.Exists() && pref.String() != "" {
		ciphers.AuthPreferredCipher = pref.String()
	} else {
		ciphers.AuthPreferredCipher = gjson.Get(output, "auth_preferred_cipher").String()
	}

	svc := gjson.Get(output, "auth_service_cipher.name")
	if svc.Exists() && svc.String() != "" {
		ciphers.AuthServiceCipher = svc.String()
	} else {
		ciphers.AuthServiceCipher = gjson.Get(output, "auth_service_cipher").String()
	}

	return ciphers
}

// Functions to manipulate the auth ciphers on the monmap.

func SetMonAllowedCiphers(ctx context.Context, ciphers []string) error {
	cipherArg := strings.Join(ciphers, ",")
	_, err := cephRunContext(ctx, "mon", "set", "auth_allowed_ciphers", cipherArg)
	if err != nil {
		return fmt.Errorf("failed to set auth_allowed_ciphers to %q: %w", cipherArg, err)
	}

	return nil
}

func SetMonPreferredCipher(ctx context.Context, cipher string) error {
	_, err := cephRunContext(ctx, "mon", "set", "auth_preferred_cipher", cipher)
	if err != nil {
		return fmt.Errorf("failed to set auth_preferred_cipher to %q: %w", cipher, err)
	}

	return nil
}

func SetMonServiceCipher(ctx context.Context, cipher string) error {
	_, err := cephRunContext(ctx, "mon", "set", "auth_service_cipher", cipher)
	if err != nil {
		return fmt.Errorf("failed to set auth_service_cipher to %q: %w", cipher, err)
	}

	return nil
}

// Configure whether new keys with insecure ciphers are allowed.
func SetMonAllowInsecureKey(ctx context.Context, allow bool) error {
	val := strconv.FormatBool(allow)
	_, err := cephRunContext(ctx, "config", "set", "mon", "mon_auth_allow_insecure_key", val)
	if err != nil {
		return fmt.Errorf("failed to set mon_auth_allow_insecure_key to %s: %w", val, err)
	}

	return nil
}

// Determine the effective key type for rotation.
// If requestedType is empty, it queries Ceph's current auth_preferred_cipher.
func ResolveTargetKeyType(ctx context.Context, requestedType string) (string, error) {
	if requestedType != "" {
		return requestedType, nil
	}

	ciphers, err := getMonCiphersFunc(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to determine default key-type: %w", err)
	}

	if ciphers.AuthPreferredCipher != "" {
		return ciphers.AuthPreferredCipher, nil
	}

	return "aes256k", nil
}

// CheckAuthRotationReadiness verifies prerequisites before rotating CephX keys:
// 1. Validates software compatibility for the requested key type.
// 2. Confirms all Ceph monitors in the monitor map are in quorum.
// 3. Confirms reachable MicroCluster members.
func CheckAuthRotationReadiness(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
	err := checkCipherCompatibilityFunc(ctx, targetKeyType)
	if err != nil {
		return fmt.Errorf("cipher compatibility check failed: %w", err)
	}

	err = checkMonQuorumReadyFunc(ctx)
	if err != nil {
		return fmt.Errorf("monitor quorum check failed: %w", err)
	}

	err = checkClusterMembersReachableFunc(ctx, s)
	if err != nil {
		return fmt.Errorf("cluster member reachability check failed: %w", err)
	}

	return nil
}

func checkCipherCompatibility(ctx context.Context, targetKeyType string) error {
	switch targetKeyType {
	case "aes":
		return nil
	case "aes256k":
		output, err := cephRunContext(ctx, "mon", "dump", "-f", "json")
		if err != nil {
			return fmt.Errorf("failed to fetch monitor dump for compatibility check: %w", err)
		}
		// If min_mon_release is present and below Squid (19), aes256k is not supported.
		minRelease := gjson.Get(output, "min_mon_release").Int()
		if minRelease > 0 && minRelease < 19 {
			return fmt.Errorf("ceph min_mon_release (%d) is older than Squid (19); cannot use aes256k", minRelease)
		}
		return nil
	default:
		return fmt.Errorf("unsupported key type %q; supported types are 'aes' and 'aes256k'", targetKeyType)
	}
}

func checkMonQuorumReady(ctx context.Context) error {
	monmap, err := getMonmapNames(ctx)
	if err != nil {
		return fmt.Errorf("failed to get monitor map: %w", err)
	}

	quorum, err := getMonQuorumNames(ctx)
	if err != nil {
		return fmt.Errorf("failed to get monitor quorum: %w", err)
	}

	for _, mon := range monmap {
		if !slices.Contains(quorum, mon) {
			return fmt.Errorf("monitor %q is out of quorum; all monitors (%v) must be in quorum before rotating keys", mon, monmap)
		}
	}

	return nil
}

func checkClusterMembersReachable(ctx context.Context, s interfaces.StateInterface) error {
	if s == nil || s.ClusterState() == nil {
		return nil
	}

	connect := s.ClusterState().Connect()
	if connect == nil {
		return nil
	}

	cluster, err := connect.Cluster(false)
	if err != nil {
		return fmt.Errorf("failed to list cluster members: %w", err)
	}

	for _, remoteClient := range cluster {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = remoteClient.Query(checkCtx, "GET", mcTypes.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, nil)
		cancel()
		if err != nil {
			return fmt.Errorf("cluster member is unreachable: %w", err)
		}
	}

	return nil
}

// This function maps (roughly) to the upstream migration steps 1 and 2.
func PrepareAuthRotation(ctx context.Context, targetKeyType string) error {
	ciphers, err := getMonCiphersFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to get monitor ciphers: %w", err)
	}

	// Step 1: Allow the requested type without dropping existing clients.
	if !slices.Contains(ciphers.AuthAllowedCiphers, targetKeyType) {
		newAllowed := append(ciphers.AuthAllowedCiphers, targetKeyType)
		err = setMonAllowedCiphersFunc(ctx, newAllowed)
		if err != nil {
			return fmt.Errorf("failed to allow cipher %q: %w", targetKeyType, err)
		}

		// Verify allowed ciphers updated.
		updated, err := getMonCiphersFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to verify updated allowed ciphers: %w", err)
		}
		if !slices.Contains(updated.AuthAllowedCiphers, targetKeyType) {
			return fmt.Errorf("verification failed: cipher %q not present in auth_allowed_ciphers %v", targetKeyType, updated.AuthAllowedCiphers)
		}
	}

	// Step 2: Set preferred default cipher for new keys.
	if ciphers.AuthPreferredCipher != targetKeyType {
		err = setMonPreferredCipherFunc(ctx, targetKeyType)
		if err != nil {
			return fmt.Errorf("failed to set auth_preferred_cipher to %q: %w", targetKeyType, err)
		}

		// Verify preferred cipher updated.
		updated, err := getMonCiphersFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to verify updated preferred cipher: %w", err)
		}
		if updated.AuthPreferredCipher != targetKeyType {
			return fmt.Errorf("verification failed: auth_preferred_cipher is %q, expected %q", updated.AuthPreferredCipher, targetKeyType)
		}
	}

	return nil
}

// This function executes upstream step 5 by setting auth_service_cipher to the requested
// key type after daemon rotation succeeds. For secure migration, it verifies that
// AUTH_INSECURE_SERVICE_TICKETS clears.
func SwitchServiceAuthentication(ctx context.Context, targetKeyType string) error {
	ciphers, err := getMonCiphersFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch monitor ciphers: %w", err)
	}

	if ciphers.AuthServiceCipher != targetKeyType {
		err = setMonServiceCipherFunc(ctx, targetKeyType)
		if err != nil {
			return fmt.Errorf("failed to set auth_service_cipher to %q: %w", targetKeyType, err)
		}

		updated, err := getMonCiphersFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to verify updated auth_service_cipher: %w", err)
		}
		if updated.AuthServiceCipher != targetKeyType {
			return fmt.Errorf("verification failed: auth_service_cipher is %q, expected %q", updated.AuthServiceCipher, targetKeyType)
		}
	}

	// For secure cipher migration (aes256k), verify AUTH_INSECURE_SERVICE_TICKETS clears.
	if targetKeyType == "aes256k" {
		hw, err := getAuthHealthWarningsFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to check health warnings after switching service cipher: %w", err)
		}
		if hw.InsecureServiceTickets {
			return fmt.Errorf("AUTH_INSECURE_SERVICE_TICKETS warning remains active after switching service cipher to %q", targetKeyType)
		}
	}

	return nil
}

// This is upstream step 7: during secure-type migration, it sets 
// mon_auth_allow_insecure_key=false and verifies that AUTH_INSECURE_KEYS_CREATABLE clears.
func PreventNewInsecureKeys(ctx context.Context, targetKeyType string) error {
	// Only apply for secure cipher migration.
	if targetKeyType != "aes256k" {
		return nil
	}

	err := setMonAllowInsecureKeyFunc(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to disable insecure key creation: %w", err)
	}

	hw, err := getAuthHealthWarningsFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to check health warnings after setting mon_auth_allow_insecure_key: %w", err)
	}

	if hw.InsecureKeysCreatable {
		return fmt.Errorf("AUTH_INSECURE_KEYS_CREATABLE warning remains active after disabling insecure key creation")
	}

	return nil
}

// This function bridges upstream steps 5 to 7:
// - Switch auth_service_cipher to the target type and verify AUTH_INSECURE_SERVICE_TICKETS clears.
// - Honors the recommended natural-expiry path for rotating service keys without wiping.
// - Sets mon_auth_allow_insecure_key=false and verifies AUTH_INSECURE_KEYS_CREATABLE clears.
func SwitchServiceAuthAndPreventInsecure(ctx context.Context, targetKeyType string) error {
	err := switchServiceAuthenticationFunc(ctx, targetKeyType)
	if err != nil {
		return err
	}

	// Upstream Step 6: Allow existing rotating service keys to expire naturally per recommended path.

	err = preventNewInsecureKeysFunc(ctx, targetKeyType)
	if err != nil {
		return err
	}

	return nil
}

// ParseKeyringData extracts the secret key string from a keyring format string.
func ParseKeyringData(data string) (string, error) {
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "key") {
			fields := strings.SplitN(trimmed, "=", 2)
			if len(fields) >= 2 {
				secret := strings.TrimSpace(fields[1])
				if secret != "" {
					return secret, nil
				}
			}
		}
	}
	return "", fmt.Errorf("couldn't find a keyring entry")
}

// This function executes upstream step 8:
// - Creates a temporary recovery credential (client.admin-backup) with full caps and tests access.
// - Rotates client.admin to targetKeyType.
// - Updates MicroCeph shared database (keyring.client.admin) and admin-keyring files.
// - Verifies admin access works with the new key.
// - Removes the temporary recovery credential.
func RotateAdminKey(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
	pathConst := constants.GetPathConst()
	tmpDir, err := os.MkdirTemp("", "microceph-admin-rotate-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory for admin rotation: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	backupKeyringPath := filepath.Join(tmpDir, "client.admin-backup.keyring")

	// Create and test temporary recovery credential
	err = createAdminBackupKeyFunc(ctx, backupKeyringPath)
	if err != nil {
		return fmt.Errorf("failed to create temporary admin recovery credential: %w", err)
	}
	// Always attempt to delete backup key on exit
	defer func() {
		_ = deleteAdminBackupKeyFunc(context.Background())
	}()

	err = verifyAdminAccessFunc(ctx, "client.admin-backup", backupKeyringPath)
	if err != nil {
		return fmt.Errorf("temporary recovery credential failed verification: %w", err)
	}

	// Rotate client.admin
	keyringContent, err := rotateEntityKeyFunc(ctx, "client.admin", targetKeyType)
	if err != nil {
		return fmt.Errorf("failed to rotate client.admin key: %w", err)
	}

	secretKey, err := ParseKeyringData(keyringContent)
	if err != nil {
		return fmt.Errorf("failed to parse secret key from rotated client.admin output: %w", err)
	}

	// Update MicroCeph shared database value
	err = updateAdminKeyringInDBFunc(ctx, s, secretKey)
	if err != nil {
		return fmt.Errorf("failed to update admin keyring in database: %w", err)
	}

	// Update local and cluster admin-keyring files
	err = updateAdminKeyringFilesFunc(ctx, s, secretKey)
	if err != nil {
		return fmt.Errorf("failed to update admin keyring files: %w", err)
	}

	// Verify access before removing temporary credential
	adminKeyringPath := filepath.Join(pathConst.ConfPath, constants.CephAdminKeyringFileName)
	err = verifyAdminAccessFunc(ctx, "client.admin", adminKeyringPath)
	if err != nil {
		return fmt.Errorf("new client.admin key failed verification: %w", err)
	}

	return nil
}

func createAdminBackupKey(ctx context.Context, backupKeyringPath string) error {
	dir := filepath.Dir(backupKeyringPath)
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return fmt.Errorf("failed to create directory for admin backup keyring: %w", err)
	}

	_, err = cephRunContext(ctx, "auth", "get-or-create", "client.admin-backup",
		"mon", "allow *",
		"osd", "allow *",
		"mds", "allow *",
		"mgr", "allow *",
		"-o", backupKeyringPath,
	)
	if err != nil {
		return fmt.Errorf("failed to create client.admin-backup credential: %w", err)
	}

	return nil
}

func verifyAdminAccess(ctx context.Context, entityName string, keyringPath string) error {
	_, err := cephRunContext(ctx, "-n", entityName, "-k", keyringPath, "auth", "ls")
	if err != nil {
		return fmt.Errorf("failed to verify access for %s using %s: %w", entityName, keyringPath, err)
	}
	return nil
}

func deleteAdminBackupKey(ctx context.Context) error {
	_, err := cephRunContext(ctx, "auth", "del", "client.admin-backup")
	if err != nil {
		logger.Warnf("failed to delete temporary backup credential client.admin-backup: %v", err)
		return err
	}
	return nil
}

func updateAdminKeyringInDB(ctx context.Context, s interfaces.StateInterface, secretKey string) error {
	if s == nil || s.ClusterState() == nil || s.ClusterState().Database() == nil {
		return nil
	}

	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		exists, err := database.ConfigItemExists(ctx, tx, constants.AdminKeyringFieldName)
		if err != nil {
			return fmt.Errorf("failed to check admin keyring in database: %w", err)
		}

		if exists {
			err = database.UpdateConfigItem(ctx, tx, constants.AdminKeyringFieldName, database.ConfigItem{
				Key:   constants.AdminKeyringFieldName,
				Value: secretKey,
			})
		} else {
			_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{
				Key:   constants.AdminKeyringFieldName,
				Value: secretKey,
			})
		}
		if err != nil {
			return fmt.Errorf("failed to record admin keyring in database: %w", err)
		}

		return nil
	})
}

func updateAdminKeyringFiles(ctx context.Context, s interfaces.StateInterface, secretKey string) error {
	pathConst := constants.GetPathConst()
	confPath := pathConst.ConfPath

	// Render local ceph.keyring
	keyring := NewCephKeyring(confPath, constants.CephAdminKeyringFileName)
	err := keyring.WriteConfig(map[string]any{
		"name": "client.admin",
		"key":  secretKey,
	}, 0640)
	if err != nil {
		return fmt.Errorf("failed to render %s: %w", constants.CephAdminKeyringFileName, err)
	}

	// Render local ceph.client.admin.keyring
	adminKeyring := NewCephKeyring(confPath, "ceph.client.admin.keyring")
	err = adminKeyring.WriteConfig(map[string]any{
		"name": "client.admin",
		"key":  secretKey,
	}, 0640)
	if err != nil {
		return fmt.Errorf("failed to render ceph.client.admin.keyring: %w", err)
	}

	// Update remote members if cluster is available
	if s != nil && s.ClusterState() != nil && s.ClusterState().Connect() != nil {
		err = client.SendUpdateClientConfRequestToClusterMembers(ctx, s)
		if err != nil {
			logger.Warnf("failed to distribute updated admin keyring to remote members: %v", err)
		}
	}

	return nil
}

// The outcome of rotating managed clients plus any blockers.
type ClientRotationResult struct {
	RotatedClients   []string
	BlockedClients   map[string]string
	UnmanagedClients []string
	HasBlockers      bool
	BlockerMessage   string
}

// Ensure the client entity name has the "client." prefix.
func NormalizeClientName(name string) string {
	trimmed := strings.TrimSpace(name)
	if !strings.HasPrefix(trimmed, "client.") {
		return "client." + trimmed
	}
	return trimmed
}

// Check whether a client entity is managed by MicroCeph.
func IsMicroCephManagedClient(entityName string) bool {
	norm := NormalizeClientName(entityName)
	if norm == "client.admin" || norm == "client.radosgw.gateway" {
		return true
	}
	if strings.HasPrefix(norm, "client.rbd-mirror.") ||
		strings.HasPrefix(norm, "client.cephfs-mirror.") ||
		strings.HasPrefix(norm, "client.bootstrap-") ||
		strings.HasPrefix(norm, "client.fsmir-") {
		return true
	}

	pathConst := constants.GetPathConst()
	// NFS Ganesha client check
	if _, err := os.Stat(filepath.Join(pathConst.ConfPath, "ganesha")); err == nil {
		return true
	}

	// Remote cluster keyring check
	remoteName := strings.TrimPrefix(norm, "client.")
	if _, err := os.Stat(filepath.Join(pathConst.ConfPath, fmt.Sprintf("%s.keyring", remoteName))); err == nil {
		return true
	}

	return false
}

func getClientKeyringPaths(clientName string) ([]string, string) {
	pathConst := constants.GetPathConst()
	norm := NormalizeClientName(clientName)

	switch norm {
	case "client.radosgw.gateway":
		return []string{
			filepath.Join(pathConst.DataPath, "radosgw", "ceph-radosgw.gateway", "keyring"),
			filepath.Join(pathConst.ConfPath, "ceph.client.radosgw.gateway.keyring"),
		}, "rgw"
	default:
		if strings.HasPrefix(norm, "client.rbd-mirror.") {
			host := strings.TrimPrefix(norm, "client.rbd-mirror.")
			return []string{
				filepath.Join(pathConst.DataPath, "rbd-mirror", fmt.Sprintf("ceph-%s", host), "keyring"),
			}, "rbd-mirror"
		}
		if strings.HasPrefix(norm, "client.cephfs-mirror.") {
			host := strings.TrimPrefix(norm, "client.cephfs-mirror.")
			return []string{
				filepath.Join(pathConst.DataPath, "cephfs-mirror", fmt.Sprintf("ceph-%s", host), "keyring"),
			}, "cephfs-mirror"
		}
		// NFS Ganesha keyring
		ganeshaKeyring := filepath.Join(pathConst.ConfPath, "ganesha", "keyring")
		if _, err := os.Stat(ganeshaKeyring); err == nil {
			return []string{ganeshaKeyring}, "nfs"
		}
		// Remote cluster keyring
		remoteName := strings.TrimPrefix(norm, "client.")
		remoteKeyring := filepath.Join(pathConst.ConfPath, fmt.Sprintf("%s.keyring", remoteName))
		if _, err := os.Stat(remoteKeyring); err == nil {
			return []string{remoteKeyring}, ""
		}
	}
	return nil, ""
}

// Check active client sessions for incompatible library/kernel versions.
func InspectClientSessionBlockers(ctx context.Context, targetKeyType string) (map[string]string, error) {
	sessions, err := getClientSessionsFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query client sessions: %w", err)
	}

	blockers := make(map[string]string)
	for _, s := range sessions {
		if !s.Open {
			continue
		}
		if !ClientSupportsKeyType(s, targetKeyType) {
			entity := NormalizeClientName(s.EntityName)
			blockers[entity] = fmt.Sprintf("connected session on host %q has incompatible client release %q; cannot use %s", s.RemoteHost, s.ConFeaturesRelease, targetKeyType)
		}
	}

	return blockers, nil
}

// RotateSingleClientKey issues a pending key alongside the old key, distributes it to
// local keyring paths, reloads associated daemons, and commits the pending key.
func RotateSingleClientKey(ctx context.Context, clientName string, targetKeyType string) error {
	norm := NormalizeClientName(clientName)
	logger.Infof("Rotating client credential for %s to %s", norm, targetKeyType)

	// 1. Check if client has a session blocker
	blockers, err := inspectClientSessionBlockersFunc(ctx, targetKeyType)
	if err == nil {
		if reason, ok := blockers[norm]; ok {
			return fmt.Errorf("client %s cannot be rotated: %s", norm, reason)
		}
	}

	// 2. Issue pending key alongside current key
	keyringContent, err := getOrCreatePendingKeyFunc(ctx, norm)
	if err != nil {
		return fmt.Errorf("failed to issue pending key for %s: %w", norm, err)
	}

	// 3. Distribute to persistent copies
	paths, service := getClientKeyringPaths(norm)
	for _, p := range paths {
		err = writeKeyringAtomically(p, keyringContent, 0600)
		if err != nil {
			logger.Warnf("failed to write client keyring %s: %v", p, err)
		}
	}

	// 4. Reload or restart service if associated
	if service != "" {
		_ = snapRestartFunc(service, true)
	}

	// 5. Commit pending key into active position, retiring the old key
	err = commitPendingKeyFunc(ctx, norm)
	if err != nil {
		return fmt.Errorf("failed to commit pending key for %s: %w", norm, err)
	}

	return nil
}

// This function executes upstream step 9:
// - Rotates and distributes MicroCeph-managed client keys one at a time.
// - Skips clients with incompatible sessions, reports unmanaged clients,
//   and returns the rotation result with blockers if any exist.
func RotateManagedClients(ctx context.Context, targetKeyType string) (ClientRotationResult, error) {
	result := ClientRotationResult{
		BlockedClients: make(map[string]string),
	}

	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to dump auth keys for client discovery: %w", err)
	}

	// Check active session blockers across the cluster
	sessionBlockers, err := inspectClientSessionBlockersFunc(ctx, targetKeyType)
	if err != nil {
		logger.Warnf("failed to inspect client session blockers: %v", err)
		sessionBlockers = make(map[string]string)
	}

	for _, entry := range entries {
		if entry.EntityType != "client" {
			continue
		}
		// client.admin is handled separately in Step 8
		if entry.EntityName == "client.admin" {
			continue
		}

		norm := NormalizeClientName(entry.EntityName)

		// Check if managed
		if !IsMicroCephManagedClient(norm) {
			// Unmanaged client: if not already on target key type, record as blocker
			if entry.KeyType != targetKeyType {
				result.UnmanagedClients = append(result.UnmanagedClients, norm)
			}
			continue
		}

		// Check session blocker
		if reason, blocked := sessionBlockers[norm]; blocked {
			result.BlockedClients[norm] = reason
			continue
		}

		// Rotate managed client
		err = rotateSingleClientKeyFunc(ctx, norm, targetKeyType)
		if err != nil {
			return result, fmt.Errorf("failed to rotate managed client %s: %w", norm, err)
		}
		result.RotatedClients = append(result.RotatedClients, norm)
	}

	// Formulate blocker message if any blockers exist
	if len(result.UnmanagedClients) > 0 {
		result.HasBlockers = true
		result.BlockerMessage = fmt.Sprintf("Unmanaged credentials must be rotated manually before rotation can proceed (unmanaged: %s)", strings.Join(result.UnmanagedClients, ", "))
	} else if len(result.BlockedClients) > 0 {
		result.HasBlockers = true
		var reasons []string
		for client, r := range result.BlockedClients {
			reasons = append(reasons, fmt.Sprintf("%s: %s", client, r))
		}
		result.BlockerMessage = fmt.Sprintf("Connected clients have incompatible library/kernel: %s", strings.Join(reasons, "; "))
	}

	return result, nil
}

// This function executes upstream step 10:
// - Verifies lockout prevention pre-conditions (no insecure service or client key health warnings).
// - Removes legacy insecure ciphers from auth_allowed_ciphers.
// - Verifies AUTH_INSECURE_KEYS_ALLOWED health check clears.
func DisallowInsecureLegacyCiphers(ctx context.Context, targetKeyType string) error {
	// If not migrating to a secure type (e.g. remaining on aes), nothing to disallow.
	if targetKeyType != "aes256k" {
		return nil
	}

	// Lockout prevention pre-condition checks:
	hw, err := getAuthHealthWarningsFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to check health warnings before disallowing insecure ciphers: %w", err)
	}

	if hw.InsecureServiceKeyType {
		return fmt.Errorf("cannot disallow insecure ciphers: service entities still use insecure key types: %v", hw.InsecureServiceDetails)
	}
	if hw.InsecureClientKeyType {
		return fmt.Errorf("cannot disallow insecure ciphers: client entities still use insecure key types: %v", hw.InsecureClientDetails)
	}
	if hw.InsecureServiceTickets {
		return fmt.Errorf("cannot disallow insecure ciphers: service tickets still using insecure cipher")
	}

	// Set auth_allowed_ciphers to only the target secure cipher.
	err = setMonAllowedCiphersFunc(ctx, []string{targetKeyType})
	if err != nil {
		return fmt.Errorf("failed to restrict auth_allowed_ciphers to %q: %w", targetKeyType, err)
	}

	// Verify allowed ciphers updated.
	ciphers, err := getMonCiphersFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to verify updated allowed ciphers: %w", err)
	}
	if slices.Contains(ciphers.AuthAllowedCiphers, "aes") {
		return fmt.Errorf("verification failed: legacy cipher 'aes' still present in auth_allowed_ciphers %v", ciphers.AuthAllowedCiphers)
	}

	// Verify AUTH_INSECURE_KEYS_ALLOWED clears.
	hw, err = getAuthHealthWarningsFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to check health warnings after disallowing insecure ciphers: %w", err)
	}
	if hw.InsecureKeysAllowed {
		return fmt.Errorf("AUTH_INSECURE_KEYS_ALLOWED warning remains active after restricting allowed ciphers")
	}

	return nil
}

// Complete the rotation by disallowing legacy insecure ciphers.
// Also updates the persistent database state to completed.
func FinalizeAuthRotation(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
	err := disallowInsecureLegacyCiphersFunc(ctx, targetKeyType)
	if err != nil {
		return err
	}

	if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			rec, err := database.GetAuthRotation(ctx, tx)
			if err != nil {
				return err
			}
			rec.State = database.AuthRotationStateCompleted
			rec.Stage = database.AuthRotationStageFinishSafely
			rec.Blocker = ""
			rec.Detail = ""
			return database.SetAuthRotation(ctx, tx, *rec)
		})
		if err != nil {
			return fmt.Errorf("failed to update auth rotation state to completed: %w", err)
		}
	}

	return nil
}

// ExecuteAuthRotation is the primary orchestrator that executes or resumes
// CephX key rotation across all stages.
func ExecuteAuthRotation(ctx context.Context, s interfaces.StateInterface, targetKeyType string, clientName string) (*database.AuthRotationRecord, error) {
	// Acquire rotation concurrency lock
	token := time.Now().UnixNano()
	staleBefore := token - int64(10*time.Minute)

	if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
		var acquired bool
		err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			acquired, err = database.TryAcquireAuthRotationLock(ctx, tx, token, staleBefore)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("failed to acquire rotation lock: %w", err)
		}
		if !acquired {
			return nil, fmt.Errorf("another auth rotation operation is currently in progress")
		}
		defer func() {
			_ = s.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
				_, err := database.ReleaseAuthRotationLock(ctx, tx, token)
				return err
			})
		}()
	}

	// Resolve target key type
	resolvedKeyType, err := resolveTargetKeyTypeFunc(ctx, targetKeyType)
	if err != nil {
		return nil, err
	}

	if clientName != "" {
		clientName = NormalizeClientName(clientName)
	}

	// Initialize or resume rotation record in database
	var rec *database.AuthRotationRecord
	if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			rec, _, err = database.InitOrResumeAuthRotation(ctx, tx, resolvedKeyType, clientName)
			return err
		})
		if err != nil {
			return nil, err
		}
	} else {
		rec = &database.AuthRotationRecord{
			TargetKeyType: resolvedKeyType,
			State:         database.AuthRotationStateInProgress,
			Stage:         database.AuthRotationStageReadiness,
			ClientName:    clientName,
		}
	}

	// Helper to update database stage
	setStage := func(stage string) error {
		rec.Stage = stage
		if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
			return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				return database.SetAuthRotationStage(ctx, tx, stage, rec.StepProgress)
			})
		}
		return nil
	}

	// Handle Single-Client Mode (--client NAME)
	if clientName != "" {
		err = rotateSingleClientKeyFunc(ctx, clientName, resolvedKeyType)
		if err != nil {
			if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
				_ = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
					return database.SetAuthRotationBlocker(ctx, tx, err.Error(), "")
				})
			}
			rec.State = database.AuthRotationStateBlocked
			rec.Blocker = err.Error()
			return rec, err
		}

		// Mark completed for single client
		if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
			_ = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				rec.State = database.AuthRotationStateCompleted
				rec.Stage = database.AuthRotationStageFinishSafely
				rec.Blocker = ""
				return database.SetAuthRotation(ctx, tx, *rec)
			})
		}
		rec.State = database.AuthRotationStateCompleted
		return rec, nil
	}

	// Full cluster rotation pipeline

	// Stage 1: Readiness checks
	_ = setStage(database.AuthRotationStageReadiness)
	err = checkAuthRotationReadinessFunc(ctx, s, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("readiness check failed: %w", err)
	}

	// Stage 2: Prepare auth (Upstream steps 1-2)
	_ = setStage(database.AuthRotationStagePrepareAuth)
	err = prepareAuthRotationFunc(ctx, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("auth preparation failed: %w", err)
	}

	// Stage 3: Rotate daemons (Upstream steps 3-4)
	_ = setStage(database.AuthRotationStageRotateDaemons)
	err = rotateDaemonsFunc(ctx, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("daemon key rotation failed: %w", err)
	}

	// Stage 4: Switch service authentication & prevent insecure keys (Upstream steps 5-7)
	_ = setStage(database.AuthRotationStageSwitchServiceAuth)
	err = switchServiceAuthAndPreventInsecureFunc(ctx, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("service auth switch failed: %w", err)
	}

	// Stage 5: Protect admin access (Upstream step 8)
	_ = setStage(database.AuthRotationStageProtectAdmin)
	err = rotateAdminKeyFunc(ctx, s, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("admin key rotation failed: %w", err)
	}

	// Stage 6: Rotate client credentials (Upstream step 9)
	_ = setStage(database.AuthRotationStageRotateClients)
	clientResult, err := rotateManagedClientsFunc(ctx, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("client key rotation failed: %w", err)
	}

	if clientResult.HasBlockers {
		rec.State = database.AuthRotationStateBlocked
		rec.Blocker = clientResult.BlockerMessage
		if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
			_ = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				return database.SetAuthRotationBlocker(ctx, tx, clientResult.BlockerMessage, "")
			})
		}
		return rec, nil
	}

	// Stage 7: Finish safely (Upstream step 10)
	_ = setStage(database.AuthRotationStageFinishSafely)
	err = finalizeAuthRotationFunc(ctx, s, resolvedKeyType)
	if err != nil {
		return rec, fmt.Errorf("safe finalization failed: %w", err)
	}

	rec.State = database.AuthRotationStateCompleted
	return rec, nil
}

// RotateEntityKey rotates the key of a Ceph authentication entity and returns the new keyring output.
func RotateEntityKey(ctx context.Context, entity string, keyType string) (string, error) {
	args := []string{"auth", "rotate", entity}
	if keyType != "" {
		args = append(args, "--key-type", keyType)
	}

	out, err := cephRunContext(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("failed to rotate key for %s: %w", entity, err)
	}

	return out, nil
}

func writeKeyringAtomically(destPath string, content string, perm os.FileMode) error {
	dir := filepath.Dir(destPath)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpFile := destPath + ".tmp"
	err = os.WriteFile(tmpFile, []byte(content), perm)
	if err != nil {
		return fmt.Errorf("failed to write temporary file %s: %w", tmpFile, err)
	}

	err = os.Rename(tmpFile, destPath)
	if err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to commit file %s: %w", destPath, err)
	}

	return nil
}

// Rotate the key for an entity and atomically writes the resulting keyring to destPath.
func RotateEntityKeyToFile(ctx context.Context, entity string, keyType string, destPath string, perm os.FileMode) error {
	content, err := rotateEntityKeyFunc(ctx, entity, keyType)
	if err != nil {
		return err
	}

	return writeKeyringAtomically(destPath, content, perm)
}

// RotateMonitorKey rotates the mon. key in Ceph auth, updates on-disk keyrings,
// and sequentially restarts monitors verifying quorum between each restart.
func RotateMonitorKey(ctx context.Context, targetKeyType string, monNames []string) error {
	pathConst := constants.GetPathConst()

	// Rotate the mon. credential in Ceph auth database.
	monKeyring, err := rotateEntityKeyFunc(ctx, "mon.", targetKeyType)
	if err != nil {
		return fmt.Errorf("failed to rotate mon. key: %w", err)
	}

	// Update persistent on-disk copies for all monitors.
	for _, monName := range monNames {
		monDataDir := filepath.Join(pathConst.DataPath, "mon", fmt.Sprintf("ceph-%s", monName))
		if _, err := os.Stat(monDataDir); err == nil {
			keyringPath := filepath.Join(monDataDir, "keyring")
			err = writeKeyringAtomically(keyringPath, monKeyring, 0600)
			if err != nil {
				return fmt.Errorf("failed to update mon keyring at %s: %w", keyringPath, err)
			}
		}
	}

	// Sequentially restart monitors, verifying quorum between each restart.
	for _, monName := range monNames {
		logger.Infof("Restarting monitor %s after key rotation", monName)
		err = snapRestartFunc("mon", false)
		if err != nil {
			return fmt.Errorf("failed to restart mon %s: %w", monName, err)
		}

		err = waitForMonQuorumFunc(ctx, monName, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("monitor %s failed to re-enter quorum: %w", monName, err)
		}
	}

	return nil
}

func waitForMonQuorum(ctx context.Context, monName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		quorum, err := getMonQuorumNames(ctx)
		if err == nil && slices.Contains(quorum, monName) {
			return nil
		}
		if timeout == 0 || time.Now().After(deadline) {
			return fmt.Errorf("monitor %q did not rejoin quorum within %v (last err: %v)", monName, timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// RotateMGRKey stops the MGR daemon, rotates its key, updates on-disk keyring,
// starts the daemon, and verifies its recovery.
func RotateMGRKey(ctx context.Context, targetKeyType string, mgrName string) error {
	pathConst := constants.GetPathConst()
	entity := fmt.Sprintf("mgr.%s", mgrName)

	logger.Infof("Rotating key for %s", entity)

	_ = snapStopFunc("mgr", false)

	keyring, err := rotateEntityKeyFunc(ctx, entity, targetKeyType)
	if err != nil {
		return fmt.Errorf("failed to rotate %s key: %w", entity, err)
	}

	mgrDataDir := filepath.Join(pathConst.DataPath, "mgr", fmt.Sprintf("ceph-%s", mgrName))
	if _, err := os.Stat(mgrDataDir); err == nil {
		keyringPath := filepath.Join(mgrDataDir, "keyring")
		err = writeKeyringAtomically(keyringPath, keyring, 0600)
		if err != nil {
			return fmt.Errorf("failed to update mgr keyring at %s: %w", keyringPath, err)
		}
	}

	err = snapStartFunc("mgr", false)
	if err != nil {
		return fmt.Errorf("failed to start mgr.%s: %w", mgrName, err)
	}

	err = waitForMGRReadyFunc(ctx, mgrName, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("mgr.%s did not recover: %w", mgrName, err)
	}

	return nil
}

func waitForMGRReady(ctx context.Context, mgrName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, err := mgrActiveOrStandby(ctx, mgrName)
		if err == nil && ready {
			return nil
		}
		if timeout == 0 || time.Now().After(deadline) {
			return fmt.Errorf("mgr.%q did not become ready within %v (last err: %v)", mgrName, timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// RotateMDSKey stops the MDS daemon, rotates its key, updates on-disk keyring,
// starts the daemon, and verifies its recovery.
func RotateMDSKey(ctx context.Context, targetKeyType string, mdsName string) error {
	pathConst := constants.GetPathConst()
	entity := fmt.Sprintf("mds.%s", mdsName)

	logger.Infof("Rotating key for %s", entity)

	_ = snapStopFunc("mds", false)

	keyring, err := rotateEntityKeyFunc(ctx, entity, targetKeyType)
	if err != nil {
		return fmt.Errorf("failed to rotate %s key: %w", entity, err)
	}

	mdsDataDir := filepath.Join(pathConst.DataPath, "mds", fmt.Sprintf("ceph-%s", mdsName))
	if _, err := os.Stat(mdsDataDir); err == nil {
		keyringPath := filepath.Join(mdsDataDir, "keyring")
		err = writeKeyringAtomically(keyringPath, keyring, 0600)
		if err != nil {
			return fmt.Errorf("failed to update mds keyring at %s: %w", keyringPath, err)
		}
	}

	err = snapStartFunc("mds", false)
	if err != nil {
		return fmt.Errorf("failed to start mds.%s: %w", mdsName, err)
	}

	err = waitForMDSReadyFunc(ctx, mdsName, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("mds.%s did not recover: %w", mdsName, err)
	}

	return nil
}

func waitForMDSReady(ctx context.Context, mdsName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, err := mdsUp(ctx, mdsName)
		if err == nil && ready {
			return nil
		}
		if timeout == 0 || time.Now().After(deadline) {
			return fmt.Errorf("mds.%q did not become ready within %v (last err: %v)", mdsName, timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// RotateOSDKey marks the OSD down, rotates its key, updates on-disk keyring and BlueStore
// label key, restarts the OSD service, and verifies the OSD comes back up.
func RotateOSDKey(ctx context.Context, targetKeyType string, osdID int64) error {
	pathConst := constants.GetPathConst()
	entity := fmt.Sprintf("osd.%d", osdID)

	logger.Infof("Rotating key for %s", entity)

	// 1. Mark OSD down
	_, _ = cephRunContext(ctx, "osd", "down", strconv.FormatInt(osdID, 10))

	// 2. Rotate key
	keyring, err := rotateEntityKeyFunc(ctx, entity, targetKeyType)
	if err != nil {
		return fmt.Errorf("failed to rotate %s key: %w", entity, err)
	}

	// 3. Update keyring file
	osdDataDir := filepath.Join(pathConst.DataPath, "osd", fmt.Sprintf("ceph-%d", osdID))
	if _, err := os.Stat(osdDataDir); err == nil {
		keyringPath := filepath.Join(osdDataDir, "keyring")
		err = writeKeyringAtomically(keyringPath, keyring, 0600)
		if err != nil {
			return fmt.Errorf("failed to update osd keyring at %s: %w", keyringPath, err)
		}

		// 4. Update BlueStore label if block device exists
		blockDev, err := getOSDBlockDevice(osdDataDir)
		if err == nil && blockDev != "" {
			err = setOSDBlueStoreLabelKeyFunc(ctx, blockDev, keyringPath)
			if err != nil {
				logger.Warnf("failed to update bluestore label for osd.%d on %s: %v", osdID, blockDev, err)
			}
		}
	}

	// 5. Restart OSD service
	err = snapRestartFunc("osd", true)
	if err != nil {
		return fmt.Errorf("failed to restart osd service: %w", err)
	}

	// 6. Verify OSD is up
	err = waitForOSDUpFunc(ctx, osdID, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("osd.%d did not come back up: %w", osdID, err)
	}

	return nil
}

func getOSDBlockDevice(osdDataDir string) (string, error) {
	blockLink := filepath.Join(osdDataDir, "block")
	info, err := os.Lstat(blockLink)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return filepath.EvalSymlinks(blockLink)
	}
	return blockLink, nil
}

func isOSDUp(ctx context.Context, osdID int64) (bool, error) {
	output, err := cephRunContext(ctx, "osd", "dump", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to fetch osd dump: %w", err)
	}

	upVal := gjson.Get(output, fmt.Sprintf("osds.#(osd==%d).up", osdID))
	if !upVal.Exists() {
		return false, fmt.Errorf("osd.%d not found in osd map", osdID)
	}

	return upVal.Int() == 1, nil
}

func waitForOSDUp(ctx context.Context, osdID int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		up, err := isOSDUp(ctx, osdID)
		if err == nil && up {
			return nil
		}
		if timeout == 0 || time.Now().After(deadline) {
			return fmt.Errorf("osd.%d did not come up within %v (last err: %v)", osdID, timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// This function executes upstream steps 3 and 4:
// - Rotates the monitor key first, updates persistent copies on disk, and sequentially restarts monitors with quorum checks.
// - Rotates manager, storage, and metadata daemon keys, updating on-disk keyrings and BlueStore labels.
// - Activates and verifies service recovery before proceeding.
// - Verifies that AUTH_INSECURE_SERVICE_KEY_TYPE clears.
func RotateDaemons(ctx context.Context, targetKeyType string) error {
	// Rotate monitor key first
	monNames, err := getMonmapNames(ctx)
	if err != nil {
		return fmt.Errorf("failed to get monitor names for rotation: %w", err)
	}

	err = rotateMonitorKeyFunc(ctx, targetKeyType, monNames)
	if err != nil {
		return fmt.Errorf("monitor key rotation failed: %w", err)
	}

	// Discover all daemon entities in Ceph auth database
	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to dump auth keys for daemon discovery: %w", err)
	}

	// Rotate MGR daemons
	for _, entry := range entries {
		if entry.EntityType == "mgr" && entry.EntityID != "" {
			err = rotateMGRKeyFunc(ctx, targetKeyType, entry.EntityID)
			if err != nil {
				return fmt.Errorf("failed to rotate mgr.%s key: %w", entry.EntityID, err)
			}
		}
	}

	// Rotate MDS daemons
	for _, entry := range entries {
		if entry.EntityType == "mds" && entry.EntityID != "" {
			err = rotateMDSKeyFunc(ctx, targetKeyType, entry.EntityID)
			if err != nil {
				return fmt.Errorf("failed to rotate mds.%s key: %w", entry.EntityID, err)
			}
		}
	}

	// Rotate OSD daemons
	for _, entry := range entries {
		if entry.EntityType == "osd" && entry.EntityID != "" {
			osdID, err := strconv.ParseInt(entry.EntityID, 10, 64)
			if err != nil {
				logger.Warnf("skipping unrecognized osd entity %s: %v", entry.EntityName, err)
				continue
			}
			err = rotateOSDKeyFunc(ctx, targetKeyType, osdID)
			if err != nil {
				return fmt.Errorf("failed to rotate osd.%d key: %w", osdID, err)
			}
		}
	}

	// Upstream Step 4: Confirm AUTH_INSECURE_SERVICE_KEY_TYPE clears
	hw, err := getAuthHealthWarningsFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to check health warnings after daemon rotation: %w", err)
	}

	if hw.InsecureServiceKeyType {
		return fmt.Errorf("AUTH_INSECURE_SERVICE_KEY_TYPE remains active after daemon rotation: %v", hw.InsecureServiceDetails)
	}

	return nil
}

// GetOrCreatePendingKey issues a pending key alongside the current key for the given entity.
func GetOrCreatePendingKey(ctx context.Context, entity string) (string, error) {
	out, err := cephRunContext(ctx, "auth", "get-or-create-pending", entity)
	if err != nil {
		return "", fmt.Errorf("failed to get or create pending key for %s: %w", entity, err)
	}

	return out, nil
}

// CommitPendingKey rotates the pending key into the active position for the given entity.
func CommitPendingKey(ctx context.Context, entity string) error {
	_, err := cephRunContext(ctx, "auth", "commit-pending", entity)
	if err != nil {
		return fmt.Errorf("failed to commit pending key for %s: %w", entity, err)
	}

	return nil
}

// ClearPendingKey removes the pending key for the given entity.
func ClearPendingKey(ctx context.Context, entity string) error {
	_, err := cephRunContext(ctx, "auth", "clear-pending", entity)
	if err != nil {
		return fmt.Errorf("failed to clear pending key for %s: %w", entity, err)
	}

	return nil
}

// WipeRotatingServiceKeys wipes rotating service keys on the monitors.
func WipeRotatingServiceKeys(ctx context.Context) error {
	_, err := cephRunContext(ctx, "auth", "wipe-rotating-service-keys")
	if err != nil {
		return fmt.Errorf("failed to wipe rotating service keys: %w", err)
	}

	return nil
}

// SetOSDBlueStoreLabelKey updates the osd_key label on an OSD's block device using ceph-bluestore-tool.
func SetOSDBlueStoreLabelKey(ctx context.Context, devPath string, keyringPath string) error {
	_, err := common.ProcessExec.RunCommandContext(
		ctx,
		"ceph-bluestore-tool",
		"--dev", devPath,
		"set-label-key",
		"--key", "osd_key",
		"-v", keyringPath,
	)
	if err != nil {
		return fmt.Errorf("failed to set bluestore osd_key label on %s: %w", devPath, err)
	}

	return nil
}

// DumpAuthKeys queries all authentication credentials and keys from Ceph.
func DumpAuthKeys(ctx context.Context) ([]AuthKeyEntry, error) {
	output, err := cephRunContext(ctx, "auth", "dump-keys", "-f", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to dump auth keys: %w", err)
	}

	return parseAuthDumpKeys(output), nil
}

// Parse the JSON output of 'ceph auth dump-keys -f json'.
func parseAuthDumpKeys(output string) []AuthKeyEntry {
	var entries []AuthKeyEntry

	// Handle secrets at either data.secrets or keys.data.secrets.
	secrets := gjson.Get(output, "data.secrets")
	if !secrets.Exists() {
		secrets = gjson.Get(output, "keys.data.secrets")
	}

	for _, sec := range secrets.Array() {
		typeStr := sec.Get("entity.type_str").String()
		id := sec.Get("entity.id").String()
		entityName := typeStr + "." + id
		if typeStr == "mon" && id == "" {
			entityName = "mon."
		}

		keyType := sec.Get("auth.key.type_str").String()
		pendingKeyType := sec.Get("auth.pending_key.type_str").String()
		created := sec.Get("auth.key.created").String()

		entries = append(entries, AuthKeyEntry{
			EntityName:     entityName,
			EntityType:     typeStr,
			EntityID:       id,
			KeyType:        keyType,
			PendingKeyType: pendingKeyType,
			Created:        created,
		})
	}

	return entries
}

// GetAuthHealthWarnings inspects Ceph health detail for CephX-related security warnings.
func GetAuthHealthWarnings(ctx context.Context) (AuthHealthWarnings, error) {
	output, err := cephRunContext(ctx, "health", "detail", "-f", "json")
	if err != nil {
		return AuthHealthWarnings{}, fmt.Errorf("failed to fetch health detail: %w", err)
	}

	return parseAuthHealthWarnings(output), nil
}

// Parse the JSON output of 'ceph health detail -f json'.
func parseAuthHealthWarnings(output string) AuthHealthWarnings {
	var hw AuthHealthWarnings

	checks := gjson.Get(output, "checks")
	if !checks.Exists() {
		return hw
	}

	if check := checks.Get("AUTH_INSECURE_SERVICE_KEY_TYPE"); check.Exists() {
		hw.InsecureServiceKeyType = true
		for _, msg := range check.Get("detail.#.message").Array() {
			hw.InsecureServiceDetails = append(hw.InsecureServiceDetails, msg.String())
		}
	}

	if check := checks.Get("AUTH_INSECURE_SERVICE_TICKETS"); check.Exists() {
		hw.InsecureServiceTickets = true
	}

	if check := checks.Get("AUTH_INSECURE_ROTATING_SERVICE_KEY_TYPE"); check.Exists() {
		hw.InsecureRotatingKeyType = true
	}

	if check := checks.Get("AUTH_INSECURE_KEYS_CREATABLE"); check.Exists() {
		hw.InsecureKeysCreatable = true
	}

	if check := checks.Get("AUTH_INSECURE_CLIENT_KEY_TYPE"); check.Exists() {
		hw.InsecureClientKeyType = true
		for _, msg := range check.Get("detail.#.message").Array() {
			hw.InsecureClientDetails = append(hw.InsecureClientDetails, msg.String())
		}
	}

	if check := checks.Get("AUTH_INSECURE_KEYS_ALLOWED"); check.Exists() {
		hw.InsecureKeysAllowed = true
	}

	return hw
}

// GetClientSessions queries active client sessions across monitors.
func GetClientSessions(ctx context.Context) ([]ClientSessionInfo, error) {
	output, err := cephRunContext(ctx, "tell", "mon.*", "sessions", "-f", "json")
	if err != nil {
		logger.Debugf("tell mon.* sessions failed (%v); falling back to active monitor session list", err)
		output, err = cephRunContext(ctx, "sessions", "-f", "json")
		if err != nil {
			return nil, fmt.Errorf("failed to query monitor sessions: %w", err)
		}
	}

	return parseClientSessions(output), nil
}

// Parse the JSON output from monitor sessions commands.
func parseClientSessions(output string) []ClientSessionInfo {
	var sessions []ClientSessionInfo

	parsed := gjson.Parse(output)
	var rawSessions []gjson.Result

	if parsed.IsArray() {
		for _, item := range parsed.Array() {
			// Some ceph versions return an array of {response: [...]} per monitor.
			resp := item.Get("response")
			if resp.IsArray() {
				rawSessions = append(rawSessions, resp.Array()...)
			} else {
				rawSessions = append(rawSessions, item)
			}
		}
	} else if parsed.IsObject() {
		// Single object response or wrapped in a 'sessions' key.
		if sessArr := parsed.Get("sessions"); sessArr.IsArray() {
			rawSessions = append(rawSessions, sessArr.Array()...)
		} else {
			rawSessions = append(rawSessions, parsed)
		}
	}

	for _, s := range rawSessions {
		name := s.Get("name").String()
		entityName := s.Get("entity_name").String()
		if entityName == "" {
			entityName = name
		}

		conFeatures := s.Get("con_features").Uint()
		release := s.Get("con_features_release").String()
		remoteHost := s.Get("remote_host").String()
		open := s.Get("open").Bool()

		sessions = append(sessions, ClientSessionInfo{
			Name:               name,
			EntityName:         entityName,
			ConFeatures:        conFeatures,
			ConFeaturesRelease: release,
			RemoteHost:         remoteHost,
			Open:               open,
		})
	}

	return sessions
}

// Known older Ceph releases that lack AES256K support.
var legacyInsecureReleases = []string{
	"argonaut", "bobtail", "cuttlefish", "dumpling", "emperor", "firefly",
	"giant", "hammer", "infernalis", "jewel", "kraken", "luminous",
	"mimic", "nautilus", "octopus", "pacific", "quincy"
}

// ClientSupportsKeyType reports whether a connected client's reported software release and features
// support the target cipher type.
func ClientSupportsKeyType(session ClientSessionInfo, targetKeyType string) bool {
	// Standard aes is supported by all clients.
	if targetKeyType == "" || targetKeyType == "aes" {
		return true
	}

	// For aes256k: check if release is explicitly known as legacy/insecure.
	release := strings.ToLower(strings.TrimSpace(session.ConFeaturesRelease))
	if release != "" {
		if slices.Contains(legacyInsecureReleases, release) {
			return false
		}
		return true
	}

	// If release is unknown or empty: if no connection features are negotiated, it's unsafe.
	if session.ConFeatures == 0 {
		return false
	}

	return true
}

// BuildAuthStatus constructs the AuthStatusResponse from the persistent database record and Ceph auth keys.
func BuildAuthStatus(ctx context.Context, s interfaces.StateInterface) (types.AuthStatusResponse, error) {
	var resp types.AuthStatusResponse
	resp.ClientDistribution = make(map[string][]string)

	// 1. Read persistent record from database if available
	if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
		err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			rec, err := database.GetAuthRotation(ctx, tx)
			if err != nil {
				return err
			}
			resp.State = rec.State
			resp.Blocker = rec.Blocker
			resp.TargetKeyType = rec.TargetKeyType
			resp.Detail = rec.Detail
			return nil
		})
		if err != nil {
			logger.Warnf("failed to read auth rotation record from database: %v", err)
		}
	}

	// 2. Query Ceph auth keys to compute client cipher distribution
	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return resp, fmt.Errorf("failed to dump auth keys: %w", err)
	}

	for _, entry := range entries {
		if entry.EntityType == "client" {
			cipher := entry.KeyType
			if cipher == "" {
				cipher = "unknown"
			}
			resp.ClientDistribution[cipher] = append(resp.ClientDistribution[cipher], entry.EntityName)
		}
	}

	// 3. Format Status string based on state and distribution
	if resp.State == database.AuthRotationStateBlocked {
		resp.Status = "blocked"
	} else if len(resp.ClientDistribution) == 1 {
		// All clients use a single cipher
		for cipher := range resp.ClientDistribution {
			resp.Status = fmt.Sprintf("All client %s", cipher)
		}
	} else if len(resp.ClientDistribution) > 1 {
		// Mixed ciphers
		var counts []string
		var lists []string
		// Sort keys for deterministic output
		var ciphers []string
		for c := range resp.ClientDistribution {
			ciphers = append(ciphers, c)
		}
		slices.Sort(ciphers)

		for _, c := range ciphers {
			clients := resp.ClientDistribution[c]
			counts = append(counts, fmt.Sprintf("%d clients on %s", len(clients), c))
			lists = append(lists, fmt.Sprintf("%s: %s", c, strings.Join(clients, ", ")))
		}
		resp.Status = fmt.Sprintf("%s (%s)", strings.Join(counts, ", "), strings.Join(lists, ", "))
	} else {
		resp.Status = "idle"
	}

	return resp, nil
}

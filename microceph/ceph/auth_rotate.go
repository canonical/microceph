package ceph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/tidwall/gjson"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
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
	getMonCiphersFunc                  = GetMonCiphers
	setMonAllowedCiphersFunc           = SetMonAllowedCiphers
	setMonPreferredCipherFunc          = SetMonPreferredCipher
	setMonServiceCipherFunc            = SetMonServiceCipher
	setMonAllowInsecureKeyFunc         = SetMonAllowInsecureKey
	getMonAllowInsecureKeyFunc         = GetMonAllowInsecureKey
	rotateEntityKeyFunc                = RotateEntityKey
	rotateEntityKeyToFileFunc          = RotateEntityKeyToFile
	getOrCreatePendingKeyFunc          = GetOrCreatePendingKey
	getEntityKeyringFunc               = GetEntityKeyring
	commitPendingKeyFunc               = CommitPendingKey
	clearPendingKeyFunc                = ClearPendingKey
	wipeRotatingServiceKeysFunc        = WipeRotatingServiceKeys
	dumpAuthKeysFunc                   = DumpAuthKeys
	getAuthHealthWarningsFunc          = GetAuthHealthWarnings
	getClientSessionsFunc              = GetClientSessions
	resolveTargetKeyTypeFunc           = ResolveTargetKeyType
	checkAuthRotationReadinessFunc     = CheckAuthRotationReadiness
	checkClusterMembersReachableFunc   = checkClusterMembersReachable
	checkMonQuorumReadyFunc            = checkMonQuorumReady
	checkCipherCompatibilityFunc       = checkCipherCompatibility
	prepareAuthRotationFunc            = PrepareAuthRotation
	snapStartFunc                      = snapStart
	snapRestartFunc                    = snapRestart
	snapCheckActiveFunc                = snapCheckActive
	waitForMonQuorumFunc               = waitForMonQuorum
	waitForMGRReadyFunc                = waitForMGRReady
	waitForMDSReadyFunc                = waitForMDSReady
	waitForOSDUpFunc                   = waitForOSDUp
	getMonQuorumNamesFunc              = getMonQuorumNames
	getMonmapNamesFunc                 = getMonmapNames
	mgrActiveOrStandbyFunc             = mgrActiveOrStandby
	mdsUpFunc                          = mdsUp
	isOSDUpFunc                        = isOSDUp
	rotateMonKeyAuthFunc               = RotateMonKeyAuth
	deployMonKeyringAndRestartFunc     = deployMonKeyringAndRestart
	rotateLocalMGRKeyFunc              = RotateLocalMGRKey
	rotateLocalMDSKeyFunc              = RotateLocalMDSKey
	rotateLocalOSDKeysFunc             = RotateLocalOSDKeys
	getLocalOSDIDsFunc                 = getLocalOSDIDs
	rotateMemberDaemonsFunc            = RotateMemberDaemons
	sendMemberAuthRotateFunc           = client.SendMemberAuthRotateToClusterMembers
	sendUpdateClientConfFunc           = client.SendUpdateClientConfRequestToClusterMembers
	sendClientDeployFunc               = client.SendClientDeployToClusterMembers
	clientConsumerMembersFunc          = clientConsumerMembers
	memberConsumesEntityFunc           = memberConsumesEntity
	getServicesFunc                    = database.GetServices
	deployClientKeyringsLocallyFunc    = deployClientKeyringsLocally
	distributeClientKeyringsFunc       = distributeClientKeyrings
	rotateDaemonsFunc                  = RotateDaemons
	activateAndCheckFunc               = ActivateAndCheck
	switchServiceAuthenticationFunc    = SwitchServiceAuthentication
	preventNewInsecureKeysFunc         = PreventNewInsecureKeys
	createAdminBackupKeyFunc           = createAdminBackupKey
	verifyAdminAccessFunc              = verifyAdminAccess
	deleteAdminBackupKeyFunc           = deleteAdminBackupKey
	updateAdminKeyringInDBFunc         = updateAdminKeyringInDB
	updateAdminKeyringFilesFunc        = updateAdminKeyringFiles
	rotateAdminKeyFunc                 = RotateAdminKey
	rotateSingleClientKeyFunc          = RotateSingleClientKey
	rotateManagedClientsFunc           = RotateManagedClients
	inspectClientSessionAdvisoriesFunc = InspectClientSessionAdvisories
	disallowInsecureLegacyCiphersFunc  = DisallowInsecureLegacyCiphers
	finalizeAuthRotationFunc           = FinalizeAuthRotation
	runAuthRotationPipelineFunc        = runAuthRotationPipeline
	buildAuthStatusFunc                = BuildAuthStatus
)

// Query the monitor map and return the current cipher config.
func GetMonCiphers(ctx context.Context) (MonCiphers, error) {
	output, err := boundedCephRun(ctx, "mon", "dump", "-f", "json")
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
	_, err := boundedCephRun(ctx, "mon", "set", "auth_allowed_ciphers", cipherArg)
	if err != nil {
		return fmt.Errorf("failed to set auth_allowed_ciphers to %q: %w", cipherArg, err)
	}

	return nil
}

func SetMonPreferredCipher(ctx context.Context, cipher string) error {
	_, err := boundedCephRun(ctx, "mon", "set", "auth_preferred_cipher", cipher)
	if err != nil {
		return fmt.Errorf("failed to set auth_preferred_cipher to %q: %w", cipher, err)
	}

	return nil
}

func SetMonServiceCipher(ctx context.Context, cipher string) error {
	_, err := boundedCephRun(ctx, "mon", "set", "auth_service_cipher", cipher)
	if err != nil {
		return fmt.Errorf("failed to set auth_service_cipher to %q: %w", cipher, err)
	}

	return nil
}

// Configure whether new keys with insecure ciphers are allowed.
func SetMonAllowInsecureKey(ctx context.Context, allow bool) error {
	val := strconv.FormatBool(allow)
	_, err := boundedCephRun(ctx, "config", "set", "mon", "mon_auth_allow_insecure_key", val)
	if err != nil {
		return fmt.Errorf("failed to set mon_auth_allow_insecure_key to %s: %w", val, err)
	}

	return nil
}

// GetMonAllowInsecureKey reads whether monitors will create keys with
// insecure ciphers (the mon_auth_allow_insecure_key option, effective value
// including defaults).
func GetMonAllowInsecureKey(ctx context.Context) (bool, error) {
	output, err := boundedCephRun(ctx, "config", "get", "mon", "mon_auth_allow_insecure_key", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to read mon_auth_allow_insecure_key: %w", err)
	}

	// The formatter may wrap the value as a JSON string, a JSON bool or bare
	// text; only an explicit "false" means disabled.
	return !strings.Contains(strings.ToLower(output), "false"), nil
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

// resolveRotationTargetKeyType determines the key type a rotation runs with.
// A fresh rotation resolves an empty request from the current preferred
// cipher. A resume of an incomplete record must NOT resolve: the preferred
// cipher is only switched by prepare_auth, so resolving an empty request
// would reject resuming a rotation that stopped before it (its target still
// differs from the yet-unswitched preferred cipher). Instead the explicit
// value is passed through as-is (possibly empty) for InitOrResumeAuthRotation
// to match against the record, whose target the caller inherits after init.
// A record without a target (legacy state) resolves as before.
func resolveRotationTargetKeyType(ctx context.Context, resuming bool, recordTarget string, targetKeyType string) (string, error) {
	if resuming && recordTarget != "" {
		return targetKeyType, nil
	}

	return resolveTargetKeyTypeFunc(ctx, targetKeyType)
}

func checkCipherCompatibility(ctx context.Context, targetKeyType string) error {
	switch targetKeyType {
	case "aes":
		return nil
	case "aes256k":
		// The monitors themselves gate aes256k key minting on the
		// cephx_auth_aes256k mon feature. min_mon_release cannot tell support
		// apart: the cipher arrived in point releases (19.2.6, 20.2.4) that
		// share their major version with releases lacking it.
		output, err := boundedCephRun(ctx, "mon", "feature", "ls", "-f", "json")
		if err != nil {
			return fmt.Errorf("failed to fetch monitor features for compatibility check: %w", err)
		}
		if !strings.Contains(output, "cephx_auth_aes256k") {
			return fmt.Errorf("monitors do not support the cephx_auth_aes256k feature; upgrade Ceph to a release with aes256k support (19.2.6, 20.2.4 or newer) before rotating to aes256k")
		}

		// Cluster members are refreshed one at a time, so a member running
		// daemons whose binaries cannot use aes256k would be locked out the
		// next time those daemons re-authenticate with their new keys. Refuse
		// until every daemon the cluster knows about runs a supporting release.
		output, err = boundedCephRun(ctx, "versions", "-f", "json")
		if err != nil {
			return fmt.Errorf("failed to fetch daemon versions for compatibility check: %w", err)
		}
		err = checkDaemonVersionsSupportAES256K(output)
		if err != nil {
			return err
		}

		return nil
	default:
		return fmt.Errorf("unsupported key type %q; supported types are 'aes' and 'aes256k'", targetKeyType)
	}
}

// checkDaemonVersionsSupportAES256K verifies that every daemon the cluster
// reports in 'ceph versions' output runs a release that supports aes256k.
func checkDaemonVersionsSupportAES256K(output string) error {
	for daemonType, versions := range gjson.Parse(output).Map() {
		if daemonType == "overall" {
			// Aggregate over the per-daemon entries already checked.
			continue
		}
		for version := range versions.Map() {
			if !versionSupportsAES256K(version) {
				return fmt.Errorf(
					"%s daemons on release %s do not support aes256k (supported from 19.2.6, 20.2.4 or any newer release); upgrade every cluster member before rotating to aes256k",
					daemonType, version)
			}
		}
	}

	return nil
}

// cephReleaseNums extracts the first numeric major.minor.patch triple from a
// version string: 'ceph versions' keys use the full form ("ceph version
// 19.2.6 (hash) squid (stable)", optionally with a build suffix), not the
// bare release.
var cephReleaseNums = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// versionSupportsAES256K reports whether a Ceph release supports aes256k: the
// cipher arrived in squid 19.2.6 and tentacle 20.2.4, and is present in any
// later release series.
func versionSupportsAES256K(version string) bool {
	nums := cephReleaseNums.FindStringSubmatch(version)
	if nums == nil {
		return false
	}

	major, err := strconv.Atoi(nums[1])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(nums[2])
	if err != nil {
		return false
	}
	patch, err := strconv.Atoi(nums[3])
	if err != nil {
		return false
	}

	switch {
	case major >= 21:
		return true
	case major == 20:
		return minor > 2 || (minor == 2 && patch >= 4)
	case major == 19:
		return minor == 2 && patch >= 6
	default:
		return false
	}
}

func checkMonQuorumReady(ctx context.Context) error {
	// Each mon query gets the shared per-call ceiling: mon-side commands can
	// block indefinitely when quorum is lost, and under the rotation's
	// detached context a hung call would wedge the rotation while the lock
	// heartbeat keeps renewing.
	monmapCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
	monmap, err := getMonmapNamesFunc(monmapCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to get monitor map: %w", err)
	}

	quorumCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
	quorum, err := getMonQuorumNamesFunc(quorumCtx)
	cancel()
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

	// Refuse to weaken a hardened cluster: an insecure target is only
	// acceptable when it is already allowed and insecure key creation has
	// not been disabled. 'mon set' does not gate these changes, and allowing
	// an insecure cipher flips mon_auth_allow_insecure_key back to its
	// permissive default (AuthMonitor::check_health), so this step must
	// never re-allow an insecure cipher that has been removed.
	if targetKeyType != "aes256k" {
		if !slices.Contains(ciphers.AuthAllowedCiphers, targetKeyType) {
			return fmt.Errorf(
				"target cipher %q is insecure and not allowed by auth_allowed_ciphers %v: MicroCeph never re-allows insecure ciphers; rotate to aes256k or adjust the cluster's cipher policy manually",
				targetKeyType, ciphers.AuthAllowedCiphers)
		}

		allowInsecure, err := getMonAllowInsecureKeyFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to read mon_auth_allow_insecure_key: %w", err)
		}
		if !allowInsecure {
			return fmt.Errorf(
				"target cipher %q is insecure and mon_auth_allow_insecure_key is false: this cluster is hardened against insecure keys, so rotating back to %q is refused",
				targetKeyType, targetKeyType)
		}
	}

	// Step 1: Allow the requested type without dropping existing clients.
	if !slices.Contains(ciphers.AuthAllowedCiphers, targetKeyType) {
		// Only reachable for the secure target: insecure targets were
		// refused above and are never re-added here.
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

// ActivateAndCheck confirms upstream step 4: after daemon rotation every
// service daemon has re-authenticated with its new key and the
// AUTH_INSECURE_SERVICE_KEY_TYPE warning has cleared. Split from
// RotateDaemons so a resumed rotation re-checks health without re-rotating
// and restarting every daemon again.
func ActivateAndCheck(ctx context.Context) error {
	hw, err := getAuthHealthWarningsFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to check health warnings after daemon rotation: %w", err)
	}

	if hw.InsecureServiceKeyType {
		return fmt.Errorf("AUTH_INSECURE_SERVICE_KEY_TYPE remains active after daemon rotation: %v", hw.InsecureServiceDetails)
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
// RotateAdminKey rotates client.admin with recovery safeguards (upstream step
// 8). A run-unique backup credential is created first and kept — together with
// a 0600 copy of its keyring under the snap's conf dir — until the new key is
// verified, so a failure never removes the recovery path; errors past that
// point reference the backup instead. The new key is issued as a pending key
// and persisted to the shared DB row and the keyring files before commit-pending
// makes it active, so a crash at any point leaves a consistent, recoverable
// state.
func RotateAdminKey(ctx context.Context, s interfaces.StateInterface, targetKeyType string) error {
	pathConst := constants.GetPathConst()

	// client.admin-backup is the name the upstream procedure tells operators
	// to create by hand: never touch one of those. A run-unique name also
	// keeps retried runs from clobbering each other's recovery credential.
	suffix := make([]byte, 4)
	_, err := rand.Read(suffix)
	if err != nil {
		return fmt.Errorf("failed to generate admin recovery credential name: %w", err)
	}
	backupName := fmt.Sprintf("client.admin-backup-%s", hex.EncodeToString(suffix))
	backupKeyringPath := filepath.Join(pathConst.ConfPath, backupName+".keyring")

	// Create the recovery credential; its keyring is durable, not private
	// tmp, so it survives a crash and can be referenced in error messages.
	err = createAdminBackupKeyFunc(ctx, backupName, backupKeyringPath)
	if err != nil {
		return fmt.Errorf("failed to create admin recovery credential: %w", err)
	}

	err = verifyAdminAccessFunc(ctx, backupName, backupKeyringPath)
	if err != nil {
		// The backup itself is unusable and nothing was changed yet: remove
		// it rather than leaving a broken credential behind.
		_ = deleteAdminBackupKeyFunc(context.Background(), backupName)
		_ = os.Remove(backupKeyringPath)
		return fmt.Errorf("admin recovery credential failed verification: %w", err)
	}

	// From the point a pending key exists, the backup is the recovery path
	// and must never be removed automatically; failures reference it instead.
	failAfterIssue := func(step string, err error) error {
		return fmt.Errorf(
			"%s: %w; the recovery credential %s remains available (keyring: %s); use it to restore access if needed, then remove it with 'ceph auth del %s' and delete the keyring file",
			step, err, backupName, backupKeyringPath, backupName)
	}

	// Issue the new admin key as a pending key alongside the active one so
	// the secret is persisted before it becomes active.
	pendingKey, err := getOrCreatePendingKeyFunc(ctx, "client.admin")
	if err != nil {
		return failAfterIssue("failed to issue pending key for client.admin", err)
	}

	// get-or-create-pending mints with the mon's auth_preferred_cipher, which
	// a single-client admin run does not prepare; verify and remint if stale.
	pendingKey, err = ensurePendingKeyType(ctx, "client.admin", pendingKey, targetKeyType)
	if err != nil {
		return failAfterIssue("pending key cipher verification failed", err)
	}

	// Persist the pending secret before it becomes active: the shared DB row
	// (every member's daemon re-renders ceph.keyring from it) and the local
	// keyring files. A crash here leaves either the old key active with the
	// old files, or the pending key active with the new files: both work.
	err = updateAdminKeyringInDBFunc(ctx, s, pendingKey)
	if err != nil {
		return failAfterIssue("failed to update admin keyring in database", err)
	}

	err = updateAdminKeyringFilesFunc(ctx, s, pendingKey)
	if err != nil {
		return failAfterIssue("failed to update admin keyring files", err)
	}

	// Commit: the pending key becomes the active key, retiring the old one.
	err = commitPendingKeyFunc(ctx, "client.admin")
	if err != nil {
		return failAfterIssue("failed to commit pending key for client.admin", err)
	}

	// Verify access with the newly deployed keyring before removing the
	// recovery credential: the backup is removed only once everything checks
	// out.
	adminKeyringPath := filepath.Join(pathConst.ConfPath, constants.CephAdminKeyringFileName)
	err = verifyAdminAccessFunc(ctx, "client.admin", adminKeyringPath)
	if err != nil {
		return failAfterIssue("new client.admin key failed verification", err)
	}

	// Success: remove the recovery credential.
	err = deleteAdminBackupKeyFunc(context.Background(), backupName)
	if err != nil {
		logger.Warnf("failed to delete admin recovery credential %s after successful rotation: %v", backupName, err)
	}
	err = os.Remove(backupKeyringPath)
	if err != nil && !os.IsNotExist(err) {
		logger.Warnf("failed to remove admin recovery keyring %s after successful rotation: %v", backupKeyringPath, err)
	}

	return nil
}

func createAdminBackupKey(ctx context.Context, backupName string, backupKeyringPath string) error {
	dir := filepath.Dir(backupKeyringPath)
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return fmt.Errorf("failed to create directory for admin backup keyring: %w", err)
	}

	_, err = boundedCephRun(ctx, "auth", "get-or-create", backupName,
		"mon", "allow *",
		"osd", "allow *",
		"mds", "allow *",
		"mgr", "allow *",
		"-o", backupKeyringPath,
	)
	if err != nil {
		return fmt.Errorf("failed to create %s credential: %w", backupName, err)
	}

	// The keyring holds a full-caps admin-equivalent secret: restrict it to
	// the owner regardless of the umask ceph wrote it with.
	err = os.Chmod(backupKeyringPath, 0600)
	if err != nil {
		return fmt.Errorf("failed to restrict admin backup keyring permissions: %w", err)
	}

	return nil
}

func verifyAdminAccess(ctx context.Context, entityName string, keyringPath string) error {
	_, err := boundedCephRun(ctx, "-n", entityName, "-k", keyringPath, "auth", "ls")
	if err != nil {
		return fmt.Errorf("failed to verify access for %s using %s: %w", entityName, keyringPath, err)
	}
	return nil
}

func deleteAdminBackupKey(ctx context.Context, backupName string) error {
	_, err := boundedCephRun(ctx, "auth", "del", backupName)
	if err != nil {
		logger.Warnf("failed to delete temporary backup credential %s: %v", backupName, err)
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

	// Update remote members if cluster is available. A member that misses the
	// update keeps authenticating with the key that the commit below revokes,
	// so the failure must fail the rotation: at this point the pending key is
	// not yet active, so a retry resumes cleanly and re-distributes.
	if s != nil && s.ClusterState() != nil && s.ClusterState().Connect() != nil {
		err = sendUpdateClientConfFunc(ctx, s)
		if err != nil {
			return fmt.Errorf("failed to distribute updated admin keyring to remote members: %w", err)
		}
	}

	return nil
}

// The outcome of rotating managed clients plus any blockers. External
// consumers are only ever blocked by name (unmanaged credentials); session
// compatibility is advisory and never blocks.
type ClientRotationResult struct {
	RotatedClients   []string
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
//
// Managed entities are those whose keys MicroCeph deploys locally and can
// redistribute after rotation. Credentials held by other parties are NOT
// managed, even though the entities exist in this cluster's auth DB:
//   - client.<remote> (cluster export): the peer holds this key.
//   - <remote>.keyring files on disk are the remote cluster's key for our
//     identity there (remote import), used to reach the remote; they are not
//     a local entity's keyring and must never be used as a distribution path.
//   - client.fsmir-* (fs mirror peer bootstrap): the remote site's
//     cephfs-mirror daemon holds this key.
//
// Such entities are reported as unmanaged (manual rotation) instead.
func IsMicroCephManagedClient(entityName string) bool {
	norm := NormalizeClientName(entityName)
	if norm == "client.admin" || norm == "client.radosgw.gateway" {
		return true
	}
	return strings.HasPrefix(norm, "client.rbd-mirror.") ||
		strings.HasPrefix(norm, "client.cephfs-mirror.") ||
		strings.HasPrefix(norm, "client.nfs.") ||
		strings.HasPrefix(norm, "client.bootstrap-")
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
		// NFS Ganesha client (client.nfs.<cluster-id>.<host>): its keyring lives
		// in the ganesha config dir of the host running that NFS instance. The
		// entity name, not the mere presence of the ganesha dir, identifies the
		// client; on an NFS node every other client must stay unclassified here.
		if strings.HasPrefix(norm, "client.nfs.") {
			ganeshaKeyring := filepath.Join(pathConst.ConfPath, "ganesha", "keyring")
			if _, err := os.Stat(ganeshaKeyring); err == nil {
				return []string{ganeshaKeyring}, "nfs"
			}
			return nil, ""
		}
	}
	return nil, ""
}

// clientConsumerMembers returns the cluster members that consume the client
// credential and must receive the new keyring before it is committed
// cluster-wide: every member running the service for shared credentials
// (radosgw gateway), or the member named in the entity for member-specific
// credentials (nfs ganesha, rbd-mirror, cephfs-mirror). Credentials without
// persistent consumers (bootstrap keys, used only by joining daemons) return
// no members.
func clientConsumerMembers(ctx context.Context, s interfaces.StateInterface, entity string) ([]string, error) {
	norm := NormalizeClientName(entity)
	switch {
	case norm == "client.radosgw.gateway":
		// Shared credential: every member running the rgw service consumes it.
		return membersRunningService(ctx, s, "rgw")
	case strings.HasPrefix(norm, "client.rbd-mirror."):
		return []string{strings.TrimPrefix(norm, "client.rbd-mirror.")}, nil
	case strings.HasPrefix(norm, "client.cephfs-mirror."):
		return []string{strings.TrimPrefix(norm, "client.cephfs-mirror.")}, nil
	case strings.HasPrefix(norm, "client.nfs."):
		// client.nfs.<cluster-id>.<hostname>: the consumer is the member named
		// by the hostname, the last dot-separated component (cluster IDs are
		// UUIDs without dots, and member names are short hostnames).
		remainder := strings.TrimPrefix(norm, "client.nfs.")
		parts := strings.Split(remainder, ".")
		return []string{parts[len(parts)-1]}, nil
	default:
		return nil, nil
	}
}

// memberConsumesEntity reports whether this member consumes the client
// credential and must deploy its keyring locally: hostname-suffixed entities
// (nfs ganesha, rbd/cephfs mirrors) only match when they name this member, and
// the shared radosgw gateway only when this member runs the rgw service.
// Without cluster state (unit tests) the service check answers yes, deferring
// the decision to local path existence.
func memberConsumesEntity(ctx context.Context, s interfaces.StateInterface, localMember string, entity string) bool {
	norm := NormalizeClientName(entity)
	switch {
	case norm == "client.radosgw.gateway":
		if s == nil || s.ClusterState() == nil || s.ClusterState().Database() == nil {
			return true
		}
		return localMemberRunsService(ctx, s, localMember, "rgw")
	case strings.HasPrefix(norm, "client.rbd-mirror."):
		return strings.TrimPrefix(norm, "client.rbd-mirror.") == localMember
	case strings.HasPrefix(norm, "client.cephfs-mirror."):
		return strings.TrimPrefix(norm, "client.cephfs-mirror.") == localMember
	case strings.HasPrefix(norm, "client.nfs."):
		remainder := strings.TrimPrefix(norm, "client.nfs.")
		parts := strings.Split(remainder, ".")
		return parts[len(parts)-1] == localMember
	}

	return false
}

// membersRunningService returns the members that have the given service
// enabled, from the cluster database.
func membersRunningService(ctx context.Context, s interfaces.StateInterface, service string) ([]string, error) {
	if s == nil || s.ClusterState() == nil || s.ClusterState().Database() == nil {
		return nil, nil
	}

	var members []string
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		services, err := getServicesFunc(ctx, tx, database.ServiceFilter{Service: &service})
		if err != nil {
			return err
		}
		for _, svc := range services {
			members = append(members, svc.Member)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query members running service %q: %w", service, err)
	}

	return members, nil
}

// localMemberRunsService reports whether the given member has the service
// enabled. Without cluster state (unit tests) it answers yes.
func localMemberRunsService(ctx context.Context, s interfaces.StateInterface, member string, service string) bool {
	if s == nil || s.ClusterState() == nil || s.ClusterState().Database() == nil {
		return true
	}

	runs := false
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		services, err := getServicesFunc(ctx, tx, database.ServiceFilter{Member: &member, Service: &service})
		if err != nil {
			return err
		}
		runs = len(services) > 0
		return nil
	})
	if err != nil {
		logger.Warnf("failed to query service %q on member %q: %v", service, member, err)
		return false
	}

	return runs
}

// deployClientKeyringsLocally writes client keyrings to this member's local
// consumer paths, restarting the associated service when activate is set.
// Entities this member does not consume are skipped, so a foreign entity's
// keyring never replaces the local one (e.g. node B's NFS identity must not
// overwrite node A's ganesha keyring). Returns whether the entity was
// deployed.
func deployClientKeyringsLocally(ctx context.Context, s interfaces.StateInterface, localMember string, entity string, pendingSecret string, finalKeyring string, activate bool) (bool, error) {
	if !memberConsumesEntityFunc(ctx, s, localMember, entity) {
		return false, nil
	}

	paths, service := getClientKeyringPaths(entity)
	if len(paths) == 0 {
		return false, nil
	}

	if pendingSecret != "" {
		for _, p := range paths {
			err := writeClientKeyring(p, entity, pendingSecret)
			if err != nil {
				return false, fmt.Errorf("failed to write client keyring %s: %w", p, err)
			}
		}
	}

	if finalKeyring != "" {
		for _, p := range paths {
			err := writeKeyringAtomically(p, finalKeyring, 0600)
			if err != nil {
				return false, fmt.Errorf("failed to finalize client keyring %s: %w", p, err)
			}
		}
	}

	if activate && service != "" {
		// A live daemon holds the old secret in memory: it must reload so it
		// picks up the keyring carrying the pending key, or it loses access
		// when the commit retires the old key. A failed reload therefore
		// fails the deployment, and the caller must not commit.
		err := snapRestartFunc(service, true)
		if err != nil {
			return false, fmt.Errorf("failed to reload service %s after deploying the %s keyring: %w", service, entity, err)
		}

		// Verify the consumer actually came back up: a daemon (re)loads its
		// keyring file at startup, so a service that is active after the
		// reload is a consumer holding the new credential. An inactive one
		// proves nothing about uptake and must fail the deployment.
		if err := snapCheckActiveFunc(service); err != nil {
			return false, fmt.Errorf("service %s is not running after reload; consumer of %s has not loaded the new credential: %w", service, entity, err)
		}
	}

	return true, nil
}

// DeployClientKeyrings writes the given client keyrings to this member's local
// consumer paths (exported for the per-member client deployment endpoint).
// Entities this member does not consume are skipped. Returns the entities
// deployed.
func DeployClientKeyrings(ctx context.Context, s interfaces.StateInterface, localMember string, pending map[string]string, finalize map[string]string) ([]string, error) {
	entities := make([]string, 0, len(pending)+len(finalize))
	seen := map[string]bool{}
	for entity := range pending {
		if !seen[entity] {
			entities = append(entities, entity)
			seen[entity] = true
		}
	}
	for entity := range finalize {
		if !seen[entity] {
			entities = append(entities, entity)
			seen[entity] = true
		}
	}
	slices.Sort(entities)

	deployed := []string{}
	for _, entity := range entities {
		ok, err := deployClientKeyringsLocally(ctx, s, localMember, entity, pending[entity], finalize[entity], pending[entity] != "")
		if err != nil {
			return deployed, err
		}
		if ok {
			deployed = append(deployed, entity)
		}
	}

	return deployed, nil
}

// distributeClientKeyrings deploys a client keyring to every consumer member:
// remote members through the per-member client deployment endpoint (members
// that do not consume the entity skip it), and the coordinator itself through
// the local deploy path. Returns the members that confirmed deployment of the
// entity.
func distributeClientKeyrings(ctx context.Context, s interfaces.StateInterface, localMember string, entity string, pendingSecret string, finalKeyring string, activate bool) (map[string]bool, error) {
	confirmed := map[string]bool{}

	if s != nil && s.ClusterState() != nil {
		req := types.MemberClientRotateRequest{}
		if pendingSecret != "" {
			req.PendingKeys = map[string]string{entity: pendingSecret}
		}
		if finalKeyring != "" {
			req.Finalize = map[string]string{entity: finalKeyring}
		}

		deployedBy, err := sendClientDeployFunc(ctx, s.ClusterState(), req)
		if err != nil {
			return confirmed, fmt.Errorf("client keyring deployment to remote members failed: %w", err)
		}
		for member, entities := range deployedBy {
			if slices.Contains(entities, entity) {
				confirmed[member] = true
			}
		}
	}

	deployed, err := deployClientKeyringsLocallyFunc(ctx, s, localMember, entity, pendingSecret, finalKeyring, activate)
	if err != nil {
		return confirmed, err
	}
	if deployed {
		confirmed[localMember] = true
	}

	return confirmed, nil
}

// InspectClientSessionAdvisories reports connected clients whose reported
// release is known to predate aes256k. ADVISORY ONLY — the result must not
// gate rotation: con_features_release is derived from connection feature
// bits, and aes256k added no client feature bit, so a client from a point
// release without the cipher reports the same release name as one with it,
// and kernel clients report stale release names regardless of capability.
// Sessions are only visible while open, and client.admin is not covered.
// External consumers are instead gated by the explicit unmanaged-credential
// blocker, which makes the operator deal with them by name.
func InspectClientSessionAdvisories(ctx context.Context, targetKeyType string) (map[string]string, error) {
	sessions, err := getClientSessionsFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query client sessions: %w", err)
	}

	advisories := make(map[string]string)
	for _, s := range sessions {
		if !s.Open {
			continue
		}
		if !ClientSupportsKeyType(s, targetKeyType) {
			entity := NormalizeClientName(s.EntityName)
			advisories[entity] = fmt.Sprintf("connected session on host %q reports client release %q which predates aes256k", s.RemoteHost, s.ConFeaturesRelease)
		}
	}

	return advisories, nil
}

// RotateSingleClientKey issues a pending key alongside the old key, deploys the
// pending keyring to EVERY consumer of the credential — remote consumer members
// through the per-member client deployment endpoint, the coordinator itself
// through its local consumer paths — and only commits the pending key once
// every consumer confirmed deployment, so the commit never retires a key that
// consumers are still using.
func RotateSingleClientKey(ctx context.Context, s interfaces.StateInterface, clientName string, targetKeyType string) error {
	norm := NormalizeClientName(clientName)
	localMember := ""
	if s != nil && s.ClusterState() != nil {
		localMember = s.ClusterState().Name()
	}
	logger.Infof("Rotating client credential for %s to %s", norm, targetKeyType)

	// 1. Issue a pending key alongside the old key.
	pendingKey, err := getOrCreatePendingKeyFunc(ctx, norm)
	if err != nil {
		return fmt.Errorf("failed to issue pending key for %s: %w", norm, err)
	}

	// 1b. Verify the minted pending key cipher before anything is distributed.
	//     get-or-create-pending takes no key-type argument: AuthMonitor mints
	//     pending keys with the mon's auth_preferred_cipher, which the full
	//     pipeline sets in PrepareAuthRotation but single-client mode must not
	//     touch (it is a cluster-wide setting). Committing an unverified key
	//     would silently install a weaker cipher than requested on clusters
	//     that were never prepared.
	pendingKey, err = ensurePendingKeyType(ctx, norm, pendingKey, targetKeyType)
	if err != nil {
		return err
	}

	// 2. Determine the consumer members: every member running the service for
	//    shared credentials (radosgw gateway), or the member named in the
	//    entity for member-specific credentials (nfs, mirrors).
	consumers, err := clientConsumerMembersFunc(ctx, s, norm)
	if err != nil {
		return fmt.Errorf("failed to determine consumers of %s: %w", norm, err)
	}

	// 3. Deploy the pending keyring to every consumer BEFORE committing.
	//    The deployed file carries the PENDING key as the active key:
	//    get-or-create-pending's raw plaintext output must never be written,
	//    because KeyRing::decode has no notion of a pending key and rejects
	//    the file with malformed_input. A deployment failure is fatal: the
	//    commit below retires the old key, and a consumer left on it would be
	//    unable to re-authenticate.
	confirmed, err := distributeClientKeyringsFunc(ctx, s, localMember, norm, pendingKey, "", true)
	if err != nil {
		return fmt.Errorf("failed to deploy pending keyring for %s: %w", norm, err)
	}
	for _, member := range consumers {
		if !confirmed[member] {
			return fmt.Errorf("consumer member %s did not confirm deployment of %s; not committing", member, norm)
		}
	}

	// 4. Commit pending key into active position, retiring the old key. Safe:
	//    every consumer now holds the pending keyring.
	err = commitPendingKeyFunc(ctx, norm)
	if err != nil {
		return fmt.Errorf("failed to commit pending key for %s: %w", norm, err)
	}

	// 5. Finalize the on-disk keyrings on every consumer with the canonical
	//    'auth get' output (the committed pending key is now the active key),
	//    so the files also carry the entity caps and match the auth DB state.
	committedKeyring, err := getEntityKeyringFunc(ctx, norm)
	if err != nil {
		return fmt.Errorf("failed to fetch committed keyring for %s: %w", norm, err)
	}
	if _, err := distributeClientKeyringsFunc(ctx, s, localMember, norm, "", committedKeyring, false); err != nil {
		// Non-fatal: consumers already hold a working keyring (the pending
		// key is now the committed key); the canonical refresh is cosmetic.
		logger.Warnf("client keyring finalization for %s incomplete: %v", norm, err)
	}

	return nil
}

// getPendingKeyType looks up the entity's pending key cipher in the auth dump.
func getPendingKeyType(ctx context.Context, entity string) (string, error) {
	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to verify pending key cipher for %s: %w", entity, err)
	}
	for _, entry := range entries {
		if entry.EntityName == entity {
			return entry.PendingKeyType, nil
		}
	}
	return "", fmt.Errorf("failed to verify pending key cipher for %s: entity not found in auth dump", entity)
}

// ensurePendingKeyType verifies that the pending key minted for the entity
// matches the requested cipher type, reminting once if a stale pending key was
// minted before the cluster's auth_preferred_cipher was prepared:
// get-or-create-pending reuses an existing pending key regardless of its
// cipher, so the stale one must be cleared first (it was never distributed,
// since verification failed before that point). Returns the verified pending
// secret. A dump that does not report a pending key type ("none") cannot be
// verified for this format and proceeds with a warning only.
func ensurePendingKeyType(ctx context.Context, entity string, pendingKey string, targetKeyType string) (string, error) {
	pendingType, err := getPendingKeyType(ctx, entity)
	if err != nil {
		return "", err
	}
	if pendingType == "" || pendingType == "none" {
		logger.Warnf("pending key cipher for %s not reported by auth dump (%q); proceeding without verification", entity, pendingType)
		return pendingKey, nil
	}
	if pendingType == targetKeyType {
		return pendingKey, nil
	}

	logger.Warnf("pending key for %s minted as cipher %q instead of %q; reminting", entity, pendingType, targetKeyType)
	err = clearPendingKeyFunc(ctx, entity)
	if err != nil {
		return "", fmt.Errorf("failed to clear stale pending key for %s (minted as %q, expected %q): %w", entity, pendingType, targetKeyType, err)
	}

	pendingKey, err = getOrCreatePendingKeyFunc(ctx, entity)
	if err != nil {
		return "", fmt.Errorf("failed to remint pending key for %s: %w", entity, err)
	}

	pendingType, err = getPendingKeyType(ctx, entity)
	if err != nil {
		return "", err
	}
	if pendingType != targetKeyType {
		return "", fmt.Errorf(
			"pending key for %s was minted as cipher %q, but %q was requested: the mon's auth_preferred_cipher is not prepared; run 'microceph auth rotate' to prepare the cluster before rotating single clients",
			entity, pendingType, targetKeyType)
	}

	return pendingKey, nil
}

// writeClientKeyring renders a minimal loadable keyring (entity + secret) with
// the shared keyring template and writes it atomically to destPath.
func writeClientKeyring(destPath string, entity string, secret string) error {
	err := os.MkdirAll(filepath.Dir(destPath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", destPath, err)
	}

	keyring := NewCephKeyring(filepath.Dir(destPath), filepath.Base(destPath))
	err = keyring.WriteConfig(map[string]any{
		"name": entity,
		"key":  secret,
	}, 0600)
	if err != nil {
		return fmt.Errorf("failed to render client keyring %s: %w", destPath, err)
	}

	return nil
}

// This function executes upstream step 9:
//   - Rotates and distributes MicroCeph-managed client keys one at a time.
//   - Skips clients with incompatible sessions, reports unmanaged clients,
//     and returns the rotation result with blockers if any exist.
//   - Skips clients already rotated on a previous attempt (recorded in
//     progress) and appends each completed client to it as it goes, so a
//     resumed rotation does not re-rotate finished credentials.
func RotateManagedClients(ctx context.Context, s interfaces.StateInterface, targetKeyType string, progress *AuthRotationProgress) (ClientRotationResult, error) {
	if progress == nil {
		progress = &AuthRotationProgress{}
	}

	result := ClientRotationResult{}

	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to dump auth keys for client discovery: %w", err)
	}

	// Session compatibility is advisory only: connection feature bits cannot
	// show aes256k support (see InspectClientSessionAdvisories), so connected
	// clients with known-legacy releases are reported but never block the
	// rotation. External consumers are gated by the explicit
	// unmanaged-credential blocker below, which makes the operator deal with
	// them by name.
	advisories, err := inspectClientSessionAdvisoriesFunc(ctx, targetKeyType)
	if err != nil {
		logger.Warnf("failed to inspect client sessions: %v", err)
	}
	for entity, reason := range advisories {
		logger.Warnf("client %s: %s (advisory: verify support for %s manually)", entity, reason, targetKeyType)
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

		// Resume progress: skip entities already rotated on a previous attempt.
		if slices.Contains(progress.RotatedClients, norm) {
			continue
		}

		// Rotate managed client
		err = rotateSingleClientKeyFunc(ctx, s, norm, targetKeyType)
		if err != nil {
			return result, fmt.Errorf("failed to rotate managed client %s: %w", norm, err)
		}
		progress.RotatedClients = append(progress.RotatedClients, norm)
		result.RotatedClients = append(result.RotatedClients, norm)
	}

	// Formulate blocker message if any blockers exist
	if len(result.UnmanagedClients) > 0 {
		result.HasBlockers = true
		result.BlockerMessage = fmt.Sprintf("Unmanaged credentials must be rotated manually before rotation can proceed (unmanaged: %s)", strings.Join(result.UnmanagedClients, ", "))
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

// isDaemonEntityName checks whether the name is a non-client Ceph entity
// (mon., mgr.<host>, osd.<id>, mds.<host>).
func isDaemonEntityName(name string) bool {
	for _, prefix := range []string{"mon.", "mgr.", "osd.", "mds."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// validateSingleClientTarget checks that a --client rotation request targets
// an entity MicroCeph can rotate through the single-client path: it must exist
// in the cluster auth DB, be a client-type entity, and be locally managed
// (client.admin is managed and routed to the protected admin rotation by the
// caller). Daemon credentials (mon., mgr., osd., mds.) are rejected: they are
// rotated by the cluster-wide pipeline only. Unmanaged or unknown entities are
// rejected before any rotation state is persisted.
func validateSingleClientTarget(ctx context.Context, clientName string) (*AuthKeyEntry, error) {
	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to dump auth keys for client validation: %w", err)
	}

	for _, entry := range entries {
		if entry.EntityName != clientName {
			continue
		}
		if entry.EntityType != "client" {
			return nil, fmt.Errorf("entity %q is a daemon credential: it is rotated by cluster-wide rotation and cannot be targeted with --client", clientName)
		}
		if !IsMicroCephManagedClient(clientName) {
			return nil, fmt.Errorf("client %q is not managed by MicroCeph: unmanaged credentials must be rotated manually", clientName)
		}
		return &entry, nil
	}

	return nil, fmt.Errorf("unknown client %q: no such entity in the cluster auth DB", clientName)
}

// AuthRotationProgress records per-entity progress inside the rotation
// pipeline's step_progress column so a resumed rotation skips entities that
// already rotated on a previous attempt.
type AuthRotationProgress struct {
	// MonRotated records that the shared mon. key was rotated and is active;
	// a resumed rotation fetches it instead of rotating again.
	MonRotated bool `json:"mon_rotated,omitempty"`
	// RotatedMembers lists cluster members whose daemon rotation completed.
	RotatedMembers []string `json:"rotated_members,omitempty"`
	// RotatedClients lists managed client entities whose rotation completed.
	RotatedClients []string `json:"rotated_clients,omitempty"`
}

// parseAuthRotationProgress decodes a step_progress payload; a missing or
// undecodable payload yields empty progress (nothing is skipped).
func parseAuthRotationProgress(raw string) *AuthRotationProgress {
	progress := &AuthRotationProgress{}
	if raw == "" {
		return progress
	}

	err := json.Unmarshal([]byte(raw), progress)
	if err != nil {
		logger.Warnf("failed to parse rotation step progress; resuming without it: %v", err)
	}

	return progress
}

// String encodes the progress for the step_progress column.
func (p *AuthRotationProgress) String() string {
	data, err := json.Marshal(p)
	if err != nil {
		// Marshalling a plain struct cannot fail.
		return "{}"
	}

	return string(data)
}

// authStageOrder lists the full-pipeline stages in execution order.
// AuthRotationStageProtectAdmin has no pipeline stage of its own: client.admin
// is not rotated automatically (see ensureAdminReadyForFinish). It stays in
// the order so records from older runs that paused at it resume sensibly.
var authStageOrder = []string{
	database.AuthRotationStageReadiness,
	database.AuthRotationStagePrepareAuth,
	database.AuthRotationStageRotateDaemons,
	database.AuthRotationStageActivateAndCheck,
	database.AuthRotationStageSwitchServiceAuth,
	database.AuthRotationStagePreventInsecureKeys,
	database.AuthRotationStageProtectAdmin,
	database.AuthRotationStageRotateClients,
	database.AuthRotationStageFinishSafely,
}

// authStageIndex returns the stage's position in the pipeline order, or -1 for
// unknown or empty stages (those resume from the beginning).
func authStageIndex(stage string) int {
	return slices.Index(authStageOrder, stage)
}

// runAuthRotationPipeline executes the full-cluster rotation stages. The record's
// stage is persisted before a stage runs, so on resume every stage before
// rec.Stage already completed and is skipped; per-entity progress recorded in
// the record's step_progress column keeps partially completed stages from
// re-rotating finished entities.
func runAuthRotationPipeline(ctx context.Context, s interfaces.StateInterface, rec *database.AuthRotationRecord, resolvedKeyType string, lockToken func() int64) (*database.AuthRotationRecord, error) {
	progress := parseAuthRotationProgress(rec.StepProgress)

	// Helper to update database stage (carrying the current progress payload).
	setStage := func(stage string) error {
		rec.Stage = stage
		rec.StepProgress = progress.String()
		if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
			return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				return database.SetAuthRotationStage(ctx, tx, stage, rec.StepProgress)
			})
		}
		return nil
	}

	// Helper to persist mid-stage progress updates without touching the stage.
	saveProgress := func() {
		rec.StepProgress = progress.String()
		if s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil {
			_ = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				return database.SetAuthRotationStage(ctx, tx, rec.Stage, rec.StepProgress)
			})
		}
	}

	// failRun records the failed state so auth status can tell a dead run from
	// a live one, persisting the partial progress along with it.
	failRun := func(msg string, err error) (*database.AuthRotationRecord, error) {
		rec.State = database.AuthRotationStateFailed
		rec.Detail = fmt.Sprintf("%s: %v", msg, err)
		rec.StepProgress = progress.String()
		// The rotation context may already be cancelled here (e.g. the lock
		// heartbeat failed); the outcome must still be recorded, so the
		// persist runs on a fresh bounded context and only while this worker
		// still holds the lock.
		persistRotationRecord(s, lockToken, rec)
		return rec, fmt.Errorf("%s: %w", msg, err)
	}

	// Resume semantics: the stage is persisted before it runs, so every stage
	// before the record's stage already completed on a previous attempt.
	resumeFrom := authStageIndex(rec.Stage)
	shouldRun := func(stage string) bool {
		return authStageIndex(stage) >= resumeFrom
	}

	// Stage 1: Readiness checks
	if shouldRun(database.AuthRotationStageReadiness) {
		_ = setStage(database.AuthRotationStageReadiness)
		err := checkAuthRotationReadinessFunc(ctx, s, resolvedKeyType)
		if err != nil {
			return failRun("readiness check failed", err)
		}
	}

	// Stage 2: Prepare auth (Upstream steps 1-2)
	if shouldRun(database.AuthRotationStagePrepareAuth) {
		_ = setStage(database.AuthRotationStagePrepareAuth)
		err := prepareAuthRotationFunc(ctx, resolvedKeyType)
		if err != nil {
			return failRun("auth preparation failed", err)
		}
	}

	// Stage 3: Rotate daemons (Upstream step 3)
	if shouldRun(database.AuthRotationStageRotateDaemons) {
		_ = setStage(database.AuthRotationStageRotateDaemons)
		err := rotateDaemonsFunc(ctx, s, resolvedKeyType, progress)
		if err != nil {
			return failRun("daemon key rotation failed", err)
		}
		saveProgress()
	}

	// Stage 4: Activate and check (Upstream step 4)
	if shouldRun(database.AuthRotationStageActivateAndCheck) {
		_ = setStage(database.AuthRotationStageActivateAndCheck)
		err := activateAndCheckFunc(ctx)
		if err != nil {
			return failRun("daemon activation check failed", err)
		}
	}

	// Stage 5: Switch service authentication (Upstream step 5)
	if shouldRun(database.AuthRotationStageSwitchServiceAuth) {
		_ = setStage(database.AuthRotationStageSwitchServiceAuth)
		err := switchServiceAuthenticationFunc(ctx, resolvedKeyType)
		if err != nil {
			return failRun("service auth switch failed", err)
		}
	}

	// Stage 6: Prevent new insecure keys (Upstream steps 6-7)
	if shouldRun(database.AuthRotationStagePreventInsecureKeys) {
		_ = setStage(database.AuthRotationStagePreventInsecureKeys)
		err := preventNewInsecureKeysFunc(ctx, resolvedKeyType)
		if err != nil {
			return failRun("insecure key prevention failed", err)
		}
	}

	// Stage 7: Rotate client credentials (Upstream step 9)
	if shouldRun(database.AuthRotationStageRotateClients) {
		_ = setStage(database.AuthRotationStageRotateClients)
		clientResult, err := rotateManagedClientsFunc(ctx, s, resolvedKeyType, progress)
		if err != nil {
			return failRun("client key rotation failed", err)
		}
		saveProgress()

		if clientResult.HasBlockers {
			rec.State = database.AuthRotationStateBlocked
			rec.Blocker = clientResult.BlockerMessage
			rec.StepProgress = progress.String()
			persistRotationRecord(s, lockToken, rec)
			return rec, nil
		}
	}

	// Stage 8: Finish safely (Upstream step 10). client.admin is deliberately
	// NOT rotated by the pipeline: the credential is shared beyond this
	// cluster, so rotating it is an explicit operator decision. The finish
	// pauses until it is done, because disallowing the insecure ciphers while
	// client.admin still holds one would lock out its holders (including
	// MicroCeph itself).
	if shouldRun(database.AuthRotationStageFinishSafely) {
		_ = setStage(database.AuthRotationStageFinishSafely)
		err := ensureAdminReadyForFinish(ctx, resolvedKeyType)
		if err != nil {
			rec.State = database.AuthRotationStateBlocked
			rec.Blocker = err.Error()
			rec.StepProgress = progress.String()
			persistRotationRecord(s, lockToken, rec)
			return rec, nil
		}

		err = finalizeAuthRotationFunc(ctx, s, resolvedKeyType)
		if err != nil {
			return failRun("safe finalization failed", err)
		}
	}

	// Persist the completed state: the record must leave in_progress, or auth
	// status would keep reporting a live run and every later rotation would
	// resume past all stages without ever closing the record.
	rec.State = database.AuthRotationStateCompleted
	rec.Blocker = ""
	rec.Detail = ""
	persistRotationRecord(s, lockToken, rec)

	return rec, nil
}

// authRotationLockLease is how long an unrenewed rotation lock lease is
// honored before another attempt may reclaim it as stale.
const authRotationLockLease = 10 * time.Minute

// authRotationHeartbeatInterval is how often the running rotation renews its
// lock lease. It must stay well below the lease duration so that clock skew
// between members cannot expire a live holder.
const authRotationHeartbeatInterval = time.Minute

// AbortAuthRotation clears an incomplete rotation record so a wedged rotation
// (a --client filter whose rotation cannot succeed, or a key type the operator
// no longer wants) stops rejecting new rotations. A rotation that is actually
// running cannot be aborted mid-flight: the abort is refused while the lock is
// live, and a lock left behind by a crashed daemon is reclaimable once its
// lease expires. Returns the pre-reset record and whether an incomplete
// rotation was actually aborted.
func AbortAuthRotation(ctx context.Context, s interfaces.StateInterface) (*database.AuthRotationRecord, bool, error) {
	if s == nil || s.ClusterState() == nil || s.ClusterState().Database() == nil {
		return nil, false, fmt.Errorf("no cluster database available")
	}

	var rec *database.AuthRotationRecord
	aborted := false
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		rec, aborted, err = database.AbortAuthRotation(ctx, tx, authRotationLockLease)
		return err
	})
	if errors.Is(err, database.ErrAuthRotationRunning) {
		return nil, false, fmt.Errorf("cannot abort: %w; if the daemon restarted mid-rotation, retry once its lock lease (%s) expires", err, authRotationLockLease)
	}
	if err != nil {
		return nil, false, err
	}

	return rec, aborted, nil
}

// runSingleClientRotation executes a --client rotation. client.admin routes to
// the protected admin rotation (the generic pending-key path matches no admin
// keyring paths, so commit-pending would retire the admin key while the
// keyring.client.admin DB row and the conf keyrings still hold the old one,
// locking MicroCeph out of the cluster). Mutates rec in place.
//
// A dedicated run (the record's filter matches the request) owns the record
// and completes it. client.admin can also run as a sub-step of an incomplete
// full run — the pipeline pauses before disallowing insecure ciphers until
// admin is rotated explicitly — in which case the record is handed back to
// the full pipeline (state back to in_progress, blocker cleared, stage
// untouched) instead of being completed.
func runSingleClientRotation(ctx context.Context, s interfaces.StateInterface, rec *database.AuthRotationRecord, requestedClient string, resolvedKeyType string, lockToken func() int64) (*database.AuthRotationRecord, error) {
	dedicated := rec.ClientName == requestedClient

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

	var rotateErr error
	if requestedClient == "client.admin" {
		if dedicated {
			_ = setStage(database.AuthRotationStageProtectAdmin)
		}
		rotateErr = rotateAdminKeyFunc(ctx, s, resolvedKeyType)
	} else {
		_ = setStage(database.AuthRotationStageRotateClients)
		rotateErr = rotateSingleClientKeyFunc(ctx, s, requestedClient, resolvedKeyType)
	}
	if rotateErr != nil {
		rec.State = database.AuthRotationStateBlocked
		rec.Blocker = rotateErr.Error()
		// The rotation context may already be cancelled here; the outcome
		// must still be recorded, so the persist runs on a fresh bounded
		// context and only while this worker still holds the lock.
		persistRotationRecord(s, lockToken, rec)
		return rec, rotateErr
	}

	if dedicated {
		// Mark completed for dedicated single-client run
		rec.State = database.AuthRotationStateCompleted
		rec.Stage = database.AuthRotationStageFinishSafely
		rec.Blocker = ""
		persistRotationRecord(s, lockToken, rec)
		return rec, nil
	}

	// Sub-step of an incomplete full run: the credential is rotated; resume
	// the paused pipeline so its completion is observable. The triggering
	// command tracks the shared record until it leaves in_progress — handing
	// the record back without resuming would leave the sub-step command
	// waiting forever. For the documented admin flow the paused stage is
	// finish_safely, which proceeds straight to disallowing insecure ciphers
	// and completing; a full run paused elsewhere (e.g. rotate_clients)
	// re-blocks on its original blocker, which the command then reports.
	rec.State = database.AuthRotationStateInProgress
	rec.Blocker = ""
	rec.Detail = ""
	persistRotationRecord(s, lockToken, rec)
	return runAuthRotationPipelineFunc(ctx, s, rec, resolvedKeyType, lockToken)
}

// ensureAdminReadyForFinish verifies client.admin is on the target cipher
// before insecure ciphers are disallowed. The pipeline deliberately does not
// rotate client.admin: the credential is shared beyond this cluster — it is
// distributed through the ceph-conf content interface (e.g. to MicroCloud's
// LXD) and often copied to other nodes, and holders that already loaded it
// into memory cannot re-authenticate once their ticket expires. It must be
// rotated explicitly with 'microceph auth rotate --client client.admin'.
func ensureAdminReadyForFinish(ctx context.Context, targetKeyType string) error {
	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return fmt.Errorf("failed to verify client.admin cipher before finalization: %w", err)
	}

	for _, entry := range entries {
		if entry.EntityName == "client.admin" && entry.KeyType != targetKeyType {
			return fmt.Errorf(
				"client.admin still uses cipher %q: the admin key is shared beyond this cluster (ceph-conf content interface, e.g. MicroCloud's LXD, and copies on other nodes) and is not rotated automatically; rotate it explicitly with 'microceph auth rotate --client client.admin' — that command resumes and finishes this rotation",
				entry.KeyType)
		}
	}

	return nil
}

// ExecuteAuthRotation starts or resumes a CephX key rotation and returns the
// initialized record. Validation, lock acquisition, and record init/resume run
// synchronously; the rotation itself then runs detached from the caller's
// context in a background goroutine: daemon restart waves routinely outlast
// CLI timeouts, and a cancelled request context must not stop a rotation in
// the middle of a key change. The running rotation is reported by auth status,
// which the CLI polls. While it runs, the rotation lock is renewed by a
// heartbeat so long stages cannot be joined by a retry that treats the lease
// as stale; losing the heartbeat (the lock was taken over) cancels the run.
func ExecuteAuthRotation(ctx context.Context, s interfaces.StateInterface, targetKeyType string, clientName string) (*database.AuthRotationRecord, error) {
	// Acquire rotation concurrency lock
	token := time.Now().UnixNano()
	staleBefore := token - int64(authRotationLockLease)

	hasDB := s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil
	if hasDB {
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
	}

	// The synchronous error paths release the lock themselves; the detached
	// runner releases it with the latest heartbeat-renewed token.
	releaseLock := func() {
		if hasDB {
			_ = s.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
				_, err := database.ReleaseAuthRotationLock(ctx, tx, token)
				return err
			})
		}
	}

	// Peek at the record to decide whether this invocation resumes an incomplete
	// rotation; a resume must not resolve an empty --key-type from the
	// preferred cipher (see resolveRotationTargetKeyType).
	resuming := false
	recordTarget := ""
	var err error
	if hasDB {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			existing, err := database.GetAuthRotation(ctx, tx)
			if err != nil {
				return err
			}
			resuming = existing.State == database.AuthRotationStateInProgress ||
				existing.State == database.AuthRotationStateBlocked ||
				existing.State == database.AuthRotationStateFailed
			recordTarget = existing.TargetKeyType
			return nil
		})
		if err != nil {
			releaseLock()
			return nil, fmt.Errorf("failed to read rotation record: %w", err)
		}
	}

	// Resolve target key type
	resolvedKeyType, err := resolveRotationTargetKeyType(ctx, resuming, recordTarget, targetKeyType)
	if err != nil {
		releaseLock()
		return nil, err
	}

	if clientName != "" && !isDaemonEntityName(clientName) {
		clientName = NormalizeClientName(clientName)
	}

	// Validate the single-client target before the rotation record is
	// initialised: a bad --client filter must never be persisted.
	if clientName != "" {
		_, err = validateSingleClientTarget(ctx, clientName)
		if err != nil {
			releaseLock()
			return nil, err
		}
	}

	// Initialize or resume rotation record in database
	var rec *database.AuthRotationRecord
	if hasDB {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			rec, _, err = database.InitOrResumeAuthRotation(ctx, tx, resolvedKeyType, clientName)
			return err
		})
		if err != nil {
			releaseLock()
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

	// The stages run against the run's actual target: on a resume this is the
	// record's target (inherited via the empty --key-type exemption), on a
	// fresh run it is identical to the resolved type.
	if rec.TargetKeyType != "" {
		resolvedKeyType = rec.TargetKeyType
	}

	if !hasDB {
		// No cluster database (unit tests): run inline so the result is
		// deterministic. No lock is held, so record persistence skips the
		// ownership check.
		if clientName != "" {
			return runSingleClientRotation(ctx, s, rec, clientName, resolvedKeyType, nil)
		}
		return runAuthRotationPipeline(ctx, s, rec, resolvedKeyType, nil)
	}

	// Detach the rotation. runCtx is deliberately not derived from the
	// request context: the rotation outlives the triggering request.
	runCtx, cancelRotation := context.WithCancel(context.Background())

	var currentToken atomic.Int64
	currentToken.Store(token)

	// Heartbeat: renew the lock lease for as long as the rotation runs, and
	// cancel the rotation if the lease is lost (another attempt took the
	// lock, e.g. after this holder was declared stale).
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(authRotationHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				newToken := time.Now().UnixNano()
				var renewed bool
				err := s.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
					var err error
					renewed, err = database.RenewAuthRotationLock(ctx, tx, currentToken.Load(), newToken)
					return err
				})
				if err != nil || !renewed {
					logger.Errorf("auth rotation lock renewal failed; aborting rotation: renewed=%v err=%v", renewed, err)
					cancelRotation()
					return
				}
				currentToken.Store(newToken)
			}
		}
	}()

	// Snapshot the initialized record for the caller BEFORE the worker
	// launches: the goroutine mutates the original record in place as it
	// progresses, and the response must neither race against those writes nor
	// mix fields from different state transitions. The copy happens while no
	// concurrent writer exists; afterwards the original record is exclusively
	// owned by the goroutine.
	snapshot := *rec

	go func() {
		defer func() {
			// Wait for the heartbeat so release uses the latest renewed
			// token, then give the lock back.
			cancelRotation()
			<-heartbeatDone
			_ = s.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
				_, err := database.ReleaseAuthRotationLock(ctx, tx, currentToken.Load())
				return err
			})
		}()

		// Record persistence verifies lock ownership against the heartbeat-
		// renewed token, so a worker whose lock was taken over cannot
		// overwrite the new attempt's record.
		tokenGetter := func() int64 { return currentToken.Load() }

		var runErr error
		if clientName != "" {
			_, runErr = runSingleClientRotation(runCtx, s, rec, clientName, resolvedKeyType, tokenGetter)
		} else {
			_, runErr = runAuthRotationPipeline(runCtx, s, rec, resolvedKeyType, tokenGetter)
		}
		if runErr != nil {
			logger.Errorf("auth rotation ended with error: %v", runErr)
		}
	}()

	return &snapshot, nil
}

// RotateEntityKey rotates the key of a Ceph authentication entity and returns the new keyring output.
func RotateEntityKey(ctx context.Context, entity string, keyType string) (string, error) {
	args := []string{"auth", "rotate", entity}
	if keyType != "" {
		args = append(args, "--key-type", keyType)
	}

	out, err := boundedCephRun(ctx, args...)
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

// MemberRotationSummary reports which of a member's own daemons were rotated locally.
type MemberRotationSummary struct {
	Hostname     string
	MonRestarted bool
	RotatedMgrs  []string
	RotatedOSDs  []int64
	RotatedMDSs  []string
}

// RotateMemberDaemons rotates and restarts ONLY the daemons running on this member:
//   - If monKeyring is non-empty and a local mon exists: writes the shared mon. keyring
//     (rotated once by the coordinator), restarts the local mon, and waits for it to
//     re-enter quorum.
//   - Stops the local mgr, rotates mgr.<hostname>, updates the on-disk keyring,
//     restarts, and verifies recovery. The stop happens before the auth-DB rotation so
//     the running daemon never holds a stale in-memory key against a rotated auth entry.
//   - Marks all local OSDs down, stops the osd service ONCE, rotates every local
//     osd.<id> key, updates keyrings, restarts the osd service once, and verifies
//     each OSD is back up.
//   - Same stop/rotate/restart/verify cycle for the local mds.
//
// The coordinator invokes this on every member (one at a time, remote members via
// the /auth/rotate/member endpoint, the local member directly) so each daemon's
// keyring is written on the machine that runs it before that daemon restarts.
func RotateMemberDaemons(ctx context.Context, s interfaces.StateInterface, keyType string, monKeyring string) (*MemberRotationSummary, error) {
	summary := &MemberRotationSummary{}

	if s == nil || s.ClusterState() == nil {
		return summary, nil
	}
	hostname := s.ClusterState().Name()
	summary.Hostname = hostname

	// Phase 0: deploy the shared mon. keyring and restart the local mon.
	if monKeyring != "" {
		restarted, err := deployMonKeyringAndRestartFunc(ctx, hostname, monKeyring)
		if err != nil {
			return summary, err
		}
		summary.MonRestarted = restarted
	}

	// Phase 1: local mgr.
	if localDaemonDataDirExists("mgr", hostname) {
		err := rotateLocalMGRKeyFunc(ctx, keyType, hostname)
		if err != nil {
			return summary, fmt.Errorf("failed to rotate local mgr key: %w", err)
		}
		summary.RotatedMgrs = append(summary.RotatedMgrs, hostname)
	}

	// Phase 2: local OSDs (single osd service stop/start for all local OSDs).
	osdIDs, err := getLocalOSDIDsFunc()
	if err != nil {
		return summary, fmt.Errorf("failed to discover local OSDs: %w", err)
	}
	if len(osdIDs) > 0 {
		err = rotateLocalOSDKeysFunc(ctx, keyType, osdIDs)
		if err != nil {
			return summary, fmt.Errorf("failed to rotate local OSD keys: %w", err)
		}
		summary.RotatedOSDs = osdIDs
	}

	// Phase 3: local mds.
	if localDaemonDataDirExists("mds", hostname) {
		err := rotateLocalMDSKeyFunc(ctx, keyType, hostname)
		if err != nil {
			return summary, fmt.Errorf("failed to rotate local mds key: %w", err)
		}
		summary.RotatedMDSs = append(summary.RotatedMDSs, hostname)
	}

	return summary, nil
}

// localDaemonDataDirExists reports whether the data directory for a daemon of the
// given service type exists on this member, i.e. whether the daemon runs here.
func localDaemonDataDirExists(service string, hostname string) bool {
	dataDir := filepath.Join(constants.GetPathConst().DataPath, service, fmt.Sprintf("ceph-%s", hostname))
	_, err := os.Stat(dataDir)
	return err == nil
}

// deployMonKeyringAndRestart writes the shared mon. keyring to the local mon data
// directory (if a mon runs on this member), restarts the local mon, and waits for it
// to re-enter quorum. Returns false when this member runs no mon.
func deployMonKeyringAndRestart(ctx context.Context, hostname string, monKeyring string) (bool, error) {
	monDataDir := filepath.Join(constants.GetPathConst().DataPath, "mon", fmt.Sprintf("ceph-%s", hostname))
	if _, err := os.Stat(monDataDir); err != nil {
		// No local mon on this member: nothing to deploy.
		return false, nil
	}

	keyringPath := filepath.Join(monDataDir, "keyring")
	err := writeKeyringAtomically(keyringPath, monKeyring, 0600)
	if err != nil {
		return false, fmt.Errorf("failed to update mon keyring at %s: %w", keyringPath, err)
	}

	logger.Infof("Restarting local mon %s after key rotation", hostname)
	err = snapRestartFunc("mon", false)
	if err != nil {
		return false, fmt.Errorf("failed to restart local mon %s: %w", hostname, err)
	}

	err = waitForMonQuorumFunc(ctx, hostname, 2*time.Minute)
	if err != nil {
		return false, fmt.Errorf("local mon %s failed to re-enter quorum: %w", hostname, err)
	}

	return true, nil
}

// RotateMonKeyAuth rotates the shared mon. credential in the Ceph auth database
// exactly once and returns the new keyring content. This runs on the coordinator
// only; each mon member then deploys the returned keyring via its own endpoint.
func RotateMonKeyAuth(ctx context.Context, keyType string) (string, error) {
	monKeyring, err := rotateEntityKeyFunc(ctx, "mon.", keyType)
	if err != nil {
		return "", fmt.Errorf("failed to rotate mon. key: %w", err)
	}
	if strings.TrimSpace(monKeyring) == "" {
		return "", fmt.Errorf("rotated mon. keyring output is empty")
	}

	return monKeyring, nil
}

// RotateLocalMGRKey stops the local mgr, rotates mgr.<hostname>, updates the
// on-disk keyring on this member, starts the daemon, and verifies its recovery.
// If the rotation or keyring write fails, the daemon is restarted best-effort on
// its on-disk keyring so the member is not left with a stopped daemon.
func RotateLocalMGRKey(ctx context.Context, keyType string, hostname string) (retErr error) {
	pathConst := constants.GetPathConst()
	entity := fmt.Sprintf("mgr.%s", hostname)

	logger.Infof("Rotating key for %s", entity)

	// Stop first so the running daemon cannot attempt re-authentication with its
	// stale in-memory key once the auth-DB entry is rotated. A failed stop must
	// abort the rotation: with the old process still alive, a later readiness
	// check would find its existing name in the map and prove nothing, and the
	// rotation would retire the key that process still holds.
	err := snapStopFunc("mgr", false)
	if err != nil {
		return fmt.Errorf("failed to stop mgr.%s before rotating its key: %w", hostname, err)
	}

	// On failure, bring the daemon back up rather than leaving it stopped. Best
	// effort: a failed restart is logged but must not mask the original error.
	defer func() {
		if retErr == nil {
			return
		}
		err := snapStartFunc("mgr", false)
		if err != nil {
			logger.Warnf("failed to restart mgr.%s after rotation failure: %v", hostname, err)
			return
		}
		logger.Infof("restarted mgr.%s after rotation failure", hostname)
	}()

	keyring, err := rotateEntityKeyFunc(ctx, entity, keyType)
	if err != nil {
		return fmt.Errorf("failed to rotate %s key: %w", entity, err)
	}

	keyringPath := filepath.Join(pathConst.DataPath, "mgr", fmt.Sprintf("ceph-%s", hostname), "keyring")
	err = writeKeyringAtomically(keyringPath, keyring, 0600)
	if err != nil {
		return fmt.Errorf("failed to update mgr keyring at %s: %w", keyringPath, err)
	}

	err = snapStartFunc("mgr", false)
	if err != nil {
		return fmt.Errorf("failed to start mgr.%s: %w", hostname, err)
	}

	// With the stop verified above, whatever registers next is a new process;
	// joining the map requires authenticating with the keyring written above,
	// so readiness here is proof the consumer loaded the new credential.
	err = waitForMGRReadyFunc(ctx, hostname, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("mgr.%s did not recover: %w", hostname, err)
	}

	return nil
}

// authRotationPersistTimeout bounds the outcome-persist transactions. They
// deliberately run on a fresh context: the rotation's own context may be
// cancelled exactly when the outcome must be recorded (lock heartbeat lost,
// takeover, abort), and a persist that inherits the cancellation would fail
// and leave the record in_progress — describing a running operation that no
// longer exists, with status reporting a live run and the CLI polling
// forever.
const authRotationPersistTimeout = 30 * time.Second

// persistRotationRecord writes the rotation record on a fresh bounded context
// (never the rotation's own, which may already be cancelled) and only while
// the rotation lock is still held by the worker's current token, so a worker
// that lost the lock to a newer attempt cannot overwrite the new attempt's
// record. Best effort: a failed persist is logged; the stale-lease reclaim
// remains the recovery path for a record stuck in_progress.
func persistRotationRecord(s interfaces.StateInterface, lockToken func() int64, rec *database.AuthRotationRecord) {
	if s == nil || s.ClusterState() == nil || s.ClusterState().Database() == nil {
		return
	}

	persistCtx, cancel := context.WithTimeout(context.Background(), authRotationPersistTimeout)
	defer cancel()

	err := s.ClusterState().Database().Transaction(persistCtx, func(ctx context.Context, tx *sql.Tx) error {
		if lockToken == nil {
			return database.SetAuthRotation(ctx, tx, *rec)
		}

		written, err := database.PersistRotationRecordIfLocked(ctx, tx, lockToken(), *rec)
		if err != nil {
			return err
		}
		if !written {
			return fmt.Errorf("rotation lock no longer held by this worker; not overwriting the record")
		}
		return nil
	})
	if err != nil {
		logger.Warnf("failed to persist auth rotation record (state %q, stage %q, blocker %q): %v", rec.State, rec.Stage, rec.Blocker, err)
	}
}

// boundedCephRun runs a single ceph CLI command with the shared per-call
// ceiling. The rotation runs on a detached background context that carries no
// deadline, so without a per-call bound a hung ceph command — mon-side
// commands block indefinitely when quorum is lost — would wedge the rotation
// forever while the lock heartbeat keeps renewing, locking out both retries
// and --abort even after the nominal stage budget has expired. Bounding each
// call keeps the wedge bounded and lets the stage fail recoverably.
func boundedCephRun(ctx context.Context, args ...string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
	defer cancel()
	return cephRunContext(callCtx, args...)
}

// The readiness poll loops below each bound their individual ceph exec with
// cephCallTimeout, following the waitForControlServiceReady
// pattern: mon-side commands (mon stat, mgr metadata, mds stat, osd dump) can
// block indefinitely when MON quorum is lost, and under the rotation's
// detached (background) context a single hung subprocess would otherwise
// block the poll forever while the lock heartbeat keeps renewing — wedging
// out both retries and --abort even after the nominal readiness budget has
// expired. With a per-call timeout a hung invocation is killed well within
// the budget, the deadline check fires, and the rotation fails recoverably.

func waitForMonQuorum(ctx context.Context, monName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		checkCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
		quorum, err := getMonQuorumNamesFunc(checkCtx)
		cancel()
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

func waitForMGRReady(ctx context.Context, mgrName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		checkCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
		ready, err := mgrActiveOrStandbyFunc(checkCtx, mgrName)
		cancel()
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

// RotateLocalMDSKey stops the local mds, rotates mds.<hostname>, updates the
// on-disk keyring on this member, starts the daemon, and verifies its recovery.
// If the rotation or keyring write fails, the daemon is restarted best-effort on
// its on-disk keyring so the member is not left with a stopped daemon.
func RotateLocalMDSKey(ctx context.Context, keyType string, hostname string) (retErr error) {
	pathConst := constants.GetPathConst()
	entity := fmt.Sprintf("mds.%s", hostname)

	logger.Infof("Rotating key for %s", entity)

	// A failed stop must abort the rotation, for the same reason as the mgr
	// path above: readiness output cannot prove uptake for a process that was
	// never stopped.
	err := snapStopFunc("mds", false)
	if err != nil {
		return fmt.Errorf("failed to stop mds.%s before rotating its key: %w", hostname, err)
	}

	// On failure, bring the daemon back up rather than leaving it stopped. Best
	// effort: a failed restart is logged but must not mask the original error.
	defer func() {
		if retErr == nil {
			return
		}
		err := snapStartFunc("mds", false)
		if err != nil {
			logger.Warnf("failed to restart mds.%s after rotation failure: %v", hostname, err)
			return
		}
		logger.Infof("restarted mds.%s after rotation failure", hostname)
	}()

	keyring, err := rotateEntityKeyFunc(ctx, entity, keyType)
	if err != nil {
		return fmt.Errorf("failed to rotate %s key: %w", entity, err)
	}

	keyringPath := filepath.Join(pathConst.DataPath, "mds", fmt.Sprintf("ceph-%s", hostname), "keyring")
	err = writeKeyringAtomically(keyringPath, keyring, 0600)
	if err != nil {
		return fmt.Errorf("failed to update mds keyring at %s: %w", keyringPath, err)
	}

	err = snapStartFunc("mds", false)
	if err != nil {
		return fmt.Errorf("failed to start mds.%s: %w", hostname, err)
	}

	// With the stop verified above, whatever registers next is a new process;
	// becoming ready requires authenticating with the keyring written above,
	// so readiness here is proof the consumer loaded the new credential.
	err = waitForMDSReadyFunc(ctx, hostname, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("mds.%s did not recover: %w", hostname, err)
	}

	return nil
}

func waitForMDSReady(ctx context.Context, mdsName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		checkCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
		ready, err := mdsUpFunc(checkCtx, mdsName)
		cancel()
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

// getLocalOSDIDs discovers the OSDs hosted on this member by scanning the local
// OSD data directories. Only directories carrying the ready marker are returned,
// mirroring the osd snap service spawn loop (dirs without it are never spawned).
func getLocalOSDIDs() ([]int64, error) {
	osdRoot := filepath.Join(constants.GetPathConst().DataPath, "osd")
	entries, err := os.ReadDir(osdRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read OSD data directory %s: %w", osdRoot, err)
	}

	ids := []int64{}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "ceph-") {
			continue
		}
		idStr := strings.TrimPrefix(entry.Name(), "ceph-")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		readyMarker := filepath.Join(osdRoot, entry.Name(), "ready")
		if _, err := os.Stat(readyMarker); err != nil {
			continue
		}
		ids = append(ids, id)
	}

	slices.Sort(ids)
	return ids, nil
}

// RotateLocalOSDKeys rotates every OSD key hosted on this member with a single
// osd service stop/start: marks all local OSDs down, stops the service once,
// rotates each osd.<id> key and updates its on-disk keyring, starts the service
// once, and verifies each OSD is back up. If a rotation or keyring write fails,
// the osd service is restarted best-effort on its on-disk keyrings so the member
// is not left with all of its OSDs stopped.
func RotateLocalOSDKeys(ctx context.Context, keyType string, osdIDs []int64) (retErr error) {
	pathConst := constants.GetPathConst()

	logger.Infof("Rotating keys for local OSDs %v", osdIDs)

	// 1. Mark all local OSDs down in the osdmap before stopping the service.
	idArgs := make([]string, 0, len(osdIDs))
	for _, id := range osdIDs {
		idArgs = append(idArgs, strconv.FormatInt(id, 10))
	}
	downArgs := append([]string{"osd", "down"}, idArgs...)
	_, err := boundedCephRun(ctx, downArgs...)
	if err != nil {
		logger.Warnf("failed to mark local OSDs %v down: %v", osdIDs, err)
	}

	// 2. Stop the osd service ONCE for all local OSDs.
	err = snapStopFunc("osd", false)
	if err != nil {
		return fmt.Errorf("failed to stop osd service: %w", err)
	}

	// On failure, bring the service back up rather than leaving every local OSD
	// stopped. Best effort: a failed restart is logged but must not mask the
	// original error.
	defer func() {
		if retErr == nil {
			return
		}
		err := snapStartFunc("osd", false)
		if err != nil {
			logger.Warnf("failed to restart osd service after rotation failure: %v", err)
			return
		}
		logger.Infof("restarted osd service after rotation failure")
	}()

	// 3. Rotate each local OSD key and update its persistent copies.
	for _, id := range osdIDs {
		keyring, err := rotateEntityKeyFunc(ctx, fmt.Sprintf("osd.%d", id), keyType)
		if err != nil {
			return fmt.Errorf("failed to rotate osd.%d key: %w", id, err)
		}

		osdDataDir := filepath.Join(pathConst.DataPath, "osd", fmt.Sprintf("ceph-%d", id))
		keyringPath := filepath.Join(osdDataDir, "keyring")
		err = writeKeyringAtomically(keyringPath, keyring, 0600)
		if err != nil {
			return fmt.Errorf("failed to update osd keyring at %s: %w", keyringPath, err)
		}
	}

	// 4. Start the osd service once.
	err = snapStartFunc("osd", false)
	if err != nil {
		return fmt.Errorf("failed to start osd service: %w", err)
	}

	// 5. Verify every local OSD is back up.
	for _, id := range osdIDs {
		err = waitForOSDUpFunc(ctx, id, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("osd.%d did not come back up: %w", id, err)
		}
	}

	return nil
}

func isOSDUp(ctx context.Context, osdID int64) (bool, error) {
	output, err := boundedCephRun(ctx, "osd", "dump", "-f", "json")
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
		checkCtx, cancel := context.WithTimeout(ctx, cephCallTimeout)
		up, err := isOSDUpFunc(checkCtx, osdID)
		cancel()
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

// RotateDaemons coordinates upstream steps 3-4 cluster-wide from the member that
// received the rotation request:
//  1. Rotates the shared mon. key exactly once (the authoritative copy now lives in
//     the auth DB, replicated to every mon via paxos) and captures the keyring.
//  2. Calls the per-member auth rotation endpoint on every OTHER member, one member
//     at a time (sequential, mirroring client.SendRestartRequestToClusterMembers), so
//     each member writes keyrings for, rotates, and restarts only its own daemons.
//     Because the fan-out is sequential and each mon member waits for its own mon to
//     re-enter quorum before replying, monitor quorum is verified between restarts.
//  3. Runs the same per-member logic locally for the coordinator's own daemons.
//  4. Verifies that AUTH_INSECURE_SERVICE_KEY_TYPE clears.
//
// Note: this health check reflects the auth DB only; the per-member keyring writes
// and restarts in step 2 are what actually bring every daemon onto the new key.
// RotateDaemons rotates the cluster-wide daemon credentials (Upstream step 3):
// the shared mon. key once on the coordinator, then every remote member's own
// daemons one at a time, then the coordinator's own. Units already completed on
// a previous attempt (recorded in progress) are skipped, so a resumed rotation
// does not re-rotate the mon. key or restart the daemons of finished members.
func RotateDaemons(ctx context.Context, s interfaces.StateInterface, targetKeyType string, progress *AuthRotationProgress) error {
	if progress == nil {
		progress = &AuthRotationProgress{}
	}

	// 1. Rotate the shared mon. key once on the coordinator.
	monKeyring := ""
	if progress.MonRotated {
		// Resume: the rotated key is already active; fetch it for the
		// remaining member deployments.
		fetched, err := getEntityKeyringFunc(ctx, "mon.")
		if err != nil {
			return fmt.Errorf("failed to fetch rotated mon. keyring: %w", err)
		}
		monKeyring = fetched
	} else {
		rotated, err := rotateMonKeyAuthFunc(ctx, targetKeyType)
		if err != nil {
			return fmt.Errorf("monitor key rotation failed: %w", err)
		}
		monKeyring = rotated
		progress.MonRotated = true
	}

	// 2. Fan out per-member rotation to remote members, one at a time, asking
	//    already-rotated members to no-op.
	if s != nil && s.ClusterState() != nil {
		req := types.MemberAuthRotateRequest{
			KeyType:    targetKeyType,
			MonKeyring: monKeyring,
			Skip:       progress.RotatedMembers,
		}
		completed, err := sendMemberAuthRotateFunc(ctx, s.ClusterState(), req)
		for _, hostname := range completed {
			if !slices.Contains(progress.RotatedMembers, hostname) {
				progress.RotatedMembers = append(progress.RotatedMembers, hostname)
			}
		}
		if err != nil {
			return fmt.Errorf("remote member daemon rotation failed: %w", err)
		}
	}

	// 3. Rotate the coordinator's own daemons unless its rotation already
	//    completed on a previous attempt. Without a cluster state the local
	//    member name is unknown and the rotation always runs.
	localName := ""
	if s != nil && s.ClusterState() != nil {
		localName = s.ClusterState().Name()
	}
	if localName == "" || !slices.Contains(progress.RotatedMembers, localName) {
		summary, err := rotateMemberDaemonsFunc(ctx, s, targetKeyType, monKeyring)
		if err != nil {
			return fmt.Errorf("local member daemon rotation failed: %w", err)
		}
		if summary != nil && summary.Hostname != "" && !slices.Contains(progress.RotatedMembers, summary.Hostname) {
			progress.RotatedMembers = append(progress.RotatedMembers, summary.Hostname)
		}
	}

	return nil
}

// GetEntityKeyring fetches the current keyring for an entity in loadable keyring format.
func GetEntityKeyring(ctx context.Context, entity string) (string, error) {
	out, err := boundedCephRun(ctx, "auth", "get", entity)
	if err != nil {
		return "", fmt.Errorf("failed to fetch keyring for %s: %w", entity, err)
	}

	return out, nil
}

// GetOrCreatePendingKey issues a pending key alongside the current key for the
// given entity and returns the pending secret. The -f json output carries the
// active key and the pending key as separate fields; the plaintext output must
// not be written to keyring files because KeyRing::decode has no notion of a
// pending key and rejects the file.
func GetOrCreatePendingKey(ctx context.Context, entity string) (string, error) {
	out, err := boundedCephRun(ctx, "auth", "get-or-create-pending", entity, "-f", "json")
	if err != nil {
		return "", fmt.Errorf("failed to get or create pending key for %s: %w", entity, err)
	}

	var doc any
	err = json.Unmarshal([]byte(out), &doc)
	if err != nil {
		return "", fmt.Errorf("failed to parse get-or-create-pending output for %s: %w", entity, err)
	}

	pendingKey, found := findJSONString(doc, "pending_key")
	if !found {
		// Deliberately exclude the raw output: it carries secrets.
		return "", fmt.Errorf("no pending_key in get-or-create-pending output for %s", entity)
	}

	return pendingKey, nil
}

// findJSONString recursively searches decoded JSON for the first string value
// stored under the given key, tolerating any wrapper shape the formatter emits.
func findJSONString(v any, key string) (string, bool) {
	switch typed := v.(type) {
	case map[string]any:
		if s, ok := typed[key].(string); ok && s != "" {
			return s, true
		}
		for _, child := range typed {
			if s, found := findJSONString(child, key); found {
				return s, true
			}
		}
	case []any:
		for _, child := range typed {
			if s, found := findJSONString(child, key); found {
				return s, true
			}
		}
	}

	return "", false
}

// CommitPendingKey rotates the pending key into the active position for the given entity.
func CommitPendingKey(ctx context.Context, entity string) error {
	_, err := boundedCephRun(ctx, "auth", "commit-pending", entity)
	if err != nil {
		return fmt.Errorf("failed to commit pending key for %s: %w", entity, err)
	}

	return nil
}

// ClearPendingKey removes the pending key for the given entity.
func ClearPendingKey(ctx context.Context, entity string) error {
	_, err := boundedCephRun(ctx, "auth", "clear-pending", entity)
	if err != nil {
		return fmt.Errorf("failed to clear pending key for %s: %w", entity, err)
	}

	return nil
}

// WipeRotatingServiceKeys wipes rotating service keys on the monitors.
func WipeRotatingServiceKeys(ctx context.Context) error {
	_, err := boundedCephRun(ctx, "auth", "wipe-rotating-service-keys")
	if err != nil {
		return fmt.Errorf("failed to wipe rotating service keys: %w", err)
	}

	return nil
}

// DumpAuthKeys queries all authentication credentials and keys from Ceph.
func DumpAuthKeys(ctx context.Context) ([]AuthKeyEntry, error) {
	output, err := boundedCephRun(ctx, "auth", "dump-keys", "-f", "json")
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
	output, err := boundedCephRun(ctx, "health", "detail", "-f", "json")
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
	output, err := boundedCephRun(ctx, "tell", "mon.*", "sessions", "-f", "json")
	if err != nil {
		logger.Debugf("tell mon.* sessions failed (%v); falling back to active monitor session list", err)
		output, err = boundedCephRun(ctx, "sessions", "-f", "json")
		if err != nil {
			return nil, fmt.Errorf("failed to query monitor sessions: %w", err)
		}
	}

	return parseClientSessions(output), nil
}

// Parse the JSON output from monitor sessions commands.
func parseClientSessions(output string) []ClientSessionInfo {
	var sessions []ClientSessionInfo
	var rawSessions []gjson.Result

	for _, segment := range splitMonTellOutput(output) {
		parsed := gjson.Parse(segment)

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
			} else if resp := parsed.Get("response"); resp.IsArray() {
				rawSessions = append(rawSessions, resp.Array()...)
			} else {
				rawSessions = append(rawSessions, parsed)
			}
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

// splitMonTellOutput splits 'tell mon.*' output into per-monitor JSON
// documents: with more than one monitor, ceph's wildcard tell prefixes each
// target's output with 'mon.<id>: ', which makes the whole output unparseable
// as a single document. Prefixed output is split on the prefix lines;
// unprefixed output (single monitor or fallback commands) passes through.
func splitMonTellOutput(output string) []string {
	trimmed := strings.TrimSpace(output)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []string{trimmed}
	}

	var segments []string
	var current strings.Builder
	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(line, "mon.") {
			if current.Len() > 0 {
				segments = append(segments, current.String())
				current.Reset()
			}
			// Strip the "mon.<id>: " prefix from the target line.
			if idx := strings.Index(line, ": "); idx != -1 {
				line = line[idx+2:]
			}
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	if current.Len() > 0 {
		segments = append(segments, current.String())
	}

	return segments
}

// Known older Ceph releases that lack AES256K support.
var legacyInsecureReleases = []string{
	"argonaut", "bobtail", "cuttlefish", "dumpling", "emperor", "firefly",
	"giant", "hammer", "infernalis", "jewel", "kraken", "luminous",
	"mimic", "nautilus", "octopus", "pacific", "quincy",
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
	resp.ServiceDistribution = make(map[string][]string)

	// 1. Read persistent record from database if available. The record's
	//    state is the authoritative rotation status: if the read fails, the
	//    response would carry an empty state, which the CLI's poll treats as
	//    terminal and reports as success while the rotation may still be
	//    running — fail instead of returning a stateless response.
	hasRecordDB := s != nil && s.ClusterState() != nil && s.ClusterState().Database() != nil
	if hasRecordDB {
		err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			rec, err := database.GetAuthRotation(ctx, tx)
			if err != nil {
				return err
			}
			resp.State = rec.State
			resp.Stage = rec.Stage
			resp.Blocker = rec.Blocker
			resp.TargetKeyType = rec.TargetKeyType
			resp.Detail = rec.Detail
			return nil
		})
		if err != nil {
			return resp, fmt.Errorf("failed to read auth rotation record: %w", err)
		}
		// A successful read always yields a state (NOT NULL, defaulting to
		// idle); an empty one must not be reported as a usable status.
		if resp.State == "" {
			return resp, fmt.Errorf("auth rotation record read returned no state")
		}
	}

	// 2. Query Ceph auth keys to compute the daemon and client cipher distributions
	entries, err := dumpAuthKeysFunc(ctx)
	if err != nil {
		return resp, fmt.Errorf("failed to dump auth keys: %w", err)
	}

	for _, entry := range entries {
		cipher := entry.KeyType
		if cipher == "" {
			cipher = "unknown"
		}
		if entry.EntityType == "client" {
			resp.ClientDistribution[cipher] = append(resp.ClientDistribution[cipher], entry.EntityName)
		} else {
			resp.ServiceDistribution[cipher] = append(resp.ServiceDistribution[cipher], entry.EntityName)
		}
	}

	// 3. Collect the CephX health checks Ceph is currently raising so the
	//    operator can see e.g. that only the rotating service keys are left to
	//    expire. Best effort: a health query failure must not hide the rest.
	hw, err := getAuthHealthWarningsFunc(ctx)
	if err != nil {
		logger.Warnf("failed to fetch auth health warnings: %v", err)
	} else {
		if hw.InsecureServiceKeyType {
			resp.HealthWarnings = append(resp.HealthWarnings, "AUTH_INSECURE_SERVICE_KEY_TYPE")
		}
		if hw.InsecureServiceTickets {
			resp.HealthWarnings = append(resp.HealthWarnings, "AUTH_INSECURE_SERVICE_TICKETS")
		}
		if hw.InsecureRotatingKeyType {
			resp.HealthWarnings = append(resp.HealthWarnings, "AUTH_INSECURE_ROTATING_SERVICE_KEY_TYPE")
		}
		if hw.InsecureKeysCreatable {
			resp.HealthWarnings = append(resp.HealthWarnings, "AUTH_INSECURE_KEYS_CREATABLE")
		}
		if hw.InsecureClientKeyType {
			resp.HealthWarnings = append(resp.HealthWarnings, "AUTH_INSECURE_CLIENT_KEY_TYPE")
		}
		if hw.InsecureKeysAllowed {
			resp.HealthWarnings = append(resp.HealthWarnings, "AUTH_INSECURE_KEYS_ALLOWED")
		}
	}

	// 4. Format Status string based on state and client distribution. The client
	//    names themselves live in client_distribution; the status carries counts
	//    only so a large mixed cluster does not produce one enormous line.
	if resp.State == database.AuthRotationStateBlocked {
		resp.Status = "blocked"
	} else if len(resp.ClientDistribution) == 1 {
		// All clients use a single cipher
		for cipher := range resp.ClientDistribution {
			resp.Status = fmt.Sprintf("All client %s", cipher)
		}
	} else if len(resp.ClientDistribution) > 1 {
		counts := make([]string, 0, len(resp.ClientDistribution))
		for _, cipher := range sortedCipherNames(resp.ClientDistribution) {
			counts = append(counts, fmt.Sprintf("%d clients on %s", len(resp.ClientDistribution[cipher]), cipher))
		}
		resp.Status = strings.Join(counts, ", ")
	} else {
		resp.Status = "idle"
	}

	return resp, nil
}

// sortedCipherNames returns the cipher keys of a distribution map in a stable
// (alphabetical) order so status output is deterministic.
func sortedCipherNames(dist map[string][]string) []string {
	names := make([]string, 0, len(dist))
	for cipher := range dist {
		names = append(names, cipher)
	}
	slices.Sort(names)
	return names
}

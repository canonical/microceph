package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

const (
	smbUpdateInterval = 30 * time.Second
	smbUpdateTimeout  = 2 * time.Minute
)

const smbUnreadyDiagnostic = "Local SMB gateway is not ready; previously applied configuration is unchanged.\n"

// startSMBUpdater observes configuration sources independently of monitor changes.
// It does not own snap service lifetimes or placement/membership reconciliation.
func startSMBUpdater(ctx context.Context, s interfaces.StateInterface) {
	startSMBMembershipUpdater(ctx)
	go func() {
		ticker := time.NewTicker(smbUpdateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			err := runSMBUpdate(ctx, s)
			if err != nil && ctx.Err() == nil {
				logger.Warn("SMB local update failed; inspect targeted service status")
			}
		}
	}()
}

func runSMBUpdate(ctx context.Context, s interfaces.StateInterface) error {
	wait := time.NewTicker(20 * time.Millisecond)
	defer wait.Stop()
	for !serviceStartMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait.C:
		}
	}
	defer serviceStartMu.Unlock()
	err := ctx.Err()
	if err != nil {
		return err
	}
	// Placement wait is shutdown-cancellable and does not consume the work budget.
	queryCtx, cancel := context.WithTimeout(ctx, smbUpdateTimeout)
	defer cancel()
	if s.ClusterState().Database().IsOpen(queryCtx) != nil {
		return nil
	}
	return refreshSMBLocked(queryCtx, s)
}

// refreshSMBLocked requires serviceStartMu. It only updates an existing local
// placement; absent/uncertain state is never authority to initialize identity.
func refreshSMBLocked(ctx context.Context, s interfaces.StateInterface) (retErr error) {
	owner, err := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	clusterID := string(owner)
	err = validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	root := smbRoot(clusterID)
	err = checkSMBStartIntent(root)
	if err != nil {
		return nil
	}
	diagnostic := "Local SMB update failed; configuration is not confirmed applied.\n"
	defer func() {
		if retErr != nil {
			_ = smbAtomicWrite(filepath.Join(root, "diagnostics/last-error"), []byte(diagnostic))
		}
	}()
	var started bool
	var clustered bool
	var ad bool
	var warning string
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := database.GetSMBConfiguredService(ctx, tx, clusterID, s.ClusterState().Name())
		if err != nil {
			return err
		}
		clustered, err = database.IsSMBClusteredGroup(ctx, tx, clusterID)
		if err != nil {
			return err
		}
		ad, err = database.HasSMBADIdentity(ctx, tx, clusterID)
		if err != nil {
			return err
		}
		if ad {
			state, stateErr := database.GetSMBADState(ctx, tx, clusterID, s.ClusterState().Name())
			if stateErr != nil {
				return stateErr
			}
			if !state.Joined || !state.Initialized {
				return fmt.Errorf("AD initialization is not confirmed; explicit maintenance required")
			}
		}
		if !clustered {
			started, err = database.GetSMBLocalSAMStarted(ctx, tx, clusterID, s.ClusterState().Name())
		}
		if err != nil {
			return err
		}
		warning, err = database.GetSMBPolicyWarning(ctx, tx, clusterID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !clustered && !started {
		return fmt.Errorf("SMB local SAM initialization is not confirmed; explicit maintenance required")
	}
	if !clustered {
		err = checkSMBLocalSAM(root)
	}
	if err != nil {
		return err
	}
	// Disabled startup intent is a skip, not permission to re-enable or publish.
	enabled, err := smbRefreshEnabled(ctx, root)
	if err != nil || !enabled {
		return err
	}
	err = writeSMBPolicyWarning(root, warning)
	if err != nil {
		return err
	}
	current, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if ad != (current.AD != nil) {
		return fmt.Errorf("SMB authentication identity context is missing or changed")
	}
	identity := ""
	if current.AD != nil {
		raw, _ := json.Marshal(current.AD)
		identity = string(raw)
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.VerifySMBAuthIdentity(ctx, tx, clusterID, s.ClusterState().Name(), identity)
	})
	if err != nil {
		return err
	}
	published, err := readSMBPublishedCandidate(root, clusterID)
	if err != nil {
		return err
	}
	appliedPath := filepath.Join(root, "applied.json")
	pendingPath := filepath.Join(root, "config-update-pending")
	pending, pendingErr := os.ReadFile(pendingPath)
	if pendingErr != nil && !errors.Is(pendingErr, os.ErrNotExist) {
		return pendingErr
	}
	applied, err := os.ReadFile(appliedPath)
	if errors.Is(err, os.ErrNotExist) {
		// Adopt the actual parent publication, never newly fetched upstream inputs.
		// A parent success row and prepared retained SAM are all required. Pending
		// publication is not a baseline, even if some of its files look complete.
		if pendingErr == nil || !current.Startup["smbd"].Prepared || current.FrontendAddress != published.Endpoint.FrontendAddress || current.SMBPort != published.Endpoint.SMBPort {
			return fmt.Errorf("SMB parent configuration is incomplete; explicit maintenance required")
		}
		applied = []byte(published.configurationFingerprint())
		err = smbAtomicWrite(appliedPath, applied)
	}
	if err != nil {
		return err
	}
	if len(applied) != 64 {
		return fmt.Errorf("invalid SMB applied configuration state")
	}
	if !current.Startup["smbd"].Prepared && errors.Is(pendingErr, os.ErrNotExist) {
		return fmt.Errorf("SMB configuration is not prepared; explicit maintenance required")
	}
	logical, err := cephRunContext(ctx, "smb", "show", "ceph.smb.cluster."+clusterID, "--format", "json")
	if err != nil {
		return fmt.Errorf("read logical SMB cluster failed")
	}
	present, err := smbLogicalPresent(clusterID, []byte(logical))
	if err != nil {
		return err
	}
	if !present {
		// Only independently confirmed deletion authorizes containment/withdrawal.
		err = stopSMBApps(root, smbSnapOps(root))
		if err != nil {
			return err
		}
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			return database.WithdrawSMBService(ctx, tx, clusterID, s.ClusterState().Name())
		})
		if err != nil {
			return err
		}
		return fmt.Errorf("logical SMB cluster is absent; local serving is stopped, retained ownership requires explicit retirement")
	}
	candidate, err := readSMBPreparedCandidate(ctx, s, clusterID, root, []byte(logical))
	if err != nil {
		return err
	}
	if candidate.Spec.hasFeature("domain") != ad || current.Clustered != clustered || candidate.Spec.hasFeature("clustered") != clustered {
		return fmt.Errorf("SMB mode change requires explicit maintenance")
	}
	if clustered && (published.Spec.ClusterMetaURI != candidate.Spec.ClusterMetaURI || published.Spec.ClusterLockURI != candidate.Spec.ClusterLockURI) {
		return fmt.Errorf("CTDB recovery sources changed; explicit maintenance required")
	}
	fingerprint := candidate.configurationFingerprint()
	if string(applied) == fingerprint && errors.Is(pendingErr, os.ErrNotExist) {
		if clustered {
			err = publishSMBMembership(ctx, root, candidate)
			if err != nil {
				return err
			}
		}
		// Health observation is not process repair or permission to reimport SAM.
		err = smbReadyNativeFunc(ctx, root, "smbd")
		if err != nil {
			diagnostic = smbUnreadyDiagnostic
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = updateSMBBootstrapCandidate(ctx, s, root, candidate)
		if err != nil {
			return err
		}
		os.Remove(filepath.Join(root, "diagnostics/last-error"))
		return nil
	}
	if string(pending) != fingerprint {
		next, roles, err := prepareSMBProjection(ctx, s, candidate, root)
		if err != nil {
			return err
		}
		expected := []string{"smbd"}
		if clustered {
			expected = []string{"ctdbd", "smbd"}
		}
		if ad {
			expected = []string{"ctdbd", "winbindd", "smbd"}
		}
		if !reflect.DeepEqual(roles, expected) || !reflect.DeepEqual(next.AD, current.AD) {
			return fmt.Errorf("SMB authentication identity or runtime role change requires explicit maintenance")
		}

		// Projection rechecks RADOS/config inputs; also re-read manager policy
		// rather than treating the earlier logical response as immutable.
		logical, err = cephRunContext(ctx, "smb", "show", "ceph.smb.cluster."+clusterID, "--format", "json")
		if err != nil {
			return fmt.Errorf("recheck logical SMB cluster failed")
		}
		present, err = smbLogicalPresent(clusterID, []byte(logical))
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("logical SMB cluster changed during preparation; retry")
		}
		recheck, err := readSMBPreparedCandidate(ctx, s, clusterID, root, []byte(logical))
		if err != nil {
			return err
		}
		if recheck.fingerprint() != candidate.fingerprint() {
			return fmt.Errorf("SMB sources changed during preparation; retry")
		}
		enabled, err = smbRefreshEnabled(ctx, root)
		if err != nil || !enabled {
			return err
		}
		// A non-fingerprint marker means publication/control is incomplete. Source
		// reversion must not bypass it after partial writes or uncertain controls.
		err = smbAtomicWrite(pendingPath, []byte("configuration update is incomplete\n"))
		if err != nil {
			return err
		}
		err = applySMBRefresh(ctx, root)
		if err != nil {
			return err
		}
		err = smbAtomicWrite(pendingPath, []byte(fingerprint))
		if err != nil {
			return err
		}
	} else if !current.Startup["smbd"].Prepared || published.configurationFingerprint() != fingerprint {
		return fmt.Errorf("SMB completed configuration state is inconsistent; explicit maintenance required")
	}
	// Successful publication/import/restart is durable before health observation.
	// Later ticks can confirm it without repeating successful mutating controls.
	err = smbReadyNativeFunc(ctx, root, "smbd")
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.UpdateSMBServiceInfo(ctx, tx, clusterID, s.ClusterState().Name(), launch.serviceInfo(candidate.Spec.ConfigURI))
	})
	if err != nil {
		return err
	}
	return confirmSMBConfiguration(root, candidate)
}

func readSMBPublishedCandidate(root, clusterID string) (smbCandidate, error) {
	var candidate smbCandidate
	raw, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
	if err != nil {
		return candidate, err
	}
	err = json.Unmarshal(raw, &candidate)
	if err != nil {
		return candidate, fmt.Errorf("invalid published SMB candidate")
	}
	if candidate.Spec.ClusterID != clusterID || candidate.Endpoint == nil || !json.Valid(candidate.Config) || !json.Valid(candidate.SpecBytes) || !json.Valid(candidate.Logical) {
		return candidate, fmt.Errorf("incomplete published SMB candidate")
	}
	spec, err := decodeSMBSpec(clusterID, candidate.SpecBytes)
	if err != nil {
		return candidate, err
	}
	if !reflect.DeepEqual(spec, candidate.Spec) || candidate.CephConfig == "" || len(candidate.Users) != len(candidate.Spec.UserSources) || len(candidate.Joins) != len(candidate.Spec.JoinSources) {
		return candidate, fmt.Errorf("inconsistent published SMB candidate")
	}
	present, err := smbLogicalPresent(clusterID, candidate.Logical)
	if err != nil {
		return candidate, err
	}
	if !present {
		return candidate, fmt.Errorf("published SMB cluster is absent")
	}
	document, err := smbCombinedDocument(candidate)
	if err != nil {
		return candidate, err
	}
	configs, ok := document["configs"].(map[string]any)
	if !ok || document["samba-container-config"] != "v0" {
		return candidate, fmt.Errorf("incomplete published SMB configuration")
	}
	_, ok = configs[clusterID].(map[string]any)
	if !ok {
		return candidate, fmt.Errorf("published SMB configuration lacks local instance")
	}
	err = validateSMBLogicalPolicy(candidate.Logical, candidate.Spec)
	return candidate, err
}

func confirmSMBConfiguration(root string, candidate smbCandidate) error {
	err := smbAtomicWrite(filepath.Join(root, "applied.json"), []byte(candidate.configurationFingerprint()))
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(root, "config-update-pending"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	os.Remove(filepath.Join(root, "diagnostics/last-error"))
	return nil
}

func smbRefreshEnabled(ctx context.Context, root string) (bool, error) {
	err := checkSMBStartIntent(root)
	if err != nil {
		return false, err
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return false, err
	}
	roles := []string{"smbd"}
	if launch.Clustered {
		roles = append(roles, "ctdbd")
	}
	if launch.AD != nil {
		roles = append(roles, "winbindd")
	}
	for _, role := range roles {
		out, err := runSMBServiceCommand(ctx, "services", "microceph."+role)
		if err != nil {
			return false, fmt.Errorf("SMB service intent check failed")
		}
		found := false
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[0] == "microceph."+role {
				if fields[1] == "disabled" {
					return false, nil
				}
				found = fields[1] == "enabled"
			}
		}
		if !found {
			return false, fmt.Errorf("SMB service intent is unknown")
		}
	}
	return true, nil
}

// applySMBRefresh quiesces consumers before context-first publication and SAM
// import. Compensation is detached but never changes snapd startup intent or
// writes the user stop fence; incomplete work remains retryable.
func applySMBRefresh(ctx context.Context, root string) (retErr error) {
	smbMembershipMu.Lock()
	defer smbMembershipMu.Unlock()
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, quiesceSMBRefresh(context.Background(), root))
		}
	}()
	err := quiesceSMBRefresh(ctx, root)
	if err != nil {
		return err
	}
	enabled, err := smbRefreshEnabled(ctx, root)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("SMB was disabled during configuration update")
	}
	err = publishSMBNativeFunc(ctx, root)
	if err != nil {
		return err
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.AD != nil {
		err = prepareSMBADPaths(root)
		if err != nil {
			return err
		}
		err = smbADPrepareFunc(ctx, root, false)
		if err != nil {
			return err
		}
		_, err = runSMBServiceCommand(ctx, "restart", "microceph.winbindd")
		if err != nil {
			return err
		}
		err = (smbSnapService{name: "winbindd", root: root}).Ready(ctx)
		if err == nil {
			err = setSMBPrepared(root, "smbd", false)
		}
	} else {
		err = prepareSMBAccounts(ctx, root, false)
	}
	if err != nil {
		return err
	}
	enabled, err = smbRefreshEnabled(ctx, root)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("SMB was disabled during configuration update")
	}
	_, err = runSMBServiceCommand(ctx, "restart", "microceph.smbd")
	return err
}

func quiesceSMBRefresh(ctx context.Context, root string) error {
	roles := []string{"smbd"}
	launch, err := loadSMBLaunchForPublication(root)
	if err != nil {
		return err
	}
	if launch.AD != nil {
		roles = append(roles, "winbindd")
	}
	for _, role := range roles {
		_, err = runSMBServiceCommand(ctx, "stop", "microceph."+role)
		if err != nil {
			return err
		}
		err = confirmSMBInactive(ctx, role)
		if err != nil {
			return err
		}
	}
	return nil
}

// updateSMBBootstrapCandidate advances only private bootstrap inputs, without
// invalidating prepared context, restarting consumers or authorizing a new join.
func updateSMBBootstrapCandidate(ctx context.Context, s interfaces.StateInterface, root string, candidate smbCandidate) error {
	if !candidate.Spec.hasFeature("domain") {
		return nil
	}
	smbMembershipMu.Lock()
	defer smbMembershipMu.Unlock()
	previous, err := readSMBPublishedCandidate(root, candidate.Spec.ClusterID)
	if err != nil {
		return err
	}
	if previous.fingerprint() == candidate.fingerprint() {
		return nil
	}
	if previous.configurationFingerprint() != candidate.configurationFingerprint() {
		return fmt.Errorf("bootstrap update changed serving configuration")
	}
	member, err := inspectSMBADMember(ctx, root, candidate)
	if err != nil {
		return err
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(member, launch.AD) {
		return fmt.Errorf("bootstrap update changed AD identity")
	}

	recheck, err := readSMBPreparedCandidate(ctx, s, candidate.Spec.ClusterID, root, candidate.Logical)
	if err != nil {
		return err
	}
	if candidate.fingerprint() != recheck.fingerprint() {
		return fmt.Errorf("SMB bootstrap sources changed during preparation")
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	err = checkSMBStartIntent(root)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	return smbAtomicWrite(filepath.Join(root, "config/candidate.json"), raw)
}

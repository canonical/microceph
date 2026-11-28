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
	var warning string
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := database.GetSMBConfiguredService(ctx, tx, clusterID, s.ClusterState().Name())
		if err != nil {
			return err
		}
		started, err = database.GetSMBLocalSAMStarted(ctx, tx, clusterID, s.ClusterState().Name())
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
	if !started {
		return fmt.Errorf("SMB local SAM initialization is not confirmed; explicit maintenance required")
	}
	err = checkSMBLocalSAM(root)
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
	fingerprint := candidate.configurationFingerprint()
	if string(applied) == fingerprint && errors.Is(pendingErr, os.ErrNotExist) {
		// Health observation is not process repair or permission to reimport SAM.
		err = smbReadyNativeFunc(ctx, root, "smbd")
		if err != nil {
			diagnostic = smbUnreadyDiagnostic
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		os.Remove(filepath.Join(root, "diagnostics/last-error"))
		return nil
	}
	if string(pending) != fingerprint {
		_, roles, err := prepareSMBProjection(ctx, s, candidate, root)
		if err != nil {
			return err
		}
		if len(roles) != 1 || roles[0] != "smbd" {
			return fmt.Errorf("SMB runtime role change requires explicit maintenance")
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
	if !reflect.DeepEqual(spec, candidate.Spec) || candidate.CephConfig == "" || len(candidate.Users) != len(candidate.Spec.UserSources) {
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
	out, err := runSMBServiceCommand(ctx, "services", "microceph.smbd")
	if err != nil {
		return false, fmt.Errorf("SMB service intent check failed")
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "microceph.smbd" {
			if fields[1] == "disabled" {
				return false, nil
			}
			if fields[1] == "enabled" {
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("SMB service intent is unknown")
}

// applySMBRefresh quiesces consumers before context-first publication and SAM
// import. Compensation is detached but never changes snapd startup intent or
// writes the user stop fence; incomplete work remains retryable.
func applySMBRefresh(ctx context.Context, root string) (retErr error) {
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, quiesceSMBRefresh(context.Background()))
		}
	}()
	err := quiesceSMBRefresh(ctx)
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
	err = prepareSMBAccounts(ctx, root, false)
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

func quiesceSMBRefresh(ctx context.Context) error {
	_, err := runSMBServiceCommand(ctx, "stop", "microceph.smbd")
	if err != nil {
		return err
	}
	return confirmSMBInactive(ctx, "smbd")
}

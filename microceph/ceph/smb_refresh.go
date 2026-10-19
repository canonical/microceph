package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

const (
	smbUpdateInterval = 30 * time.Second
	smbUpdateTimeout  = 25 * time.Second
)

const smbMembershipPending = "membership publication is not confirmed applied\n"

const smbUnreadyDiagnostic = "Local SMB gateway is not ready; previously applied configuration is unchanged.\n"

// SMBLocalStatus reports ownership/readiness without private source fingerprints.
func SMBLocalStatus(ctx context.Context) (types.SMBStatus, error) {
	status := types.SMBStatus{}
	owner, err := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.ClusterID = string(owner)
	err = validateSMBClusterID(status.ClusterID)
	if err != nil {
		return status, err
	}
	root := smbRoot(status.ClusterID)
	_, err = os.Stat(filepath.Join(root, "stop-requested"))
	status.StopRequested = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	diagnostic, err := os.ReadFile(filepath.Join(root, "diagnostics", "last-error"))
	if err == nil {
		status.Error = "local update failed; configuration is not confirmed applied"
		if string(diagnostic) == smbUnreadyDiagnostic {
			status.Error = "local SMB gateway is not ready; previously applied configuration is unchanged"
		}
	}
	_, err = os.Stat(filepath.Join(root, "config-update-pending"))
	if err == nil {
		status.Error = "local configuration update is not confirmed applied"
	}
	if !status.StopRequested {
		status.Ready = smbReadyNativeFunc(ctx, root, "smbd") == nil
	}
	return status, nil
}

// startSMBUpdater has no dependency on the monitor-change loop. It is the only
// native periodic source updater. Placement remains an explicit/startup path.
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
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	// The work budget must not expire while a placement holds the lock.
	// Shutdown while queued is not a failed configuration application.
	err := ctx.Err()
	if err != nil {
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, smbUpdateTimeout)
	defer cancel()
	if s.ClusterState().Database().IsOpen(queryCtx) != nil {
		return nil
	}
	return refreshSMBLocked(queryCtx, s)
}

// refreshSMBLocked only updates an already owned, configured deployment. Call
// with serviceStartMu held. It never enters enablement, reserves an owner,
// provisions keys/module/resources, or inserts an enabled service row.
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
	applied, err := os.ReadFile(filepath.Join(root, "applied.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		return nil
	}
	diagnostic := "Local SMB update failed; configuration is not confirmed applied.\n"
	defer func() {
		if retErr != nil {
			_ = smbAtomicWrite(filepath.Join(root, "diagnostics", "last-error"), []byte(diagnostic))
		}
	}()
	var clustered bool
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		clustered, err = database.GetSMBConfiguredService(ctx, tx, clusterID, s.ClusterState().Name())
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
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
		// Independently confirmed logical deletion is exceptional containment,
		// not ordinary refresh. Unknown source failures cannot reach this path.
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
	candidate, err := readSMBPreparedCandidate(ctx, s, clusterID, root, []byte(logical), true)
	if err != nil {
		return err
	}
	if candidate.Spec.hasFeature("clustered") != clustered {
		return fmt.Errorf("SMB mode changed; explicit maintenance required")
	}
	contextBytes, err := os.ReadFile(filepath.Join(root, "config", "context.json"))
	if err != nil {
		return err
	}
	var current smbLaunch
	err = json.Unmarshal(contextBytes, &current)
	if err != nil {
		return err
	}
	if current.Clustered != clustered {
		return fmt.Errorf("SMB launch mode does not match configured ownership")
	}
	roles, err := smbCoreRoles(candidate.Spec, current.Proxy)
	if err != nil {
		return err
	}
	pending := filepath.Join(root, "config-update-pending")
	pendingFingerprint, pendingErr := os.ReadFile(pending)
	if pendingErr != nil && !errors.Is(pendingErr, os.ErrNotExist) {
		return pendingErr
	}
	if string(applied) == candidate.fingerprint() && errors.Is(pendingErr, os.ErrNotExist) {
		// Health is observation, not an input change or permission to reimport
		// SAM/start services. snapd and CTDB own ordinary process recovery.
		for _, role := range roles {
			err = smbReadyNativeFunc(ctx, root, role)
			if err != nil {
				diagnostic = smbUnreadyDiagnostic
				return err
			}
		}
		os.Remove(filepath.Join(root, "diagnostics", "last-error"))
		return nil
	}
	if string(pendingFingerprint) != candidate.fingerprint() {
		// Compare actual published configuration, not the older confirmed hash:
		// membership can change while completed config work awaits health.
		configurationChanged := true
		published, readErr := os.ReadFile(filepath.Join(root, "config", "candidate.json"))
		if readErr == nil {
			var live smbCandidate
			readErr = json.Unmarshal(published, &live)
			if readErr != nil {
				return readErr
			}
			configurationChanged = live.configurationFingerprint() != candidate.configurationFingerprint()
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		// A partial publication/control failure still requires the config path.
		if pendingErr == nil && len(pendingFingerprint) != 64 && string(pendingFingerprint) != smbMembershipPending {
			configurationChanged = true
		}

		launch, nextRoles, err := prepareSMBProjection(ctx, s, candidate, root)
		if err != nil {
			return err
		}
		if launch.Clustered != current.Clustered || launch.Proxy != current.Proxy {
			return fmt.Errorf("SMB runtime roles changed; explicit maintenance required")
		}
		err = checkSMBStartIntent(root)
		if err != nil {
			return err
		}
		// Invalidate any completed-control fingerprint before live writes. Partial
		// publication/control must retry, even if sources subsequently revert.
		incomplete := "configuration update is not confirmed applied\n"
		if !configurationChanged {
			incomplete = smbMembershipPending
		}
		err = smbAtomicWrite(pending, []byte(incomplete))
		if err != nil {
			return err
		}
		err = publishSMBNativeFunc(ctx, root, true)
		if err != nil {
			return err
		}
		// NSS files are private bind mounts, and SAM preparation runs at smbd
		// launch. Restart only Samba consumers, without enable/disable flags.
		if configurationChanged {
			err = refreshSMBApps(root, nextRoles)
			if err != nil {
				return err
			}
		}
		// Publication and all restart requests succeeded; health is observation
		// from here on, not permission to repeat completed configuration work.
		err = smbAtomicWrite(pending, []byte(candidate.fingerprint()))
		if err != nil {
			return err
		}
		current, roles = launch, nextRoles
	}
	for _, role := range roles {
		err = smbReadyNativeFunc(ctx, root, role)
		if err != nil {
			return err
		}
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.UpdateSMBServiceInfo(ctx, tx, clusterID, s.ClusterState().Name(), current.serviceInfo(candidate.Spec.ConfigURI))
	})
	if err != nil {
		return err
	}
	err = smbAtomicWrite(filepath.Join(root, "applied.json"), []byte(candidate.fingerprint()))
	if err != nil {
		return err
	}
	err = os.Remove(pending)
	if err != nil {
		return err
	}
	os.Remove(filepath.Join(root, "diagnostics", "last-error"))
	return nil
}

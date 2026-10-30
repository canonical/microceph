package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// SMBServicePlacement identifies an upstream logical cluster, never its secrets.
type SMBServicePlacement struct {
	ClusterID string `json:"cluster_id"`
}

// PopulateParams validates the existing service-family payload.
func (p *SMBServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	err := json.Unmarshal([]byte(payload), p)
	if err != nil {
		return err
	}
	return validateSMBClusterID(p.ClusterID)
}

// HospitalityCheck defers to the group-aware native ownership transaction.
func (p *SMBServicePlacement) HospitalityCheck(s interfaces.StateInterface) error { return nil }

// ServiceInit prepares, applies and records the local service under the placement lock.
func (p *SMBServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	return enableSMBLocked(ctx, s, p.ClusterID)
}

// PostPlacementCheck is included in ServiceInit to contain failed readiness.
func (p *SMBServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error { return nil }

// DbUpdate is included in ServiceInit to contain failed publication.
func (p *SMBServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return nil
}

func smbBase() string                 { return filepath.Join(os.Getenv("SNAP_COMMON"), "smb") }
func smbRoot(clusterID string) string { return filepath.Join(smbBase(), clusterID) }

func ensureSMBModule(ctx context.Context) error {
	out, err := cephRunContext(ctx, "mgr", "module", "ls", "--format", "json")
	if err != nil {
		return fmt.Errorf("query SMB module setup failed")
	}
	var modules struct {
		Enabled []string `json:"enabled_modules"`
	}
	err = json.Unmarshal([]byte(out), &modules)
	if err != nil {
		return err
	}
	active := false
	for _, name := range modules.Enabled {
		active = active || name == "smb"
	}
	option, optionErr := cephRunContext(ctx, "config", "get", "mgr", "mgr/smb/update_orchestration")
	if active && (optionErr != nil || strings.TrimSpace(option) != "false") {
		return fmt.Errorf("SMB module is active with conflicting/unknown orchestration setup; disable it in maintenance, set mgr/smb/update_orchestration=false, then reinitialize")
	}
	if active {
		return nil
	}
	_, err = cephRunContext(ctx, "config", "set", "mgr", "mgr/smb/update_orchestration", "false")
	if err != nil {
		return fmt.Errorf("persist SMB orchestration setting failed")
	}
	_, err = cephRunContext(ctx, "mgr", "module", "enable", "smb")
	if err != nil {
		return fmt.Errorf("initialize SMB module failed")
	}
	return nil
}

func smbReadSource(ctx context.Context, clusterID, root, uri string) ([]byte, error) {
	err := validateSMBSourceURI(clusterID, uri)
	if err != nil {
		return nil, err
	}
	args := []string{"--id", "smb.config.cluster." + clusterID, "--keyring", filepath.Join(root, "identity", "config.keyring")}
	var out string
	if strings.HasPrefix(uri, "rados:mon-config-key:") {
		key := strings.TrimPrefix(uri, "rados:mon-config-key:")
		out, err = common.ProcessExec.RunCommandContext(ctx, "ceph", append(args, "config-key", "get", key)...)
		if err == nil {
			return []byte(out), nil
		}
	} else {
		object := strings.TrimPrefix(uri, "rados://.smb/"+clusterID+"/")
		file, createErr := os.CreateTemp(filepath.Join(root, "config"), "source-*")
		if createErr != nil {
			return nil, createErr
		}
		path := file.Name()
		file.Close()
		defer os.Remove(path)
		_, err = common.ProcessExec.RunCommandContext(ctx, "rados", append(args, "-p", ".smb", "-N", clusterID, "get", object, path)...)
		if err == nil {
			return os.ReadFile(path)
		}
	}
	_ = smbAtomicWrite(filepath.Join(root, "diagnostics", "source-error"), []byte(err.Error()))
	// Permission/transport errors never authorize removal of an override.
	if strings.Contains(err.Error(), "No such file or directory") || strings.Contains(err.Error(), "Error ENOENT") {
		return nil, errSMBSourceMissing
	}
	return nil, fmt.Errorf("SMB source read failed")
}

func ensureSMBConfigKey(ctx context.Context, clusterID, root string) error {
	name := "smb.config.cluster." + clusterID
	key, err := CreateClientKey(ctx, name, []string{"mon", `allow r, allow command "config-key get" with "key" prefix "smb/config/` + clusterID + `/"`}, []string{"osd", "allow rw pool=.smb namespace=" + clusterID})
	if err != nil {
		return fmt.Errorf("retrieve restricted SMB config identity failed")
	}
	return smbAtomicWrite(filepath.Join(root, "identity", "config.keyring"), []byte("[client."+name+"]\n\tkey = "+key+"\n"))
}

func enableSMBLocked(ctx context.Context, s interfaces.StateInterface, clusterID string) (retErr error) {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	root := smbRoot(clusterID)
	err = ensureSMBModule(ctx)
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
		owner, ownerErr := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
		if ownerErr == nil && string(owner) == clusterID {
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
		}
		return fmt.Errorf("logical SMB cluster is absent; local serving is stopped, retained ownership requires explicit retirement")
	}
	// Reject unsupported policy before local key, ownership or serving effects.
	specRaw, err := common.ProcessExec.RunCommandContext(ctx, "rados", "-p", ".smb", "-N", clusterID, "get", "spec.smb", "-")
	if err != nil {
		return fmt.Errorf("read logical SMB policy spec failed")
	}
	spec, err := decodeSMBSpec(clusterID, []byte(specRaw))
	if err != nil {
		return err
	}
	err = validateSMBLogicalPolicy([]byte(logical), spec)
	if err != nil {
		return err
	}
	for _, dir := range []string{"config", "identity", "samba/private", "samba/state", "samba/cache", "samba/lock", "samba/run", "samba/ncalrpc", "ctdb", "diagnostics"} {
		err = smbPrivateDir(filepath.Join(root, dir))
		if err != nil {
			return err
		}
	}
	defer func() {
		if retErr != nil {
			_ = smbAtomicWrite(filepath.Join(root, "diagnostics", "last-error"), []byte("Local SMB application failed; configuration is not confirmed applied.\n"))
		}
	}()
	err = ensureSMBConfigKey(ctx, clusterID, root)
	if err != nil {
		return err
	}
	candidate, err := readSMBPreparedCandidateUnreserved(ctx, s, clusterID, root, []byte(logical))
	if err != nil {
		return err
	}
	err = inspectSMBPolicyFunc(ctx, root, candidate)
	if err != nil {
		return err
	}
	ownerFile := filepath.Join(smbBase(), "owner.json")
	previous, err := os.ReadFile(ownerFile)
	if err == nil && string(previous) != clusterID {
		return fmt.Errorf("another local SMB cluster retains ownership")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var policyWarning string
	var samStarted bool
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if candidate.Spec.hasFeature("clustered") {
			_, err = database.ReserveSMBClusteredOwner(ctx, tx, clusterID, s.ClusterState().Name())
			if err == nil {
				candidate.CTDB, err = database.GetSMBCTDBView(ctx, tx, clusterID, s.ClusterState().Name())
			}
		} else {
			err = database.ReserveSMBOwner(ctx, tx, clusterID, s.ClusterState().Name())
		}
		if err != nil {
			return err
		}
		policyWarning, err = database.GetSMBPolicyWarning(ctx, tx, clusterID)
		if err != nil {
			return err
		}
		if !candidate.Spec.hasFeature("clustered") {
			samStarted, err = database.GetSMBLocalSAMStarted(ctx, tx, clusterID, s.ClusterState().Name())
		}
		return err
	})
	if err != nil {
		return err
	}
	err = writeSMBPolicyWarning(root, policyWarning)
	if err != nil {
		return err
	}
	err = smbAtomicWrite(ownerFile, []byte(clusterID))
	if err != nil {
		return err
	}
	if !candidate.Spec.hasFeature("clustered") {
		if samStarted {
			err = checkSMBLocalSAM(root)
		} else {
			err = checkSMBFreshLocalSAM(root, true)
		}
	}
	if err != nil {
		return err
	}
	err = ensureSMBDataKey(ctx, clusterID, filepath.Join(root, "identity", "data.keyring"))
	if err != nil {
		return err
	}
	if candidate.CTDB != nil {
		if candidate.Endpoint.FrontendAddress == "" {
			candidate.Endpoint.FrontendAddress = candidate.CTDB.Member.Node
		}
		err = ensureSMBRecoveryKey(ctx, clusterID, root)
		if err != nil {
			return err
		}
		err = updateConfigFunc(ctx, s)
		if err != nil {
			return fmt.Errorf("render SMB recovery client: %w", err)
		}
		data, readErr := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, constants.CephConfFileName))
		if readErr != nil {
			return readErr
		}
		candidate.CephConfig = string(data)
	}
	applied, appliedErr := os.ReadFile(filepath.Join(root, "applied.json"))
	_, pendingErr := os.Lstat(filepath.Join(root, "config-update-pending"))
	if appliedErr == nil && string(applied) == candidate.configurationFingerprint() && errors.Is(pendingErr, os.ErrNotExist) {
		roles, roleErr := smbCoreRoles(candidate.Spec)
		ready := roleErr == nil
		for _, role := range roles {
			if smbReadyNativeFunc(ctx, root, role) != nil {
				ready = false
			}
		}
		if ready {
			if candidate.CTDB != nil {
				err = publishSMBMembership(ctx, root, candidate)
			}
			return err
		}
	}
	launch, roles, err := prepareSMBProjection(ctx, s, candidate, root)
	if err != nil {
		return err
	}
	ops := smbSnapOps(root)
	// Explicit maintenance always quiesces consumers before live publication.
	err = stopSMBApps(root, ops)
	if err != nil {
		return err
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.WithdrawSMBService(ctx, tx, clusterID, s.ClusterState().Name())
	})
	if err != nil {
		return err
	}
	err = publishSMBNativeFunc(ctx, root)
	if err != nil {
		return err
	}
	if launch.Clustered {
		err = prepareSMBCTDBFilesFunc(ctx, root, os.Getenv("SNAP"))
		if err != nil {
			return err
		}
	}
	err = os.Remove(filepath.Join(root, "stop-requested"))
	if err != nil {
		return err
	}
	ops.prepare = func(ctx context.Context, role string) error {
		if role != "smbd" {
			return nil
		}
		if !launch.Clustered {
			err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				return database.StartSMBLocalSAM(ctx, tx, clusterID, s.ClusterState().Name())
			})
			if err != nil {
				return err
			}
		}
		return prepareSMBAccounts(ctx, root, !samStarted)
	}
	err = startSMBApps(ctx, root, roles, ops)
	if err != nil {
		return err
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.RecordSMBService(ctx, tx, clusterID, s.ClusterState().Name(), launch.serviceInfo(candidate.Spec.ConfigURI))
	})
	if err != nil {
		return errors.Join(err, stopSMBApps(root, ops))
	}
	return confirmSMBConfiguration(root, candidate)
}

// DisableSMB retires only the exact confirmed-stopped local native owner.
// Purge never deletes upstream resources, shared keys or CephFS data.
func DisableSMB(ctx context.Context, s interfaces.StateInterface, clusterID string, purge bool) error {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	return disableSMBLocked(ctx, s, clusterID, purge)
}

var removeSMBLocalStateFunc = os.RemoveAll

func disableSMBLocked(ctx context.Context, s interfaces.StateInterface, clusterID string, purge bool) error {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	ownerPath := filepath.Join(smbBase(), "owner.json")
	owner, ownerErr := os.ReadFile(ownerPath)
	if ownerErr != nil && !errors.Is(ownerErr, os.ErrNotExist) {
		return ownerErr
	}
	if ownerErr == nil && string(owner) != clusterID {
		return fmt.Errorf("SMB local owner does not match requested cluster")
	}
	var groups []string
	var samStarted bool
	var clustered bool
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		groups, err = database.GetSMBOwnerGroups(ctx, tx, s.ClusterState().Name())
		if err != nil || !purge {
			return err
		}
		clustered, err = database.IsSMBClusteredGroup(ctx, tx, clusterID)
		if err != nil {
			return err
		}
		if clustered {
			samStarted, err = database.HasSMBClusteredHistory(ctx, tx, clusterID, s.ClusterState().Name())
		} else {
			samStarted, err = database.GetSMBLocalSAMStarted(ctx, tx, clusterID, s.ClusterState().Name())
		}
		return err
	})
	if err != nil {
		return err
	}
	if len(groups) > 1 || (len(groups) == 1 && groups[0] != clusterID) {
		return fmt.Errorf("SMB retained ownership conflicts with requested cluster")
	}
	historicalPurge := len(groups) == 0 && errors.Is(ownerErr, os.ErrNotExist)
	if historicalPurge && (!purge || !samStarted) {
		return ownerErr // absence alone is not authority to stop or destroy state
	}
	root := smbRoot(clusterID)
	if historicalPurge {
		// No current owner authorizes stopping the shared app. An exact retired
		// history permits destruction only after read-only inactive confirmation.
		err = smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop requested\n"))
		if err == nil {
			for _, role := range smbStopOrder {
				err = confirmSMBInactive(ctx, role)
				if err != nil {
					break
				}
			}
		}
	} else {
		err = stopSMBApps(root, smbSnapOps(root))
	}
	if err != nil {
		return err
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.RetireSMBOwner(ctx, tx, clusterID, s.ClusterState().Name())
	})
	if err != nil {
		return err
	}
	err = os.Remove(ownerPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if purge {
		err = removeSMBLocalStateFunc(root)
		if err != nil {
			return err
		}
		if clustered {
			return nil
		}
		return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			return database.ClearSMBLocalSAMStarted(ctx, tx, clusterID, s.ClusterState().Name())
		})
	}
	return nil
}

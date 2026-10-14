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

// ServiceInit prepares, applies and records the local service while placement holds the lifecycle lock.
func (p *SMBServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	return enableSMBLocked(ctx, s, p.ClusterID, true)
}

// PostPlacementCheck is included in ServiceInit so failed readiness stops attempted apps.
func (p *SMBServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error { return nil }

// DbUpdate is included in ServiceInit so a failed publication stops attempted apps.
func (p *SMBServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return nil
}

func smbBase() string                 { return filepath.Join(os.Getenv("SNAP_COMMON"), "smb") }
func smbRoot(clusterID string) string { return filepath.Join(smbBase(), clusterID) }

func smbNativeCommand(ctx context.Context, command, root string, args ...string) error {
	argv := []string{"-B", filepath.Join(os.Getenv("SNAP"), "smb", "native.py"), command, root}
	argv = append(argv, args...)
	_, err := common.ProcessExec.RunCommandContext(ctx, filepath.Join(os.Getenv("SNAP"), "bin", "python3"), argv...)
	if err != nil {
		return fmt.Errorf("native SMB %s failed; inspect private cluster diagnostics", command)
	}
	return nil
}

func smbWaitInactive(role string) error {
	out, err := common.ProcessExec.RunCommand("snapctl", "services", "microceph."+role)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "microceph."+role && fields[2] == "inactive" {
			return nil
		}
	}
	return fmt.Errorf("SMB %s exit is unconfirmed", role)
}

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
	// Only a documented ENOENT is absence. Permission/transport errors must
	// never be interpreted as removal of an override or identity source.
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

func enableSMBLocked(ctx context.Context, s interfaces.StateInterface, clusterID string, explicit bool) (retErr error) {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	root := smbRoot(clusterID)
	for _, dir := range []string{"config", "identity", "samba/private", "samba/state", "samba/cache", "samba/lock", "samba/run", "samba/ncalrpc", "ctdb", "proxy-work", "diagnostics"} {
		err = os.MkdirAll(filepath.Join(root, dir), 0700)
		if err != nil {
			return err
		}
	}
	defer func() {
		if retErr != nil {
			_ = smbAtomicWrite(filepath.Join(root, "diagnostics", "last-error"), []byte("Local SMB application failed; configuration is not confirmed applied.\n"))
		}
	}()
	if !explicit {
		err = checkSMBStartIntent(root)
		if err != nil {
			return err
		}
	}
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
	err = ensureSMBConfigKey(ctx, clusterID, root)
	if err != nil {
		return err
	}
	read := func(uri string) ([]byte, error) { return smbReadSource(ctx, clusterID, root, uri) }
	candidate, err := readSMBCandidate(clusterID, read)
	if err != nil {
		return err
	}
	cephConfig, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "ceph.conf"))
	if err != nil {
		return err
	}
	candidate.CephConfig = string(cephConfig)
	candidate.Logical = json.RawMessage(logical)
	// Cluster/domain/remote roles are attached by their subsequent feature stages.
	_, err = smbCoreRoles(candidate.Spec, false)
	if err != nil {
		return err
	}
	if len(candidate.Spec.BindAddresses) > 0 {
		return fmt.Errorf("native bind-address planning requires the network feature stage")
	}
	ownerFile := filepath.Join(smbBase(), "owner.json")
	previous, err := os.ReadFile(ownerFile)
	if err == nil && string(previous) != clusterID {
		return fmt.Errorf("another local SMB cluster retains ownership")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := database.ReserveSMBOwner(ctx, tx, clusterID, s.ClusterState().Name(), candidate.Spec.hasFeature("clustered"))
		return err
	})
	if err != nil {
		return err
	}
	err = smbAtomicWrite(ownerFile, []byte(clusterID))
	if err != nil {
		return err
	}
	if explicit {
		_, stopErr := os.Stat(filepath.Join(root, "stop-requested"))
		if stopErr == nil {
			err = stopSMBApps(root, smbSnapOps(root))
			if err != nil {
				return err
			}
			err = os.Remove(filepath.Join(root, "stop-requested"))
			if err != nil {
				return err
			}
		} else if !errors.Is(stopErr, os.ErrNotExist) {
			return stopErr
		}
	}
	err = ensureSMBDataKey(ctx, clusterID, filepath.Join(root, "identity", "data.keyring"))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	err = smbAtomicWrite(filepath.Join(root, "config", "candidate.next.json"), raw)
	if err != nil {
		return err
	}
	err = smbNativeCommand(ctx, "compile", root)
	if err != nil {
		return err
	}
	// Re-read all bytes after render/preflight; reject an obvious torn update.
	recheck, err := readSMBCandidate(clusterID, read)
	if err != nil {
		return err
	}
	recheck.CephConfig = candidate.CephConfig
	recheck.Logical = candidate.Logical
	if recheck.fingerprint() != candidate.fingerprint() {
		return fmt.Errorf("SMB sources changed during preparation; retry")
	}
	applied, err := os.ReadFile(filepath.Join(root, "applied.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var launch struct {
		Proxy   bool `json:"proxy"`
		SMBPort int  `json:"smb_port"`
	}
	contextBytes, err := os.ReadFile(filepath.Join(root, "config", "next", "context.json"))
	if err != nil {
		return err
	}
	err = json.Unmarshal(contextBytes, &launch)
	if err != nil {
		return err
	}
	roles, err := smbCoreRoles(candidate.Spec, launch.Proxy)
	if err != nil {
		return err
	}
	unchanged := string(applied) == candidate.fingerprint()
	ready := true
	for _, role := range roles {
		if smbNativeCommand(ctx, "ready", root, role) != nil {
			ready = false
			break
		}
	}
	if unchanged && ready {
		os.Remove(filepath.Join(root, "diagnostics", "last-error"))
		return nil
	}
	ops := smbSnapOps(root)
	// Conservatively stop consumers before every changed effective projection.
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
	err = smbNativeCommand(ctx, "publish", root)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(root, "stop-requested"))
	if err != nil {
		return err
	}
	err = startSMBApps(ctx, root, roles, ops)
	if err != nil {
		return err
	}
	info, _ := json.Marshal(map[string]any{"clustered": false, "smb_port": launch.SMBPort})
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.RecordSMBService(ctx, tx, clusterID, s.ClusterState().Name(), string(info))
	})
	if err != nil {
		return errors.Join(err, stopSMBApps(root, ops))
	}
	err = smbAtomicWrite(filepath.Join(root, "applied.json"), []byte(candidate.fingerprint()))
	if err != nil {
		return errors.Join(err, stopSMBApps(root, ops))
	}
	os.Remove(filepath.Join(root, "diagnostics", "last-error"))
	return nil
}

// DisableSMB stops and retires only the exact local native owner. It never
// removes shared identities, upstream resources, CephFS data or AD accounts.
func DisableSMB(ctx context.Context, s interfaces.StateInterface, clusterID string, purge bool) error {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	owner, err := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
	if err != nil {
		return err
	}
	if string(owner) != clusterID {
		return fmt.Errorf("SMB local owner does not match requested cluster")
	}
	root := smbRoot(clusterID)
	err = stopSMBApps(root, smbSnapOps(root))
	if err != nil {
		return err
	}
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.RetireSMBOwner(ctx, tx, clusterID, s.ClusterState().Name())
	})
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(smbBase(), "owner.json"))
	if err != nil {
		return err
	}
	if purge {
		return os.RemoveAll(root)
	}
	return nil
}

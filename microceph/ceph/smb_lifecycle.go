package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

const smbSnapService = "smbd"

// FinalizeSMBServiceGroup clears local SMB rank reservations after upstream
// confirms the SMB cluster resource has been removed.
func FinalizeSMBServiceGroup(ctx context.Context, s interfaces.StateInterface, clusterID string, expectedConfig ...string) error {
	return database.FinalizeSMBServiceGroup(ctx, s, clusterID, expectedConfig...)
}

// DisableSMB stops a node-local SMB service and removes its local configuration.
func DisableSMB(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()

	localID, localErr := currentSMBClusterID()
	if localErr != nil && !os.IsNotExist(localErr) {
		return fmt.Errorf("failed to read local SMB cluster ID: %w", localErr)
	}
	if localErr == nil && localID != clusterID {
		return fmt.Errorf("SMB service already manages cluster '%s' on this host", localID)
	}
	exists, err := database.GroupedServicesQuery.ExistsOnHost(ctx, s, "smb", clusterID)
	if err != nil {
		return fmt.Errorf("failed to verify SMB service cluster ID: %w", err)
	}
	if !exists {
		services, err := database.GroupedServicesQuery.GetGroupedServicesOnHost(ctx, s)
		if err != nil {
			return fmt.Errorf("failed to check local SMB memberships: %w", err)
		}
		for _, service := range services {
			if service.Service == "smb" && service.GroupID != clusterID {
				return fmt.Errorf("SMB service already manages cluster '%s' on this host", service.GroupID)
			}
		}
		if os.IsNotExist(localErr) {
			return nil
		}
		// A matching local cluster marker is evidence of interrupted placement,
		// even if no successful member record was ever committed.
	}

	mode, err := getRecordedSMBModeFunc(ctx, s, clusterID)
	if err != nil {
		return fmt.Errorf("failed reading recorded SMB mode for removal: %w", err)
	}
	clustered := isSMBClusteredLocal()
	if mode != nil {
		clustered = *mode
	}
	services := []string{smbSnapService}
	if clustered {
		services = append(services, "ctdb-nodes", "ctdbd")
	}
	for _, service := range services {
		err = smbSnapAction(ctx, "stop", service, true)
		if err != nil {
			return fmt.Errorf("failed to stop SMB service %s: %w", service, err)
		}
	}

	if clustered && (exists || hasSMBCTDBRetirementInputs(clusterID)) {
		err = retireSMBCTDBMemberFunc(ctx, clusterID)
		if err != nil {
			return err
		}
	}
	if exists {
		err = database.GroupedServicesQuery.RemoveForHost(ctx, s, "smb", clusterID)
		if err != nil {
			return fmt.Errorf("failed to remove SMB service record: %w", err)
		}
	}

	err = removeSMBLocalState(clusterID)
	if err != nil {
		return err
	}

	if clustered {
		err = removeSMBConfigAuthIfUnused(ctx, s, clusterID)
		if err != nil {
			// The member and its database record are already gone, so returning
			// an error would make every retry fail before reaching this cleanup.
			logger.Warnf("failed to remove unused SMB configuration identity: %v", err)
		}
	}

	return nil
}

func removeSMBConfigAuthIfUnused(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	services, err := database.GroupedServicesQuery.GetGroupedServices(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to check remaining SMB service records: %w", err)
	}
	for _, service := range services {
		if service.Service == "smb" && service.GroupID == clusterID {
			return nil
		}
	}

	entity := fmt.Sprintf("client.smb.config.%s", clusterID)
	_, err = runSMBCommand(ctx, "ceph", "auth", "del", entity)
	if err != nil {
		return fmt.Errorf("failed to remove SMB configuration identity: %w", err)
	}
	return nil
}

type smbSavedFile struct {
	path string
	data []byte
	mode os.FileMode
	dir  bool
}

type smbLocalSnapshot struct {
	configDir string
	roots     []string
	files     []smbSavedFile
}

func snapshotSMBLocalState(clusterID string) (*smbLocalSnapshot, error) {
	paths := constants.GetPathConst()
	configDir := filepath.Join(paths.ConfPath, "samba")
	snapshot := &smbLocalSnapshot{configDir: configDir, roots: []string{
		configDir,
		filepath.Join(filepath.Dir(paths.ConfPath), "samba"),
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.fs.cluster.%s.keyring", clusterID)),
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.config.%s.keyring", clusterID)),
	}}
	if paths.DataPath != "" {
		snapshot.roots = append(snapshot.roots, filepath.Join(paths.DataPath, "samba", "smb.ctdb.conf"))
	}
	for _, root := range snapshot.roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			saved := smbSavedFile{path: path, mode: info.Mode().Perm(), dir: entry.IsDir()}
			if !entry.IsDir() {
				if !info.Mode().IsRegular() {
					return fmt.Errorf("cannot snapshot non-regular SMB file %s", path)
				}
				saved.data, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			snapshot.files = append(snapshot.files, saved)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func (snapshot *smbLocalSnapshot) restore() error {
	for _, root := range snapshot.roots {
		var err error
		if root == snapshot.configDir {
			err = clearSMBConfigDirectory(root)
		} else {
			err = os.RemoveAll(root)
		}
		if err != nil {
			return err
		}
	}
	// WalkDir visits parents first, but sort also handles files under different roots.
	sort.Slice(snapshot.files, func(i, j int) bool {
		return len(snapshot.files[i].path) < len(snapshot.files[j].path)
	})
	for _, file := range snapshot.files {
		if file.dir {
			err := os.MkdirAll(file.path, file.mode)
			if err != nil {
				return err
			}
			continue
		}
		err := os.MkdirAll(filepath.Dir(file.path), constants.PermissionOnlyUserAccess)
		if err != nil {
			return err
		}
		err = writeSMBFileAtomic(file.path, file.data, file.mode)
		if err != nil {
			return err
		}
	}
	return nil
}

func isSMBClusteredLocal() bool {
	paths := constants.GetPathConst()
	path := filepath.Join(filepath.Dir(paths.ConfPath), "samba", "ctdb.json")
	_, err := os.Stat(path)
	return err == nil
}

func hasSMBCTDBRetirementInputs(clusterID string) bool {
	paths := constants.GetPathConst()
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")
	pathsToCheck := []string{
		filepath.Join(runtimeDir, "ctdb-rank"),
		filepath.Join(runtimeDir, "ctdb-identity"),
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.config.%s.keyring", clusterID)),
	}
	for _, path := range pathsToCheck {
		_, err := os.Stat(path)
		if err != nil {
			return false
		}
	}
	return true
}

// clearSMBConfigDirectory keeps the inode bind-mounted at /etc/samba. Removing
// and recreating it leaves the snap mount namespace attached to a deleted directory.
func clearSMBConfigDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		err = os.RemoveAll(filepath.Join(path, entry.Name()))
		if err != nil {
			return err
		}
	}
	return nil
}

func removeSMBLocalState(clusterID string) error {
	paths := constants.GetPathConst()
	configDir := filepath.Join(paths.ConfPath, "samba")
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")
	keyringPaths := []string{
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.fs.cluster.%s.keyring", clusterID)),
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.config.%s.keyring", clusterID)),
	}

	err := clearSMBConfigDirectory(configDir)
	if err != nil {
		return fmt.Errorf("failed to clear SMB configuration directory: %w", err)
	}

	if paths.DataPath != "" {
		ctdbIncludePath := filepath.Join(paths.DataPath, "samba", "smb.ctdb.conf")
		err = os.Remove(ctdbIncludePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove SMB CTDB include: %w", err)
		}
	}

	for _, keyringPath := range keyringPaths {
		err = os.Remove(keyringPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove SMB CephX keyring: %w", err)
		}
	}

	err = clearSMBRuntimeDirectory(runtimeDir)
	if err != nil {
		return fmt.Errorf("failed to clear SMB runtime directory: %w", err)
	}
	markerPath := filepath.Join(runtimeDir, "cluster-id")
	err = os.Remove(markerPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove SMB cluster marker: %w", err)
	}
	err = os.Remove(runtimeDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove SMB runtime directory: %w", err)
	}

	return nil
}

// clearSMBRuntimeDirectory removes runtime state while retaining the ownership
// marker so interrupted cleanup can safely resume.
func clearSMBRuntimeDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "cluster-id" {
			continue
		}
		err = os.RemoveAll(filepath.Join(path, entry.Name()))
		if err != nil {
			return err
		}
	}
	return nil
}

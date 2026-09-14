package ceph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
)

const smbBaseConfig = "[global]\nconfig backend = registry\nlock directory = /var/lib/samba/lock\npid directory = /var/lib/samba/run\nncalrpc dir = /var/lib/samba/ncalrpc\nwinbindd socket directory = /var/lib/samba/winbindd\nstate directory = /var/lib/samba/state\ncache directory = /var/cache/samba\nprivate dir = /var/lib/samba/private\n"

var fetchSMBSourceFunc = fetchSMBSource

func materializeSMBConfig(placement *SMBServicePlacement) error {
	err := validateSMBConfigURI(placement.ClusterID, placement.ConfigURI)
	if err != nil {
		return err
	}

	for _, source := range placement.UserSources {
		err = validateSMBUserSourceURI(placement.ClusterID, source)
		if err != nil {
			return err
		}
	}

	containerConfig, err := fetchSMBSourceFunc(placement.ConfigURI)
	if err != nil {
		return fmt.Errorf("failed to fetch SMB container configuration: %w", err)
	}

	err = validateSMBContainerConfig(containerConfig)
	if err != nil {
		return err
	}

	userConfigs := make([][]byte, 0, len(placement.UserSources))
	for _, source := range placement.UserSources {
		userConfig, err := fetchSMBSourceFunc(source)
		if err != nil {
			return fmt.Errorf("failed to fetch SMB user configuration: %w", err)
		}
		userConfigs = append(userConfigs, userConfig)
	}

	paths := constants.GetPathConst()
	configDir := filepath.Join(paths.ConfPath, "samba")
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")

	err = os.MkdirAll(configDir, constants.PermissionWorldNoAccess)
	if err != nil {
		return fmt.Errorf("failed to create SMB configuration directory: %w", err)
	}

	err = os.MkdirAll(runtimeDir, constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to create SMB runtime configuration directory: %w", err)
	}

	err = writeSMBFileAtomic(filepath.Join(configDir, "smb.conf"), []byte(smbBaseConfig), constants.PermissionUserRwWorldRAccess)
	if err != nil {
		return fmt.Errorf("failed to write SMB base configuration: %w", err)
	}

	err = writeSMBFileAtomic(filepath.Join(runtimeDir, "container.json"), containerConfig, constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write SMB container configuration: %w", err)
	}

	err = writeSMBFileAtomic(filepath.Join(runtimeDir, "cluster-id"), []byte(placement.ClusterID+"\n"), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write SMB cluster ID: %w", err)
	}

	for index, userConfig := range userConfigs {
		userConfigPath := filepath.Join(runtimeDir, fmt.Sprintf("users-%d.json", index))
		err = writeSMBFileAtomic(userConfigPath, userConfig, constants.PermissionOnlyUserAccess)
		if err != nil {
			return fmt.Errorf("failed to write SMB user configuration: %w", err)
		}
	}

	err = removeStaleSMBUserConfigs(runtimeDir, len(userConfigs))
	if err != nil {
		return err
	}

	return nil
}

func fetchSMBSource(uri string) ([]byte, error) {
	if strings.HasPrefix(uri, "rados://") {
		return fetchSMBRADOSSource(uri)
	}

	const configKeyPrefix = "rados:mon-config-key:"
	if strings.HasPrefix(uri, configKeyPrefix) {
		key := strings.TrimPrefix(uri, configKeyPrefix)
		output, err := cephRun("config-key", "get", key)
		if err != nil {
			return nil, err
		}
		return []byte(output), nil
	}

	return nil, fmt.Errorf("unsupported SMB configuration URI %q", uri)
}

func fetchSMBRADOSSource(uri string) ([]byte, error) {
	matches := smbRADOSURIRegex.FindStringSubmatch(uri)
	if matches == nil {
		return nil, fmt.Errorf("invalid SMB RADOS URI %q", uri)
	}

	tempFile, err := os.CreateTemp("", "microceph-smb-source-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary SMB source file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	err = tempFile.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close temporary SMB source file: %w", err)
	}

	pool := ".smb"
	namespace := matches[1]
	object := matches[2]
	_, err = radosRun("--pool", pool, "-N", namespace, "get", object, tempPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SMB source from RADOS: %w", err)
	}

	return data, nil
}

func writeSMBFileAtomic(destPath string, data []byte, mode os.FileMode) error {
	tmpPath := destPath + ".tmp"
	err := os.WriteFile(tmpPath, data, mode)
	if err != nil {
		return err
	}

	err = os.Rename(tmpPath, destPath)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}

func removeStaleSMBUserConfigs(runtimeDir string, expectedCount int) error {
	paths, err := filepath.Glob(filepath.Join(runtimeDir, "users-*.json"))
	if err != nil {
		return fmt.Errorf("failed to list SMB user configurations: %w", err)
	}

	for _, path := range paths {
		base := filepath.Base(path)
		indexText := strings.TrimSuffix(strings.TrimPrefix(base, "users-"), ".json")
		var index int
		_, err = fmt.Sscanf(indexText, "%d", &index)
		if err != nil || index < expectedCount {
			continue
		}

		err = os.Remove(path)
		if err != nil {
			return fmt.Errorf("failed to remove stale SMB user configuration: %w", err)
		}
	}

	return nil
}

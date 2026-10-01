package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
)

// Include the registry instead of switching backends, which discards local overrides.
const smbBaseConfig = "[global]\nregistry shares = yes\ninclude = registry\nlock directory = /var/lib/samba/lock\npid directory = /var/lib/samba/run\nncalrpc dir = /var/lib/samba/ncalrpc\nwinbindd socket directory = /var/lib/samba/winbindd\nstate directory = /var/lib/samba/state\ncache directory = /var/cache/samba\nprivate dir = /var/lib/samba/private\n"

var fetchSMBSourceFunc = fetchSMBSource
var writeSMBFileFunc = os.WriteFile
var renameSMBFileFunc = os.Rename

type smbSourceData struct {
	container []byte
	users     [][]byte
}

func fetchSMBConfigSources(ctx context.Context, placement *SMBServicePlacement) (*smbSourceData, error) {
	err := validateSMBConfigURI(placement.ClusterID, placement.ConfigURI)
	if err != nil {
		return nil, err
	}
	for _, source := range placement.UserSources {
		err = validateSMBUserSourceURI(placement.ClusterID, source)
		if err != nil {
			return nil, err
		}
	}

	container, err := fetchSMBSourceFunc(ctx, placement.ConfigURI)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SMB container configuration: %w", err)
	}
	err = validateSMBContainerConfig(container)
	if err != nil {
		return nil, err
	}
	data := &smbSourceData{container: container}
	for _, source := range placement.UserSources {
		user, err := fetchSMBSourceFunc(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch SMB user configuration: %w", err)
		}
		data.users = append(data.users, user)
	}
	return data, nil
}

func materializeSMBConfig(ctx context.Context, placement *SMBServicePlacement) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	data := placement.configData
	var err error
	if data == nil {
		data, err = fetchSMBConfigSources(ctx, placement)
		if err != nil {
			return err
		}
	}
	containerConfig := data.container
	userConfigs := data.users

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

	if placement.isClustered() {
		err = materializeSMBCTDBConfig(runtimeDir, placement)
		if err != nil {
			return err
		}
	} else {
		err = removeSMBCTDBConfig(runtimeDir)
		if err != nil {
			return err
		}
	}

	baseConfig := smbBaseConfig
	if placement.bindAddress != "" {
		baseConfig += fmt.Sprintf(
			"bind interfaces only = yes\ninterfaces = %s\n",
			placement.bindAddress,
		)
	}
	smbPort := placement.CustomPorts["smb"]
	if smbPort != 0 {
		baseConfig += fmt.Sprintf("smb ports = %d\n", smbPort)
	}
	err = writeSMBFileAtomic(filepath.Join(configDir, "smb.conf"), []byte(baseConfig), constants.PermissionUserRwWorldRAccess)
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

func materializeSMBCTDBConfig(runtimeDir string, placement *SMBServicePlacement) error {
	// CTDB compares the complete recovery-lock command between members. Snap
	// revisions can differ by host and change on refresh, so persist stable paths.
	const snapRoot = "/snap/microceph/current"
	command := filepath.Join(snapRoot, "bin", "python3") + " " +
		filepath.Join(snapRoot, "commands", "sambacc.start")
	config := struct {
		Version string `json:"samba-container-config"`
		CTDB    struct {
			RecoveryLock   string   `json:"recovery_lock"`
			ClusterMetaURI string   `json:"cluster_meta_uri"`
			NodesCommand   string   `json:"nodes_cmd"`
			ConfigIncludes []string `json:"conf_file_includes,omitempty"`
		} `json:"ctdb"`
	}{Version: "v0"}
	config.CTDB.RecoveryLock = "!" + command +
		" --samba-command-prefix " + filepath.Join(snapRoot, "commands", "samba-command") +
		" ctdb-rados-mutex " + placement.ClusterLockURI
	config.CTDB.ClusterMetaURI = placement.ClusterMetaURI
	config.CTDB.NodesCommand = command + " ctdb-list-nodes"
	config.CTDB.ConfigIncludes = []string{"/var/lib/samba/smb.ctdb.conf"}

	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode CTDB configuration: %w", err)
	}
	err = writeSMBFileAtomic(filepath.Join(runtimeDir, "ctdb.json"), data, constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write CTDB configuration: %w", err)
	}
	rank := fmt.Sprintf("%d\n", placement.ctdb.Rank)
	err = writeSMBFileAtomic(filepath.Join(runtimeDir, "ctdb-rank"), []byte(rank), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write CTDB rank: %w", err)
	}
	identity := placement.ctdb.Identity + "\n"
	err = writeSMBFileAtomic(filepath.Join(runtimeDir, "ctdb-identity"), []byte(identity), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write CTDB identity: %w", err)
	}
	return nil
}

func removeSMBCTDBConfig(runtimeDir string) error {
	paths := []string{
		filepath.Join(runtimeDir, "ctdb.json"),
		filepath.Join(runtimeDir, "ctdb-rank"),
		filepath.Join(runtimeDir, "ctdb-identity"),
		filepath.Join(runtimeDir, "ctdb-address"),
	}
	dataPath := constants.GetPathConst().DataPath
	if dataPath != "" {
		paths = append(paths, filepath.Join(dataPath, "samba", "smb.ctdb.conf"))
	}
	for _, path := range paths {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale CTDB configuration: %w", err)
		}
	}
	return nil
}

func fetchSMBSource(ctx context.Context, uri string) ([]byte, error) {
	if strings.HasPrefix(uri, "rados://") {
		return fetchSMBRADOSSource(ctx, uri)
	}

	const configKeyPrefix = "rados:mon-config-key:"
	if strings.HasPrefix(uri, configKeyPrefix) {
		key := strings.TrimPrefix(uri, configKeyPrefix)
		output, err := runSMBCommand(ctx, "ceph", "config-key", "get", key)
		if err != nil {
			return nil, err
		}
		return []byte(output), nil
	}

	return nil, fmt.Errorf("unsupported SMB configuration URI %q", uri)
}

func fetchSMBRADOSSource(ctx context.Context, uri string) ([]byte, error) {
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
	_, err = runSMBCommand(ctx, "rados", "--pool", pool, "-N", namespace, "get", object, tempPath)
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
	err := writeSMBFileFunc(tmpPath, data, mode)
	if err != nil {
		return err
	}

	err = renameSMBFileFunc(tmpPath, destPath)
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

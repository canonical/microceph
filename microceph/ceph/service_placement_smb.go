package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var smbRADOSURIRegex = regexp.MustCompile(`^rados://\.smb/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)

// SMBServicePlacement describes the node-local configuration of an SMB service.
type SMBServicePlacement struct {
	ClusterID   string   `json:"cluster_id"`
	ConfigURI   string   `json:"config_uri"`
	Features    []string `json:"features"`
	JoinSources []string `json:"join_sources"`
	UserSources []string `json:"user_sources"`

	upstreamSpec json.RawMessage
}

// PopulateParams validates an SMB placement payload before it is applied locally.
func (smb *SMBServicePlacement) PopulateParams(_ interfaces.StateInterface, payload string) error {
	err := smb.decodePayload(payload)
	if err != nil {
		return err
	}

	if !types.SMBClusterIDRegex.MatchString(smb.ClusterID) {
		return fmt.Errorf("expected cluster_id to be valid (regex: '%s')", types.SMBClusterIDRegex.String())
	}

	err = validateSMBFeatures(smb.Features)
	if err != nil {
		return err
	}

	if len(smb.JoinSources) > 0 {
		return fmt.Errorf("direct SMB service does not support domain join sources")
	}

	err = validateSMBConfigURI(smb.ClusterID, smb.ConfigURI)
	if err != nil {
		return err
	}

	for _, source := range smb.UserSources {
		err = validateSMBUserSourceURI(smb.ClusterID, source)
		if err != nil {
			return err
		}
	}

	return nil
}

func (smb *SMBServicePlacement) decodePayload(payload string) error {
	upstreamSpec := []byte(payload)
	data := upstreamSpec

	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	err := json.Unmarshal(data, &envelope)
	if err != nil {
		return fmt.Errorf("failed to decode SMB service payload: %w", err)
	}
	if len(envelope.Spec) > 0 && string(envelope.Spec) != "null" {
		data = envelope.Spec
	}

	var decoded SMBServicePlacement
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		return fmt.Errorf("failed to decode SMB service payload: %w", err)
	}

	decoded.upstreamSpec = append(decoded.upstreamSpec, upstreamSpec...)
	*smb = decoded
	return nil
}

func (smb *SMBServicePlacement) upstreamSpecJSON() []byte {
	return append([]byte(nil), smb.upstreamSpec...)
}

// HospitalityCheck verifies that the SMB service can run with the required identity-switching permission.
func (smb *SMBServicePlacement) HospitalityCheck(_ interfaces.StateInterface) error {
	if !isIntfConnected("smb-identity") {
		return fmt.Errorf("SMB service requires the smb-identity interface connection")
	}

	clusterID, err := currentSMBClusterID()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if clusterID != smb.ClusterID {
		return fmt.Errorf("SMB service already manages cluster '%s' on this host", clusterID)
	}

	return nil
}

// ServiceInit materializes the current SMB configuration and starts or restarts smbd.
func (smb *SMBServicePlacement) ServiceInit(_ context.Context, _ interfaces.StateInterface) error {
	err := materializeSMBConfig(smb)
	if err != nil {
		return err
	}

	err = writeSMBDataKeyring(smb.ClusterID)
	if err != nil {
		return err
	}

	err = snapCheckActive("smbd")
	if err != nil {
		err = snapStart("smbd", true)
		if err != nil {
			return fmt.Errorf("failed to start SMB service: %w", err)
		}
		return nil
	}

	err = snapRestart("smbd", false)
	if err != nil {
		return fmt.Errorf("failed to restart SMB service: %w", err)
	}

	return nil
}

// PostPlacementCheck verifies that smbd remains active after placement.
func (smb *SMBServicePlacement) PostPlacementCheck(_ interfaces.StateInterface) error {
	return genericPostPlacementCheck("smbd")
}

// DbUpdate records the successful SMB configuration and local member state.
func (smb *SMBServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	groupConfig := database.SMBServiceGroupConfig{DesiredSpec: smb.upstreamSpecJSON()}
	serviceInfo := database.SMBServiceInfo{ConfigURI: smb.ConfigURI}
	return database.GroupedServicesQuery.AddOrUpdate(ctx, s, "smb", smb.ClusterID, groupConfig, serviceInfo)
}

func currentSMBClusterID() (string, error) {
	paths := constants.GetPathConst()
	path := filepath.Join(filepath.Dir(paths.ConfPath), "samba", "cluster-id")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	clusterID := strings.TrimSpace(string(data))
	if clusterID == "" {
		return "", fmt.Errorf("SMB cluster ID file is empty")
	}

	return clusterID, nil
}

func writeSMBDataKeyring(clusterID string) error {
	entity := fmt.Sprintf("client.smb.fs.cluster.%s", clusterID)
	keyring, err := cephRun("auth", "get", entity)
	if err != nil {
		return fmt.Errorf("failed to fetch SMB CephX keyring: %w", err)
	}

	paths := constants.GetPathConst()
	path := filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.%s.keyring", entity))
	err = writeSMBFileAtomic(path, []byte(keyring), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write SMB CephX keyring: %w", err)
	}

	return nil
}

func validateSMBFeatures(features []string) error {
	for _, feature := range features {
		return fmt.Errorf("direct SMB service does not support SMB feature '%s'", feature)
	}

	return nil
}

func validateSMBConfigURI(clusterID string, configURI string) error {
	matches := smbRADOSURIRegex.FindStringSubmatch(configURI)
	if matches == nil {
		return fmt.Errorf("expected config_uri to be a .smb RADOS URI")
	}

	namespace := matches[1]
	object := matches[2]
	if namespace != clusterID {
		return fmt.Errorf("config_uri must use SMB cluster namespace '%s'", clusterID)
	}

	if object != "config.smb" {
		return fmt.Errorf("config_uri must name the config.smb object")
	}

	return nil
}

func validateSMBUserSourceURI(clusterID string, source string) error {
	prefix := fmt.Sprintf("rados:mon-config-key:smb/config/%s/", clusterID)
	if !strings.HasPrefix(source, prefix) {
		return fmt.Errorf("user source must use the SMB config-key prefix for cluster '%s'", clusterID)
	}

	name := strings.TrimPrefix(source, prefix)
	if name == "" || !strings.HasSuffix(name, ".json") {
		return fmt.Errorf("user source must name an SMB JSON config key")
	}

	return nil
}

func validateSMBContainerConfig(data []byte) error {
	var config struct {
		Shares map[string]struct {
			Options map[string]any `json:"options"`
		} `json:"shares"`
	}

	err := json.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("failed to decode SMB container configuration: %w", err)
	}

	for shareName, share := range config.Shares {
		vfsObjects, ok := share.Options["vfs objects"].(string)
		if !ok || !containsVFSObject(vfsObjects, "ceph_new") {
			return fmt.Errorf("share '%s' must use direct samba-vfs/new", shareName)
		}

		proxy, ok := share.Options["ceph_new:proxy"].(string)
		if !ok || !strings.EqualFold(proxy, "no") {
			return fmt.Errorf("share '%s' must use direct samba-vfs/new", shareName)
		}
	}

	return nil
}

func containsVFSObject(vfsObjects string, object string) bool {
	for _, current := range strings.Fields(vfsObjects) {
		if current == object {
			return true
		}
	}

	return false
}

package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// smbClusterIDPattern mirrors the pinned mgr/smb validation.valid_id contract.
var smbClusterIDPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,16}[a-zA-Z0-9])?$`)

func validateSMBClusterID(clusterID string) error {
	if !smbClusterIDPattern.MatchString(clusterID) {
		return fmt.Errorf("invalid SMB cluster ID")
	}
	return nil
}

// validateSMBSourceURI limits reads to the selected cluster's upstream stores.
func validateSMBSourceURI(clusterID, uri string) error {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	prefixes := []string{"rados://.smb/" + clusterID + "/", "rados:mon-config-key:smb/config/" + clusterID + "/"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(uri, prefix) {
			object := strings.TrimPrefix(uri, prefix)
			if object != "" && object != "." && object != ".." && !strings.ContainsAny(object, "/\\\x00\r\n?#%") {
				return nil
			}
		}
	}
	return fmt.Errorf("SMB source URI is outside the selected cluster's stores")
}

// ensureSMBDataKey creates/retrieves the stable identity with no requested caps.
// The mgr grants CephFS access when shares exist; native code never copies caps
// into this keyring or treats an upstream key replacement as an ordinary update.
func ensureSMBDataKey(ctx context.Context, clusterID, path string) error {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	clientName := "smb.fs.cluster." + clusterID
	key, err := CreateClientKey(ctx, clientName)
	if err != nil {
		return fmt.Errorf("retrieve SMB data identity: %w", err)
	}
	content := []byte(fmt.Sprintf("[client.%s]\n\tkey = %s\n", clientName, key))
	previous, err := os.ReadFile(path)
	if err == nil {
		if string(previous) != string(content) {
			return fmt.Errorf("SMB data key changed; explicit recovery required")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := path + ".tmp"
	err = os.WriteFile(tmp, content, 0600)
	if err != nil {
		return err
	}
	err = os.Rename(tmp, path)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

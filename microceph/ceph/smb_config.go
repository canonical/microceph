package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func ensureSMBRecoveryKey(ctx context.Context, clusterID, root string) error {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	name := "smb.cluster." + clusterID
	key, err := CreateClientKey(ctx, name)
	if err != nil {
		return fmt.Errorf("retrieve SMB recovery identity failed")
	}
	_, err = cephRunContext(ctx, "auth", "caps", "client."+name, "mon", "allow r", "osd", "allow rwx pool=.smb namespace="+clusterID)
	if err != nil {
		return fmt.Errorf("set SMB recovery identity capabilities failed")
	}
	return smbAtomicWrite(filepath.Join(root, "identity/recovery.keyring"), []byte("[client."+name+"]\n\tkey = "+key+"\n"))
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

// ensureSMBDataKey requests no caps. The mgr grants CephFS access when shares
// exist; upstream key replacement requires explicit recovery, not re-enable.
func ensureSMBDataKey(ctx context.Context, clusterID, path string) error {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	// Equal retained bytes must obey the same no-symlink policy as new writes.
	err = smbPrivateDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("native file is an escaping symlink")
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
	return writeSMBNativeFile(path, content)
}

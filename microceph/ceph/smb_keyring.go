package ceph

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
)

// smbRADOSPool is the pool mgr/smb keeps its objects in; URIs pointing at
// it get the enhanced caps CTDB needs to lock the cluster meta object.
const smbRADOSPool = ".smb"

// smbEntityRegex guards cephx entity names used to build keyring file
// paths; the spec is external input.
var smbEntityRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// SMBDaemonEntity returns the per-node cephx entity for an smb cluster.
func SMBDaemonEntity(clusterID, hostname string) string {
	return fmt.Sprintf("client.smb.%s.%s", clusterID, hostname)
}

// smbPoolCapsFromURI mirrors cephadm's SMBService._pool_caps_from_uri:
// read access for foreign pools, and for the smb pool additionally rwx on
// the cluster.meta. object prefix (the x perm locks the CTDB reclock
// object).
func smbPoolCapsFromURI(uri string) []string {
	if !strings.HasPrefix(uri, "rados://") {
		logger.Debugf("ignoring unexpected uri scheme: %s", uri)
		return nil
	}

	part := strings.TrimRight(strings.TrimPrefix(uri, "rados://"), "/")
	pool, rest, found := strings.Cut(part, "/")
	if !found {
		logger.Debugf("ignoring poolless uri: %s", uri)
		return nil
	}

	namespace := ""
	if strings.Contains(rest, "/") {
		namespace, _, _ = strings.Cut(rest, "/")
	}

	if pool != smbRADOSPool {
		return []string{fmt.Sprintf("allow r pool=%s", pool)}
	}

	return []string{
		fmt.Sprintf("allow r pool=%s", pool),
		fmt.Sprintf("allow rwx pool=%s namespace=%s object_prefix cluster.meta.", pool, namespace),
	}
}

// smbOSDCaps unions the pool caps over every RADOS URI in the spec,
// deduplicated and sorted for stable comparisons. Clustered specs also
// get access to the per-cluster CTDB reclock object (see RenderCTDBConf:
// the 4.19 rados mutex helper is namespace-blind, so the lock lives in
// the lock pool's default namespace under our own prefix).
func smbOSDCaps(spec *types.SMBSpec) string {
	uris := []string{spec.ConfigURI}
	uris = append(uris, spec.UserSources...)

	capSet := map[string]bool{}
	for _, uri := range uris {
		for _, cap := range smbPoolCapsFromURI(uri) {
			capSet[cap] = true
		}
	}

	for _, feature := range spec.Features {
		if feature != "clustered" {
			continue
		}
		lockPool, _, _, err := parseRADOSURI(spec.ClusterLockURI)
		if err == nil {
			capSet[fmt.Sprintf("allow rwx pool=%s object_prefix %s", lockPool, smbReclockObject)] = true
		}
	}

	caps := make([]string, 0, len(capSet))
	for cap := range capSet {
		caps = append(caps, cap)
	}
	sort.Strings(caps)

	return strings.Join(caps, ", ")
}

// smbMonCaps allows mon reads plus fetching this smb cluster's config
// keys from the mon config-key store (mirrors cephadm).
func smbMonCaps(clusterID string) string {
	return fmt.Sprintf(`allow r, allow command "config-key get" with "key" prefix "smb/config/%s/"`, clusterID)
}

// smbKeyringPath returns the standard-named keyring file for an entity,
// discovered by the default /etc/ceph search path (bound to confDir in
// the snap).
func smbKeyringPath(confDir, entity string) string {
	return filepath.Join(confDir, fmt.Sprintf("ceph.%s.keyring", entity))
}

// fetchSMBKeyring writes the entity's keyring file under confDir,
// atomically and readable by root only.
func fetchSMBKeyring(confDir, entity string) error {
	path := smbKeyringPath(confDir, entity)
	tmpFile := path + ".tmp"

	_, err := cephRun("auth", "get", entity, "-o", tmpFile)
	if err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to fetch keyring for '%s': %w", entity, err)
	}

	err = os.Chmod(tmpFile, 0600)
	if err != nil {
		os.Remove(tmpFile)
		return err
	}

	err = os.Rename(tmpFile, path)
	if err != nil {
		os.Remove(tmpFile)
		return err
	}

	return nil
}

// checkSMBEntities validates every entity name the spec makes us touch on
// disk or pass to ceph.
func checkSMBEntities(entities []string) error {
	for _, entity := range entities {
		if !smbEntityRegex.MatchString(entity) {
			return fmt.Errorf("'%s' is not a valid cephx entity name", entity)
		}
	}
	return nil
}

// EnsureSMBKeyrings creates or updates the per-node smb daemon key (caps
// converge on re-apply) and fetches the spec's include_ceph_users keys,
// writing standard-named keyring files under confDir.
func EnsureSMBKeyrings(spec *types.SMBSpec, hostname, confDir string) error {
	entity := SMBDaemonEntity(spec.ClusterID, hostname)

	err := checkSMBEntities(append([]string{entity}, spec.IncludeCephUsers...))
	if err != nil {
		return err
	}

	// get-or-create without caps, then converge caps separately: a plain
	// get-or-create fails when the entity exists with different caps, and
	// re-applies may legitimately change the URI-derived caps.
	_, err = cephRun("auth", "get-or-create", entity)
	if err != nil {
		return fmt.Errorf("failed to ensure cephx entity '%s': %w", entity, err)
	}

	_, err = cephRun("auth", "caps", entity, "mon", smbMonCaps(spec.ClusterID), "osd", smbOSDCaps(spec))
	if err != nil {
		return fmt.Errorf("failed to set caps for '%s': %w", entity, err)
	}

	err = fetchSMBKeyring(confDir, entity)
	if err != nil {
		return err
	}

	for _, user := range spec.IncludeCephUsers {
		err = fetchSMBKeyring(confDir, user)
		if err != nil {
			return err
		}
	}

	return nil
}

// RemoveSMBKeyrings deletes the per-node daemon key and every keyring
// file this node fetched for the cluster. The include_ceph_users
// entities themselves belong to mgr/smb and are left in place.
func RemoveSMBKeyrings(spec *types.SMBSpec, hostname, confDir string) error {
	entity := SMBDaemonEntity(spec.ClusterID, hostname)

	err := checkSMBEntities(append([]string{entity}, spec.IncludeCephUsers...))
	if err != nil {
		return err
	}

	_, err = cephRun("auth", "del", entity)
	if err != nil {
		return fmt.Errorf("failed to delete cephx entity '%s': %w", entity, err)
	}

	for _, name := range append([]string{entity}, spec.IncludeCephUsers...) {
		err = os.Remove(smbKeyringPath(confDir, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

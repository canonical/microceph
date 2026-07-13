package ceph

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
)

// smbMonConfigKeyPrefix is the URI scheme mgr/smb uses for user/group
// sources stored in the mon config-key store.
const smbMonConfigKeyPrefix = "rados:mon-config-key:"

// Injectable seams for unit tests.
var (
	smbPasswdImportFunc    = smbPasswdImport
	smbUserImportRetries   = 10
	smbUserImportInterval  = 3 * time.Second
	fetchSMBUserSourceFunc = fetchSMBUserSource
)

// smbUsersDoc is the subset of the sambacc users-and-groups document the
// passdb import needs.
type smbUsersDoc struct {
	Users struct {
		AllEntries []struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		} `json:"all_entries"`
	} `json:"users"`
}

// fetchSMBUserSource reads the document behind a user_sources URI:
// mgr/smb publishes them either as mon config-key entries or as RADOS
// pool objects.
func fetchSMBUserSource(uri string) ([]byte, error) {
	key, found := strings.CutPrefix(uri, smbMonConfigKeyPrefix)
	if found {
		out, err := cephRun("config-key", "get", key)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch '%s': %w", uri, err)
		}
		return []byte(out), nil
	}
	return fetchSMBConfigObject(uri)
}

// smbPasswdImport adds (or re-adds) one user to the clustered passdb via
// smbpasswd against the rendered smb.conf. The password goes over stdin
// (-s reads it twice), never through argv.
func smbPasswdImport(confPath, name, password string) error {
	cmd := exec.Command("smbpasswd", "-c", confPath, "-s", "-a", name)
	cmd.Stdin = strings.NewReader(password + "\n" + password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("smbpasswd -a %s failed: %v (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SeedSMBUsers imports every user from the spec's user_sources into the
// cluster's passdb. It must run on a placed member with ctdbd up: the
// passdb is CTDB-replicated, so one node seeding is cluster-wide. Each
// user needs a matching system user to already exist (Phase 1 leaves
// system-user provisioning to the admin). Imports retry while the CTDB
// cluster settles.
func SeedSMBUsers(spec *types.SMBSpec, p SMBRenderParams) error {
	confPath := filepath.Join(p.Paths.Conf, "samba", "smb.conf")

	for _, uri := range spec.UserSources {
		raw, err := fetchSMBUserSourceFunc(uri)
		if err != nil {
			return err
		}

		var doc smbUsersDoc
		err = json.Unmarshal(raw, &doc)
		if err != nil {
			return fmt.Errorf("cannot parse user source '%s': %w", uri, err)
		}

		for _, user := range doc.Users.AllEntries {
			err = importSMBUserWithRetry(confPath, user.Name, user.Password)
			if err != nil {
				return err
			}
			logger.Infof("seeded smb user '%s' for cluster '%s'", user.Name, spec.ClusterID)
		}
	}

	return nil
}

// SeedSMBUsersNode is the env-wired entry point used by the node-scoped
// users endpoint: it parses the spec payload and seeds this node.
func SeedSMBUsersNode(payload string) error {
	var spec types.SMBSpec
	err := json.Unmarshal([]byte(payload), &spec)
	if err != nil {
		return fmt.Errorf("cannot parse smb spec for user seeding: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	return SeedSMBUsers(&spec, NewSMBRenderParams(spec.ClusterID, hostname, true))
}

// importSMBUserWithRetry retries the passdb import while ctdbd finishes
// recovery; smbpasswd fails against a ctdb-backed passdb until then.
func importSMBUserWithRetry(confPath, name, password string) error {
	var err error
	for attempt := 0; attempt < smbUserImportRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(smbUserImportInterval)
		}
		err = smbPasswdImportFunc(confPath, name, password)
		if err == nil {
			return nil
		}
		logger.Infof("smb user import attempt %d for '%s' failed: %v", attempt+1, name, err)
	}
	return fmt.Errorf("failed to import smb user '%s': %w", name, err)
}

package ceph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// smbADMember is the nonsecret logical identity, not a join/trust assertion.
// Samba owns the machine password and CTDB-backed secrets.tdb/autorid.tdb.
// These databases must survive restart/refresh; missing initialized state must
// require manual maintenance, never a new join or a fresh idmap allocation.
type smbADMember struct {
	Realm       string `json:"realm"`
	Workgroup   string `json:"workgroup"`
	NetBIOSName string `json:"netbios_name"`
	FQDN        string `json:"fqdn"`
}

var smbADShortNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,14}$`)
var smbDNSLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func smbEffectiveGlobals(description smbBindingDescription) map[string]string {
	effective := make(map[string]string)
	for _, option := range description.Globals {
		effective[smbPolicyOptionName(option[0])] = strings.TrimSpace(option[1])
	}
	return effective
}

func smbADIdentity(spec smbSpec, effective map[string]string) (*smbADMember, error) {
	realm := effective["realm"]
	labels := strings.Split(realm, ".")
	if len(realm) > 253 || len(labels) < 2 {
		return nil, fmt.Errorf("AD member requires a DNS realm")
	}
	for _, label := range labels {
		if !smbDNSLabelPattern.MatchString(label) {
			return nil, fmt.Errorf("invalid AD DNS realm")
		}
	}
	name := effective["netbiosname"]
	if len(spec.ClusterID) > 15 || !strings.EqualFold(name, spec.ClusterID) {
		return nil, fmt.Errorf("AD NetBIOS name must match the logical cluster ID (maximum 15 characters)")
	}
	if len(name+"."+realm) > 253 {
		return nil, fmt.Errorf("AD service FQDN exceeds DNS length limit")
	}
	workgroup := effective["workgroup"]
	if !smbADShortNamePattern.MatchString(workgroup) {
		return nil, fmt.Errorf("invalid AD short-domain workgroup (maximum 15 characters)")
	}
	if effective["netbiosaliases"] != "" || effective["additionaldnshostnames"] != "" {
		return nil, fmt.Errorf("AD member aliases require separate DNS/SPN qualification")
	}
	if !strings.EqualFold(effective["idmapconfig*:backend"], "autorid") || effective["idmapconfig*:range"] != "2000-9999999" {
		return nil, fmt.Errorf("AD member requires the upstream autorid backend and range")
	}
	// Do not admit a second identity provider or node-local trust/mapping store.
	for key, value := range effective {
		if strings.HasPrefix(key, "idmapconfig") && key != "idmapconfig*:backend" && key != "idmapconfig*:range" {
			return nil, fmt.Errorf("AD member custom idmap configuration is not supported")
		}
		if key == "ctdb:secrets.tdb" || key == "ctdb:autorid.tdb" || key == "ctdb:winbindd_idmap.tdb" {
			if value != "1" && !strings.EqualFold(value, "yes") && !strings.EqualFold(value, "true") {
				return nil, fmt.Errorf("AD trust and idmap databases must remain CTDB-backed")
			}
		}
	}
	method := effective["kerberosmethod"]
	if method != "" && smbPolicyOptionName(method) != "secretsonly" {
		return nil, fmt.Errorf("AD member requires secrets-only Kerberos verification")
	}
	return &smbADMember{Realm: strings.ToUpper(realm), Workgroup: strings.ToUpper(workgroup), NetBIOSName: strings.ToUpper(name), FQDN: strings.ToLower(name + "." + realm)}, nil
}

func smbMemberContext(candidate smbCandidate, description smbBindingDescription) (*smbADMember, error) {
	err := validateSMBEffectivePolicy(candidate.Spec, description)
	if err != nil {
		return nil, err
	}
	if !candidate.Spec.hasFeature("domain") {
		var logical struct {
			AuthMode string `json:"auth_mode"`
		}
		if len(candidate.Logical) != 0 {
			err = json.Unmarshal(candidate.Logical, &logical)
			if err != nil || logical.AuthMode == "active-directory" {
				return nil, fmt.Errorf("logical authentication does not match generated spec")
			}
		}
		return nil, nil
	}
	member, err := smbADIdentity(candidate.Spec, smbEffectiveGlobals(description))
	if err != nil {
		return nil, err
	}
	var logical struct {
		AuthMode       string `json:"auth_mode"`
		DomainSettings struct {
			Realm string `json:"realm"`
		} `json:"domain_settings"`
	}
	err = json.Unmarshal(candidate.Logical, &logical)
	if err != nil || logical.AuthMode != "active-directory" || !strings.EqualFold(logical.DomainSettings.Realm, member.Realm) {
		return nil, fmt.Errorf("AD member identity does not match logical authentication/realm")
	}
	if len(candidate.Users) > 0 || len(description.Users) > 0 || len(description.Groups) > 0 || len(candidate.Spec.UserSources) > 0 {
		return nil, fmt.Errorf("AD member must not use local user/group sources")
	}
	if len(candidate.Joins) == 0 || len(candidate.Joins) != len(candidate.Spec.JoinSources) {
		return nil, fmt.Errorf("AD member requires ordered join credential sources")
	}
	for _, raw := range candidate.Joins {
		var credentials struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		err = json.Unmarshal(raw, &credentials)
		if err != nil || credentials.Username == "" || credentials.Password == "" || strings.ContainsAny(credentials.Username, "%\x00\r\n") || strings.HasPrefix(credentials.Username, "-") || strings.ContainsAny(credentials.Password, "\x00\r\n") {
			// Do not include JSON decode errors or any source value in diagnostics.
			return nil, fmt.Errorf("invalid AD join username/password source")
		}
	}
	return member, nil
}

// prepareSMBADPaths prepares only sockets, never databases or a joined marker.
// Binding the public directory bypasses the private root's traversal barrier;
// keep the privileged pipe under the root-only state directory, not that bind.
func prepareSMBADPaths(root string) error {
	for name, mode := range map[string]os.FileMode{"samba/winbindd": 0755, "samba/state/winbindd_privileged": 0700} {
		path := filepath.Join(root, name)
		err := smbPrivateDir(path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("unsafe AD socket directory ownership/permissions")
		}
		err = os.Chmod(path, mode)
		if err != nil {
			return err
		}
	}
	return nil
}

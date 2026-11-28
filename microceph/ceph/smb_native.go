package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// smbBinding is the small upstream-language boundary, not a native controller.
type smbBinding func(command string, request any, result any) error

type smbNativePlan struct {
	Files  map[string][]byte
	Launch smbLaunch
}

type smbBindingIdentity struct {
	Name        string   `json:"name"`
	Fields      []string `json:"fields"`
	HasPassword bool     `json:"has_password"`
}

type smbBindingDescription struct {
	CTDB             map[string]any `json:"ctdb"`
	CTDBGlobals      [][2]string    `json:"ctdb_globals"`
	Clustered        bool           `json:"clustered"`
	DomainController bool           `json:"domain_controller"`
	Globals          [][2]string    `json:"globals"`
	Shares           []struct {
		Name    string      `json:"name"`
		Options [][2]string `json:"options"`
	} `json:"shares"`
	Users  []smbBindingIdentity `json:"users"`
	Groups []smbBindingIdentity `json:"groups"`
}

type smbBindingRequest struct {
	ClusterID  string         `json:"cluster_id"`
	Config     map[string]any `json:"config"`
	ConfigPath string         `json:"config_path,omitempty"`
	Checks     []smbDataCheck `json:"checks,omitempty"`
}

type smbDataCheck struct {
	Filesystem string `json:"filesystem"`
	Path       string `json:"path"`
	User       string `json:"user"`
}

var smbNativeRootPattern = regexp.MustCompile(`^(/[a-zA-Z0-9_.-]+)+$`)

func smbNativeRoot(root, clusterID string) error {
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != clusterID || !smbNativeRootPattern.MatchString(root) {
		return fmt.Errorf("invalid native cluster root")
	}
	return nil
}

func smbObject(parent map[string]any, key string) (map[string]any, error) {
	value, exists := parent[key]
	if !exists {
		value = map[string]any{}
		parent[key] = value
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid SMB %s object", key)
	}
	return object, nil
}

func smbNames(parent map[string]any, key string) ([]string, error) {
	value := parent[key]
	if value == nil {
		return nil, nil
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid SMB %s list", key)
	}
	names := make([]string, 0, len(array))
	for _, entry := range array {
		name, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("invalid SMB %s name", key)
		}
		names = append(names, name)
	}
	return names, nil
}

func smbOptionName(key string) string { return strings.Join(strings.Fields(strings.ToLower(key)), " ") }

func smbOverlay(options map[string]any, required map[string]any) error {
	for key, value := range options {
		normalized := smbOptionName(key)
		desired, exists := required[normalized]
		if !exists {
			continue
		}
		if fmt.Sprint(value) != fmt.Sprint(desired) {
			return fmt.Errorf("native option conflict: %s", normalized)
		}
		delete(options, key)
	}
	for key, value := range required {
		options[key] = value
	}
	return nil
}

func smbRejectPublic(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "public_addrs" || key == "cluster_public_addrs" || key == "public_addresses" {
				nonempty := child != nil
				switch v := child.(type) {
				case []any:
					nonempty = len(v) != 0
				case map[string]any:
					nonempty = len(v) != 0
				case string:
					nonempty = v != ""
				case bool:
					nonempty = v
				case json.Number:
					nonempty = v != "0"
				}
				if nonempty {
					return fmt.Errorf("public addresses require a separate HA/LB implementation")
				}
			}
			err := smbRejectPublic(child)
			if err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			err := smbRejectPublic(child)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func smbDecodeObject(raw []byte) (map[string]any, error) {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&result)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("SMB document must be an object")
	}
	return result, nil
}

func smbIdentityEntries(document map[string]any, kind string) ([]any, error) {
	object, err := smbObject(document, kind)
	if err != nil {
		return nil, err
	}
	if object["all_entries"] == nil {
		return []any{}, nil
	}
	entries, ok := object["all_entries"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid SMB %s entries", kind)
	}
	return entries, nil
}

func smbCombinedDocument(candidate smbCandidate) (map[string]any, error) {
	document, err := smbDecodeObject(candidate.Config)
	if err != nil {
		return nil, err
	}
	for _, kind := range []string{"users", "groups"} {
		entries, err := smbIdentityEntries(document, kind)
		if err != nil {
			return nil, err
		}
		for _, raw := range candidate.Users {
			source, err := smbDecodeObject(raw)
			if err != nil {
				return nil, err
			}
			extra, err := smbIdentityEntries(source, kind)
			if err != nil {
				return nil, err
			}
			entries = append(entries, extra...)
		}
		names := map[string]bool{}
		for _, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid SMB %s entry", kind)
			}
			name, ok := entry["name"].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("invalid SMB %s name", kind)
			}
			if names[name] {
				return nil, fmt.Errorf("duplicate %s name", kind)
			}
			names[name] = true
		}
		object, err := smbObject(document, kind)
		if err != nil {
			return nil, err
		}
		object["all_entries"] = entries
	}
	return document, nil
}

func smbControlledShareOption(key string) bool {
	if key == "vfs objects" {
		return true
	}
	for _, module := range []string{"ceph", "ceph_new"} {
		for _, suffix := range []string{"config_file", "filesystem", "user_id", "proxy"} {
			if key == module+":"+suffix {
				return true
			}
		}
	}
	return false
}

func smbNormalizeShare(options map[string]any) error {
	for key, value := range options {
		normalized := smbOptionName(key)
		if !smbControlledShareOption(normalized) {
			continue
		}
		_, text := value.(string)
		if !text {
			return fmt.Errorf("invalid native option type: %s", normalized)
		}
		if normalized == key {
			continue
		}
		existing, exists := options[normalized]
		if exists && fmt.Sprint(existing) != fmt.Sprint(value) {
			return fmt.Errorf("native option conflict: %s", normalized)
		}
		delete(options, key)
		options[normalized] = value
	}
	return nil
}

func projectSMBNative(candidate smbCandidate, root string, binding smbBinding) (smbNativePlan, error) {
	plan := smbNativePlan{Files: map[string][]byte{}}
	id := candidate.Spec.ClusterID
	err := smbNativeRoot(root, id)
	if err != nil {
		return plan, err
	}
	err = validateSMBLocalSpec(candidate.Spec)
	if err != nil {
		return plan, err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return plan, err
	}
	entire, err := smbDecodeObject(raw)
	if err != nil {
		return plan, err
	}
	err = smbRejectPublic(entire)
	if err != nil {
		return plan, err
	}
	document, err := smbCombinedDocument(candidate)
	if err != nil {
		return plan, err
	}
	ordered, err := smbOrderedBindingDocument(candidate.Config, document, nil)
	if err != nil {
		return plan, err
	}
	request := smbBindingRequest{ClusterID: id, Config: ordered}
	var description smbBindingDescription
	err = binding("inspect", request, &description)
	if err != nil {
		return plan, err
	}
	err = validateSMBPolicyDescription(candidate, description)
	if err != nil {
		return plan, err
	}
	endpoint, err := candidate.Spec.endpointPorts()
	if err != nil {
		return plan, err
	}
	if candidate.Endpoint != nil {
		endpoint = *candidate.Endpoint
	}
	clustered := candidate.Spec.hasFeature("clustered")
	if clustered != (candidate.CTDB != nil) {
		return plan, fmt.Errorf("effective CTDB mode does not match native reservation")
	}
	plan.Launch = smbLaunch{Clustered: clustered, FrontendAddress: endpoint.FrontendAddress, SMBPort: endpoint.SMBPort}
	required := map[string]any{"config backend": "file", "private dir": root + "/samba/private", "state directory": root + "/samba/state", "cache directory": root + "/samba/cache", "lock directory": root + "/samba/lock", "pid directory": root + "/samba/run", "ncalrpc dir": root + "/samba/ncalrpc", "log file": root + "/diagnostics/log.%m", "smb ports": strconv.Itoa(endpoint.SMBPort)}
	if clustered {
		if plan.Launch.FrontendAddress == "" {
			plan.Launch.FrontendAddress = candidate.CTDB.Member.Node
		}
		for _, option := range description.CTDBGlobals {
			if option[0] != "include" {
				required[option[0]] = option[1]
			}
		}
		required["registry shares"] = "no"
		required["ctdbd socket"] = root + "/ctdb/run/ctdbd.socket"
		pnn := candidate.CTDB.Member.PNN
		plan.Launch.CTDBPNN = &pnn
		err = projectSMBCTDB(candidate, root, document, description.CTDB, plan.Files)
		if err != nil {
			return plan, err
		}
	}
	if plan.Launch.FrontendAddress != "" {
		required["interfaces"] = plan.Launch.FrontendAddress
		required["bind interfaces only"] = "yes"
	}
	configs, err := smbObject(document, "configs")
	if err != nil {
		return plan, err
	}
	instance, err := smbObject(configs, id)
	if err != nil {
		return plan, err
	}
	globals, err := smbObject(document, "globals")
	if err != nil {
		return plan, err
	}
	globalNames, err := smbNames(instance, "globals")
	if err != nil {
		return plan, err
	}
	for _, name := range globalNames {
		section, err := smbObject(globals, name)
		if err != nil {
			return plan, err
		}
		options, err := smbObject(section, "options")
		if err != nil {
			return plan, err
		}
		err = smbOverlay(options, required)
		if err != nil {
			return plan, err
		}
	}
	globals["_native"] = map[string]any{"options": required}
	refs := make([]any, 0, len(globalNames)+1)
	for _, name := range globalNames {
		refs = append(refs, name)
	}
	instance["globals"] = append(refs, "_native")
	shares, err := smbObject(document, "shares")
	if err != nil {
		return plan, err
	}
	shareNames, err := smbNames(instance, "shares")
	if err != nil {
		return plan, err
	}
	for _, name := range shareNames {
		share, err := smbObject(shares, name)
		if err != nil {
			return plan, err
		}
		options, err := smbObject(share, "options")
		if err != nil {
			return plan, err
		}
		err = smbNormalizeShare(options)
		if err != nil {
			return plan, err
		}
		for _, module := range []string{"ceph", "ceph_new"} {
			key := module + ":config_file"
			if value, exists := options[key]; exists {
				if value != "/etc/ceph/ceph.conf" {
					return plan, fmt.Errorf("native option conflict: %s", key)
				}
				options[key] = root + "/config/ceph-direct.conf"
			}
		}
	}
	// Effective aliases/defaults remain upstream-owned.
	for _, share := range description.Shares {
		options := map[string]any{}
		for _, pair := range share.Options {
			options[pair[0]] = pair[1]
		}
		err = smbNormalizeShare(options)
		if err != nil {
			return plan, err
		}
		for _, module := range strings.Fields(fmt.Sprint(options["vfs objects"])) {
			if module != "ceph" && module != "ceph_new" {
				continue
			}
			user, _ := options[module+":user_id"].(string)
			if user != "smb.fs.cluster."+id {
				return plan, fmt.Errorf("native option conflict: Ceph data identity")
			}
			filesystem, _ := options[module+":filesystem"].(string)
			path, _ := options["path"].(string)
			if filesystem == "" || path == "" {
				return plan, fmt.Errorf("incomplete native CephFS share")
			}
			plan.Launch.DataChecks = append(plan.Launch.DataChecks, smbDataCheck{Filesystem: filesystem, Path: path, User: user})
		}
	}
	err = smbProjectNSS(document, description, plan.Files)
	if err != nil {
		return plan, err
	}
	var rendered struct {
		Config string `json:"smb_conf"`
	}
	request.Config, err = smbOrderedBindingDocument(candidate.Config, document, required)
	if err != nil {
		return plan, err
	}
	err = binding("render", request, &rendered)
	if err != nil {
		return plan, err
	}
	if rendered.Config == "" {
		return plan, fmt.Errorf("empty upstream SMB rendering")
	}
	plan.Files["config/smb.conf"] = []byte(rendered.Config)
	plan.Files["config/nsswitch.conf"] = []byte("passwd: files\ngroup: files\nhosts: files dns\nservices: files\n")
	plan.Files["config/ceph-direct.conf"], err = smbNativeCephConfig(candidate.CephConfig, id, root)
	if err != nil {
		return plan, err
	}
	plan.Launch.Startup = smbDefaultStartup()
	plan.Files["config/context.json"], err = json.Marshal(plan.Launch)
	if err != nil {
		return plan, err
	}
	plan.Files["config/candidate.json"], err = json.Marshal(candidate)
	return plan, err
}

func smbProjectNSS(document map[string]any, description smbBindingDescription, files map[string][]byte) error {
	for _, kind := range []string{"users", "groups"} {
		entries, err := smbIdentityEntries(document, kind)
		if err != nil {
			return err
		}
		raw := map[string]map[string]any{}
		for _, value := range entries {
			entry := value.(map[string]any)
			raw[entry["name"].(string)] = entry
		}
		identities := description.Users
		header := "root:x:0:0:root:/root:/bin/false\nnobody:x:65534:65534:nobody:/nonexistent:/bin/false\n"
		name := "passwd"
		count := 7
		if kind == "groups" {
			identities = description.Groups
			header = "root:x:0:\nnogroup:x:65534:\n"
			name = "group"
			count = 4
		}
		var output strings.Builder
		output.WriteString(header)
		for _, entry := range identities {
			fields := append([]string(nil), entry.Fields...)
			if len(fields) != count || fields[0] != entry.Name {
				return fmt.Errorf("invalid NSS %s projection", kind)
			}
			record := raw[entry.Name]
			if kind == "users" && !entry.HasPassword {
				return fmt.Errorf("managed user lacks a password or hash")
			}
			for key, index := range map[string]int{"uid": 2, "gid": 3} {
				if kind == "groups" {
					if key == "uid" {
						continue
					}
					index = 2
				}
				value, exists := record[key]
				if exists {
					fields[index] = fmt.Sprint(value)
				}
			}
			for _, field := range fields {
				if strings.ContainsAny(field, ":\n\r\x00") {
					return fmt.Errorf("invalid NSS %s field", kind)
				}
			}
			for _, index := range []int{2, 3} {
				if kind == "groups" && index == 3 {
					continue
				}
				_, err := strconv.ParseUint(fields[index], 10, 32)
				if err != nil {
					return fmt.Errorf("invalid NSS numeric identity")
				}
			}
			output.WriteString(strings.Join(fields, ":"))
			output.WriteByte('\n')
		}
		files["config/"+name] = []byte(output.String())
	}
	return nil
}

func smbNativeCephConfig(text, id, root string) ([]byte, error) {
	options := map[string]string{}
	keys := []string{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != "global" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid native Ceph global configuration")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		_, exists := options[key]
		if !exists {
			keys = append(keys, key)
		}
		options[key] = strings.TrimSpace(value)
	}
	identity := "client.smb.fs.cluster." + id
	keyring := root + "/identity/data.keyring"
	_, exists := options["keyring"]
	if !exists {
		keys = append(keys, "keyring")
	}
	options["keyring"] = keyring
	var out strings.Builder
	out.WriteString("[global]\n")
	for _, key := range keys {
		fmt.Fprintf(&out, "%s = %s\n", key, options[key])
	}
	fmt.Fprintf(&out, "\n[%s]\nkeyring = %s\n\n", identity, keyring)
	return []byte(out.String()), nil
}

func writeSMBNativeFile(path string, content []byte) error {
	err := smbPrivateDir(filepath.Dir(path))
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
	config := Config{configDir: filepath.Dir(path), configFile: filepath.Base(path)}
	return config.WriteRendered(content, 0600)
}

func publishSMBNative(ctx context.Context, root string) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	err = ctx.Err()
	if err != nil {
		return err
	}
	next := filepath.Join(root, "config/next")
	files := map[string][]byte{}
	err = filepath.WalkDir(next, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		err := ctx.Err()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("native candidate is an escaping symlink")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(next, path)
		if err != nil {
			return err
		}
		if !filepath.IsLocal(relative) || (filepath.Dir(relative) != "config" && relative != "ctdb/nodes" && relative != "ctdb/ctdb.conf") {
			return fmt.Errorf("invalid native candidate path")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = data
		return nil
	})
	if err != nil {
		return err
	}
	const contextPath = "config/context.json"
	_, hasContext := files[contextPath]
	if !hasContext {
		return fmt.Errorf("native candidate lacks startup context")
	}
	// Fence preparation before advancing the candidate or any live file.
	names := []string{contextPath}
	for name := range files {
		if name != contextPath {
			names = append(names, name)
		}
	}
	sort.Strings(names[1:])
	for _, name := range names {
		err = ctx.Err()
		if err != nil {
			return err
		}
		if name == contextPath {
			err = publishSMBContext(root, files[name])
		} else {
			err = writeSMBNativeFile(filepath.Join(root, name), files[name])
		}
		if err != nil {
			return err
		}
	}
	err = publishSMBCTDBLayout(root, false)
	if err != nil {
		return err
	}
	launch, err := loadSMBLaunchForPublication(root)
	if err == nil && launch.Clustered {
		notifySMBMembershipChangeFunc()
	}
	return err
}

package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/canonical/microceph/microceph/database"
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
	Clustered        bool        `json:"clustered"`
	DomainController bool        `json:"domain_controller"`
	Globals          [][2]string `json:"globals"`
	Shares           []struct {
		Name    string      `json:"name"`
		Options [][2]string `json:"options"`
	} `json:"shares"`
	Users       []smbBindingIdentity `json:"users"`
	Groups      []smbBindingIdentity `json:"groups"`
	CTDB        map[string]any       `json:"ctdb"`
	CTDBGlobals [][2]string          `json:"ctdb_globals"`
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
var smbNativeTokenPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var smbCTDBURIPattern = regexp.MustCompile(`^rados://([a-zA-Z0-9_.][a-zA-Z0-9_.-]*)/([a-zA-Z0-9-]+)/([a-zA-Z0-9_.][a-zA-Z0-9_.-]*)$`)

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
	clustered := candidate.Spec.hasFeature("clustered")
	member, err := smbMemberContext(candidate, description)
	if err != nil {
		return plan, err
	}
	if clustered != (candidate.CTDB != nil) {
		return plan, fmt.Errorf("effective CTDB mode does not match native reservation")
	}
	endpoint, err := candidate.Spec.endpointPorts()
	if err != nil {
		return plan, err
	}
	if candidate.Endpoint != nil {
		endpoint = *candidate.Endpoint
	}
	plan.Launch = smbLaunch{AD: member, Clustered: clustered, FrontendAddress: endpoint.FrontendAddress, SMBPort: endpoint.SMBPort, MetricsPort: endpoint.MetricsPort, CustomDNS: len(candidate.Spec.CustomDNS) > 0}
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
		required["winbindd socket directory"] = root + "/samba/winbindd"
		pnn := candidate.CTDB.Member.PNN
		plan.Launch.CTDBPNN = &pnn
		err = projectSMBCTDB(candidate, root, document, description.CTDB, plan.Files)
		if err != nil {
			return plan, err
		}
	}
	if member != nil {
		required["winbindd socket directory"] = "/run/samba/winbindd"
		required["kerberos method"] = "secrets only"
		for _, name := range []string{"secrets.tdb", "autorid.tdb", "winbindd_idmap.tdb"} {
			required["ctdb:"+name] = "yes"
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
	// Clustering was checked using its final effective value. Do not reject
	// superseded occurrences here; the required native value is still last.
	overlayRequired := maps.Clone(required)
	delete(overlayRequired, "clustering")
	for _, name := range globalNames {
		section, err := smbObject(globals, name)
		if err != nil {
			return plan, err
		}
		options, err := smbObject(section, "options")
		if err != nil {
			return plan, err
		}
		err = smbOverlay(options, overlayRequired)
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
				mode := "direct"
				if module == "ceph_new" && strings.ToLower(fmt.Sprint(options[module+":proxy"])) == "yes" {
					mode = "proxy"
				}
				options[key] = root + "/config/ceph-" + mode + ".conf"
			}
		}
	}
	// Effective share inspection remains upstream-owned (including aliases/defaults).
	for _, share := range description.Shares {
		options := map[string]any{}
		for _, pair := range share.Options {
			options[pair[0]] = pair[1]
		}
		err = smbNormalizeShare(options)
		if err != nil {
			return plan, err
		}
		modules := strings.Fields(fmt.Sprint(options["vfs objects"]))
		for _, module := range modules {
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
			plan.Launch.Proxy = plan.Launch.Proxy || (module == "ceph_new" && strings.ToLower(fmt.Sprint(options[module+":proxy"])) == "yes")
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
	nss := "files"
	if member != nil {
		nss = "files winbind"
	}
	plan.Files["config/nsswitch.conf"] = []byte("passwd: " + nss + "\ngroup: " + nss + "\nhosts: files dns\nservices: files\n")
	if plan.Launch.CustomDNS {
		var resolv strings.Builder
		for _, address := range candidate.Spec.CustomDNS {
			ip := net.ParseIP(address)
			if ip == nil {
				return plan, fmt.Errorf("invalid DNS address")
			}
			fmt.Fprintf(&resolv, "nameserver %s\n", ip.String())
		}
		plan.Files["config/resolv.conf"] = []byte(resolv.String())
	}
	for _, kind := range []string{"direct", "proxy"} {
		plan.Files["config/ceph-"+kind+".conf"], err = smbNativeCephConfig(candidate.CephConfig, id, root)
		if err != nil {
			return plan, err
		}
	}
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

func smbCTDBNodes(candidate smbCandidate) ([]byte, error) {
	view := candidate.CTDB
	if view == nil {
		return nil, fmt.Errorf("CTDB view is absent")
	}
	nodes := map[uint64]string{}
	identities := map[string]bool{}
	addresses := map[string]bool{}
	all := append([]database.SMBCTDBMember{view.Member}, view.Peers...)
	for _, entry := range all {
		_, duplicate := nodes[entry.PNN]
		ip := net.ParseIP(entry.Node)
		validState := entry.State == "new" || entry.State == "ready" || entry.State == "replaced" || entry.State == "changed" || entry.State == "gone"
		if entry.PNN > 65535 || duplicate || !smbNativeTokenPattern.MatchString(entry.Identity) || identities[entry.Identity] || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || addresses[ip.String()] || !validState {
			return nil, fmt.Errorf("invalid or duplicate CTDB member")
		}
		identities[entry.Identity] = true
		addresses[ip.String()] = true
		nodes[entry.PNN] = "#"
		if entry.State != "changed" && entry.State != "gone" {
			nodes[entry.PNN] = entry.Node
		}
	}
	if view.Member.State == "changed" || view.Member.State == "gone" || view.SlotCount == 0 || view.SlotCount > 65536 {
		return nil, fmt.Errorf("invalid local CTDB member or slot count")
	}
	slots := make([]string, int(view.SlotCount))
	for i := range slots {
		slots[i] = "#"
	}
	for pnn, address := range nodes {
		if pnn >= view.SlotCount {
			return nil, fmt.Errorf("invalid authoritative CTDB slot count")
		}
		slots[pnn] = address
	}
	return []byte(strings.Join(slots, "\n") + "\n"), nil
}

func projectSMBCTDB(candidate smbCandidate, root string, document map[string]any, defaults map[string]any, files map[string][]byte) error {
	nodes, err := smbCTDBNodes(candidate)
	if err != nil {
		return err
	}
	id := candidate.Spec.ClusterID
	meta := smbCTDBURIPattern.FindStringSubmatch(candidate.Spec.ClusterMetaURI)
	lock := smbCTDBURIPattern.FindStringSubmatch(candidate.Spec.ClusterLockURI)
	if len(meta) != 4 || len(lock) != 4 || meta[1] != lock[1] || meta[2] != id || lock[2] != id || meta[3] == lock[3] {
		return fmt.Errorf("invalid CTDB metadata/recovery sources")
	}
	for _, components := range [][]string{meta, lock} {
		if components[1] == "." || components[1] == ".." || components[3] == "." || components[3] == ".." {
			return fmt.Errorf("invalid CTDB RADOS component")
		}
	}
	args := []string{"/snap/microceph/current/libexec/ctdb/ctdb_mutex_ceph_rados_helper", "ceph", "client.smb.cluster." + id, lock[1], lock[3], "-n", id, "-R"}
	required := map[string]any{"cluster_meta_uri": candidate.Spec.ClusterMetaURI, "nodes_path": root + "/ctdb/nodes", "recovery_lock": "!" + strings.Join(args, " "), "nodes_cmd": "", "conf_file_includes": []any{}, "ctdb_port": 0}
	options, err := smbObject(document, "ctdb")
	if err != nil {
		return err
	}
	allowed := map[string]bool{"cluster_meta_uri": true, "nodes_path": true, "recovery_lock": true, "nodes_cmd": true, "conf_file_includes": true, "ctdb_port": true, "public_addresses": true, "log_level": true, "script_log_level": true, "realtime_scheduling": true}
	for key := range options {
		if !allowed[key] {
			return fmt.Errorf("unsupported native CTDB option")
		}
	}
	err = smbOverlay(options, required)
	if err != nil {
		return err
	}
	for key, value := range defaults {
		_, exists := options[key]
		if !exists {
			options[key] = value
		}
	}
	for _, key := range []string{"log_level", "script_log_level", "realtime_scheduling"} {
		value, ok := options[key].(string)
		if !ok || !regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(value) {
			return fmt.Errorf("invalid CTDB option: %s", key)
		}
	}
	// These native-owned fields extend the pinned shared logging/lock settings.
	conf := fmt.Sprintf("[logging]\nlocation = file:%s/diagnostics/ctdb.log\nlog level = %s\n\n[cluster]\nnode address = %s\nnodes list = %s/ctdb/nodes\nrecovery lock = %s\n\n[legacy]\nrealtime scheduling = %s\nscript log level = %s\n\n[database]\n", root, options["log_level"], candidate.CTDB.Member.Node, root, options["recovery_lock"], options["realtime_scheduling"], options["script_log_level"])
	for _, kind := range []string{"volatile", "persistent", "state"} {
		conf += fmt.Sprintf("%s database directory = %s/ctdb/%s\n", kind, root, kind)
	}
	conf += "\n[event]\nscript directory = " + root + "/ctdb/events\n"
	files["ctdb/ctdb.conf"] = []byte(conf)
	files["ctdb/nodes"] = nodes
	files["config/ctdb-rank"] = []byte(strconv.FormatUint(candidate.CTDB.Member.PNN, 10) + "\n")
	files["config/ctdb-identity"] = []byte(candidate.CTDB.Member.Identity + "\n")
	files["config/ctdb-address"] = []byte(candidate.CTDB.Member.Node + "\n")
	raw, err := json.Marshal(map[string]any{"samba-container-config": "v0", "ctdb": options})
	files["config/ctdb.json"] = append(raw, '\n')
	return err
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

func publishSMBNative(ctx context.Context, root string, configOnly bool) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	err = ctx.Err()
	if err != nil {
		return err
	}
	if configOnly {
		err := checkSMBStartIntent(root)
		if err != nil {
			return err
		}
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
		if filepath.Dir(relative) == "." {
			relative = filepath.Join("config", relative)
		}
		if !filepath.IsLocal(relative) {
			return fmt.Errorf("invalid native candidate path")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if configOnly {
			protected := relative == "ctdb/ctdb.conf" || (filepath.Dir(relative) == "config" && strings.HasPrefix(filepath.Base(relative), "ctdb"))
			if protected {
				target := filepath.Join(root, relative)
				info, err := os.Lstat(target)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("protected CTDB file is a symlink")
				}
				current, err := os.ReadFile(target)
				if err != nil {
					return err
				}
				equal := bytes.Equal(current, data)
				if !equal && relative == "config/ctdb.json" {
					// The former Python publisher used spaced JSON. Compare this
					// descriptor semantically, but never rewrite its retained bytes.
					var old, next any
					oldErr := json.Unmarshal(current, &old)
					nextErr := json.Unmarshal(data, &next)
					equal = oldErr == nil && nextErr == nil && reflect.DeepEqual(old, next)
				}
				if !equal {
					return fmt.Errorf("CTDB configuration changed; explicit maintenance required")
				}
				return nil
			}
			if strings.HasPrefix(relative, "ctdb/") {
				return nil
			}
			if !strings.HasPrefix(relative, "config/") {
				return fmt.Errorf("configuration refresh cannot publish runtime state")
			}
		}
		files[relative] = data
		return nil
	})
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		err = ctx.Err()
		if err != nil {
			return err
		}
		if configOnly {
			err = checkSMBStartIntent(root)
			if err != nil {
				return err
			}
		}
		err = writeSMBNativeFile(filepath.Join(root, name), files[name])
		if err != nil {
			return err
		}
	}
	return nil
}

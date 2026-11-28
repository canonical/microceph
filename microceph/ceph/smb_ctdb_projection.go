package ceph

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/canonical/microceph/microceph/database"
)

var smbCTDBURIPattern = regexp.MustCompile(`^rados://([a-zA-Z0-9_.][a-zA-Z0-9_.-]*)/([a-zA-Z0-9-]+)/([a-zA-Z0-9_.][a-zA-Z0-9_.-]*)$`)
var smbNativeTokenPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func smbCTDBNodes(candidate smbCandidate) ([]byte, error) {
	view := candidate.CTDB
	if view == nil || view.SlotCount == 0 || view.SlotCount > 65536 {
		return nil, fmt.Errorf("invalid CTDB view or slot count")
	}
	slots := make([]string, int(view.SlotCount))
	for i := range slots {
		slots[i] = "#"
	}
	ranks := map[uint64]bool{}
	identities := map[string]bool{}
	addresses := map[string]bool{}
	for _, entry := range append([]database.SMBCTDBMember{view.Member}, view.Peers...) {
		ip := net.ParseIP(entry.Node)
		valid := entry.State == "new" || entry.State == "ready" || entry.State == "replaced" || entry.State == "changed" || entry.State == "gone"
		if entry.PNN >= view.SlotCount || ranks[entry.PNN] || !smbNativeTokenPattern.MatchString(entry.Identity) || identities[entry.Identity] || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || addresses[ip.String()] || !valid {
			return nil, fmt.Errorf("invalid or duplicate CTDB member")
		}
		ranks[entry.PNN] = true
		identities[entry.Identity] = true
		addresses[ip.String()] = true
		if entry.State != "changed" && entry.State != "gone" {
			slots[entry.PNN] = ip.String()
		}
	}
	if view.Member.State == "changed" || view.Member.State == "gone" {
		return nil, fmt.Errorf("invalid local CTDB member")
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
	conf := fmt.Sprintf("[logging]\nlocation = file:%s/diagnostics/ctdb.log\nlog level = %s\n\n[cluster]\nnode address = %s\nnodes list = %s/ctdb/nodes\nrecovery lock = %s\n\n[legacy]\nrealtime scheduling = %s\nscript log level = %s\n\n[database]\n", root, options["log_level"], candidate.CTDB.Member.Node, root, options["recovery_lock"], options["realtime_scheduling"], options["script_log_level"])
	for _, kind := range []string{"volatile", "persistent", "state"} {
		conf += fmt.Sprintf("%s database directory = %s/ctdb/%s\n", kind, root, kind)
	}
	files["ctdb/ctdb.conf"] = []byte(conf)
	files["ctdb/nodes"] = nodes
	files["config/ctdb-rank"] = []byte(strconv.FormatUint(candidate.CTDB.Member.PNN, 10) + "\n")
	files["config/ctdb-identity"] = []byte(candidate.CTDB.Member.Identity + "\n")
	files["config/ctdb-address"] = []byte(candidate.CTDB.Member.Node + "\n")
	raw, err := json.Marshal(map[string]any{"samba-container-config": "v0", "ctdb": options})
	files["config/ctdb.json"] = append(raw, '\n')
	return err
}

package ceph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
)

// SMBPaths carries the snap path roots the renderers embed in configs.
// Snap is the revision-specific $SNAP dir; SnapStable is the /snap/<name>/
// current alias for content that must be identical across nodes.
type SMBPaths struct {
	Conf       string
	Run        string
	Data       string
	Log        string
	Snap       string
	SnapStable string
}

// SMBRenderParams carries per-node, per-cluster rendering inputs.
type SMBRenderParams struct {
	ClusterID string
	Hostname  string
	// Entity is the node's daemon cephx entity (client. prefixed).
	Entity    string
	Clustered bool
	Paths     SMBPaths
}

// NewSMBRenderParams builds render params from the snap environment.
func NewSMBRenderParams(clusterID, hostname string, clustered bool) SMBRenderParams {
	pathConsts := constants.GetPathConst()
	return SMBRenderParams{
		ClusterID: clusterID,
		Hostname:  hostname,
		Entity:    SMBDaemonEntity(clusterID, hostname),
		Clustered: clustered,
		Paths: SMBPaths{
			Conf:       pathConsts.ConfPath,
			Run:        pathConsts.RunPath,
			Data:       pathConsts.DataPath,
			Log:        pathConsts.LogPath,
			Snap:       strings.TrimRight(pathConsts.SnapPath, "/"),
			SnapStable: filepath.Join("/snap", os.Getenv("SNAP_NAME"), "current"),
		},
	}
}

// parseRADOSURI splits rados://<pool>/[<namespace>/]<object>.
func parseRADOSURI(uri string) (string, string, string, error) {
	if !strings.HasPrefix(uri, "rados://") {
		return "", "", "", fmt.Errorf("'%s' is not a rados:// URI", uri)
	}

	part := strings.TrimRight(strings.TrimPrefix(uri, "rados://"), "/")
	fields := strings.Split(part, "/")
	switch len(fields) {
	case 2:
		return fields[0], "", fields[1], nil
	case 3:
		return fields[0], fields[1], fields[2], nil
	}
	return "", "", "", fmt.Errorf("cannot parse rados URI '%s'", uri)
}

// microcephGlobalOptions returns the snap-specific smb.conf globals the
// generated config must carry: the known-working paths from the live
// experiment, plus clustering wiring when CTDB is on.
func microcephGlobalOptions(p SMBRenderParams) map[string]any {
	dataDir := filepath.Join(p.Paths.Data, "samba", p.ClusterID)
	runDir := filepath.Join(p.Paths.Run, "samba", p.ClusterID)

	options := map[string]any{
		"security":        "user",
		"netbios name":    strings.ToUpper(p.ClusterID),
		"private dir":     filepath.Join(dataDir, "private"),
		"lock directory":  filepath.Join(dataDir, "lock"),
		"state directory": filepath.Join(dataDir, "state"),
		"cache directory": filepath.Join(dataDir, "cache"),
		"pid directory":   runDir,
		"ncalrpc dir":     filepath.Join(runDir, "ncalrpc"),
		"log file":        filepath.Join(p.Paths.Log, "samba", p.ClusterID, "log.%m"),
	}

	if p.Clustered {
		options["clustering"] = "yes"
		options["ctdbd socket"] = filepath.Join(p.Paths.Run, "ctdb", "ctdbd.socket")
	}

	return options
}

// translateShareOptions rewrites ceph_new vfs options to the classic ceph
// module: the snap's samba 4.19 ships only ceph.so, while mgr/smb's
// default share provider emits the proxied ceph_new form. Drop this when
// the snap moves to samba >= 4.20.
func translateShareOptions(options map[string]any) {
	if vfs, ok := options["vfs objects"].(string); ok {
		fields := strings.Fields(vfs)
		for i, field := range fields {
			if field == "ceph_new" {
				fields[i] = "ceph"
			}
		}
		options["vfs objects"] = strings.Join(fields, " ")
	}

	delete(options, "ceph_new:proxy")
	for key, value := range options {
		if strings.HasPrefix(key, "ceph_new:") {
			options["ceph:"+strings.TrimPrefix(key, "ceph_new:")] = value
			delete(options, key)
		}
	}
}

// TranslateSMBConfig rewrites the mgr/smb sambacc config document for
// this backend and reports whether the instance is CTDB-clustered. The
// output feeds sambacc print-config.
func TranslateSMBConfig(raw []byte, p SMBRenderParams) ([]byte, bool, error) {
	var doc map[string]any
	err := json.Unmarshal(raw, &doc)
	if err != nil {
		return nil, false, fmt.Errorf("cannot parse smb config document: %w", err)
	}

	version, _ := doc["samba-container-config"].(string)
	if version != "v0" {
		return nil, false, fmt.Errorf("unsupported samba-container-config version '%s'", version)
	}

	configs, _ := doc["configs"].(map[string]any)
	instance, _ := configs[p.ClusterID].(map[string]any)
	if instance == nil {
		return nil, false, fmt.Errorf("config document has no instance for cluster '%s'", p.ClusterID)
	}

	clustered := false
	if features, ok := instance["instance_features"].([]any); ok {
		for _, feature := range features {
			if feature == "ctdb" {
				clustered = true
			}
		}
	}
	p.Clustered = clustered

	// Inject the microceph globals section and reference it last so its
	// options win over the mgr-emitted ones.
	globals, ok := doc["globals"].(map[string]any)
	if !ok {
		globals = map[string]any{}
		doc["globals"] = globals
	}
	globals["microceph"] = map[string]any{"options": microcephGlobalOptions(p)}

	globalRefs, _ := instance["globals"].([]any)
	instance["globals"] = append(globalRefs, "microceph")

	if shares, ok := doc["shares"].(map[string]any); ok {
		for _, share := range shares {
			shareMap, ok := share.(map[string]any)
			if !ok {
				continue
			}
			if options, ok := shareMap["options"].(map[string]any); ok {
				translateShareOptions(options)
			}
		}
	}

	translated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}

	return append(translated, '\n'), clustered, nil
}

// smbReclockObject is the CTDB cluster lock object prefix; the samba 4.19
// rados mutex helper has no namespace support, so the lock lives in the
// lock pool's default namespace under a per-cluster name instead of at
// the (namespaced) cluster_lock_uri object, which stays owned by mgr/smb.
const smbReclockObject = "microceph.reclock."

// RenderCTDBConf renders ctdb.conf with the cluster lock held via the
// bundled rados mutex helper (4.19 syntax: 'cluster lock').
func RenderCTDBConf(p SMBRenderParams, lockURI string) (string, error) {
	pool, _, _, err := parseRADOSURI(lockURI)
	if err != nil {
		return "", fmt.Errorf("cannot derive cluster lock from cluster_lock_uri: %w", err)
	}

	// CTDB refuses to run when the cluster lock command line differs
	// between nodes, so it must avoid anything node-specific: the shared
	// per-cluster entity (not the per-node daemon key) and the
	// revision-independent snap path ($SNAP embeds the local revision).
	helper := filepath.Join(p.Paths.SnapStable, "libexec", "ctdb", "ctdb_mutex_ceph_rados_helper")
	object := smbReclockObject + p.ClusterID
	dbDir := filepath.Join(p.Paths.Data, "ctdb")

	// The [database] paths and the logging location override ctdbd's
	// compiled-in /var/lib/ctdb and /var/log defaults, which do not exist
	// inside the snap (the event daemon dies at init without a usable
	// logging location).
	return fmt.Sprintf(`[logging]
    location = file:%s
    log level = NOTICE

[database]
    volatile database directory = %s
    persistent database directory = %s
    state database directory = %s

[cluster]
    cluster lock = !%s ceph %s %s %s
`, filepath.Join(p.Paths.Log, "ctdb", "log.ctdb"),
		filepath.Join(dbDir, "volatile"), filepath.Join(dbDir, "persistent"), filepath.Join(dbDir, "state"),
		helper, SMBClusterEntity(p.ClusterID), pool, object), nil
}

// RenderCTDBNodes renders the nodes file: one private address per line.
// Callers must pass a stable, append-only ordering (CTDB node numbers
// are line indices); ordering by DB row id provides that.
func RenderCTDBNodes(ips []string) string {
	var b strings.Builder
	for _, ip := range ips {
		b.WriteString(ip)
		b.WriteByte('\n')
	}
	return b.String()
}

// atomicWriteFile writes via a .tmp sibling and rename so a failed write
// cannot leave partial config state on disk.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmpFile := path + ".tmp"
	err := os.WriteFile(tmpFile, data, mode)
	if err != nil {
		return err
	}
	err = os.Rename(tmpFile, path)
	if err != nil {
		os.Remove(tmpFile)
		return err
	}
	return nil
}

// fetchSMBConfigObject reads the object behind a rados:// URI.
func fetchSMBConfigObject(uri string) ([]byte, error) {
	pool, namespace, object, err := parseRADOSURI(uri)
	if err != nil {
		return nil, err
	}

	args := []string{"get", "--pool", pool}
	if namespace != "" {
		args = append(args, "-N", namespace)
	}
	args = append(args, object, "-")

	out, err := radosRun(args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch '%s': %w", uri, err)
	}
	return []byte(out), nil
}

// renderSMBConfText runs the bundled sambacc to turn a translated config
// document into smb.conf text (pure-python path, no samba binaries).
func renderSMBConfText(configPath, identity string) (string, error) {
	out, err := common.ProcessExec.RunCommand("python3",
		"-m", "sambacc.commands.main",
		"--config", configPath,
		"--identity", identity,
		"print-config")
	if err != nil {
		return "", fmt.Errorf("sambacc print-config failed: %w", err)
	}
	return out, nil
}

// WriteSMBNodeConfigs fetches the cluster's sambacc config document,
// renders smb.conf through sambacc, and writes the CTDB config set for
// this node. nodeIPs must already carry the stable CTDB ordering.
func WriteSMBNodeConfigs(spec *types.SMBSpec, p SMBRenderParams, nodeIPs []string, resolveIface func(cidr string) (string, error)) error {
	raw, err := fetchSMBConfigObject(spec.ConfigURI)
	if err != nil {
		return err
	}

	translated, clustered, err := TranslateSMBConfig(raw, p)
	if err != nil {
		return err
	}
	p.Clustered = clustered

	sambaDir := filepath.Join(p.Paths.Conf, "samba")
	err = os.MkdirAll(sambaDir, 0755)
	if err != nil {
		return err
	}

	configPath := filepath.Join(sambaDir, "config.json")
	err = atomicWriteFile(configPath, translated, 0644)
	if err != nil {
		return err
	}

	smbConf, err := renderSMBConfText(configPath, p.ClusterID)
	if err != nil {
		return err
	}
	err = atomicWriteFile(filepath.Join(sambaDir, "smb.conf"), []byte(smbConf), 0644)
	if err != nil {
		return err
	}

	if !clustered {
		return nil
	}

	ctdbDir := filepath.Join(p.Paths.Conf, "ctdb")
	err = os.MkdirAll(ctdbDir, 0755)
	if err != nil {
		return err
	}

	ctdbConf, err := RenderCTDBConf(p, spec.ClusterLockURI)
	if err != nil {
		return err
	}
	err = atomicWriteFile(filepath.Join(ctdbDir, "ctdb.conf"), []byte(ctdbConf), 0644)
	if err != nil {
		return err
	}

	err = atomicWriteFile(filepath.Join(ctdbDir, "nodes"), []byte(RenderCTDBNodes(nodeIPs)), 0644)
	if err != nil {
		return err
	}

	publicAddresses, err := RenderCTDBPublicAddresses(spec.ClusterPublicAddrs, resolveIface)
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(ctdbDir, "public_addresses"), []byte(publicAddresses), 0644)
}

// RenderCTDBPublicAddresses renders public_addresses: '<cidr> <iface>'
// per line, with the interface resolved on this node for each VIP.
func RenderCTDBPublicAddresses(addrs []types.SMBPublicAddrSpec, resolveIface func(cidr string) (string, error)) (string, error) {
	var b strings.Builder
	for _, addr := range addrs {
		iface, err := resolveIface(addr.Address)
		if err != nil {
			return "", fmt.Errorf("cannot resolve interface for public address '%s': %w", addr.Address, err)
		}
		b.WriteString(addr.Address)
		b.WriteByte(' ')
		b.WriteString(iface)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

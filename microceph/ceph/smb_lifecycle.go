package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// Injectable seams for unit tests.
var (
	smbMemberAddressesFunc = smbMemberAddresses
	resolveSMBIfaceFunc    = resolveSMBIface
)

// smbStockCTDBScripts are the legacy event scripts the ctdb deb enables
// by default (shipped read-only at $SNAP/etc/ctdb); 10.interface is what
// assigns public addresses.
var smbStockCTDBScripts = []string{
	"00.ctdb.script",
	"01.reclock.script",
	"05.system.script",
	"10.interface.script",
}

// smbMemberAddresses maps cluster member names to their host addresses
// from the local trust store (no network round trip).
func smbMemberAddresses(s interfaces.StateInterface) (map[string]string, error) {
	remotes := s.ClusterState().Truststore().RemoteAddresses()
	addresses := make(map[string]string, len(remotes))
	for name, addrPort := range remotes {
		addresses[name] = addrPort.Addr().String()
	}
	return addresses, nil
}

// resolveSMBIface returns the local interface whose subnet contains the
// given VIP address.
func resolveSMBIface(cidr string) (string, error) {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			_, ifaceNet, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			if ifaceNet.Contains(ip) {
				return iface.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no local interface covers '%s'", cidr)
}

// smbOrderedNodeIPs returns the group members' host addresses in row-id
// order: stable and append-only, as the CTDB nodes file requires. The
// local node is appended when its own row does not exist yet: during
// enable, rendering runs before DbUpdate records this node, and the
// membership row lands next (so appending preserves row-id order).
func smbOrderedNodeIPs(ctx context.Context, s interfaces.StateInterface, clusterID, hostname string) ([]string, error) {
	records, err := database.GroupedServicesQuery.GetGroupMemberRecords(ctx, s, "smb", clusterID)
	if err != nil {
		return nil, err
	}

	// CTDB node numbers are nodes-file line indices: the order must be
	// stable and append-only, which row ids provide.
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })

	selfRecorded := false
	for _, record := range records {
		if record.Member == hostname {
			selfRecorded = true
		}
	}
	if !selfRecorded {
		records = append(records, database.GroupedService{Member: hostname})
	}

	addresses, err := smbMemberAddressesFunc(s)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster member addresses: %w", err)
	}

	ips := make([]string, 0, len(records))
	for _, record := range records {
		ip, ok := addresses[record.Member]
		if !ok {
			return nil, fmt.Errorf("no address known for cluster member '%s'", record.Member)
		}
		ips = append(ips, ip)
	}

	return ips, nil
}

// populateCTDBBase fills CTDB_BASE with the stock deb-enabled event
// script links, our snapctl-based 50.samba, the functions library and
// script.options.
func populateCTDBBase(ctdbDir, snapPath string) error {
	legacyDir := filepath.Join(ctdbDir, "events", "legacy")
	err := os.MkdirAll(legacyDir, 0755)
	if err != nil {
		return err
	}

	relink := func(target, link string) error {
		err := os.Remove(link)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return os.Symlink(target, link)
	}

	for _, script := range smbStockCTDBScripts {
		err = relink(filepath.Join(snapPath, "etc", "ctdb", "events", "legacy", script), filepath.Join(legacyDir, script))
		if err != nil {
			return err
		}
	}

	err = relink(filepath.Join(snapPath, "ctdb", "events", "legacy", "50.samba.script"), filepath.Join(legacyDir, "50.samba.script"))
	if err != nil {
		return err
	}

	for _, file := range []string{"functions", "notify.sh"} {
		err = relink(filepath.Join(snapPath, "etc", "ctdb", file), filepath.Join(ctdbDir, file))
		if err != nil {
			return err
		}
	}

	options, err := os.ReadFile(filepath.Join(snapPath, "ctdb", "script.options"))
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(ctdbDir, "script.options"), options, 0644)
}

// removeDirContents deletes everything inside dir but keeps dir itself.
func removeDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		err = os.RemoveAll(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

// smbNodeDirs returns the per-cluster directories the daemons need, with
// their modes. Everything is root-owned: confined daemons have no
// dac_override, so a foreign-owned dir would be unreadable.
func smbNodeDirs(p SMBRenderParams) map[string]os.FileMode {
	dataDir := filepath.Join(p.Paths.Data, "samba", p.ClusterID)
	runDir := filepath.Join(p.Paths.Run, "samba", p.ClusterID)

	return map[string]os.FileMode{
		p.Paths.Conf:                                      0755,
		filepath.Join(p.Paths.Conf, "samba"):              0755,
		filepath.Join(dataDir, "private"):                 0700,
		filepath.Join(dataDir, "lock"):                    0755,
		filepath.Join(dataDir, "state"):                   0755,
		filepath.Join(dataDir, "cache"):                   0755,
		filepath.Join(runDir, "ncalrpc"):                  0755,
		filepath.Join(p.Paths.Run, "ctdb"):                0755,
		filepath.Join(p.Paths.Log, "samba", p.ClusterID):  0755,
		filepath.Join(p.Paths.Data, "ctdb", "volatile"):   0700,
		filepath.Join(p.Paths.Data, "ctdb", "persistent"): 0700,
		filepath.Join(p.Paths.Data, "ctdb", "state"):      0700,
		filepath.Join(p.Paths.Log, "ctdb"):                0755,
	}
}

// enableSMBNodeLocal brings this node into an smb cluster: directories,
// keyrings, rendered configs, CTDB_BASE and the ctdbd service (which
// starts smbd through the 50.samba event script).
func enableSMBNodeLocal(ctx context.Context, s interfaces.StateInterface, spec *types.SMBSpec, p SMBRenderParams) error {
	for dir, mode := range smbNodeDirs(p) {
		err := os.MkdirAll(dir, mode)
		if err != nil {
			return err
		}
	}

	err := EnsureSMBKeyrings(spec, p.Hostname, p.Paths.Conf)
	if err != nil {
		return err
	}

	ips, err := smbOrderedNodeIPs(ctx, s, spec.ClusterID, p.Hostname)
	if err != nil {
		return err
	}

	err = WriteSMBNodeConfigs(spec, p, ips, resolveSMBIfaceFunc)
	if err != nil {
		return err
	}

	err = populateCTDBBase(filepath.Join(p.Paths.Conf, "ctdb"), p.Paths.Snap)
	if err != nil {
		return err
	}

	err = snapStart("ctdbd", true)
	if err != nil {
		return fmt.Errorf("failed to start ctdbd: %w", err)
	}

	logger.Infof("enabled smb cluster '%s' on this node", spec.ClusterID)
	return nil
}

// EnableSMB is the env-wired entry point used by the placement flow.
func EnableSMB(ctx context.Context, s interfaces.StateInterface, spec *types.SMBSpec) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	clustered := false
	for _, feature := range spec.Features {
		if feature == "clustered" {
			clustered = true
		}
	}

	return enableSMBNodeLocal(ctx, s, spec, NewSMBRenderParams(spec.ClusterID, hostname, clustered))
}

// disableSMBLocal tears this node out of an smb cluster: services,
// keyrings, configs, runtime dirs and per-cluster data, then the DB
// record. Missing group config downgrades to best-effort file cleanup.
func disableSMBLocal(ctx context.Context, s interfaces.StateInterface, clusterID string, p SMBRenderParams) error {
	err := snapStop("ctdbd", true)
	if err != nil {
		logger.Warnf("failed to stop ctdbd while disabling smb '%s': %v", clusterID, err)
	}
	err = snapStop("smbd", true)
	if err != nil {
		logger.Warnf("failed to stop smbd while disabling smb '%s': %v", clusterID, err)
	}

	config, err := database.GroupedServicesQuery.GetGroupConfig(ctx, s, "smb", clusterID)
	if err == nil {
		var spec types.SMBSpec
		err = json.Unmarshal([]byte(config), &spec)
		if err == nil {
			err = RemoveSMBKeyrings(&spec, p.Hostname, p.Paths.Conf)
			if err != nil {
				logger.Warnf("failed to remove smb keyrings for '%s': %v", clusterID, err)
			}
		}
	} else {
		logger.Warnf("no stored config for smb cluster '%s'; skipping keyring cleanup: %v", clusterID, err)
	}

	for _, path := range []string{
		filepath.Join(p.Paths.Conf, "samba", "smb.conf"),
		filepath.Join(p.Paths.Conf, "samba", "config.json"),
	} {
		err = os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	// conf/ctdb is the target of the /etc/ctdb layout bind: removing the
	// directory itself leaves the snap namespace bound to a dead inode
	// (services then read an empty ghost dir until the ns is rebuilt), so
	// only its contents are cleared.
	err = removeDirContents(filepath.Join(p.Paths.Conf, "ctdb"))
	if err != nil {
		return err
	}

	for _, dir := range []string{
		filepath.Join(p.Paths.Run, "samba", clusterID),
		filepath.Join(p.Paths.Run, "ctdb"),
		filepath.Join(p.Paths.Data, "samba", clusterID),
		filepath.Join(p.Paths.Data, "ctdb"),
	} {
		err = os.RemoveAll(dir)
		if err != nil {
			return err
		}
	}

	return database.GroupedServicesQuery.RemoveForHost(ctx, s, "smb", clusterID)
}

// regenerateSMBNodeLocal re-renders this node's configs from the stored
// spec and restarts ctdbd; used when membership or the spec changes.
func regenerateSMBNodeLocal(ctx context.Context, s interfaces.StateInterface, clusterID string, p SMBRenderParams) error {
	config, err := database.GroupedServicesQuery.GetGroupConfig(ctx, s, "smb", clusterID)
	if err != nil {
		return fmt.Errorf("failed to fetch config for smb cluster '%s': %w", clusterID, err)
	}

	var spec types.SMBSpec
	err = json.Unmarshal([]byte(config), &spec)
	if err != nil {
		return fmt.Errorf("cannot parse stored spec for smb cluster '%s': %w", clusterID, err)
	}

	ips, err := smbOrderedNodeIPs(ctx, s, clusterID, p.Hostname)
	if err != nil {
		return err
	}

	err = WriteSMBNodeConfigs(&spec, p, ips, resolveSMBIfaceFunc)
	if err != nil {
		return err
	}

	err = snapRestart("ctdbd", false)
	if err != nil {
		return fmt.Errorf("failed to restart ctdbd: %w", err)
	}

	logger.Infof("regenerated smb cluster '%s' configs on this node", clusterID)
	return nil
}

// RegenerateSMBNode is the env-wired entry point for config regeneration.
func RegenerateSMBNode(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	return regenerateSMBNodeLocal(ctx, s, clusterID, NewSMBRenderParams(clusterID, hostname, true))
}

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
)

// SMBServiceGroupConfig retains rank history and actual owner reservations.
// Owners are recorded before startup, not desired or successful deployments.
// Removing a member must not remove its uncertain ownership from this object.
type SMBServiceGroupConfig struct {
	Clustered bool              `json:"clustered"`
	NextRank  uint64            `json:"next_rank"`
	Ranks     map[string]uint64 `json:"ranks"`
	Owners    map[string]string `json:"owners"` // member database identity -> member name
	// Endpoints retains private IP evidence even after core-member removal.
	Endpoints map[string]string `json:"endpoints,omitempty"`
}

func smbMemberIdentity(ctx context.Context, tx *sql.Tx, member string) (string, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM core_cluster_members WHERE name=?", member).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("resolve SMB member identity: %w", err)
	}
	return strconv.FormatInt(id, 10), nil
}

func smbMemberIP(ctx context.Context, tx *sql.Tx, identity string) (string, error) {
	var address string
	err := tx.QueryRowContext(ctx, "SELECT address FROM core_cluster_members WHERE id=?", identity).Scan(&address)
	if err != nil {
		return "", fmt.Errorf("SMB owner has uncertain private address: %w", err)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid SMB private member address: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return "", fmt.Errorf("invalid SMB private member IP")
	}
	return ip.Unmap().String(), nil
}

// GetSMBOwnerGroups finds exact actual owners, including failed deployments.
// A reused member name is not authorization to retire an older identity.
func GetSMBOwnerGroups(ctx context.Context, tx *sql.Tx, member string) ([]string, error) {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT group_id, config FROM service_groups WHERE service='smb'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []string{}
	for rows.Next() {
		var group, raw string
		err = rows.Scan(&group, &raw)
		if err != nil {
			return nil, err
		}
		var config SMBServiceGroupConfig
		err = json.Unmarshal([]byte(raw), &config)
		if err != nil || config.Owners == nil || config.Ranks == nil {
			return nil, fmt.Errorf("invalid retained SMB ownership")
		}
		for id, name := range config.Owners {
			if name == member || id == identity {
				if id != identity || name != member {
					return nil, fmt.Errorf("SMB owner identity does not match; retain uncertainty")
				}
				groups = append(groups, group)
			}
		}
	}
	sort.Strings(groups)
	return groups, rows.Err()
}

func smbGroup(ctx context.Context, tx *sql.Tx, clusterID string) (int64, SMBServiceGroupConfig, error) {
	var id int64
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT id, config FROM service_groups WHERE service='smb' AND group_id=?", clusterID).Scan(&id, &raw)
	if err != nil {
		return 0, SMBServiceGroupConfig{}, err
	}
	var config SMBServiceGroupConfig
	err = json.Unmarshal([]byte(raw), &config)
	if err != nil {
		return 0, config, fmt.Errorf("invalid retained SMB ownership: %w", err)
	}
	if config.Ranks == nil || config.Owners == nil {
		return 0, config, fmt.Errorf("missing retained SMB rank/owner information")
	}
	return id, config, nil
}

func saveSMBGroup(ctx context.Context, tx *sql.Tx, id int64, config SMBServiceGroupConfig) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE service_groups SET config=? WHERE id=?", string(raw), id)
	return err
}

// ReserveSMBOwner atomically reserves a member owner and, if clustered, a stable CTDB rank.
// Call in a short database write transaction, before starting any app. Retain
// the reservation if startup cleanup cannot prove that every app has stopped.
func ReserveSMBOwner(ctx context.Context, tx *sql.Tx, clusterID, member string, clustered bool) (uint64, error) {
	if clusterID == "" || member == "" {
		return 0, fmt.Errorf("SMB cluster and member must be nonempty")
	}
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return 0, err
	}
	endpoint := ""
	if clustered {
		endpoint, err = smbMemberIP(ctx, tx, identity)
		if err != nil {
			return 0, err
		}
	}
	// Scan reservations, not success rows: a failed activation can still run.
	rows, err := tx.QueryContext(ctx, "SELECT group_id, config FROM service_groups WHERE service='smb'")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var group, raw string
		err = rows.Scan(&group, &raw)
		if err != nil {
			rows.Close()
			return 0, err
		}
		var config SMBServiceGroupConfig
		err = json.Unmarshal([]byte(raw), &config)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("invalid retained SMB ownership: %w", err)
		}
		if config.Owners == nil || config.Ranks == nil {
			rows.Close()
			return 0, fmt.Errorf("missing retained SMB ownership")
		}
		for ownerID, name := range config.Owners {
			if clustered && config.Clustered {
				reservedIP := config.Endpoints[ownerID]
				if reservedIP == "" {
					reservedIP, err = smbMemberIP(ctx, tx, ownerID)
					if err != nil {
						rows.Close()
						return 0, err
					}
				}
				if (ownerID != identity && reservedIP == endpoint) || (ownerID == identity && reservedIP != endpoint) {
					rows.Close()
					return 0, fmt.Errorf("SMB private endpoint has retained ownership; retire the exact owner first")
				}
			}
			if name == member || ownerID == identity {
				if group != clusterID || ownerID != identity || name != member {
					rows.Close()
					return 0, fmt.Errorf("member %q has retained SMB ownership in %q; retire or fence the exact owner first", member, group)
				}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if errors.Is(err, sql.ErrNoRows) {
		config = SMBServiceGroupConfig{Clustered: clustered, Ranks: map[string]uint64{}, Owners: map[string]string{}}
		raw, marshalErr := json.Marshal(config)
		if marshalErr != nil {
			return 0, marshalErr
		}
		result, createErr := tx.ExecContext(ctx, "INSERT INTO service_groups(service, group_id, config) VALUES('smb', ?, ?)", clusterID, string(raw))
		if createErr != nil {
			return 0, createErr
		}
		id, err = result.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	if config.Clustered != clustered {
		return 0, fmt.Errorf("SMB mode conflicts with retained rank/identity state; maintenance required")
	}
	rank, exists := config.Ranks[identity]
	if clustered && !exists {
		// CTDB PNN is signed 32-bit. Never wrap or recycle a historical rank.
		if config.NextRank > 2147483647 {
			return 0, fmt.Errorf("SMB CTDB rank space exhausted")
		}
		rank = config.NextRank
		config.NextRank++
		config.Ranks[identity] = rank
	}
	config.Owners[identity] = member
	if clustered {
		if config.Endpoints == nil {
			config.Endpoints = map[string]string{}
		}
		config.Endpoints[identity] = endpoint
	}
	err = saveSMBGroup(ctx, tx, id, config)
	return rank, err
}

// RecordSMBService publishes the normal service row only after actual readiness.
// Refresh updates that row without replacing the group's reservation history.
func RecordSMBService(ctx context.Context, tx *sql.Tx, clusterID, member, info string) error {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	if config.Owners[identity] != member {
		return fmt.Errorf("SMB service has no matching owner reservation")
	}
	result, err := tx.ExecContext(ctx, "UPDATE grouped_services SET info=? WHERE service_group_id=? AND member_id=?", info, id, identity)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n > 1 {
		return fmt.Errorf("duplicate SMB service rows")
	}
	if n == 0 {
		_, err = tx.ExecContext(ctx, "INSERT INTO grouped_services(service_group_id, member_id, info) VALUES(?, ?, ?)", id, identity, info)
	}
	return err
}

// RetireSMBOwner removes the exact local owner and success row after verified
// stop. It preserves shared group/rank history, auth and all filesystem data.
// An offline/forced removal must not call this function as proof of a stop.
func RetireSMBOwner(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	for id, name := range config.Owners {
		if (name == member || id == identity) && (id != identity || name != member) {
			return fmt.Errorf("SMB owner identity does not match; retain uncertainty")
		}
	}
	if config.Owners[identity] == "" {
		// Stop was independently verified by the caller. Permit a local cleanup
		// retry after SQL retirement committed but owner-file removal failed.
		return nil
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM grouped_services WHERE service_group_id=? AND member_id=?", id, identity)
	if err != nil {
		return err
	}
	delete(config.Owners, identity)
	return saveSMBGroup(ctx, tx, id, config)
}

// WithdrawSMBService removes only the successful row while a local update has
// confirmed its consumers stopped. Actual ownership and rank history remain.
func WithdrawSMBService(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	if config.Owners[identity] != member {
		return fmt.Errorf("SMB owner identity does not match; retain uncertainty")
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM grouped_services WHERE service_group_id=? AND member_id=?", id, identity)
	return err
}

// SMBCTDBMember describes an actual reserved owner using its immutable SQL identity.
type SMBCTDBMember struct {
	Identity string `json:"identity"`
	PNN      uint64 `json:"pnn"`
	Node     string `json:"node"`
	State    string `json:"state"`
}

// SMBCTDBView is the authoritative native startup input, not desired membership.
type SMBCTDBView struct {
	SlotCount uint64          `json:"slot_count"`
	Member    SMBCTDBMember   `json:"member"`
	Peers     []SMBCTDBMember `json:"peers"`
}

// GetSMBCTDBView joins retained reservations to private MicroCluster addresses.
// Missing members with retained ownership fail closed; retirement is the only
// operation that removes them. Retired ranks remain holes, never new peers.
func GetSMBCTDBView(ctx context.Context, tx *sql.Tx, clusterID, member string) (*SMBCTDBView, error) {
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return nil, err
	}
	if !config.Clustered {
		return nil, fmt.Errorf("SMB group is not clustered")
	}
	localID, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return nil, err
	}
	if config.NextRank == 0 || config.NextRank > 65536 {
		return nil, fmt.Errorf("invalid retained SMB CTDB slot count")
	}
	view := &SMBCTDBView{Peers: []SMBCTDBMember{}, SlotCount: config.NextRank}
	ranks := map[uint64]bool{}
	addresses := map[netip.Addr]bool{}
	for identity, owner := range config.Owners {
		rank, ok := config.Ranks[identity]
		if !ok || rank >= config.NextRank || rank > 65535 || ranks[rank] {
			return nil, fmt.Errorf("invalid retained SMB CTDB rank")
		}
		ranks[rank] = true
		var name, address string
		err = tx.QueryRowContext(ctx, "SELECT name, address FROM core_cluster_members WHERE id=?", identity).Scan(&name, &address)
		if err != nil || name != owner {
			return nil, fmt.Errorf("SMB owner %q has uncertain member identity/address; retire or fence first", owner)
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid SMB private member address: %w", err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || addresses[ip.Unmap()] {
			return nil, fmt.Errorf("invalid or duplicate SMB private member IP")
		}
		if retained := config.Endpoints[identity]; retained != "" && retained != ip.Unmap().String() {
			return nil, fmt.Errorf("SMB private endpoint changed without exact-owner retirement")
		}
		addresses[ip.Unmap()] = true
		entry := SMBCTDBMember{Identity: identity, PNN: rank, Node: ip.Unmap().String(), State: "new"}
		if identity == localID {
			view.Member = entry
		} else {
			view.Peers = append(view.Peers, entry)
		}
	}
	if view.Member.Identity == "" {
		return nil, fmt.Errorf("local SMB member has no reservation")
	}
	sort.Slice(view.Peers, func(i, j int) bool { return view.Peers[i].PNN < view.Peers[j].PNN })
	return view, nil
}

// GetSMBConfiguredService verifies the exact retained owner and an already
// successful deployment. It is read-only, including for nonclustered groups.
func GetSMBConfiguredService(ctx context.Context, tx *sql.Tx, clusterID, member string) (bool, error) {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return false, err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return false, err
	}
	if config.Owners[identity] != member {
		return false, fmt.Errorf("SMB service has no matching owner reservation")
	}
	var count int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM grouped_services WHERE service_group_id=? AND member_id=?", id, identity).Scan(&count)
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, sql.ErrNoRows
	}
	if count != 1 {
		return false, fmt.Errorf("duplicate SMB service rows")
	}
	return config.Clustered, nil
}

// UpdateSMBServiceInfo updates observation only; it cannot publish new placement.
func UpdateSMBServiceInfo(ctx context.Context, tx *sql.Tx, clusterID, member, info string) error {
	_, err := GetSMBConfiguredService(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE grouped_services SET info=? WHERE service_group_id=(SELECT id FROM service_groups WHERE service='smb' AND group_id=?) AND member_id=(SELECT id FROM core_cluster_members WHERE name=?)`, info, clusterID, member)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("SMB configured service changed during update")
	}
	return nil
}

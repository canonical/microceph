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
	"strings"
)

// SMBServiceGroupConfig records actual owner reservations, not desired or
// successful deployments. Offline removal must preserve uncertain ownership.
type SMBServiceGroupConfig struct {
	Owners map[string]string `json:"owners"` // member database identity -> member name
	// LocalSAMStarted is write-ahead uncertainty, not successful preparation.
	// Nil denotes unknown legacy history; only a new group starts with an empty map.
	LocalSAMStarted map[string]string `json:"local_sam_started"`
	// Clustered groups retain immutable ranks and private endpoint evidence.
	// Omit these fields for existing local-user groups and their rollback path.
	Clustered bool `json:"clustered,omitempty"`
	// NextRank advances monotonically; retirement never recycles old slots.
	NextRank uint64 `json:"next_rank,omitempty"`
	// Ranks retains each member database identity's original slot.
	Ranks map[string]uint64 `json:"ranks,omitempty"`
	// Endpoints retains canonical private addresses after owner retirement.
	Endpoints map[string]string `json:"endpoints,omitempty"`
}

func decodeSMBOwnership(raw string) (SMBServiceGroupConfig, error) {
	var config SMBServiceGroupConfig
	if !json.Valid([]byte(raw)) {
		return config, fmt.Errorf("invalid retained SMB ownership document")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&config)
	if err != nil || config.Owners == nil {
		return config, fmt.Errorf("invalid or unsupported retained SMB ownership")
	}
	if config.Clustered && (config.Ranks == nil || config.Endpoints == nil) {
		return config, fmt.Errorf("missing retained SMB clustered rank/address information")
	}
	if config.Clustered {
		err = validateSMBClusteredHistory(config)
		if err != nil {
			return config, err
		}
	}
	if !config.Clustered && (config.Ranks != nil || config.Endpoints != nil || config.NextRank != 0) {
		return config, fmt.Errorf("clustered history conflicts with local SMB mode")
	}
	for _, identities := range []map[string]string{config.Owners, config.LocalSAMStarted} {
		for identity, member := range identities {
			id, err := strconv.ParseInt(identity, 10, 64)
			if err != nil || id <= 0 || member == "" {
				return config, fmt.Errorf("invalid retained SMB owner identity")
			}
		}
	}
	return config, nil
}

func smbMemberIdentity(ctx context.Context, tx *sql.Tx, member string) (string, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM core_cluster_members WHERE name=?", member).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("resolve SMB member identity: %w", err)
	}
	return strconv.FormatInt(id, 10), nil
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
		config, err := decodeSMBOwnership(raw)
		if err != nil {
			return nil, err
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
	config, err := decodeSMBOwnership(raw)
	return id, config, err
}

func saveSMBGroup(ctx context.Context, tx *sql.Tx, id int64, config SMBServiceGroupConfig) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE service_groups SET config=? WHERE id=?", string(raw), id)
	return err
}

// ReserveSMBOwner atomically reserves a single local-user gateway in a short
// write transaction before starting smbd. Retain the reservation if cleanup
// cannot prove that the daemon has stopped.
func ReserveSMBOwner(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	_, err := reserveSMBOwner(ctx, tx, clusterID, member, false)
	return err
}

// ReserveSMBClusteredOwner reserves an actual owner with its stable CTDB rank.
// Call in a short write transaction; failed startup must retain uncertainty.
func ReserveSMBClusteredOwner(ctx context.Context, tx *sql.Tx, clusterID, member string) (uint64, error) {
	return reserveSMBOwner(ctx, tx, clusterID, member, true)
}

func reserveSMBOwner(ctx context.Context, tx *sql.Tx, clusterID, member string, clustered bool) (uint64, error) {
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
		config, err := decodeSMBOwnership(raw)
		if err != nil {
			rows.Close()
			return 0, err
		}
		for startedID, name := range config.LocalSAMStarted {
			if (name == member || startedID == identity) && (startedID != identity || name != member) {
				rows.Close()
				return 0, fmt.Errorf("SMB local SAM member identity does not match; retain uncertainty")
			}
		}
		for ownerID, name := range config.Owners {
			if clustered && config.Clustered {
				reserved := config.Endpoints[ownerID]
				if reserved == "" || (ownerID != identity && reserved == endpoint) || (ownerID == identity && reserved != endpoint) {
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
		config = SMBServiceGroupConfig{Owners: map[string]string{}, LocalSAMStarted: map[string]string{}, Clustered: clustered}
		if clustered {
			config.Ranks = map[string]uint64{}
			config.Endpoints = map[string]string{}
		}
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
	// Exact retries remain possible for retained legacy multi-owner groups;
	// neither failed nor offline reservations authorize a new local owner.
	if !clustered && config.Owners[identity] != member && len(config.Owners) > 0 {
		owners := make([]string, 0, len(config.Owners))
		for _, name := range config.Owners {
			owners = append(owners, name)
		}
		sort.Strings(owners)
		return 0, fmt.Errorf("Nonclustered SMB cluster %q already has owner %q; retire that owner after verified stop or create a new logical cluster", clusterID, owners[0])
	}
	rank, exists := config.Ranks[identity]
	if clustered {
		if !exists {
			if config.Owners[identity] == member {
				return 0, fmt.Errorf("retained SMB owner has lost its CTDB rank")
			}
			if config.NextRank > 2147483647 {
				return 0, fmt.Errorf("SMB CTDB rank space exhausted")
			}
			rank = config.NextRank
			config.NextRank++
			config.Ranks[identity] = rank
		}
		config.Endpoints[identity] = endpoint
	}
	config.Owners[identity] = member
	err = saveSMBGroup(ctx, tx, id, config)
	return rank, err
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

// RecordSMBService publishes the service row only after actual readiness.
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

// RetireSMBOwner removes the exact owner and success row after verified stop.
// It preserves the group and all filesystem data. Offline/forced removal must
// never call this function as proof of a stop.
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
		// Permit cleanup retry after SQL commit but before owner-file removal.
		return nil
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM grouped_services WHERE service_group_id=? AND member_id=?", id, identity)
	if err != nil {
		return err
	}
	delete(config.Owners, identity)
	return saveSMBGroup(ctx, tx, id, config)
}

// WithdrawSMBService removes only the success row after confirmed stop;
// actual ownership remains authoritative until explicit retirement.
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

// GetSMBPolicyWarning reports retained multi-owner placement without changing it.
func GetSMBPolicyWarning(ctx context.Context, tx *sql.Tx, clusterID string) (string, error) {
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return "", err
	}
	if config.Clustered || len(config.Owners) <= 1 {
		return "", nil
	}
	owners := make([]string, 0, len(config.Owners))
	for _, name := range config.Owners {
		owners = append(owners, name)
	}
	sort.Strings(owners)
	return fmt.Sprintf("Nonclustered SMB cluster %q is policy-noncompliant: retained owners %q; exact-owner retries remain allowed, new owners are blocked. Retire surplus owners after verified stop; no automatic shutdown or mode conversion.", clusterID, owners), nil
}

func smbLocalSAMIdentity(config SMBServiceGroupConfig, identity, member string) (bool, error) {
	if config.Clustered {
		return false, fmt.Errorf("clustered SMB reservation cannot authorize local SAM initialization")
	}
	if config.LocalSAMStarted == nil {
		return false, fmt.Errorf("SMB local SAM history is unknown; explicit recovery is required")
	}
	for id, name := range config.LocalSAMStarted {
		if (id == identity || name == member) && (id != identity || name != member) {
			return false, fmt.Errorf("SMB local SAM member identity does not match; retain uncertainty")
		}
	}
	return config.LocalSAMStarted[identity] == member, nil
}

// GetSMBLocalSAMStarted reads exact-member history independently of active ownership.
func GetSMBLocalSAMStarted(ctx context.Context, tx *sql.Tx, clusterID, member string) (bool, error) {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return false, err
	}
	_, config, err := smbGroup(ctx, tx, clusterID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return smbLocalSAMIdentity(config, identity, member)
}

// StartSMBLocalSAM records write-ahead uncertainty before any mutating SAM binding.
// It survives nonpurge retirement and offline member removal.
func StartSMBLocalSAM(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	_, err = smbLocalSAMIdentity(config, identity, member)
	if err != nil {
		return err
	}
	if config.Owners[identity] != member {
		return fmt.Errorf("SMB local SAM initialization lacks exact owner reservation")
	}
	config.LocalSAMStarted[identity] = member
	return saveSMBGroup(ctx, tx, id, config)
}

// ClearSMBLocalSAMStarted clears only exact-member history after confirmed stop
// and successful explicit local-state destruction. Never use it for retirement.
func ClearSMBLocalSAMStarted(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	_, err = smbLocalSAMIdentity(config, identity, member)
	if err != nil {
		return err
	}
	if config.Owners[identity] != "" {
		return fmt.Errorf("SMB local SAM owner must be retired before clearing history")
	}
	delete(config.LocalSAMStarted, identity)
	return saveSMBGroup(ctx, tx, id, config)
}

// GetSMBConfiguredService requires an exact owner and an existing success row.
// Observation cannot reserve ownership or complete a failed initial placement.
func GetSMBConfiguredService(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
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
	var count int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM grouped_services WHERE service_group_id=? AND member_id=?", id, identity).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	if count != 1 {
		return fmt.Errorf("duplicate SMB service rows")
	}
	return nil
}

// UpdateSMBServiceInfo updates observation only; it cannot publish new placement.
func UpdateSMBServiceInfo(ctx context.Context, tx *sql.Tx, clusterID, member, info string) error {
	err := GetSMBConfiguredService(ctx, tx, clusterID, member)
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

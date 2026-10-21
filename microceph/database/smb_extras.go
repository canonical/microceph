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
	Clustered    bool              `json:"clustered"`
	AuthIdentity string            `json:"auth_identity,omitempty"`
	AD           *SMBADState       `json:"ad,omitempty"`
	NextRank     uint64            `json:"next_rank"`
	Ranks        map[string]uint64 `json:"ranks"`
	Owners       map[string]string `json:"owners"` // member database identity -> member name
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

// GetSMBRecoveryGroups returns clustered reservations owned by this exact local
// member, including pending/failed startup but excluding retired and foreign owners.
func GetSMBRecoveryGroups(ctx context.Context, tx *sql.Tx, member string) ([]string, error) {
	groups, err := GetSMBOwnerGroups(ctx, tx, member)
	if err != nil {
		return nil, err
	}
	clustered := []string{}
	for _, group := range groups {
		_, config, err := smbGroup(ctx, tx, group)
		if err != nil {
			return nil, err
		}
		if config.Clustered {
			clustered = append(clustered, group)
		}
	}
	return clustered, nil
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
	return reserveSMBOwner(ctx, tx, clusterID, member, clustered, "")
}

// ReserveSMBADOwner retains an immutable logical member identity, not credentials.
func ReserveSMBADOwner(ctx context.Context, tx *sql.Tx, clusterID, member, identity string) (uint64, error) {
	if identity == "" || identity == "user" {
		return 0, fmt.Errorf("invalid AD identity")
	}
	return reserveSMBOwner(ctx, tx, clusterID, member, true, identity)
}

func reserveSMBOwner(ctx context.Context, tx *sql.Tx, clusterID, member string, clustered bool, authIdentity string) (uint64, error) {
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
		config = SMBServiceGroupConfig{Clustered: clustered, AuthIdentity: authIdentity, Ranks: map[string]uint64{}, Owners: map[string]string{}}
		if authIdentity != "" {
			config.AD = &SMBADState{}
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
	retainedIdentity := config.AuthIdentity
	// Empty identity denotes legacy/current user-mode, never an empty AD identity.
	if retainedIdentity != authIdentity {
		return 0, fmt.Errorf("SMB authentication identity changed; new logical ID and manual maintenance required")
	}
	if authIdentity != "" && config.AD == nil {
		return 0, fmt.Errorf("AD attempt state lost; retain identity and use manual maintenance")
	}
	if authIdentity == "" && config.AD != nil {
		return 0, fmt.Errorf("retained AD identity conflicts with user mode")
	}
	if config.Clustered != clustered {
		return 0, fmt.Errorf("SMB mode conflicts with retained rank/identity state; maintenance required")
	}
	// Only an exact retained owner may retry a legacy multi-owner group.
	// Failed/offline reservations are just as authoritative as healthy owners.
	if !clustered && config.Owners[identity] != member && len(config.Owners) > 0 {
		owners := make([]string, 0, len(config.Owners))
		for _, name := range config.Owners {
			owners = append(owners, name)
		}
		sort.Strings(owners)
		return 0, fmt.Errorf("Nonclustered SMB cluster %q already has owner %q; retire that owner after verified stop or create a new logical cluster in clustered mode", clusterID, owners[0])
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

// GetSMBPolicyWarning reports legacy placement without changing retained owners.
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

// SMBADState is the narrow initial-join record, never an expiring placement lease.
// A nonempty JoinOwner without Joined is an uncertain external effect. It is
// deliberately not cleared by stop, retirement, time, credential refresh or retry.
// Samba owns all passwords and mappings; no secrets are kept in this record.
type SMBADState struct {
	JoinOwner   string `json:"join_owner,omitempty"`
	Joined      bool   `json:"joined"`
	Initialized bool   `json:"initialized"`
}

func smbADOwner(ctx context.Context, tx *sql.Tx, clusterID, member string) (int64, SMBServiceGroupConfig, string, error) {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return 0, SMBServiceGroupConfig{}, "", err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return 0, config, "", err
	}
	if !config.Clustered || config.AD == nil || config.Owners[identity] != member {
		return 0, config, "", fmt.Errorf("AD requires an exact clustered owner")
	}
	return id, config, identity, nil
}

// GetSMBADState observes join history for an exact retained owner.
func GetSMBADState(ctx context.Context, tx *sql.Tx, clusterID, member string) (SMBADState, error) {
	_, config, _, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return SMBADState{}, err
	}
	return *config.AD, nil
}

// BeginSMBADJoin consumes the sole initial attempt in a short write transaction.
// Commit before execution. Even the same actor cannot automatically retry.
func BeginSMBADJoin(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	id, config, identity, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if config.AD.JoinOwner != "" || config.AD.Joined {
		return fmt.Errorf("AD join already attempted or uncertain; manual maintenance required, never retry on timeout")
	}
	config.AD.JoinOwner = identity
	return saveSMBGroup(ctx, tx, id, config)
}

// CompleteSMBADJoin records only the initiating actor's confirmed success.
func CompleteSMBADJoin(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	id, config, identity, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if config.AD.JoinOwner != identity || config.AD.Joined {
		return fmt.Errorf("AD join actor does not match pending attempt")
	}
	config.AD.Joined = true
	return saveSMBGroup(ctx, tx, id, config)
}

// InitializeSMBAD records that trust, autorid and Winbind/NSS were checked before serving.
func InitializeSMBAD(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	id, config, identity, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if !config.AD.Joined || (!config.AD.Initialized && config.AD.JoinOwner != identity) {
		return fmt.Errorf("AD initial state is not confirmed")
	}
	config.AD.Initialized = true
	return saveSMBGroup(ctx, tx, id, config)
}

// CheckSMBADPurge refuses deletion of the final retained AD identity replica.
// Caller must check in the same transaction as retirement. Local AD persistent
// databases are retained even on peer purge; this is not a backup guarantee.
func CheckSMBADPurge(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	if config.AD != nil && (len(config.Owners) <= 1 || !config.AD.Initialized) {
		return fmt.Errorf("refusing purge of final or uncertain AD identity state; manual maintenance required")
	}
	return nil
}

// HasSMBADIdentity identifies retained AD state, including after owner retirement.
func HasSMBADIdentity(ctx context.Context, tx *sql.Tx, clusterID string) (bool, error) {
	_, config, err := smbGroup(ctx, tx, clusterID)
	return config.AD != nil || config.AuthIdentity != "", err
}

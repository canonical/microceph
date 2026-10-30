package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
)

// SMBCTDBMember identifies an actual owner by its immutable SQL member identity.
type SMBCTDBMember struct {
	// Identity is the core member database identity, not its reusable name.
	Identity string `json:"identity"`
	// PNN is the retained CTDB slot number.
	PNN uint64 `json:"pnn"`
	// Node is the canonical private member IP.
	Node string `json:"node"`
	// State is the native projection's membership state.
	State string `json:"state"`
}

// SMBCTDBView describes retained actual owners, including retired slot holes.
type SMBCTDBView struct {
	// SlotCount includes all historically allocated ranks.
	SlotCount uint64 `json:"slot_count"`
	// Member identifies the exact local reservation.
	Member SMBCTDBMember `json:"member"`
	// Peers contains actual other owners ordered by rank.
	Peers []SMBCTDBMember `json:"peers"`
}

func validateSMBClusteredHistory(config SMBServiceGroupConfig) error {
	if config.NextRank == 0 {
		return fmt.Errorf("retained SMB clustered history is empty; explicit recovery required")
	}
	if len(config.LocalSAMStarted) > 0 {
		return fmt.Errorf("local SAM history conflicts with clustered SMB identity")
	}
	seen := map[uint64]bool{}
	for identity, rank := range config.Ranks {
		id, err := strconv.ParseInt(identity, 10, 64)
		if err != nil || id <= 0 || rank >= config.NextRank || rank > 2147483647 || seen[rank] {
			return fmt.Errorf("invalid retained SMB rank history")
		}
		seen[rank] = true
		address := config.Endpoints[identity]
		ip, err := netip.ParseAddr(address)
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.Unmap().String() != address {
			return fmt.Errorf("invalid retained SMB private endpoint")
		}
	}
	for identity := range config.Endpoints {
		_, exists := config.Ranks[identity]
		if !exists {
			return fmt.Errorf("SMB endpoint has lost its retained rank history")
		}
	}
	for identity := range config.Owners {
		_, exists := config.Ranks[identity]
		if !exists {
			return fmt.Errorf("SMB owner has lost its retained rank history")
		}
	}
	return nil
}

// IsSMBClusteredGroup reads retained mode without reserving or reconstructing state.
func IsSMBClusteredGroup(ctx context.Context, tx *sql.Tx, clusterID string) (bool, error) {
	_, config, err := smbGroup(ctx, tx, clusterID)
	return config.Clustered, err
}

// HasSMBClusteredHistory identifies this exact member's retained rank after retirement.
func HasSMBClusteredHistory(ctx context.Context, tx *sql.Tx, clusterID, member string) (bool, error) {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return false, err
	}
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return false, err
	}
	_, exists := config.Ranks[identity]
	return config.Clustered && exists, nil
}

// GetSMBCTDBView reads the authoritative reserved membership, not desired placement.
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
	view := &SMBCTDBView{SlotCount: config.NextRank, Peers: []SMBCTDBMember{}}
	ranks := map[uint64]bool{}
	addresses := map[string]bool{}
	for identity, owner := range config.Owners {
		rank, ok := config.Ranks[identity]
		if !ok || rank >= config.NextRank || rank > 65535 || ranks[rank] {
			return nil, fmt.Errorf("invalid retained SMB CTDB rank")
		}
		ranks[rank] = true
		var name string
		err = tx.QueryRowContext(ctx, "SELECT name FROM core_cluster_members WHERE id=?", identity).Scan(&name)
		if err != nil || name != owner {
			return nil, fmt.Errorf("SMB owner %q has uncertain member identity; retire or fence first", owner)
		}
		address, err := smbMemberIP(ctx, tx, identity)
		if err != nil {
			return nil, err
		}
		if addresses[address] || config.Endpoints[identity] != address {
			return nil, fmt.Errorf("SMB private endpoint is missing, duplicated or changed without exact-owner retirement")
		}
		addresses[address] = true
		entry := SMBCTDBMember{Identity: identity, PNN: rank, Node: address, State: "new"}
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

// GetSMBRecoveryGroups returns clustered groups actually owned by this exact member.
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

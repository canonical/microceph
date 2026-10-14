package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
}

func smbMemberIdentity(ctx context.Context, tx *sql.Tx, member string) (string, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM core_cluster_members WHERE name=?", member).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("resolve SMB member identity: %w", err)
	}
	return strconv.FormatInt(id, 10), nil
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

// ReserveSMBOwner atomically reserves a stable CTDB rank or a singleton owner.
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
		for ownerID, name := range config.Owners {
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
	if !clustered {
		for ownerID := range config.Owners {
			if ownerID != identity {
				return 0, fmt.Errorf("nonclustered SMB cluster %q already has a native owner", clusterID)
			}
		}
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
	if config.Owners[identity] != member {
		return fmt.Errorf("SMB owner identity does not match; retain uncertainty")
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

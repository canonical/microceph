package database

import (
	"context"
	"database/sql"
	"fmt"
)

// RgwFrontend holds a member's last successfully applied public frontend settings.
type RgwFrontend struct {
	Member  string
	Port    int
	SSLPort int
	SSL     bool
}

// UpsertRGWFrontend records frontend settings alongside the member's service row.
func UpsertRGWFrontend(ctx context.Context, tx *sql.Tx, member string, port, sslPort int, ssl bool) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO rgw_frontends (member_id, port, ssl_port, ssl)
VALUES ((SELECT id FROM core_cluster_members WHERE name = ?), ?, ?, ?)
ON CONFLICT(member_id) DO UPDATE SET
  port = excluded.port, ssl_port = excluded.ssl_port, ssl = excluded.ssl
WHERE port != excluded.port OR ssl_port != excluded.ssl_port OR ssl != excluded.ssl`, member, port, sslPort, ssl)
	if err != nil {
		return fmt.Errorf("failed to upsert rgw frontend for %s: %w", member, err)
	}
	return nil
}

// GetRGWFrontends returns the recorded frontend settings for cluster members.
func GetRGWFrontends(ctx context.Context, tx *sql.Tx) ([]RgwFrontend, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT m.name, f.port, f.ssl_port, f.ssl
  FROM rgw_frontends f
  JOIN core_cluster_members m ON m.id = f.member_id
 ORDER BY m.name`)
	if err != nil {
		return nil, fmt.Errorf("failed to query rgw frontends: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var frontends []RgwFrontend
	for rows.Next() {
		var f RgwFrontend
		err := rows.Scan(&f.Member, &f.Port, &f.SSLPort, &f.SSL)
		if err != nil {
			return nil, fmt.Errorf("failed to scan rgw frontend: %w", err)
		}
		frontends = append(frontends, f)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("rgw frontend rows error: %w", err)
	}
	return frontends, nil
}

// DeleteRGWFrontendByMember removes a member's frontend record if present.
func DeleteRGWFrontendByMember(ctx context.Context, tx *sql.Tx, member string) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM rgw_frontends WHERE member_id = (SELECT id FROM core_cluster_members WHERE name = ?)`, member)
	if err != nil {
		return fmt.Errorf("failed to delete rgw frontend for %s: %w", member, err)
	}
	return nil
}

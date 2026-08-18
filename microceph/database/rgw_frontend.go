package database

import (
	"context"
	"database/sql"
	"fmt"
)

// RgwFrontend is the observed RGW beast frontend configuration recorded for a
// cluster member (CE142 placement-rgw). It stores ports and a TLS on/off flag
// only — never cert/key bytes, which remain on disk in server.crt/server.key.
// Member is the cluster member name (resolved to/from member_id at the DB
// layer), matching PlacementObservedMember.Member.
type RgwFrontend struct {
	Member  string
	Port    int
	SSLPort int
	SSL     bool
}

// UpsertRGWFrontend records (or replaces) the RGW frontend configuration for
// the named member. It is called from the member-side service DbUpdate in the
// same transaction that records the services row, so the services record and
// the observed frontend stay consistent. The member name is resolved to
// member_id via a subquery, mirroring the generated client_config mapper. A
// re-apply with new port/TLS overwrites the prior row (INSERT OR REPLACE on the
// UNIQUE(member_id) constraint), making rotation and port changes idempotent.
func UpsertRGWFrontend(ctx context.Context, tx *sql.Tx, member string, port, sslPort int, ssl bool) error {
	sslInt := 0
	if ssl {
		sslInt = 1
	}
	_, err := tx.ExecContext(ctx, `
INSERT OR REPLACE INTO rgw_frontends (member_id, port, ssl_port, ssl)
VALUES ((SELECT id FROM core_cluster_members WHERE name = ?), ?, ?, ?)`, member, port, sslPort, sslInt)
	if err != nil {
		return fmt.Errorf("failed to upsert rgw frontend for %s: %w", member, err)
	}
	return nil
}

// GetRGWFrontends returns the recorded RGW frontend configuration for every
// member that has one, keyed by member name. Used by GetPlacementStatus to
// populate PlacementObservedMember.RgwFrontend in the same DB transaction as
// the rest of the observed state.
func GetRGWFrontends(ctx context.Context, tx *sql.Tx) ([]RgwFrontend, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT m.name, f.port, f.ssl_port, f.ssl
  FROM rgw_frontends f
  JOIN core_cluster_members m ON m.id = f.member_id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query rgw frontends: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var frontends []RgwFrontend
	for rows.Next() {
		var f RgwFrontend
		var sslInt int
		err := rows.Scan(&f.Member, &f.Port, &f.SSLPort, &sslInt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan rgw frontend: %w", err)
		}
		f.SSL = sslInt != 0
		frontends = append(frontends, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rgw frontend rows error: %w", err)
	}
	return frontends, nil
}

// DeleteRGWFrontendByMember removes the RGW frontend record for the named
// member. A missing row is not an error, so scale-down and retried applies are
// idempotent. Called from DisableRGW alongside removeServiceDatabase.
func DeleteRGWFrontendByMember(ctx context.Context, tx *sql.Tx, member string) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM rgw_frontends WHERE member_id = (SELECT id FROM core_cluster_members WHERE name = ?)`, member)
	if err != nil {
		return fmt.Errorf("failed to delete rgw frontend for %s: %w", member, err)
	}
	return nil
}

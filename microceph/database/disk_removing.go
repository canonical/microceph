package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MarkDiskRemoving flags an OSD's disk row as being removed, recording the
// marking time. The row is kept (the removal's cleanup and retry steps need
// it) but the failure domain scaling counts (MembersDiskCnt and its exclude
// variant) stop counting it, so a concurrent disk add on another member
// cannot count a drained OSD as a remaining host and switch pools back to a
// wider failure domain. The timestamp lets the daemon distinguish flags left
// by a crash mid-removal from flags of removals still in flight.
func MarkDiskRemoving(ctx context.Context, tx *sql.Tx, osd int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE disks SET removing_since = ? WHERE id = ?`, time.Now().UnixNano(), osd)
	if err != nil {
		return fmt.Errorf("failed to mark osd.%d as removing: %w", osd, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("failed to mark osd.%d as removing: no matching disk row", osd)
	}
	return nil
}

// ClearDiskRemoving removes the removing flag from an OSD's disk row, making
// it eligible for the failure domain scaling counts again. It is the rollback
// of MarkDiskRemoving after a removal that failed before the OSD was purged.
// A missing row is not an error: the row may already be deleted.
func ClearDiskRemoving(ctx context.Context, tx *sql.Tx, osd int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE disks SET removing_since = 0 WHERE id = ?`, osd)
	if err != nil {
		return fmt.Errorf("failed to clear removing flag of osd.%d: %w", osd, err)
	}
	return nil
}

// ListRemovingDisks returns the IDs of the given member's OSDs whose disk
// rows were flagged as removing before the given time (unix nanoseconds).
// Pass the daemon's start time to list only flags left by an interrupted
// removal and never flags of removals in flight.
func ListRemovingDisks(ctx context.Context, tx *sql.Tx, member string, markedBefore int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT disks.id
FROM disks
JOIN core_cluster_members ON disks.member_id = core_cluster_members.id
WHERE core_cluster_members.name = ? AND disks.removing_since != 0 AND disks.removing_since < ?`, member, markedBefore)
	if err != nil {
		return nil, fmt.Errorf("failed to list removing disks of member %s: %w", member, err)
	}
	defer rows.Close()

	osds := []int64{}
	for rows.Next() {
		var osd int64
		err = rows.Scan(&osd)
		if err != nil {
			return nil, fmt.Errorf("failed to scan removing disk: %w", err)
		}
		osds = append(osds, osd)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("failed to list removing disks of member %s: %w", member, err)
	}
	return osds, nil
}

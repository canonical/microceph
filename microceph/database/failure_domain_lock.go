package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/logger"
)

// TryAcquireFailureDomainLock atomically acquires the cluster-wide failure
// domain transition lock via a conditional UPDATE on the failure_domain_lock
// singleton row. token is the acquirer's unique identifier, its acquisition
// time in Unix nanoseconds, so the token doubles as a lease timestamp.
// staleBefore is the oldest acquisition time still considered live: a held
// lock whose token is older is treated as abandoned by a crashed daemon and
// is reclaimed. Returns true when the lock was acquired (including by stale
// reclaim), false when a live holder exists.
//
// The conditional UPDATE executes atomically in dqlite, so two members racing
// to acquire cannot both succeed. Clock skew between members shortens or
// extends the lease by the skew; the lease is sized in minutes so ordinary
// NTP-level skew is harmless.
func TryAcquireFailureDomainLock(ctx context.Context, tx *sql.Tx, token int64, staleBefore int64) (bool, error) {
	// Read the existing token BEFORE the conditional UPDATE so a stale-reclaim
	// can be distinguished from a clean acquire in logs. The read and the
	// conditional UPDATE run in the same transaction, so they observe a
	// consistent snapshot.
	var prevToken int64
	err := tx.QueryRowContext(ctx, `SELECT lock_token FROM failure_domain_lock WHERE id = 1`).Scan(&prevToken)
	if err != nil {
		return false, fmt.Errorf("failed to read failure domain lock token: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
UPDATE failure_domain_lock
   SET lock_token = ?
 WHERE id = 1 AND (lock_token = 0 OR lock_token < ?)`, token, staleBefore)
	if err != nil {
		return false, fmt.Errorf("failed to acquire failure domain lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 1 {
		if prevToken != 0 {
			// Stale reclaim: a prior holder's lease expired (its daemon likely
			// crashed mid-transition). Warn so operators can correlate a
			// clobbered transition with the reclaim event.
			logger.Warnf("failure domain lock reclaimed from abandoned holder (held %ds); a prior transition likely crashed mid-operation", (token-prevToken)/int64(1e9))
		}
		logger.Debugf("failure domain lock acquired (token=%d, reclaimed=%v)", token, prevToken != 0)
		return true, nil
	}
	return false, nil
}

// ReleaseFailureDomainLock releases the failure domain transition lock, but
// only when it is still held by token: if the holder's lease expired
// mid-transition and another writer reclaimed the lock, the reclaimer's lock
// must not be clobbered. Returns true when the lock was released, false when
// it was no longer held by token (already reclaimed or released).
func ReleaseFailureDomainLock(ctx context.Context, tx *sql.Tx, token int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE failure_domain_lock SET lock_token = 0 WHERE id = 1 AND lock_token = ?`, token)
	if err != nil {
		return false, fmt.Errorf("failed to release failure domain lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	return rows == 1, nil
}

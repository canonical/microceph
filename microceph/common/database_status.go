package common

import (
	"net/http"
	"strings"

	lxdApi "github.com/canonical/lxd/shared/api"
	microTypes "github.com/canonical/microcluster/v2/rest/types"
)

// IsDatabaseUpgradeWaitingError returns true when an error represents the
// "database is waiting for an upgrade" state emitted by microcluster.
//
// We intentionally combine:
//   - status-code matching (503) when we have a typed StatusError, and
//   - canonical status text matching for compatibility with callers that only
//     propagate plain error strings.
func IsDatabaseUpgradeWaitingError(err error) bool {
	if err == nil {
		return false
	}

	waitingStatus := strings.ToLower(string(microTypes.DatabaseWaiting))
	hasWaitingText := strings.Contains(strings.ToLower(err.Error()), waitingStatus)
	if !hasWaitingText {
		return false
	}

	// Prefer typed status checks when available.
	if statusCode, isStatusErr := lxdApi.StatusErrorMatch(err); isStatusErr {
		return statusCode == http.StatusServiceUnavailable
	}

	// Compatibility fallback for non-status errors propagated as plain strings.
	return true
}

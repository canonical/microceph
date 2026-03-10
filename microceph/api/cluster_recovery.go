package api

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
	"github.com/gorilla/mux"
)

var clusterForceRemoveCmd = rest.Endpoint{
	Path: "cluster/members/{name}/force",
	// Must be reachable while DB is waiting for upgrade. Handler-level checks
	// narrow usage to that state only.
	AllowedBeforeInit: true,
	Delete:            rest.EndpointAction{Handler: cmdClusterForceRemoveDelete, ProxyTarget: false},
}

var clusterRemotesSyncCmd = rest.Endpoint{
	Path:              "cluster/remotes/sync",
	AllowedBeforeInit: true,
	Post:              rest.EndpointAction{Handler: cmdClusterRemotesSyncPost, ProxyTarget: false},
}

// forceRemoveRecoveryMode captures whether the recovery-only endpoint may run
// under the current database state.
type forceRemoveRecoveryMode int

const (
	forceRemoveRecoveryModeAllowed forceRemoveRecoveryMode = iota
	forceRemoveRecoveryModeUseStandardPath
	forceRemoveRecoveryModeUnavailable
)

// getForceRemoveRecoveryMode interprets Database().IsOpen() result for the
// recovery endpoint:
//   - nil => DB is healthy/open, callers should use normal cluster remove flow
//   - waiting-for-upgrade => recovery endpoint is allowed
//   - anything else => recovery endpoint is unavailable
func getForceRemoveRecoveryMode(dbOpenErr error) (forceRemoveRecoveryMode, error) {
	if dbOpenErr == nil {
		return forceRemoveRecoveryModeUseStandardPath, fmt.Errorf("recovery force removal is only available while the database is waiting for an upgrade")
	}

	if common.IsDatabaseUpgradeWaitingError(dbOpenErr) {
		return forceRemoveRecoveryModeAllowed, nil
	}

	return forceRemoveRecoveryModeUnavailable, dbOpenErr
}

func cmdClusterForceRemoveDelete(s state.State, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.BadRequest(err)
	}

	mode, modeErr := getForceRemoveRecoveryMode(s.Database().IsOpen(r.Context()))
	switch mode {
	// Recovery path is intentionally restricted so we don't bypass standard
	// remove orchestration in healthy database conditions.
	case forceRemoveRecoveryModeUseStandardPath:
		return response.BadRequest(fmt.Errorf("%w; use 'microceph cluster remove %s --force'", modeErr, name))
	case forceRemoveRecoveryModeUnavailable:
		return response.SmartError(modeErr)
	}

	err = ceph.ForceRemoveClusterMember(r.Context(), interfaces.CephState{State: s}, name)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func cmdClusterRemotesSyncPost(s state.State, r *http.Request) response.Response {
	err := ceph.SyncTrustStoreFromDatabase(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		return response.SmartError(err)
	}

	err = ceph.ReconcileMonHostEntries(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		return response.SmartError(err)
	}

	err = ceph.UpdateConfig(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		logger.Warnf("cluster remotes sync: failed to regenerate local ceph config: %v", err)
	}

	return response.EmptySyncResponse
}

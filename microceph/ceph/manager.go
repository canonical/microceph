package ceph

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/tidwall/gjson"
)

// Manager Daemon Ops
func bootstrapMgr(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("mgr.%s", hostname),
		"mon", "allow profile mgr",
		"osd", "allow *",
		"mds", "allow *",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	return nil
}

func getActiveMgrs() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "mgr", "dump", "-f", "json")
	if err != nil {
		logger.Errorf("Failed fetching Mgr dump: %v", err)
		return nil, err
	}

	logger.Debugf("Mgr Dump:\n%s", output)

	// Get the active mgr services.
	activeMgrs := []string{}
	result := gjson.Get(output, "standbys.#.name")
	for _, name := range result.Array() {
		activeMgrs = append(activeMgrs, name.String())
	}
	activeMgrs = append(activeMgrs, gjson.Get(output, "active_name").String())

	return activeMgrs, nil
}

// ensureMgrAbsentFunc commits MGR map removal after the local daemon is
// stopped. It is injectable so DeleteService ordering can be unit-tested
// without touching Ceph.
var ensureMgrAbsentFunc = ensureMgrAbsent

// evictMgrFunc actively removes a MGR daemon from the mgrmap. Injectable for
// testing.
var evictMgrFunc = evictMgr

// evictMgr runs `ceph mgr fail <hostname>`, which removes a standby MGR from
// the mgrmap (or fails the active MGR so a standby takes over) instead of
// waiting out the mon_mgr_beacon_grace aging window.
func evictMgr(ctx context.Context, hostname string) error {
	_, err := cephRunContext(ctx, "mgr", "fail", hostname)
	if err != nil {
		logger.Errorf("failed to fail mgr %q: %v", hostname, err)
		return fmt.Errorf("failed to fail mgr %q: %w", hostname, err)
	}
	return nil
}

// ensureMgrAbsent makes MGR teardown synchronous: it actively evicts the MGR
// from the mgrmap and verifies the map converged before returning, so a
// reconcile does not report success while a stopped standby still lingers in
// `ceph mgr metadata` during the beacon-aging window.
//
// The operation is idempotent. An already-absent MGR returns immediately, so
// a retry after a later teardown phase failed resumes local cleanup rather
// than erroring. `ceph mgr fail` on an already-gone daemon may itself error;
// that is tolerated as long as verification confirms absence (possibly via
// beacon aging), mirroring the ambiguous-outcome handling in ensureMonAbsent.
func ensureMgrAbsent(ctx context.Context, hostname string) error {
	present, err := mgrActiveOrStandby(ctx, hostname)
	if err != nil {
		return fmt.Errorf("failed to read mgr map: %w", err)
	}
	if !present {
		return nil
	}

	evictErr := evictMgrFunc(ctx, hostname)
	absent, verifyErr := verifyControlDaemonAbsent(ctx, hostname, mgrActiveOrStandby)
	if verifyErr != nil {
		if evictErr != nil {
			return fmt.Errorf("%w; failed to verify mgr removal: %v", evictErr, verifyErr)
		}
		return fmt.Errorf("mgr %q removal could not be verified: %w", hostname, verifyErr)
	}
	if absent {
		// Absent now: either the eviction committed, or a stopped standby aged
		// out within the verification window. An ambiguous eviction error is
		// therefore benign.
		return nil
	}
	if evictErr != nil {
		return evictErr
	}
	return fmt.Errorf("mgr %q remains in the mgr map after removal", hostname)
}

// Mgr Module Ops

// EnableMgrModule enabled a mgr module on specified ceph cluster and verifies if is comes up
func EnableMgrModule(ctx context.Context, module string, remote string, local string) error {
	args := []string{"mgr", "module", "enable", module}

	cmd := appendRemoteClusterArgs(args, remote, local)

	_, err := cephRun(cmd...)
	if err != nil {
		logger.Errorf("Failed to enable remote cluster (%s) mgr module %s: %v", remote, module, err)
		return err
	}

	return nil
}

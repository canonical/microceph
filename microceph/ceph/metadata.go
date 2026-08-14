package ceph

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/tidwall/gjson"
)

// ensureMdsAbsentFunc commits MDS map removal after the local daemon is
// stopped. Injectable so DeleteService ordering can be unit-tested without
// touching Ceph.
var ensureMdsAbsentFunc = ensureMdsAbsent

// evictMdsFunc actively removes an MDS daemon from the fsmap. Injectable for
// testing.
var evictMdsFunc = evictMds

// evictMds runs `ceph mds fail <hostname>`, which removes a standby MDS from
// the fsmap (or fails the active MDS so a standby takes over) instead of
// waiting out the beacon-aging window.
func evictMds(ctx context.Context, hostname string) error {
	_, err := cephRunContext(ctx, "mds", "fail", hostname)
	if err != nil {
		logger.Errorf("failed to fail mds %q: %v", hostname, err)
		return fmt.Errorf("failed to fail mds %q: %w", hostname, err)
	}
	return nil
}

// ensureMdsAbsent makes MDS teardown synchronous: it actively evicts the MDS
// from the fsmap and verifies the map converged before returning, so a
// reconcile does not report success while a stopped standby still lingers in
// `ceph mds stat` during the beacon-aging window.
//
// The operation is idempotent. An already-absent MDS returns immediately, so
// a retry after a later teardown phase failed resumes local cleanup rather
// than erroring. `ceph mds fail` on an already-gone daemon may itself error;
// that is tolerated as long as verification confirms absence (possibly via
// beacon aging), mirroring the ambiguous-outcome handling in ensureMonAbsent.
func ensureMdsAbsent(ctx context.Context, hostname string) error {
	present, err := mdsUp(ctx, hostname)
	if err != nil {
		return fmt.Errorf("failed to read mds map: %w", err)
	}
	if !present {
		return nil
	}

	evictErr := evictMdsFunc(ctx, hostname)
	absent, verifyErr := verifyControlDaemonAbsent(ctx, hostname, mdsUp)
	if verifyErr != nil {
		if evictErr != nil {
			return fmt.Errorf("%w; failed to verify mds removal: %v", evictErr, verifyErr)
		}
		return fmt.Errorf("mds %q removal could not be verified: %w", hostname, verifyErr)
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
	return fmt.Errorf("mds %q remains in the mds map after removal", hostname)
}

func bootstrapMds(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("mds.%s", hostname),
		"mon", "allow profile mds",
		"mgr", "allow profile mds",
		"mds", "allow *",
		"osd", "allow *",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	return nil
}

func getActiveMdss() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "fs", "status", "-f", "json")
	if err != nil {
		logger.Errorf("Failed fetching fs status: %v", err)
		return nil, err
	}

	logger.Debugf("Fs Status:\n%s", output)

	// Get the active mds services.
	activeMdss := []string{}
	result := gjson.Get(output, "mdsmap.#.name")
	for _, name := range result.Array() {
		activeMdss = append(activeMdss, name.String())
	}

	return activeMdss, nil
}

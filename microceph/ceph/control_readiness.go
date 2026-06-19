package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
)

// controlServiceReadyFunc checks whether a control service on a given member
// is viable in Ceph — not merely placed (DB row exists) or snap-locally-active.
// For MON: the member must be in the quorum. For MGR: active or standby. For
// MDS: up (active or standby). Returns (false, nil) if the service is not yet
// viable; (false, err) if the check itself failed (caller treats as not-ready).
//
// It is injectable for testing.
var controlServiceReadyFunc = controlServiceReady

// controlServiceReady is the real implementation.
func controlServiceReady(ctx context.Context, service string, member string) (bool, error) {
	switch service {
	case "mon":
		return monInQuorum(ctx, member)
	case "mgr":
		return mgrActiveOrStandby(ctx, member)
	case "mds":
		return mdsUp(ctx, member)
	default:
		return false, fmt.Errorf("unknown control service: %s", service)
	}
}

// monInQuorum checks whether a MON daemon on member is in the Ceph quorum.
func monInQuorum(ctx context.Context, member string) (bool, error) {
	output, err := cephRunContext(ctx, "mon", "stat", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to run 'ceph mon stat': %w", err)
	}
	var stat struct {
		QuorumNames []string `json:"quorum_names"`
	}
	if err := json.Unmarshal([]byte(output), &stat); err != nil {
		return false, fmt.Errorf("failed to parse 'ceph mon stat' output: %w", err)
	}
	return containsString(stat.QuorumNames, member), nil
}

// mgrActiveOrStandby checks whether a MGR daemon on member is active or standby.
func mgrActiveOrStandby(ctx context.Context, member string) (bool, error) {
	output, err := cephRunContext(ctx, "mgr", "stat", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to run 'ceph mgr stat': %w", err)
	}
	var stat struct {
		ActiveName string `json:"active_name"`
		Standbys   []struct {
			Name string `json:"name"`
		} `json:"standbys"`
	}
	if err := json.Unmarshal([]byte(output), &stat); err != nil {
		return false, fmt.Errorf("failed to parse 'ceph mgr stat' output: %w", err)
	}
	if stat.ActiveName == member {
		return true, nil
	}
	for _, s := range stat.Standbys {
		if s.Name == member {
			return true, nil
		}
	}
	return false, nil
}

// mdsUp checks whether an MDS daemon on member is up (active or standby). Uses
// plain-text 'ceph mds stat' because the JSON schema varies across Ceph versions.
// The output contains entries like "node-a=up:active" or "node-b=up:standby".
func mdsUp(ctx context.Context, member string) (bool, error) {
	output, err := cephRunContext(ctx, "mds", "stat")
	if err != nil {
		return false, fmt.Errorf("failed to run 'ceph mds stat': %w", err)
	}
	// If no filesystems exist, MDS daemons have nothing to serve; treat as
	// not viable.
	if strings.Contains(output, "no filesystems") || strings.TrimSpace(output) == "" {
		return false, nil
	}
	return strings.Contains(output, member+"=up:"), nil
}

// waitForControlServiceReady polls Ceph readiness for a single control service
// on a member until it is viable or the deadline is reached. Returns true if the
// service became viable, false otherwise (including check errors, which are
// treated conservatively as not-ready).
func waitForControlServiceReady(ctx context.Context, service, member string, deadline time.Time) bool {
	pollInterval := 5 * time.Second
	for {
		ready, err := controlServiceReadyFunc(ctx, service, member)
		if err != nil {
			logger.Warnf("readiness check for %s on %s failed: %v; treating as not ready", service, member, err)
			return false
		}
		if ready {
			return true
		}
		if time.Now().After(deadline) {
			logger.Warnf("%s on %s not viable in Ceph after timeout; not counting as retainer", service, member)
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollInterval):
		}
	}
}

// controlReadinessTimeout bounds the total polling time for Ceph readiness
// verification during placement. Default 2 minutes; tests override to 0 for
// an immediate single check.
var controlReadinessTimeout = 2 * time.Minute

// verifyControlServicesReady polls Ceph readiness for all observed control
// services and marks non-viable ones as not viable (sets the viability map
// entry to false). The caller must keep this viability map separate from the
// observed/existence map: a DB service row or locally-active snap is still a
// removal target even when it is not viable enough to count as a retainer.
//
// Only call this when there are pending removals; otherwise it is unnecessary
// work. The deadline bounds the total polling time (shared across all services).
func verifyControlServicesReady(ctx context.Context, viableControl map[string]map[string]bool) {
	deadline := time.Now().Add(controlReadinessTimeout)
	for _, svc := range controlServices {
		for memberName := range viableControl[svc] {
			if !viableControl[svc][memberName] {
				continue
			}
			if !waitForControlServiceReady(ctx, svc, memberName, deadline) {
				viableControl[svc][memberName] = false
			}
		}
	}
}

// copyObservedControlServices returns a deep copy of observed control service
// placement. ApplyPlacement uses the copy as a viability map so readiness
// checks cannot erase service-existence information needed for removals.
func copyObservedControlServices(observedControl map[string]map[string]bool) map[string]map[string]bool {
	result := make(map[string]map[string]bool, len(observedControl))
	for svc, members := range observedControl {
		result[svc] = make(map[string]bool, len(members))
		for memberName, exists := range members {
			result[svc][memberName] = exists
		}
	}
	return result
}

// hasPendingControlRemovals reports whether the policy requests control:false
// on any member that currently has an observed control service. If false, the
// readiness verification step can be skipped (nothing to remove).
func hasPendingControlRemovals(policy types.PlacementPolicy, observedControl map[string]map[string]bool) bool {
	for _, svc := range controlServices {
		for memberName, mp := range policy.Members {
			if mp.Control != nil && !*mp.Control && observedControl[svc][memberName] {
				return true
			}
		}
	}
	return false
}

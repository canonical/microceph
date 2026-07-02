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
// It parses 'ceph mon stat -f json', which on recent Ceph releases (Tentacle)
// exposes quorum members as a "quorum" array of {"rank":N,"name":"<host>"}
// objects rather than the legacy "quorum_names" string array. Both shapes are
// accepted so the check works across Ceph versions.
func monInQuorum(ctx context.Context, member string) (bool, error) {
	output, err := cephRunContext(ctx, "mon", "stat", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to run 'ceph mon stat': %w", err)
	}
	var stat struct {
		QuorumNames []string `json:"quorum_names"`
		Quorum      []struct {
			Name string `json:"name"`
		} `json:"quorum"`
	}
	if err := json.Unmarshal([]byte(output), &stat); err != nil {
		return false, fmt.Errorf("failed to parse 'ceph mon stat' output: %w", err)
	}
	if containsString(stat.QuorumNames, member) {
		return true, nil
	}
	for _, q := range stat.Quorum {
		if q.Name == member {
			return true, nil
		}
	}
	return false, nil
}

// mgrActiveOrStandby checks whether a MGR daemon on member is active or
// standby. It uses 'ceph mgr metadata -f json', which returns a JSON array of
// every registered MGR daemon (active and standbys), each with a "name" field.
// 'ceph mgr stat -f json' is not used because on Tentacle it only exposes
// active_name and a standby count (no standby names), and 'ceph mgr dump'
// emits invalid JSON (literal newlines in module error strings).
func mgrActiveOrStandby(ctx context.Context, member string) (bool, error) {
	output, err := cephRunContext(ctx, "mgr", "metadata", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to run 'ceph mgr metadata': %w", err)
	}
	var daemons []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(output), &daemons); err != nil {
		return false, fmt.Errorf("failed to parse 'ceph mgr metadata' output: %w", err)
	}
	for _, d := range daemons {
		if d.Name == member {
			return true, nil
		}
	}
	return false, nil
}

// mdsUp checks whether an MDS daemon on member is up (active or standby). It
// parses 'ceph mds stat -f json', whose fsmap lists standby daemons in
// "standbys" (each with "name" and "state") and active daemons in
// "filesystems[].mdsmap.info" (a map of gid keys to {"name","state"} objects).
// A member's MDS is viable iff its name appears with a state beginning "up:"
// in either location. This correctly handles clusters with no filesystem
// (all MDS are standbys and still viable) as well as active+standby setups.
func mdsUp(ctx context.Context, member string) (bool, error) {
	output, err := cephRunContext(ctx, "mds", "stat", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to run 'ceph mds stat': %w", err)
	}
	var stat struct {
		FSMap struct {
			Standbys []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"standbys"`
			Filesystems []struct {
				MDSMap struct {
					Info map[string]struct {
						Name  string `json:"name"`
						State string `json:"state"`
					} `json:"info"`
				} `json:"mdsmap"`
			} `json:"filesystems"`
		} `json:"fsmap"`
	}
	if err := json.Unmarshal([]byte(output), &stat); err != nil {
		return false, fmt.Errorf("failed to parse 'ceph mds stat' output: %w", err)
	}
	for _, s := range stat.FSMap.Standbys {
		if s.Name == member && strings.HasPrefix(s.State, "up:") {
			return true, nil
		}
	}
	for _, fs := range stat.FSMap.Filesystems {
		for _, info := range fs.MDSMap.Info {
			if info.Name == member && strings.HasPrefix(info.State, "up:") {
				return true, nil
			}
		}
	}
	return false, nil
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

// verifyControlServicesReady polls Ceph readiness for observed control
// services that can act as keep-one retainers and marks non-viable ones as not
// viable (sets the viability map entry to false). The caller must keep this
// viability map separate from the observed/existence map: a DB service row or
// locally-active snap is still a removal target even when it is not viable
// enough to count as a retainer.
//
// Members whose policy entry explicitly sets control:false are not polled:
// they can never count as retainers (the keep-one keeper check excludes them
// regardless of viability), so polling them only burns the shared deadline —
// e.g. migrating control off a dead node would otherwise wait the full budget
// for the dead node's services. Their viability entries stay at the copied
// value and are never read for retainer counting.
//
// Only call this when there are pending removals; otherwise it is unnecessary
// work. All pending (service, member) pairs are polled concurrently against a
// single shared deadline so that a service which needs most of the budget to
// become ready (e.g. MON quorum re-forming) cannot starve later services of the
// deadline and mark them spuriously non-viable. The ctx already carries the
// placement request deadline (placementPutTimeout), so in-flight polls cannot
// leak past the request lifetime. Map writes are confined to this goroutine
// (the polling goroutines only read existing entries via
// waitForControlServiceReady and return booleans), so there is no data race on
// viableControl.
func verifyControlServicesReady(ctx context.Context, viableControl map[string]map[string]bool, policy types.PlacementPolicy) {
	deadline := time.Now().Add(controlReadinessTimeout)

	type pendingCheck struct {
		service string
		member  string
	}
	var pending []pendingCheck
	for _, svc := range controlServices {
		for memberName := range viableControl[svc] {
			if !viableControl[svc][memberName] {
				continue
			}
			mp, inMap := policy.Members[memberName]
			if inMap && mp.Control != nil && !*mp.Control {
				// Explicit removal target: never a retainer, skip polling.
				continue
			}
			pending = append(pending, pendingCheck{service: svc, member: memberName})
		}
	}
	if len(pending) == 0 {
		return
	}

	type checkResult struct {
		service string
		member  string
		ready   bool
	}
	resultCh := make(chan checkResult, len(pending))
	for _, p := range pending {
		go func(p pendingCheck) {
			ready := waitForControlServiceReady(ctx, p.service, p.member, deadline)
			resultCh <- checkResult{service: p.service, member: p.member, ready: ready}
		}(p)
	}
	for i := 0; i < len(pending); i++ {
		res := <-resultCh
		if !res.ready {
			viableControl[res.service][res.member] = false
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

// formatControlMap renders a service→members map as "mon:[a+ b-],mgr:[a+],mds:[]"
// for debug logs. A '+' suffix means the entry is true (observed/viable); a '-'
// suffix means false (e.g. a service marked non-viable by the readiness check).
// Services are iterated in the stable controlServices order so output is
// deterministic. Members absent from a service's map are omitted (the service
// was never observed on them).
func formatControlMap(m map[string]map[string]bool) string {
	var parts []string
	for _, svc := range controlServices {
		members := m[svc]
		var names []string
		for name, on := range members {
			if on {
				names = append(names, name+"+")
			} else {
				names = append(names, name+"-")
			}
		}
		parts = append(parts, fmt.Sprintf("%s:[%s]", svc, strings.Join(names, " ")))
	}
	return strings.Join(parts, ",")
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

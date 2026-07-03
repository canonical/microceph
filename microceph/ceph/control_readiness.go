package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
)

// controlServiceReadyFunc checks whether a control service on a given member
// is viable in Ceph — not merely placed (DB row exists) or snap-locally-active.
// For MON: the member must be in the quorum. For MGR: active or standby. For
// MDS: up (active or standby). Returns (false, nil) if the service is not yet
// viable; (false, err) if the check itself failed (caller retries until the
// deadline, treating transient errors as not-ready rather than aborting).
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
// It parses 'ceph mon stat -f json', which exposes quorum members as a "quorum"
// array of {"rank":N,"name":"<host>"} objects. A legacy "quorum_names" string
// array (emitted by 'ceph -s'/'ceph quorum_status', not 'mon stat') is also
// accepted as defense-in-depth so the check works if a future code path feeds
// quorum_status-shaped output into this helper.
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
	err = json.Unmarshal([]byte(output), &stat)
	if err != nil {
		return false, fmt.Errorf("failed to parse 'ceph mon stat' output: %w", err)
	}
	if slices.Contains(stat.QuorumNames, member) {
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
	err = json.Unmarshal([]byte(output), &daemons)
	if err != nil {
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
	err = json.Unmarshal([]byte(output), &stat)
	if err != nil {
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

// controlReadinessCheckTimeout bounds each individual ceph CLI invocation
// during readiness polling. Ceph mon-side commands (mon stat, mgr metadata,
// mds stat) can block indefinitely when MON quorum is lost; without a per-call
// timeout a single hung subprocess would consume the entire request budget
// (placementPutTimeout, 10 min) instead of the 2-minute readiness budget.
// 30 seconds is generous for a healthy command to return while ensuring a
// hung invocation does not starve the poll loop.
const controlReadinessCheckTimeout = 30 * time.Second

// waitForControlServiceReady polls Ceph readiness for a single control service
// on a member until it is viable or the deadline is reached. Returns true if
// the service became viable, false otherwise. Transient check errors (e.g. a
// momentary MON election blip, truncated output) are treated as not-ready and
// the poll continues until the deadline — a single shared transient cause can
// error every goroutine at the same instant, and aborting immediately would
// mark all retainers non-viable in one shot. Only parent-context cancellation
// (the request deadline) causes an immediate bail.
func waitForControlServiceReady(ctx context.Context, service, member string, deadline time.Time) bool {
	pollInterval := 5 * time.Second
	for {
		// Bound each individual ceph exec so a hung subprocess cannot consume
		// the full request budget. The parent ctx (placementPutTimeout) is the
		// outer lifetime; this per-call timeout ensures a blocked 'ceph mon
		// stat' is killed well within the 2-minute readiness budget.
		checkCtx, cancel := context.WithTimeout(ctx, controlReadinessCheckTimeout)
		ready, err := controlServiceReadyFunc(checkCtx, service, member)
		cancel()
		if err != nil {
			// If the parent context was cancelled, bail immediately; otherwise
			// the error is transient (e.g. MON mid-election, per-call timeout
			// on a hung command) and we keep polling until the deadline.
			if ctx.Err() != nil {
				return false
			}
			logger.Warnf("readiness check for %s on %s failed: %v; will retry", service, member, err)
		} else if ready {
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

// controlServiceViability returns a fresh map of which observed control
// services are viable enough to act as keep-one retainers. It never mutates
// observedControl; the returned map is a deep copy.
//
// Viability is seeded from observed existence (a DB service row or
// locally-active snap), then refined by polling Ceph readiness (MON quorum,
// MGR active/standby, MDS health) for retainer candidates. Non-viable
// services are marked false so the remove loop never counts them as
// retainers, while still treating them as removal targets when another viable
// retainer exists. Keep the returned viability map separate from observed
// existence at the call site: a non-viable existing service is still a
// removal target, it just cannot count as a retainer.
//
// Polling is skipped entirely when the policy has no pending control removals
// (nothing to keep-one-guard). Members whose policy entry explicitly sets
// control:false are also skipped: they can never count as retainers (the
// keep-one keeper check excludes them regardless of viability), and polling
// them would only burn the shared deadline — e.g. migrating control off a
// dead node would otherwise wait the full budget for the dead node's
// services. Their viability entries stay at the copied value and are never
// read for retainer counting.
//
// All pending (service, member) pairs are polled concurrently against a single
// shared deadline so that a service which needs most of the budget to become
// ready (e.g. MON quorum re-forming) cannot starve later services of the
// deadline and mark them spuriously non-viable. The ctx already carries the
// placement request deadline (placementPutTimeout), so in-flight polls cannot
// leak past the request lifetime. Map writes are confined to this goroutine
// (the polling goroutines only read existing entries via
// waitForControlServiceReady and return booleans), so there is no data race on
// the returned map.
func controlServiceViability(ctx context.Context, observedControl map[string]map[string]bool, policy types.PlacementPolicy) map[string]map[string]bool {
	// Seed viability from observed existence (fresh copy: observedControl is
	// never mutated, so the removal loop retains service-existence info).
	viableControl := make(map[string]map[string]bool, len(observedControl))
	for svc, members := range observedControl {
		memberViable := make(map[string]bool, len(members))
		for member, exists := range members {
			memberViable[member] = exists
		}
		viableControl[svc] = memberViable
	}

	// In a single pass, detect whether any observed service sits on a pending
	// removal target (the gate for polling) and collect the retainer candidates
	// to poll (observed services not on an explicit control:false member).
	deadline := time.Now().Add(controlReadinessTimeout)
	type pendingCheck struct {
		service string
		member  string
	}
	var pending []pendingCheck
	hasRemovals := false
	for _, svc := range controlServices {
		for member, exists := range viableControl[svc] {
			if !exists {
				continue
			}
			mp, inMap := policy.Members[member]
			if inMap && mp.Control != nil && !*mp.Control {
				// Explicit removal target: never a retainer, skip polling it.
				hasRemovals = true
				continue
			}
			pending = append(pending, pendingCheck{service: svc, member: member})
		}
	}

	// Skip polling when there is nothing to keep-one-guard, or no retainer
	// candidates to check: return the existence-seeded viability map as-is.
	if !hasRemovals || len(pending) == 0 {
		return viableControl
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
	return viableControl
}

// formatControlMap renders a service→members map as "mon:[a+ b-],mgr:[a+],mds:[]"
// for debug logs. A '+' suffix means the entry is true (observed/viable); a '-'
// suffix means false (e.g. a service marked non-viable by the readiness check).
// Services are iterated in the stable controlServices order and member names
// within each service are sorted so output is deterministic across runs.
// Members absent from a service's map are omitted (the service was never
// observed on them).
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
		sort.Strings(names)
		parts = append(parts, fmt.Sprintf("%s:[%s]", svc, strings.Join(names, " ")))
	}
	return strings.Join(parts, ",")
}

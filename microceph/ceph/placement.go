package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// Control services governed by the declarative placement API.
var controlServices = []string{"mon", "mgr", "mds"}

// ErrCephNotBootstrapped is returned when a non-empty placement policy is
// applied before Ceph has been bootstrapped. It is a client-side sentinel so
// the API handler maps it to HTTP 400 rather than the SmartError 500 fallback.
var ErrCephNotBootstrapped = fmt.Errorf("Ceph not bootstrapped")

// ErrUnknownPlacementMember is returned when the placement policy references a
// member that is not a known MicroCluster member. It is a client-side sentinel
// so the API handler maps it to HTTP 400.
var ErrUnknownPlacementMember = fmt.Errorf("unknown cluster member in placement policy")

// ErrKeepOneInvariant is returned when the placement engine refuses to remove
// the last viable MON, MGR, or MDS. It is a client-side sentinel so the API
// handler maps it to HTTP 400 rather than implying a server fault.
var ErrKeepOneInvariant = fmt.Errorf("keep-one invariant")

// ErrPlacementApplyInProgress is returned when another placement apply holds
// the cluster-wide apply lock. It is a retryable condition, mirroring
// ErrCephBootstrapInProgress.
var ErrPlacementApplyInProgress = fmt.Errorf("placement apply already in progress")

// placementApplyLease bounds how long a placement apply may hold the
// cluster-wide dqlite lock before it is considered abandoned (daemon crashed
// mid-apply) and reclaimable by the next writer. It must comfortably exceed
// the API handler's server-side apply deadline (placementPutTimeout, 10 min)
// so a live apply can never have its lock reclaimed underneath it.
const placementApplyLease = 15 * time.Minute

// LockPlacementApplyFunc is the injectable wrapper for LockPlacementApply,
// used by the API handler so tests can override it.
var LockPlacementApplyFunc = LockPlacementApply

// LockPlacementApply acquires the cluster-wide placement apply lock (CE142).
// ReconcilePlacement reads observed service state and then mutates services
// over minutes; two overlapping applies (possibly served by different members)
// could each count the other's removal targets as keep-one retainers and
// together remove the last viable control service. The dqlite-backed lock
// makes the read-modify cycle mutually exclusive across all cluster members.
//
// The returned token must be passed to UnlockPlacementApply. If another apply
// holds the lock, ErrPlacementApplyInProgress is returned; a lock older than
// placementApplyLease is treated as abandoned and reclaimed.
func LockPlacementApply(ctx context.Context, s interfaces.StateInterface) (int64, error) {
	token := time.Now().UnixNano()
	staleBefore := token - placementApplyLease.Nanoseconds()

	var acquired bool
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		acquired, err = database.TryAcquirePlacementApplyLock(ctx, tx, token, staleBefore)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("failed to acquire placement apply lock: %w", err)
	}
	if !acquired {
		logger.Debugf("placement apply lock not acquired: another apply holds it")
		return 0, fmt.Errorf("%w: retry after the current apply completes", ErrPlacementApplyInProgress)
	}
	return token, nil
}

// UnlockPlacementApplyFunc is the injectable wrapper for UnlockPlacementApply,
// used by the API handler so tests can override it.
var UnlockPlacementApplyFunc = UnlockPlacementApply

// UnlockPlacementApply releases the cluster-wide placement apply lock acquired
// by LockPlacementApply. Releasing is conditional on the token: if this
// holder's lease expired mid-apply and another writer reclaimed the lock, the
// reclaimer's lock is left alone and a warning is logged.
func UnlockPlacementApply(ctx context.Context, s interfaces.StateInterface, token int64) error {
	var released bool
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		released, err = database.ReleasePlacementApplyLock(ctx, tx, token)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to release placement apply lock: %w", err)
	}
	if !released {
		logger.Warnf("placement apply lock was not held by this apply on release; its lease likely expired and another apply reclaimed it")
	} else {
		logger.Debugf("placement apply lock released (token=%d)", token)
	}
	return nil
}

// ValidatePlacementFunc is the injectable wrapper for ValidatePlacement, used
// by the API handler so tests can override it.
var ValidatePlacementFunc = ValidatePlacement

// ReconcilePlacementFunc is the injectable wrapper for ReconcilePlacement, used
// by the API handler so tests can override it.
var ReconcilePlacementFunc = ReconcilePlacement

// getClusterLifecycleFunc reads the Ceph lifecycle state for the pre-bootstrap
// guard. It is injectable for testing.
var getClusterLifecycleFunc = func(ctx context.Context, s interfaces.StateInterface) (*database.ClusterLifecycle, error) {
	var lc *database.ClusterLifecycle
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		lc, err = database.GetClusterLifecycle(ctx, tx)
		return err
	})
	return lc, err
}

// ValidatePlacement checks a desired placement snapshot against cluster
// preconditions without touching any service or the stored policy (CE142).
//
// It is the first of the three phases the API handler drives -- validate,
// store, reconcile -- and exists as a separate phase so that a policy which can
// never apply is rejected before it replaces the stored desired state, while a
// policy that merely fails to converge is still persisted and observable.
//
// Rejections:
//   - Ceph is not bootstrapped and the policy is non-empty
//     (ErrCephNotBootstrapped).
//   - The policy names a member that is not a known cluster member
//     (ErrUnknownPlacementMember).
//
// Both are client-side sentinels so the API handler maps them to HTTP 400
// rather than the SmartError 500 fallback. An empty members map (the CE142
// waiting policy) always validates, including before Ceph is bootstrapped.
func ValidatePlacement(ctx context.Context, s interfaces.StateInterface, policy types.PlacementPolicy) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	// Empty members map: the CE142 waiting policy. It is accepted before the
	// pre-bootstrap guard below so the charm can install it while Ceph is still
	// unbootstrapped (relation missing, no assignments published, or only
	// pending/error entries). Storing it is not inert: an empty members map is
	// an empty storage allow-list, so once active it denies new OSD enrollment
	// on every member (see OSDManager.checkStorageEligibility).
	if len(policy.Members) == 0 {
		return nil
	}

	// Pre-bootstrap guard: a non-empty placement policy requires Ceph to be
	// bootstrapped. cephIsBootstrappedFunc uses the lifecycle row as the primary
	// signal and falls back to config-row presence (fsid + admin keyring), so a
	// stale lifecycle row on a fresh non-deferred cluster does not reject valid
	// placement. Without this, EnableService -> UpdateConfig fails with an
	// obscure 'failed to locate IP on public network' error because no Ceph
	// config exists yet.
	bootstrapped, err := cephIsBootstrappedFunc(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to determine Ceph bootstrap state: %w", err)
	}
	if !bootstrapped {
		return fmt.Errorf("%w: run `microceph cluster bootstrap-ceph` first", ErrCephNotBootstrapped)
	}

	// Validate all members in the map are known cluster members.
	members, err := GetClusterMemberNamesFunc(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to list cluster members: %w", err)
	}
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m] = true
	}
	for memberName := range policy.Members {
		if !memberSet[memberName] {
			return fmt.Errorf("%w: %s", ErrUnknownPlacementMember, memberName)
		}
	}

	return nil
}

// ReconcilePlacement converges observed service placement onto a desired
// placement snapshot (CE142). It is the core of the placement engine: it
// computes the diff between the desired snapshot and observed placement, then
// applies control-service adds before removals, refusing to remove the last
// viable MON, MGR, or MDS.
//
// The policy is a complete desired-state snapshot, not a delta (see
// types.PlacementPolicy): every decision below is taken from this policy alone,
// and the previously stored policy is never consulted or merged.
//
// Rules:
//   - An empty members map performs no service operations (the waiting policy).
//   - A member absent from the map is unmanaged: no service is added to or
//     removed from it. Absence is not a grant -- it also leaves that member off
//     the storage_eligible allow-list once the policy is stored.
//   - A nil service field on a present member is unmanaged for that service
//     class: no operation is performed, and the field is not inherited from the
//     policy this one replaces.
//   - control:true adds MON/MGR/MDS; control:false removes them (after safety).
//   - The engine never removes the last viable MON, MGR, or MDS.
//
// Reconciliation is deliberately a no-op for unmanaged fields, but that does
// not make the stored policy a delta: StorePlacementPolicy replaces the whole
// desired snapshot regardless of which fields caused work here.
//
// The caller must have run ValidatePlacement on the same policy first;
// ReconcilePlacement does not re-check bootstrap state or member membership.
//
// If a removal is refused for keep-one safety the adds remain in effect (a
// partial convergence) and the function returns ErrKeepOneInvariant so the
// caller can surface a clear blocked reason. The desired policy has already
// been stored by then, so GET /placement reports the observed-vs-desired gap
// with last_refusal explaining it.
func ReconcilePlacement(ctx context.Context, s interfaces.StateInterface, policy types.PlacementPolicy) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	// Empty members map (the waiting policy): no service operations. The policy
	// still governs storage eligibility once stored; it is only reconciliation
	// that has nothing to do.
	if len(policy.Members) == 0 {
		logger.Debug("Placement policy has empty members map: no service operations")
		return nil
	}

	// Get current observed control services.
	observedControl, err := getObservedControlServicesFunc(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to get observed control services: %w", err)
	}
	logger.Debugf("Placement: observed control services: %s", formatControlMap(observedControl))

	// Compute desired control members (those with control:true).
	desiredControl := make(map[string]bool)
	for memberName, mp := range policy.Members {
		if mp.Control != nil && *mp.Control {
			desiredControl[memberName] = true
		}
	}

	// Add-before-remove: first add control services to members that should
	// have them but don't yet.
	for _, svc := range controlServices {
		for memberName := range desiredControl {
			if !observedControl[svc][memberName] {
				err = addControlServiceFunc(ctx, s, memberName, svc)
				if err != nil {
					return fmt.Errorf("failed to add %s on %s: %w", svc, memberName, err)
				}
				// Update observed state so the removal loop sees the new service.
				observedControl[svc][memberName] = true
			}
		}
	}

	// Compute control-service viability for keep-one safety: a DB service row
	// or locally-active snap does not prove MON quorum, MGR active/standby, or
	// MDS health, so only viable services count as retainers. Viability is
	// kept separate from observed existence — a non-viable existing service is
	// still a removal target, it just cannot count as a retainer.
	// controlServiceViability never mutates observedControl.
	viableControl := controlServiceViability(ctx, observedControl, policy)
	logger.Debugf("Placement: control service viability: %s", formatControlMap(viableControl))

	// Then remove control services from members that have control:false and
	// are present in the map, subject to keep-one safety.
	var refused []string
	for _, svc := range controlServices {
		for memberName, mp := range policy.Members {
			if mp.Control == nil || *mp.Control || !observedControl[svc][memberName] {
				// Only remove from present members with control:false that
				// currently have the service. Omitted control (nil),
				// control:true, and members without the service are skipped.
				continue
			}

			// Count how many members OTHER than this one retain the service.
			// A member retains the service only if it has the service observed AND
			// viable (the readiness check sets non-viable entries to false) AND
			// is a keeper (control:true or control field omitted/nil or absent
			// from the policy map). This ensures keep-one is based on real Ceph
			// viability, not stale DB/snap records.
			retainers := 0
			for otherName := range observedControl[svc] {
				if otherName == memberName {
					continue
				}
				if !viableControl[svc][otherName] {
					continue // not viable (readiness check marked it false)
				}
				otherMp, inMap := policy.Members[otherName]
				if !inMap || otherMp.Control == nil || *otherMp.Control {
					retainers++
				}
			}

			// keep-one: refuse removal if no other member retains the service.
			logger.Debugf("Placement: keep-one check for %s on %s: %d viable retainer(s)", svc, memberName, retainers)
			if retainers == 0 {
				logger.Warnf("refusing to remove last %s on %s: keep-one invariant", svc, memberName)
				refused = append(refused, fmt.Sprintf("%s on %s", svc, memberName))
				continue
			}
			err = removeControlServiceFunc(ctx, s, memberName, svc)
			if err != nil {
				return fmt.Errorf("failed to remove %s on %s: %w", svc, memberName, err)
			}
			// Update observed and viability state so subsequent keep-one checks are accurate.
			observedControl[svc][memberName] = false
			viableControl[svc][memberName] = false
		}
	}

	if len(refused) > 0 {
		return fmt.Errorf("%w: refused to remove last control service(s): %s", ErrKeepOneInvariant, strings.Join(refused, ", "))
	}

	return nil
}

// getObservedControlServicesFunc returns a map of service name to a set of
// member names that currently have that service. It is injectable for testing.
var getObservedControlServicesFunc = func(ctx context.Context, s interfaces.StateInterface) (map[string]map[string]bool, error) {
	result := make(map[string]map[string]bool)
	for _, svc := range controlServices {
		result[svc] = make(map[string]bool)
	}

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		services, err := database.GetServices(ctx, tx)
		if err != nil {
			return err
		}
		for _, svc := range services {
			if _, ok := result[svc.Service]; ok {
				result[svc.Service][svc.Member] = true
			}
		}
		return nil
	})
	return result, err
}

// addControlServiceFunc adds a control service on a member. Injectable for testing.
var addControlServiceFunc = func(ctx context.Context, s interfaces.StateInterface, member string, service string) error {
	logger.Infof("Placement: adding %s on %s", service, member)
	return prodAddControlService(ctx, s, member, service)
}

// removeControlServiceFunc removes a control service from a member. Injectable for testing.
var removeControlServiceFunc = func(ctx context.Context, s interfaces.StateInterface, member string, service string) error {
	logger.Infof("Placement: removing %s from %s", service, member)
	return prodRemoveControlService(ctx, s, member, service)
}

// ProdAddControlServiceFunc is the injectable hook for the production add
// implementation. The daemon package sets this at init time; the default is nil
// (no-op for tests that don't need real service placement).
var ProdAddControlServiceFunc func(ctx context.Context, s interfaces.StateInterface, member string, service string) error

// ProdRemoveControlServiceFunc is the injectable hook for the production remove
// implementation. The daemon package sets this at init time; the default is nil
// (no-op for tests that don't need real service removal).
var ProdRemoveControlServiceFunc func(ctx context.Context, s interfaces.StateInterface, member string, service string) error

// prodAddControlService delegates to the injected production function, or is a
// no-op when no production function is wired (e.g. in unit tests).
func prodAddControlService(ctx context.Context, s interfaces.StateInterface, member string, service string) error {
	if ProdAddControlServiceFunc != nil {
		return ProdAddControlServiceFunc(ctx, s, member, service)
	}
	return nil
}

// prodRemoveControlService delegates to the injected production function, or is
// a no-op when no production function is wired (e.g. in unit tests).
func prodRemoveControlService(ctx context.Context, s interfaces.StateInterface, member string, service string) error {
	if ProdRemoveControlServiceFunc != nil {
		return ProdRemoveControlServiceFunc(ctx, s, member, service)
	}
	return nil
}

// GetPlacementStatusFunc is the injectable wrapper for GetPlacementStatus,
// used by the API handler so tests can override it.
var GetPlacementStatusFunc = GetPlacementStatus

// secretPattern matches Ceph cephx key tokens (e.g. "AQAR...==") so key
// material can be redacted from operator-facing API responses. Full error
// detail is preserved in logs (logger.Errorf).
var secretPattern = regexp.MustCompile(`AQ[A-Za-z0-9+/]{20,}={0,2}`)

// redactSecrets masks cephx key material in a string before it is exposed via
// the placement status API. It is a defense-in-depth measure; underlying error
// messages should not echo keys, but this prevents accidental leakage if a
// future change surfaces key-bearing text.
func redactSecrets(s string) string {
	return secretPattern.ReplaceAllString(s, "AQ****REDACTED****==")
}

// GetPlacementStatus reads the canonical desired placement policy and the
// lifecycle state from the database and returns a PlacementStatus response.
// The returned Policy is the complete desired snapshot as stored, so callers
// can compare it against Observed without reconstructing it from earlier
// requests.
func GetPlacementStatus(ctx context.Context, s interfaces.StateInterface) (*types.PlacementStatus, error) {
	if s.ClusterState().ServerCert() == nil {
		return nil, fmt.Errorf("no server certificate")
	}

	status := &types.PlacementStatus{}

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rec, err := database.GetPlacementPolicy(ctx, tx)
		if err != nil {
			return err
		}
		status.Active = rec.Active
		if rec.Active && rec.PolicyJSON != "" {
			var policy types.PlacementPolicy
			err = json.Unmarshal([]byte(rec.PolicyJSON), &policy)
			if err != nil {
				return fmt.Errorf("failed to unmarshal placement policy: %w", err)
			}
			status.Policy = &policy
		}
		status.PlacementRefusal = redactSecrets(rec.LastRefusal)

		lc, err := database.GetClusterLifecycle(ctx, tx)
		if err != nil {
			return err
		}
		status.BootstrapState = lc.CephBootstrapState
		status.BootstrapTarget = lc.CephBootstrapTarget
		if lc.CephBootstrapState == database.CephStateFailed {
			status.BlockedReason = redactSecrets(lc.Detail)
		}

		// Observed control services.
		services, err := database.GetServices(ctx, tx)
		if err != nil {
			return err
		}
		observedByMember := make(map[string]*types.PlacementObservedMember)
		for _, svc := range services {
			om, ok := observedByMember[svc.Member]
			if !ok {
				om = &types.PlacementObservedMember{Member: svc.Member}
				observedByMember[svc.Member] = om
			}
			switch svc.Service {
			case "mon", "mgr", "mds":
				om.Control = true
			case "rgw":
				om.Rgw = true
			}
		}

		// Observed NFS: NFS placements are recorded in the grouped-services
		// tables keyed by group ID, not in the plain services table, so they
		// are collected separately. Report each member's NFS group IDs.
		groupedServices, err := database.GetGroupedServices(ctx, tx)
		if err != nil {
			return err
		}
		for _, gs := range groupedServices {
			if gs.Service != "nfs" {
				continue
			}
			om, ok := observedByMember[gs.Member]
			if !ok {
				om = &types.PlacementObservedMember{Member: gs.Member}
				observedByMember[gs.Member] = om
			}
			om.Nfs = append(om.Nfs, gs.GroupID)
		}

		for _, om := range observedByMember {
			status.Observed = append(status.Observed, *om)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return status, nil
}

// StorePlacementPolicyFunc is the injectable wrapper for StorePlacementPolicy,
// used by the API handler so tests can override it.
var StorePlacementPolicyFunc = StorePlacementPolicy

// StorePlacementPolicy replaces the stored canonical desired placement policy
// with the given snapshot and marks a policy active. The write is a full
// replacement: the previously stored policy is discarded rather than merged, so
// a member or field the caller omitted is not carried forward. Consumers read
// this record as the authoritative statement of desired placement --
// OSDManager.checkStorageEligibility gates OSD enrollment on it.
func StorePlacementPolicy(ctx context.Context, s interfaces.StateInterface, policy types.PlacementPolicy) error {
	data, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("failed to marshal placement policy: %w", err)
	}

	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.SetPlacementPolicy(ctx, tx, true, string(data))
	})
}

// ClearPlacementPolicyFunc clears the active placement policy.
var ClearPlacementPolicyFunc = ClearPlacementPolicy

// SetPlacementRefusalFunc persists (or clears) the last placement refusal
// reason. Injectable for testing.
var SetPlacementRefusalFunc = SetPlacementRefusal

// SetPlacementRefusal persists (or clears, if reason is empty) the last
// placement refusal reason in the placement_policy table.
func SetPlacementRefusal(ctx context.Context, s interfaces.StateInterface, reason string) error {
	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.SetPlacementRefusal(ctx, tx, reason)
	})
}

// ClearPlacementPolicy clears the canonical desired placement policy and marks
// role-managed placement inactive, without adding or removing services. It is
// the stand-down path: with no active policy, storage eligibility is unmanaged
// again and OSD enrollment is no longer gated.
func ClearPlacementPolicy(ctx context.Context, s interfaces.StateInterface) error {
	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.ClearPlacementPolicy(ctx, tx)
	})
}

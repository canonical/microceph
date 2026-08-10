# Plan: Gateway RGW role in the MicroCeph snap (CE142)

## Scope

This plan covers **only the MicroCeph snap side** of the `gateway` role's **RGW
flavor**, as defined in CE142. The charm-side OS106 Requirer, NFS flavor, and
storage eligibility are out of scope here (tracked separately), though the
design keeps NFS/storage in mind so the same engine extends cleanly.

The goal: make `PUT /1.0/placement` honour each member's `rgw` field so RGW is
enabled/disabled declaratively and idempotently, with add-before-remove and
scale-to-zero, exactly like the control services already wired in `ApplyPlacement`.

## Current state (already merged)

The deferred-bootstrap + declarative-placement feature (`ed2f079`) already landed:

- `PUT/GET/DELETE /1.0/placement` endpoints and the cluster-wide apply lock.
- `ApplyPlacement` wires **control** services (mon/mgr/mds): add-before-remove,
  keep-one invariant, readiness/viability polling.
- `MemberPlacement` already has `Rgw *bool`, `Nfs []NFSPlacement`,
  `StorageEligible *bool` — but **only `Control` is applied**.
- `GET /1.0/placement` already **reports** observed `rgw` per member
  (`om.Rgw = true` for the `rgw` service row), so status readback is done.
- RGW enable/disable primitives exist and are targetable per member:
  - `EnableRGW(...)` via `ServicePlacementHandler` -> `RgwServicePlacement`.
  - `DisableRGW(ctx, s)` via `cmdRGWServiceDelete`.
  - Client helpers `SendServicePlacementReq(...target)` and
    `DeleteService(...target, "rgw")` already proxy to a target member.
  - Prod wiring pattern established in `cmd/microcephd/prod_wiring.go`
    (`ProdAddControlServiceFunc` / `ProdRemoveControlServiceFunc`).

**The gap:** `ApplyPlacement` never looks at `mp.Rgw`. RGW placement is entirely
unhandled by the engine — it can only be driven through the legacy per-service
`enable/disable rgw` CLI/API.

## Design

Mirror the control-service structure in `ceph/placement.go`, but RGW is a
**single, per-member, non-quorum** service, so it is simpler than control:

- No keep-one invariant (RGW is not a control-plane singleton; scale-to-zero is
  explicitly allowed by the spec).
- No cross-member viability/quorum polling. Readiness is local
  (`snapCheckActive("rgw")` / the existing `PostPlacementCheck`).
- Still honour **add-before-remove** across the whole apply so a rolling RGW
  migration never dips below the requested set while a replacement comes up.

### Payload semantics (from spec)

For each member **present** in `policy.Members`:

- `rgw: true`  -> ensure RGW enabled on that member.
- `rgw: false` -> ensure RGW disabled on that member (incl. scale-to-zero).
- `rgw` omitted (nil) -> leave RGW untouched on that member.

Members **absent** from the map are never touched. This is identical to the
`Control` rules already implemented, so the observed/desired diff logic is reused.

### RGW parameters

The declarative payload's `rgw` is a bare bool — it carries no port/SSL params.
The snap must supply RGW enable params itself. Two sub-decisions:

1. **Port/SSL:** For the first iteration, enable RGW with the snap defaults
   (port 80, no SSL) — the same defaults the legacy path uses when no flags are
   given. SSL/cert material is charm-derived deployment knowledge; per CE142 the
   charm handles RGW cert config through the existing `certificate_set_rgw` /
   identity-service path, which remains separate from placement. Placement only
   controls *presence*, not TLS config. (Document this explicitly.)
2. **Idempotency:** enabling RGW where it is already active must be a no-op, not
   an error. The existing `genericHospitalityCheck("rgw")` returns an error if
   the service is already active. The engine must treat "already present" as
   success (observed state check before calling enable), matching how the
   control path only adds when `!observed`.

## Implementation steps (snap)

### 1. Observed RGW state
Add an injectable `getObservedRGWServicesFunc(ctx, s) (map[string]bool, error)`
returning the set of members with an `rgw` service row. Reuse the same
`database.GetServices` scan already used by `getObservedControlServicesFunc`
(filter `svc.Service == "rgw"`). Injectable `var` for unit tests.

### 2. Add / remove primitives (injectable)
Add package-level injectable vars in `ceph/placement.go`:

```go
var addRGWServiceFunc    = func(ctx, s, member string) error { ... prodAddRGWService ... }
var removeRGWServiceFunc = func(ctx, s, member string) error { ... prodRemoveRGWService ... }
var ProdAddRGWServiceFunc    func(ctx, s, member string) error
var ProdRemoveRGWServiceFunc func(ctx, s, member string) error
```

Wire the prod implementations in `cmd/microcephd/prod_wiring.go`:

- **add:** `client.SendServicePlacementReq(ctx, leaderClient, &types.EnableService{
  Name: "rgw", Wait: true, Payload: <default RgwServicePlacement JSON>}, member)`.
- **remove:** `client.DeleteService(ctx, leaderClient, member, "rgw")`.

Both reuse the existing per-service proxy path (`ProxyTarget: true`), so no new
API endpoint is needed — the declarative engine composes the existing primitives.

### 3. RGW reconcile in ApplyPlacement
After the control block (and its keep-one handling), add an RGW pass:

```go
// Desired RGW set: members present with rgw != nil.
// Add-before-remove: enable on desired&&!observed first, then disable
// on present&&rgw==false&&observed.
```

- Add loop: for each member with `mp.Rgw != nil && *mp.Rgw && !observedRGW[m]`
  -> `addRGWServiceFunc`; update `observedRGW[m] = true`.
- Remove loop: for each member with `mp.Rgw != nil && !*mp.Rgw && observedRGW[m]`
  -> `removeRGWServiceFunc`; update observed.
- No keep-one; scale-to-zero allowed.
- Surface add/remove errors the same way control errors are surfaced (return
  wrapped error; API handler records refusal / maps to status).

Ordering vs control: run **all** adds (control + rgw) before **all** removes so
the global add-before-remove guarantee holds across service classes. Simplest:
keep control add loop, add rgw add loop, then control remove loop, then rgw
remove loop. (Control keep-one refusal still returns `ErrKeepOneInvariant`; RGW
removals are independent and should still be attempted — decide whether an RGW
error aborts the whole apply or is collected; recommend: RGW add/remove errors
abort with a wrapped error, consistent with control add errors.)

### 4. GET status
Already reports observed `rgw`. Confirm no change needed; add a test asserting
observed RGW round-trips through `GET /1.0/placement`.

### 5. Pre-bootstrap guard
The existing guard rejects any non-empty policy before Ceph is bootstrapped.
RGW placement is naturally covered — a members map with `rgw` entries is
non-empty, so it is rejected pre-bootstrap with `ErrCephNotBootstrapped`. Add a
test to lock this in.

## Testing (snap unit tests in ceph/placement_test.go)

Follow the existing `withObservedControl` / `withAddRemoveRecorder` harness;
add `withObservedRGW` and RGW add/remove recorders.

- RGW `true` on a member with no RGW -> one add, no removes.
- RGW `false` on a member with RGW -> one remove.
- RGW omitted (nil) -> untouched even if observed present/absent.
- Member absent from map -> untouched.
- Idempotent: RGW `true` where already present -> no add, no error.
- Scale-to-zero: all mentioned members `rgw:false` -> all removed, no error, no
  keep-one block (RGW has none).
- Add-before-remove across a migration (drop node-a, add node-b) -> add(node-b)
  precedes remove(node-a) in the ordered event log.
- Mixed policy: control + rgw in one PUT converge together, all adds before all
  removes.
- Pre-bootstrap: non-empty rgw policy rejected with ErrCephNotBootstrapped.
- Unknown member with rgw field -> ErrUnknownPlacementMember (existing check
  already covers this; add a test).

## Integration / Robot

Extend the existing declarative-placement Robot suites:

- Bootstrap Ceph, `PUT /1.0/placement` with `rgw:true` on one member, assert the
  `rgw` service appears via `microceph status` / services API and RGW answers.
- Flip to `rgw:false`, assert RGW removed (scale-to-zero).
- Migration: move RGW from node-a to node-b, assert node-b up before node-a down.

## Out of scope (explicit)

- NFS flavor placement in the engine (same pattern, separate change).
- `storage_eligible` allow-list enforcement in disk APIs (separate change).
- All charm-side OS106 work (Requirer, role-managed config, bootstrap-member
  designation, Traefik backend generation).
- RGW TLS/cert material via placement — remains charm/identity-service driven.

## Files touched

- `ceph/placement.go` — observed RGW, add/remove injectables, RGW reconcile pass.
- `cmd/microcephd/prod_wiring.go` — wire prod add/remove RGW funcs.
- `ceph/placement_test.go` — RGW unit tests + harness helpers.
- `tests/robot/suites/.../*_tests.robot` — RGW placement integration cases.
- Docs: note RGW-via-placement and TLS-stays-separate.

---

## Implementation status (landed on `feat/role-rgw`)

> This section supersedes the stale notes above (which predate Option B and
> describe TLS as out-of-scope). The shipped design carries port + TLS material
> in the placement payload so placement and RGW frontend config apply atomically.

**Wire shape (CE142 Option B).** `MemberPlacement.Rgw` is `*RgwPlacement`
 (an object), not a bool: `{enabled, port, ssl_port, ssl_certificate,
 ssl_private_key}`. `nil` = untouched; `{enabled:false}` = remove;
 `{enabled:true, ...}` = place/reconcile. The charm never shipped a bool `rgw`,
 so this is a clean break (a bare bool 400s via `DisallowUnknownFields`); the
 charm gates on the `placement-rgw` capability marker.

**Observed frontend in `GET /placement`.** Each observed member reports
 `rgw_frontend` (`{port, ssl_port, ssl}`) sourced from a new `rgw_frontends`
 dqlite table (`schemaUpdate10`), read in the same DB transaction as the rest
 of the status — no per-member fan-out, and available even when a member is
 down. Ports + a TLS flag only; cert/key bytes are never stored.

**Member-side enable is idempotent and restart-on-change.** `applyRGWFrontend`
 renders `radosgw.conf` + SSL material, compares against on-disk state, and
 restarts RGW only when something changed (re-apply is a no-op). It subsumes
 cert rotation. `DisableRGW` removals are `os.IsNotExist`-tolerant so
 scale-down/retry converges.

**Secrets posture.** SSL cert/key travel the authenticated API to the member,
 then are stripped before the policy is stored (`PolicyForStorage`) and redacted
 again on GET (`redactStoredPolicy`). They live only in `server.crt`/`server.key
 (0600) on the member, matching today's posture.

**Engine.** An RGW reconcile pass runs after the control pass in
 `ApplyPlacement`: add-before-remove, sorted, no keep-one (scale-to-zero
 allowed), only remove where observed, omitted = untouched. Malformed TLS
 surfaces `ErrRgwFrontendInvalid` -> HTTP 400.

**Capability.** `cluster/capabilities` advertises `placement-rgw`.

**Conf-file invariant.** Per `AGENTS.md`, all conf files are microcephd-owned
 with dqlite as the source of truth; `rgw_frontends` retires the prior
 radosgw.conf-only exception (frontend settings are now persisted, so a future
 follow-up can re-render radosgw.conf from dqlite like `ceph.conf`).

See `~/rgw-snap.md` for the full step-by-step plan and `~/data/specs/CE142 -*
 for the cross-team spec.

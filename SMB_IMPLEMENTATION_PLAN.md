# Native SMB implementation plan

This replaces the earlier strategy documents and records the agreed scope for PR #863.

Source implementation is now in the working tree. Local Go unit/static/vet/race checks, Python tests, ShellCheck, documentation build, and Robot dry-run checks have been exercised. Runtime qualification remains pending: `snapcraft pack -v` stopped at the repository's dirty-worktree guard. No new snap or successful SMB integration run is claimed.

## Review follow-up

The agreed source fixes from the combined-change review are implemented:

- Attempt every requested placement, but perform no removals if any placement fails.
- Preserve absent/retired CTDB slots as comments in the existing Sambacc wrapper, for both startup and monitoring; verify the actual local PNN before recording placement.
- Retire CTDB membership after stopping a removed instance and before deleting its local metadata/keyring. Support same-address rejoin and fresh additions without renumbering survivors; in-place address migration remains unsupported.
- Extend fresh-placement rollback through readiness/database failures. Stop attempted services before cleanup, and retain configuration if stopping or membership retirement fails.
- Pass bounded placement contexts through source fetches, keyring operations, and local service commands. Rollback uses a separate bounded cleanup context.
- Validate recorded clustering mode from the stored spec rather than file existence, so missing generated CTDB configuration is repairable.
- Count eligible online hosts when placement count is unspecified; honour explicit counts.
- Match upstream cluster-ID syntax in validation and documentation.

No-op local placements still persist shared rank reservations; avoiding a daemon restart must not discard new group metadata. Unit-level regressions and isolated packaged-CTDB nodes-list checks cover these paths. Full snap/RADOS/failover qualification remains pending a clean-source build.

## Boundaries

- Do not change Ceph's SMB manager, resource schema, or callback interfaces for this feature. Confirmed upstream bugs may be handled separately.
- Ceph owns desired SMB resources, share configuration, CephX authorization, and when orchestration occurs. MicroCeph owns managed CLI/API convenience, its orchestrator backend, local placement, and observed state.
- No managed-request lock, extra desired-membership store, background membership reconciler, distributed operation-ID transport, revision/acknowledgement framework, credential reservations, or automatic crash recovery.
- A successful Ceph SMB manager response is the managed operation's acceptance boundary. Concurrent full-placement replacements follow upstream semantics; cumulative concurrent additions are not guaranteed.
- Leave CTDB's existing privileges and NSS attachment in place. Privilege reduction is deferred.
- Leave the temporary Snapd channel and stable-availability test arrangement unchanged.
- Preserve the existing uncommitted fixes unless this plan explicitly replaces their behaviour. Do not restore the removed, unrelated deferred-placement retry.

## State ownership

Read the current Ceph SMB cluster resource and submit the requested configuration to the manager. Do not derive desired membership solely from observed `grouped_services` rows, and distinguish upstream absence from a failed lookup.

Keep the existing table roles: `service_groups.config` records the upstream spec/shared configuration received during placement; `grouped_services` records successfully placed instances. Neither becomes a new authoritative desired-membership controller. No lock columns or schema migration are required. Keep the existing empty-parent cleanup behaviour.

## 1. Internal execution context and completion

**Files:** managed SMB API/CLI/client, `microceph/ceph/smb_managed.go`, existing subprocess helpers as necessary.

Use one bounded internal context for accepted managed work. Prefer a daemon-lifecycle parent where available; otherwise use the existing detached-request-context pattern with an explicit timeout. Client disconnect must not cancel accepted work.

- Thread this context through backend setup, create/show/apply/remove commands and local polling. Use context-aware subprocess execution instead of introducing unbounded `context.Background()` calls.
- `--wait=true` waits for the worker result; `--wait=false` accepts the validated request and launches the same worker. The worker owns cancellation and resource cleanup. No database admission lock or reservation is taken.
- Retain the existing API response conventions; do not add operation IDs, a status endpoint, or a durable operation-state table. Asynchronous failures are logged safely; acceptance is not deployment success.
- Log failures safely, without adding a durable operation-status subsystem. If existing database bookkeeping needs to run after cancellation, it must use a separate bounded cleanup context rather than the expired context. Temporary files are removed on normal worker exit.
- Local cancellation does not prove Ceph manager work stopped. No distributed cancellation or exactly-once guarantee is claimed.
- Choose named timeout constants compatible with the existing client/member-call limits and measured startup behaviour. Do not promise five-minute total completion simply because an individual wait has that limit.

### First enable versus deployment

Initial enable succeeds when Ceph confirms creation of the cluster and credential resources. Report “configured; create the first CephFS-backed share to deploy.” Do not wait for `grouped_services` rows or create database reservations while awaiting the first share.

The first share triggers the unchanged upstream flow:

```text
Ceph SMB manager -> microceph-orch -> node-local PlacementIntf
```

Before that callback, `PostPlacementCheck` does not run. Once placement occurs, use the existing local lifecycle:

```text
PopulateParams -> HospitalityCheck -> ServiceInit -> PostPlacementCheck -> DbUpdate
```

No pending-deployment table is required. Empty observed membership alone proves neither failure nor absence of shares. Parse Ceph resource results, not just process exit codes, before reporting successful configuration.

**Tests:** context propagation, asynchronous context handoff, independent request dispatch without database admission, request disconnect, safe uncertain-result errors, resource cleanup, and initial creation returning without placement callbacks.

## 2. Secure, non-interactive credentials

**Files:** enable-SMB CLI, API types/validation, managed SMB resource creation, docs and test fixtures.

- Remove `--define-user-pass`. Add `--credentials-file <path|->`; `-` reads stdin. No interactive prompts.
- Keep `--user-group-ref` for existing upstream resources. Reject combining it with a credentials file. Credentials remain creation-only.
- The standard JSON input is `{"users":[{"name":"alice","password":"..."}]}`. Preserve multiple users and validate without echoing input. Do not expose numeric-ID allocation through this convenience format.
- Apply a generated `ceph.smb.usersgroups` resource and its referencing cluster resource through `ceph smb apply -i ... --password-filter-out hidden`. Use existing upstream resource fields, including appropriate linked-resource lifecycle handling.
- With the current runner, use a mode-`0600` temporary input file. Close and remove it on worker completion, failure, and cancellation. Deferred cleanup is not guaranteed after a process crash.
- Never place secrets in child argv, dqlite, logs, or error messages. Do not forward raw credential-command stdout/stderr or wrapped subprocess errors: Tentacle's invalid-resource path can bypass password filtering.
- Keep credentials only in memory/protected temporary input. After uncertain creation, inspect upstream state before retrying. Require the caller to resupply credentials if needed; report partial/conflicting state rather than blindly overwriting or deleting it.

Document the journey: protected credentials file or secret-manager pipe -> first enable -> create first share -> add members without credentials.

**Tests:** file/stdin input, conflicts, creation-only validation, multiple users, no secrets in argv/logs/errors (including malformed-resource responses), temporary-file permissions/cleanup, and safe handling of existing or partially created resources.

## 3. Clustered default, explicit non-clustered mode

**Files:** enable-SMB CLI/API, managed resource builder, orchestrator validation, local SMB placement/configuration and tests.

- Support `--clustering always|never`. On creation, omission means `always`; on update, omission preserves the existing mode. Preserve option presence through the API.
- `always` uses CTDB even with one member and can scale to multiple members. Scaling down to one retains CTDB.
- `never` permits one desired member, starts only `smbd`, and does not require CTDB metadata, services, or the CTDB plug.
- Repeating the existing mode is allowed; changing it in either direction is rejected before MicroCeph changes files/services. A new mode requires cluster removal and recreation.
- Use upstream cluster mode for managed validation and retain enough non-secret mode information in existing group configuration to validate backend/local updates. This is not a second desired-membership store. Preserve unrelated group configuration fields.
- Check both orchestrator and node-local placement boundaries. Remove automatic transition branches but retain both fresh-placement paths. Backend rejection cannot undo a resource mutation already accepted directly by upstream Ceph.
- Keep defensive CTDB metadata checks for clustered requests.

Both modes continue using the direct `samba-vfs/new` CephFS data path; “non-clustered” means no CTDB, not a different VFS.

**Tests:** defaults and omission, same-mode updates, second-member rejection for `never`, transition rejection before mutation, both startup/removal paths, and clustered scale-down.

## 4. Minimal Samba identity files, upstream numeric IDs

**Files:** `snapcraft/commands/smb-common`, Samba startup/import helpers, tests and SMB documentation.

- Replace full host passwd/group copies with a minimal baseline plus Sambacc-imported configured identities. Qualify the required baseline on the packaged build; `root` and `nobody`/`nogroup` are candidates.
- Generate files atomically and coordinate their shared use; an independent CTDB restart must not reset identities underneath active Samba. Leave CTDB's plug and NSS attachment unchanged, rather than implementing the deferred privilege separation.
- Every member fetches the same current upstream user resources. Keep common runtime setup separate from local-user import to allow future Winbind/AD support, without implementing AD now.
- Match Sambacc 0.9 allocation. No dqlite UID/GID allocator, reserved ranges, or retired-ID bookkeeping.

Document precisely: absent explicit IDs, users receive UID/primary GID `1000 + list position`; configured groups also use positional GIDs, with virtual user groups where needed. Identical ordered inputs yield identical current mappings, not stable historical identities. Appending preserves earlier positions; deletion/insertion/reordering may reassign IDs. Existing CephFS ownership is numeric and is not rewritten, so a different user can inherit its interpretation. AD is future work, not a currently available workaround.

**Tests:** no unrelated host identities, matching node mappings, append/delete/reorder behaviour, atomic generation, import failures, independent service restarts, and local authentication/I/O. Do not claim NSS-file regeneration alone proves complete Samba passdb account revocation.

## 5. Bounded individual startup waits

**Files:** `snapcraft/commands/smb-common`, `smbd.start`, `ctdb-nodes.start`.

Replace the four currently infinite waits with shared helpers: SMB configuration files, CTDB configuration files, and the duplicated CTDB `pnn` polling loops. Check each required file set together, bound each probe (including kill-after handling), and return specific non-zero timeout errors. Start with a five-minute limit per wait and shorten limits in tests.

Leave existing bounded `wait_for_config` and Sambacc waits unchanged. These individual waits can accumulate; there is no shared total startup deadline. Snapd may restart a failed process according to its policy.

**Tests:** eventual success, missing/empty-file diagnostics, failed and hung probes, finite timeout, and error propagation from all affected startup scripts.

## 6. Readiness in the existing placement interface

**Files:** `microceph/ceph/service_placement_smb.go` and its tests.

Strengthen SMB's `PostPlacementCheck`, not the managed API:

- Require active `smbd` plus a bounded response from the actual daemon. Qualify a local probe such as `smbcontrol smbd ping` under strict confinement before adopting it.
- In clustered mode, also check CTDB services and usable local CTDB state; do not demand every remote member be healthy.
- Do not accept an active startup shell as proof of daemon readiness. Ensure unchanged-placement shortcuts do not bypass the readiness requirement.
- On failure, use the existing error/rollback path and skip the new `DbUpdate`. Prior successful records are not automatically deleted.

This establishes local startup readiness only. Authentication, share permissions, and CephFS I/O remain integration checks. It adds no distributed acknowledgement protocol.

**Tests:** active-but-not-ready, readiness timeout, both clustering modes, unchanged placement, rollback, and SMB grouped-table writes only after checks succeed.

## Execution and verification

Implement in the numbered order, with regression tests before behaviour changes. Keep logical changes separable; do not commit or push as part of this planning step.

1. Establish the existing working-tree test baseline and preserve its diagnostics.
2. Run focused Go/API/CLI, orchestrator, and shell/helper tests after each change.
3. Run `make check-unit` and `make check-static` from `microceph/`, orchestrator tests, and Robot helper tests in the isolated test environment.
4. Run `tox -e robot -- --dryrun` across suites to catch keyword/argument regressions.
5. Build the snap and exercise both clustering modes, first-share deployment, member addition/removal, non-terminal CTDB rank removal/rejoin, no-op versus changed-content reapply, restart/re-enable, custom ports, and authentication/I/O.
6. Exercise request dispatch and interrupted operations: verify no managed lock/reservation is taken, uncertain outcomes are reported honestly, and accepted background work owns its context. Do not claim cumulative concurrent updates or distributed fencing.
7. Update user documentation for credentials, creation completion, clustering omission, UID/GID limitations, and upstream full-placement replacement semantics.

Existing fixes for stable CTDB ranks, effective-config no-op detection, rollback, startup re-enable, custom ports, best-effort target loops, and safe status reporting must remain covered. Runtime probe compatibility and the exact minimal identity baseline are qualification tasks, not already-proven facts.

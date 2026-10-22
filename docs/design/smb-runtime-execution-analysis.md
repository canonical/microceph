# Native SMB execution and confinement accounting

Status: approved mechanisms implemented in the isolated
`refactor/smb-runtime-component` source. The helper executable, helper-only
applications, IPC and build wiring are removed. **No installed snap/profile was
changed and no target strict-snap qualification is claimed.**

The primary corpus is `../microceph-smb-runtime-refactor`, based on committed AD
baseline `955ef872823d3f6699ace619036dec86482a482c`. Pinned Samba, snapd and Go
sources are lookup evidence, not additional implementation scope. Robot/CI and
the retained AD lab are excluded from edits. The
[path plan](smb-runtime-path-plan.md) records accepted ownership and path choices.

## Service ownership

Preparation and control stay in `microceph/ceph`, following existing placement,
one-shot preparation and snapctl patterns. The fresh five-service table does
not supervise processes. Main, microcluster hooks and common Ceph startup remain
at baseline. Membership and source maintenance run under the existing Ceph
startup context; cancellation does not stop healthy services. Membership changes
wake the worker after successful publication, with coalesced in-process hints
and a 30-second recovery fallback. An unchanged membership pass queries status
once; it no longer repeats the full identity/node-health sweep every two seconds.
Existing on-demand readiness checks remain unchanged.

| Snap startup script | Fixed final native exec | Private view |
| --- | --- | --- |
| `smbd.start` | `smbd --foreground --no-process-group --configfile R/config/smb.conf` | N; C if clustered; W if AD; optional D |
| `ctdbd.start` | `ctdbd --interactive --configfile=R/ctdb/ctdb.conf --pidfile=R/ctdb/run/ctdbd.pid` | C; optional D |
| `winbindd.start` | `winbindd -F --no-process-group --configfile R/config/smb.conf` | N+C+W; optional D |
| `cephfs-proxy.start` | `libcephfsd --socket R/proxy-work/libcephfsd.sock` | Optional D; owner-local CWD |
| `smbmetrics.start` | `smbmetrics --address 0.0.0.0 --port <prepared-port>` | Optional D |

Here `R` is the validated active `$SNAP_COMMON/smb/<cluster-id>` directory.

- **N:** owner-local passwd, group and nsswitch files onto their fixed `/etc`
  paths. Other applications and the daemon keep normal NSS.
- **C:** `R/ctdb/run` onto `/run/ctdb`, required by the pinned production CTDB CLI.
- **W:** `R/samba/winbindd` onto the public `/run/samba/winbindd` leaf. The
  privileged directory stays owner-local and is discovered through native IPC.
- **D:** the owner-local resolver only for explicit custom DNS, at the reviewed
  regular-file or systemd-stub target. Inherited DNS needs no bind.

`/etc/ctdb` is the stable recipe layout from
`$SNAP_COMMON/smb/.conf/ctdb`, not a private runtime mount. `/var/lib/ctdb` remains
scratch tmpfs; all three native database classes remain explicitly owner-local.
Resource aliases use the existing `/usr/share/ctdb` layout, avoiding revision
paths that later snap garbage collection can remove. Go prepares these aliases;
startup validates resources and the minimal enabled event set without repair.

Snapd owns each service and its application profile. Stock unshare/slave setup
and a fixed literal body end in native exec: no forked supervisor, self-reexec,
role dispatcher, startup broker or private `.exec` family. CTDB, Samba and
Winbind have restart backoff so missing boot/upgrade prerequisites do not exhaust
the default start burst before preparation becomes available.

## Go-owned native operations

| Operation | Execution / interpretation | View and limits |
| --- | --- | --- |
| Explicit Start/Stop/intent | Bounded snapctl through service methods; Stop confirms inactivity | Existing daemon snapctl access; 1-minute client budget, caller deadline wins |
| Configuration refresh | Check enablement; stop/confirm user-mode smbd, import SAM, recheck intent, request restart without enable/disable flags | No CTDB service control during ordinary refresh |
| Config inspect/render, testparm, CephFS data check | Existing Go-owned binding/preparation calls | Explicit input/config paths, not a serving process |
| Account import | Existing `native.py accounts` binding, before smbd start/restart | N and optional D; 30-second startup preparation budget; configured PDB CTDB socket remains explicit |
| CTDB identity and health | Fixed `pnn`, numeric `nodestatus`, `status` clients, parsed in Go | C; 5-second control budgets |
| Membership | Publish cached desired nodes, validate survivors/retired slots, reload and verify observed membership | C; existing 25-second update budget; no helper socket/PID proof |
| AD inventory/records | Native `ctdb -t 5 getdbmap` before `pfetch` | C; non-creating inventory before record access |
| Initial AD join | Durable sole SQL attempt, then fixed native binding/Joiner and private stdin | N+C+W and optional D; 60-second join budget; ambiguous outcomes never authorize retry |
| Winbind readiness | `wbinfo --ping`, `--check-secret`, domain SID and group mapping | W; 12-second AD readiness envelope |
| NSS readiness | Native `getent group <numeric-gid>` | N+W; actual private NSS path, not a substitute query |
| Samba readiness | PID/signal-0 plus configured `smbcontrol ... ping` | Existing owner-local access and intra-snap signaling |
| Proxy/exporter readiness | Owner-local Unix socket / loopback HTTP with nonempty successful response | Existing access; no CTDB runtime bind for exporter metadata |

A fixed-native-client constructor uses `SysProcAttr.Cloneflags = CLONE_NEWNS`.
It never uses `Unshareflags`, changes daemon CWD/environment/namespace, launches
a serving binary or falls back to the daemon's ordinary view. The child sources
the same mechanical library used by service startup, establishes only its fixed
view, then execs the native client. Command selection and policy remain in Go.

Whole transient process groups are canceled, with a bounded wait. Thread-bound
`Pdeathsig` is not used as process-tree cleanup. Existing systemd daemon
control-group cleanup covers descendants after daemon failure; independent
service units are not members of that group. A cancellation or lost reply is
not evidence that AD or snapd rolled back an already-issued action.

Go-created children validate existing runtime mountpoints but do not create
host `/run` directories. Service startup may create vetted empty stubs under
its existing app grant, rejecting symlinks, unsafe modes/ownership and foreign
contents. Directory creation itself is not namespace-isolated.

## Exact daemon permission delta

The recipe adds only these reviewed plugs to the daemon, retaining its existing
`ctdb-socket` grant:

- `smb-control-mounts`: seven owner-scoped bind declarations for passwd, group,
  nsswitch, the resolver and reviewed stub alternative, CTDB run directory and
  public Winbind leaf.
- `smb-control-access`: read `/run/ctdb` and `/run/samba`; write only the public
  `/run/samba/winbindd/pipe` endpoint.

No daemon `network-control`, `smb-identity`, `process-control`, `ctdb-run` or
`winbind-run` is added. Serving identity switching remains in Samba/Winbind.
The pinned PassDBLoader/non-server Samba shim is not justification for copying
server identity grants. Native command traces under the target profile remain
an acceptance gate, not a reason to preemptively broaden permissions.

Generated policy is broader than the YAML pathname list alone:

- Mount-control adds mount/unmount syscalls, utility execution, unmount rules,
  mountinfo access and mount-table bookkeeping access.
- System-files directory reads expand recursively to `rk`; writes expand to
  `rwkl`. Grants apply to the parent daemon profile too. Child-only use is a
  launcher invariant, not namespace-specific AppArmor enforcement.
- Clone alone does not isolate shared propagation. Each bind rejects unsafe
  source/destination mount ancestry. Native mount uses `--no-mtab
  --internal-only --bind` with `LIBMOUNT_FORCE_MOUNT2=always`; no mount-table
  update or new mount-API expansion is selected.

These changes are in source only. Local unsigned installations need explicit
connections documented in [the native SMB guide](../snap/how-to/configure-native-smb.rst).

## Prepared-state checks

Go owns preparation policy in the existing `config/context.json`. Sourced
startup code mechanically validates owner, stop intent, types, prepared flags,
required native record keys, layout ownership and safe private context. It does
not query SQL, render configuration, import accounts, join, repair or supervise.

Only authorized first Winbind initialization receives trust-only requirements.
Retained Winbind/smbd starts require the workgroup-qualified machine-password
and SID records plus NUL-terminated autorid `CONFIG` (`0x434f4e46494700`).
`getdbmap` is checked before `pfetch`, which can otherwise create/attach a missing
database. Record values never become command lines or public errors.

Go fences its allocating SID-to-GID request before issuing it. A failed NSS
check, uncertain reply, stale SQL initialization value or surviving marker cannot
downgrade future requirements. Readiness cannot promote an unprepared AD
projection. Configuration publication retains the durable fence; membership-only
publication preserves prepared services. Independent restart uses this prepared
data, not a live daemon listener.

Stop publishes intent and drains membership work while stopping consumers.
Both periodic jobs honor cancellation while waiting for locks. Native startup
continues to enforce its own required-record checks while the daemon is down.
Native backend allocation, damaged-record behavior and real restart/reboot
retention still need target qualification.

## Readiness evidence is not a test trace

Fixture labels such as `ready ctdbd` record invocation order only. They are not
upstream output, a health signal or proof that a readiness call succeeded.
Production health depends on native command results and parsed state.

Pinned CTDB exposes `-X` machine-readable node fields, and `nodestatus` returns
node flags as its exit status. Machine-readable `status` returns before querying
recovery mode and omits total slot count, including deleted slots. Replacing the
full current status check with that format would weaken recovery/slot-history
checks. The narrow human-status parser therefore remains explicit and tested;
a future structured-output change must preserve those facts, not merely remove
string matching.

## Established packaging precedents and retained private execution

**Decision:** retain the current owner-private CTDB, Winbind and NSS views,
including CTDB rather than migrating it to a host-owned endpoint. The user
accepted this after reviewing the standard packaging alternatives and host
Winbind implications. No host-endpoint prototype, additional app, privilege
broker or permission expansion is selected. This retains the source boundary;
it is not target qualification or proof of coexistence with host Samba.

- [Snapcraft command chains](https://github.com/canonical/snapcraft/blob/20b089f54184e047e14e104ad02b2b6f603ec2d5/snapcraft/models/project.py#L881-L890)
  are explicitly setup wrappers before an application's command.
- [Snap security policy documentation](https://github.com/canonical/snap-docs/blob/6ecc7bb7760f0dc8eae7d6ac4e2f83280f5dfc37/docs/explanation/security/security-policies.md)
  specifies command-specific AppArmor/seccomp policy, but a mount namespace
  shared across the snap's commands. Merely executing another app's wrapper is
  not evidence of a confinement transition or per-app filesystem view.
- [Layout requirements](https://github.com/canonical/snapcraft/blob/20b089f54184e047e14e104ad02b2b6f603ec2d5/docs/reference/layouts.rst)
  describe the standard compiled-path solution and exclude `/run` and `/var/run`
  targets. The local validator also rejected the required `/run/ctdb` layout.
- **MicroOVN is a concrete strict-snap wrapper example.** Its
  [Go client](https://github.com/canonical/microovn/blob/25d6cfa30e610c0e58c15f3966ba7d37b4affc1b/microovn/ovn/cmd/commands.go#L235-L239)
  directly invokes an ordinary
  [command wrapper](https://github.com/canonical/microovn/blob/25d6cfa30e610c0e58c15f3966ba7d37b4affc1b/snapcraft/commands/ovn-nbctl),
  which loads
  [supported runtime-path environment](https://github.com/canonical/microovn/blob/25d6cfa30e610c0e58c15f3966ba7d37b4affc1b/snapcraft/ovn.env)
  and execs the native client. It does not demonstrate a daemon-to-other-app
  privilege switch. LXD's command chains also coexist with `lxd-support`; those
  broader privileges are not justification to copy its policy here.

Use ordinary wrappers, supported configuration and layouts wherever they meet
SMB's constraints. The pinned CTDB/NSS fixed-path views remain explicit private
mapping exceptions. No documented cross-app privilege bridge was established by
these references; do not hide the retained boundary behind a renamed command or
claim that a recipe entry alone solves it. Lookup copies and exact revisions are under
`/tmp/microceph-smb-approved-cutover/precedents`.

### Runtime paths: alternative evaluated, not selected

[WireGuard encountered the same fixed `/var/run` problem](https://forum.snapcraft.io/t/access-to-var-run-for-confined-snap/7745).
The discussion identifies relocation/upstream support or an appropriate
interface for the real host path; the snapd security guidance explicitly names
`/run/snap.<snap name>/` as the snap-specific runtime directory. Current default
AppArmor policy permits that directory. A concrete maintained example is
[Docker's wrapper](https://github.com/canonical/docker-snap/blob/097186a0dda9f84f83eed009e636c7856776bba7/bin/dockerd-wrapper#L55-L81):
it uses `/run/snap.$SNAP_INSTANCE_NAME` and passes native `--exec-root` and
`--pidfile` options, rather than remapping those paths per client.

This does **not** establish custom child namespaces as Snapcraft's recommended
solution. The CTDB mapping is required by our chosen owner-local socket location
and the staged binary's fixed lookup, not by an inherent snap requirement.
The recipe already declares `ctdb-run` and `ctdb-socket` system-files access to
the real `/run/ctdb` directory/socket. Using that endpoint directly is a candidate
standard interface-backed route, not an implemented or qualified migration.
The bounded CTDB-only source/local evaluation gave a **conditional GO** for
feasibility, not a drop-in change or installed acceptance. The user subsequently
chose to retain private CTDB views; this alternative is closed, not pending
implementation. The snapd 2.78 policy generator covers
the declared server-directory and daemon-socket accesses; no added daemon
network-control or mount grant is indicated for ordinary CTDB clients. Retained
databases can remain owner-local, and the stable `/etc/ctdb` layout remains useful.

That alternative would require **exclusive use of the real host `/run/ctdb`**,
with startup, restart and client ownership checks. Pinned `ctdb_daemon.c:1195` calls
`sock_clean`, which unconditionally unlinks the existing socket before bind.
Root UID, expected filename and mode are not ownership proof; a failed connect
is not permission to delete a stale foreign endpoint. Existing local owner/PNN
checks do not establish the peer's process identity. Requiring an empty directory
on every launch would instead break recovery from a stale socket after a crash.
A preflight is not protection against an uncooperative concurrent host root
process, so coexistence with another host CTDB cannot be promised.

Reopening this alternative would require updating Samba's rendered `ctdbd socket`, all shared startup
consumers and both ordinary/AD CTDB client paths together. Preserve bounded
execution, private diagnostics, stop fencing, independently managed startup and
owner-local databases. Remove only the CTDB mapping/grant when unused; Winbind,
NSS and optional custom DNS remain separate. Test stop/owner handoff and
upgrade/revert, including an old revision's empty-mountpoint checks against
leftover new host endpoints. No claim/cleanup protocol or host-path takeover was
selected, and no implementation or installed policy changed in this evaluation.

Evidence under `/tmp/microceph-smb-approved-cutover/ctdb-direct-path`: five local
Unix-socket semantics probes passed, source/recipe assertions passed, and derived
socket path rules parsed with `apparmor_parser -Q -K` without loading. These are
not native CTDB runs or confinement tests. The human-readable `EVALUATION.md`
records the ownership matrix and migration surface; production source hashes
are retained there. Target core26 native operation, crash/daemon-down restart
and actual ownership/rollback behavior still require qualification.

Do not assume newer documentation's `mount-control` `namespace: snap` attribute
is available on the target: inspection of the **2.78 tag** found no such handling
in its interface or `snapctl mount` implementation, which creates systemd mount
units. It is not an established snap-private `/run/ctdb` layout substitute for
this target. Lookup sources are retained with the other precedents.

## Verification and remaining gates

Local evidence is collected under `/tmp/microceph-smb-approved-cutover`:

- Both MicroCeph binaries build; command/package wiring contains only those two
  owned targets. Main/hooks/common startup and Robot/CI match the baseline.
- Full Go unit tests pass: 390 top-level tests, two skips. The complete `ceph`
  package race run passes: 215 top-level tests, two skips. The full
  repository race run fails in unchanged `logger.TestLogConcurrent`; the same
  failure reproduces with that test alone. It is not repaired in this change.
- Pinned Sambacc Python/startup/packaging tests: 37 passed, two exact-package
  skips. Bash/Shellcheck, vet, lint (zero issues) and the real skeleton metadata
  validator pass.
  Fixture-native executables and simulated kernel primitives do not establish
  installed Samba/CTDB, strict confinement or SMB I/O compatibility.
- The earlier daemon-derived seccomp probe allowed Go clone/new-mount-namespace
  creation while direct unshare remained denied. Private binds left the parent
  namespace/view unchanged; negative propagation/isolation controls failed.
  Candidate AppArmor parsed offline but was not loaded.

The host validator is snapd 2.77.1 on Ubuntu 24.04; the artifact declares core26
and snapd 2.78. Target validation must cover the actual utility/library closure,
new daemon policy, first AD setup, uncertain outcomes, missing required records,
independent daemon-down service restart, upgrade/reboot and SMB I/O. The two
exact-native-package Python cases remain skipped locally. No snap build,
installation, deployment, lab operation or commit occurred in this refactor.

Robot/CI still has `ctdb-nodes` unit expectations in its separate ownership
scope; update those before integration qualification.

## Lookup sources

- Samba/CTDB/Winbind `2:4.23.6+dfsg-1ubuntu2.2`: production path/config/CLI tables,
  `wb_common.c`, `idmap_autorid.c`, PassDBLoader and non-server `smbd_shim`.
- Sambacc 0.9, exact wheel hash in `snapcraft/smb/requirements.txt`.
- Snapd `7fde758df6518848072deefc5a633e09f4e8dc34`: mount-control/system-files,
  seccomp template, mount namespace setup and service control-group generation.
- Go 1.27 Linux fork/exec source: `Cloneflags` versus `Unshareflags` semantics.
- Mechanism/policy evidence: `/tmp/microceph-smb-execution-analysis.WJc8p7`;
  earlier DNS namespace controls: `/tmp/microceph-smb-boundary-cleanup.tsqHln`.

These source/local mechanism checks do not replace qualification of the built
core26/snapd 2.78 artifact or unrelated retained-lab results.

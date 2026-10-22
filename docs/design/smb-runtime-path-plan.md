# Native SMB service and path boundaries

Status: source cutover implemented in `refactor/smb-runtime-component`, based on
AD baseline `955ef872823d3f6699ace619036dec86482a482c`. This is **not target
strict-snap qualification**. The isolated worktree is
`../microceph-smb-runtime-refactor`; Robot/CI belongs to another owner.

## Already done

- Removed `cmd/microceph-smb-runtime`, its Makefile/build/strip/prime entries,
  helper IPC and the helper-only `ctdb-nodes`/`smb-remote` snap applications.
- Kept the five upstream services independently supervised by snapd: Samba,
  CTDB, Winbind, the CephFS proxy and the exporter. Their individual startup
  scripts finish with a fixed native exec, not a resident wrapper.
- Kept preparation, typed native-client operations, account import, membership
  maintenance and authorized AD joining in `microceph/ceph`.
- Retained the existing `ceph.Start`/`startSMBUpdater` boundary. Main, microcluster
  hooks and common Ceph startup are unchanged. Cancellation stops internal
  maintenance, not healthy snap-managed services.
- Added the two reviewed daemon control plugs to the recipe, without copying
  service `network-control`, identity-switching or runtime-directory grants.
  No installed snap connections or loaded profiles have been changed.

The five-service table is an internal Start/Ready/Stop dispatch table, not a
supervisor. Start enables the selected service; Stop disables it and confirms
inactivity. Configuration refresh preserves enablement and stop intent. User
SAM preparation runs with smbd stopped before requesting its restart. Unknown
service names are rejected before execution.

## Path decisions

Use recipe layouts first, supported upstream configuration second, and private
runtime mappings only where those alternatives cannot preserve behavior. Never
patch upstream packages or use their self-test path overrides in production.

The user chose to retain the current owner-private CTDB, Winbind and NSS views
after reviewing direct host-runtime alternatives. No host-endpoint migration
or prototype is selected. This does not establish coexistence with host Samba:
current startup still refuses unrelated state in runtime mountpoint targets.

`R` means `$SNAP_COMMON/smb/<validated-cluster-id>`.
`L` means the stable `$SNAP_COMMON/smb/.conf/ctdb` directory.

| Path | Selected source and scope | Reason / invariant |
| --- | --- | --- |
| `/etc/ctdb` | Recipe bind from `L` | Pinned CTDB's production config loader uses its compiled path. Go publishes owner-bound configuration atomically. The directory inode is never replaced; consumers are stopped before owner transitions. |
| `/var/lib/ctdb` | Existing recipe tmpfs, scratch only | All volatile, persistent and state database directories remain explicit under `R/ctdb`. No owner-local runtime overlay or alternate persistent-state home. |
| `/usr/share/ctdb` | Existing recipe symlink to the current snap's resources | Prepared `R/ctdb` resource aliases resolve through this layout, not through a revision path that can be garbage-collected. Only `00.ctdb.script` is enabled. |
| `/etc/passwd` | Private bind from `R/config/passwd` | Owner-local Unix identities, never a global NSS layout or host-account edit. |
| `/etc/group` | Private bind from `R/config/group` | Published atomically with the identity projection; fresh service contexts see the new inode. |
| `/etc/nsswitch.conf` | Private bind from `R/config/nsswitch.conf` | `files`, or `files winbind` for AD. Other applications and the daemon keep their normal NSS view. |
| Resolver | Inherited unless custom DNS is explicit; otherwise private bind from `R/config/resolv.conf` | Only `/etc/resolv.conf` or its reviewed systemd stub target is permitted. No whole resolver-directory bind. |
| `/run/ctdb` | Private bind from `R/ctdb/run` | Pinned CTDB has no supported production runtime/socket override. Do not use `CTDB_TEST_MODE`/`CTDB_SOCKET`. Clients with supported explicit socket configuration keep it. |
| `/run/samba/winbindd` | AD-only private bind from `R/samba/winbindd` | The native NSS client uses its compiled public endpoint. Never bind all of `/run/samba` or alias the owner-local privileged pipe into a host path. |

A global bind-file would retain the old inode after atomic publication. That is
why NSS and optional DNS remain private execution-context mappings rather than
snap-wide layouts. Retained Samba/CTDB identities and databases are not moved.

The proxy keeps its owner-local working directory and explicit socket argument.
The exporter keeps explicit `SMBMETRICS_*` inputs. Its offline `ctdb version`
query does not justify a CTDB runtime bind.

## Service versus transient-client execution

Snapd starts each service under that application's existing profile. Services
needing private mounts use stock `unshare --mount --propagation slave`, a fixed
literal setup body and native exec. Inherited-DNS proxy/exporter startup needs
no namespace. Startup validates prepared inputs and safe mountpoints; it does
not render configuration, import accounts, join AD, query SQL, control services,
repair data or monitor a running process.

Transient daemon clients use `SysProcAttr.Cloneflags = CLONE_NEWNS`, a bounded
process group and the same mechanical setup library. They never change daemon
environment, CWD or namespace and never fall back to its ordinary namespace.
Source/destination ancestry is checked for shared propagation before each bind.
The daemon consumes existing runtime mountpoint stubs; it cannot create them.

Mounts use `--no-mtab --internal-only --bind` and
`LIBMOUNT_FORCE_MOUNT2=always`. Neither mount-table writes nor new mount-API
permission expansion is selected. Process-group cancellation bounds transient
commands; native serving processes are never their children. Daemon failure
cleanup relies on its existing systemd control group, not thread-bound
`Pdeathsig` or a new stop hook.

See [execution/confinement accounting](smb-runtime-execution-analysis.md) for
operations, grants, evidence and qualification limits.

## Prepared state and AD safety

Go extends the existing `config/context.json` with per-role prepared flags and
required native record keys. There is no second startup manifest, checksum
protocol, private command family or live control socket.

- User SAM import completes in Go before smbd is marked prepared.
- AD joining consumes the durable SQL attempt before the external operation.
  Ambiguous failures retain that attempt and require manual maintenance, not
  an automatic rejoin.
- Only authorized first Winbind initialization can receive trust-only startup
  requirements. Smbd and retained Winbind starts require the trust records and
  NUL-terminated autorid `CONFIG` record selected by Go.
- Native database inventory uses non-creating `getdbmap` before `pfetch`, which
  otherwise could attach/create a missing database.
- Go tightens the durable initialization fence before its allocating SID-to-GID
  request, not after an uncertain reply. A stale SQL initialization result or
  surviving initialization marker cannot weaken that fence.
- AD readiness cannot turn an unprepared publication into prepared startup
  data. Membership-only publication preserves consumers; configuration changes invalidate
  their preparation without downgrading initialized state.
- Completed membership publication wakes reconciliation immediately. A
  30-second fallback covers recovery without a two-second full-health sweep;
  unchanged reconciliation needs one status query. Failed/no-op publication
  emits no completion hint. Hints are coalesced, not a new persistent protocol.
- Stop intent is published before draining membership work. Actor cancellation
  is cooperative even while a placement/stop lock is held.

Independent restart consumes durable prepared state without a daemon handshake.
Missing required trust/idmap state is refusal, not authorization to reconstruct
it. Full native-backend initialization and retained-state loss behavior still
require the target qualification below.

## Pending acceptance

- Build and qualify the complete strict snap on core26/snapd 2.78, including
  actual native clients under the new daemon profile, staged utility closure,
  independent service restarts while the daemon is down, reboot retention,
  membership retirement and AD uncertain-outcome/lost-record cases.
- Run the two exact-native-package Python cases; fixture-level tests do not
  establish Samba/CTDB I/O or confinement compatibility.
- Coordinate Robot/CI's removal of `ctdb-nodes` unit expectations. Those files
  were inspected for compatibility but are not edited in this worktree.
- Repository-wide race testing exposes `logger.TestLogConcurrent`; logger
  sources are unchanged and the failure reproduces in isolation. Keep that
  unrelated correction separate.

## Will not be done

No third/disguised executable, hidden command, daemon self-execution, generic
runtime broker, SMB-specific microcluster hooks or lifecycle framework. No
Go-owned serving-process supervision, automatic trust/idmap reconstruction,
blanket daemon `network-control`, global NSS layouts, upstream path patches,
private `.exec` script family or unauthorized installed-snap/lab changes.

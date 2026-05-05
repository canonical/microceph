.. _release-notes:

Release notes
=============

The following provides details on major MicroCeph releases, beginning with the MicroCeph squid release.

MicroCeph Tentacle
------------------

The Ceph team is happy to announce the release of MicroCeph v20
(tentacle). This is the first stable release in the Tentacle series of
releases.

The MicroCeph tentacle release can be installed from the tentacle/stable
track.

Highlights
~~~~~~~~~~

-  Uses Ceph 20.2.0 (tentacle)
-  Built on Ubuntu 24.04 (``core24`` snap base)
-  Upgraded to microcluster v3
-  Log rotation for the ``logs/`` directory via ``logrotate``
-  Reference architecture documentation
-  Consolidated charm-microceph and MicroCeph documentation
-  Includes all features and fixes from the squid stable cycle


Important changes
~~~~~~~~~~~~~~~~~

The snap is now built on the ``core24`` base. Hosts must be running an
Ubuntu release that supports ``core24`` snaps.

The microcluster dependency has been upgraded from v2 to v3. This is an
internal change but affects the underlying cluster database schema.


Known issues
~~~~~~~~~~~~

Upgrades from quincy directly to tentacle are not supported. Upgrade to
squid first, then to tentacle.

Erasure-coded pool users are advised that the upstream Ceph developers
identified a bug where OSDs crash when ``allow_ec_optimizations`` is
set on a pool, regardless of the ``allow_ec_overwrites`` setting,
making the cluster unusable. Read `Tracker Issue 74813
<https://tracker.ceph.com/issues/74813>`__ before enabling
``allow_ec_optimizations`` on Ceph 20.2.0.


List of pull requests
~~~~~~~~~~~~~~~~~~~~~

- `#718 <https://github.com/canonical/microceph/pull/718>`__: test: upgrade squid to tentacle
- `#714 <https://github.com/canonical/microceph/pull/714>`__: feat: add log rotation for the logs directory
- `#713 <https://github.com/canonical/microceph/pull/713>`__: docs: Add reference architecture
- `#707 <https://github.com/canonical/microceph/pull/707>`__: docs: consolidate MicroCeph charm documentation with MicroCeph docs
- `#706 <https://github.com/canonical/microceph/pull/706>`__: feat: move to MicroCeph Tentacle, upgrade cluster library to v3, and rebase on core 24
- `#698 <https://github.com/canonical/microceph/pull/698>`__: ci: add weekly health report workflow


MicroCeph Squid
---------------

The Ceph team is happy to announce the release of MicroCeph v19
(squid). This is the first stable release in the Squid series of
releases.

The MicroCeph squid release can be installed from the squid/stable
track.

Highlights
~~~~~~~~~~

-  Uses Ceph 19.2.0 (squid)
-  Support for RBD remote replication
-  CephFS remote replication (enable/disable, status, listing)
-  NFS service support via Ceph NFS Ganesha
-  Adopt existing (non-MicroCeph) Ceph clusters into MicroCeph
   management
-  Availability Zone support for OSDs
-  Cluster maintenance mode with monitor-quorum protection
-  MicroCeph orchestrator module shipped in the snap
-  DSL-based device matching for OSD, WAL, and DB selection
-  Support for modifying RGW SSL certificates at runtime
-  ``microceph waitready`` command to verify cluster readiness
-  ``stripingv2`` enabled by default in the RBD feature set
-  OSD support for many additional block device types such as NVMe,
   partitions, LVM volumes
-  Improved ipv6 support
-  Updated dependencies, based off of Ubuntu 24.04
-  Various fixes and documentation improvements


Important changes
~~~~~~~~~~~~~~~~~

For added security, MicroCeph now checks hostnames upon cluster
joining. This means that the name used when running `microceph cluster
add <name>` must match the hostname of the node where `microceph
cluster join` is being run. If the hostname does not match joining the
node will fail, and log a message `Joining server certificate SAN does
not contain join token name` to syslog.

Monitors are now enforced to use the ``v2`` (msgr2) protocol. Clients
that only support ``v1`` will not be able to connect.

The joiner address is now auto-detected from the join token peers when
running ``microceph cluster join``; manual address overrides remain
supported.


Known issues
~~~~~~~~~~~~

iSCSI users are advised that the upstream developers of Ceph encountered
a bug during an upgrade from Ceph 19.1.1 to Ceph 19.2.0. Read Tracker
Issue 68215 before attempting an upgrade to 19.2.0.

List of pull requests
~~~~~~~~~~~~~~~~~~~~~

Squid stable updates (post-v19.2.0):

- `#711 <https://github.com/canonical/microceph/pull/711>`__: fix: update Go module dependencies
- `#710 <https://github.com/canonical/microceph/pull/710>`__: fix: auto-detect joiner address from join token peers
- `#708 <https://github.com/canonical/microceph/pull/708>`__: fix: resolve references to stale paths
- `#703 <https://github.com/canonical/microceph/pull/703>`__: fix: increase disk operation timeout
- `#702 <https://github.com/canonical/microceph/pull/702>`__: fix: resolve monitor refresh loop
- `#700 <https://github.com/canonical/microceph/pull/700>`__: fix: resolve all Go static check failures and drop the previous linter
- `#699 <https://github.com/canonical/microceph/pull/699>`__: feat: add support for declarative WAL and DB device usage with execution, cleanup, and validation
- `#697 <https://github.com/canonical/microceph/pull/697>`__: feat: add support for modifying the RGW SSL certificate
- `#696 <https://github.com/canonical/microceph/pull/696>`__: fix: wait for RBD mirror health before testing disable operations
- `#695 <https://github.com/canonical/microceph/pull/695>`__: ci: cache the snap build artifact between jobs
- `#691 <https://github.com/canonical/microceph/pull/691>`__: fix: re-enable services after a snap disable and enable cycle
- `#688 <https://github.com/canonical/microceph/pull/688>`__: docs: add a database schema update guide to the developer docs
- `#687 <https://github.com/canonical/microceph/pull/687>`__: feat: add Availability Zone support
- `#684 <https://github.com/canonical/microceph/pull/684>`__: refactor: maintenance mode quality improvements
- `#683 <https://github.com/canonical/microceph/pull/683>`__: feat: add a wait-ready command to verify the cluster is ready
- `#682 <https://github.com/canonical/microceph/pull/682>`__: docs: fix a documentation link
- `#681 <https://github.com/canonical/microceph/pull/681>`__: fix: resolve "no disks present" error when adding all disks
- `#680 <https://github.com/canonical/microceph/pull/680>`__: fix: resolve missing unlock of encrypted WAL and DB at OSD start
- `#679 <https://github.com/canonical/microceph/pull/679>`__: feat: close inactive issues automatically
- `#677 <https://github.com/canonical/microceph/pull/677>`__: ci: avoid building Sphinx from source
- `#676 <https://github.com/canonical/microceph/pull/676>`__: fix: resolve unexpected loop device behaviour
- `#672 <https://github.com/canonical/microceph/pull/672>`__: fix: make device-node matching conform to the device DSL spec
- `#668 <https://github.com/canonical/microceph/pull/668>`__: feat: add declarative device matching for OSD selection
- `#661 <https://github.com/canonical/microceph/pull/661>`__: tests: functional test helper housekeeping
- `#659 <https://github.com/canonical/microceph/pull/659>`__: fix: amend the command parameter
- `#657 <https://github.com/canonical/microceph/pull/657>`__: fix: multi-monitor adopt bootstrap
- `#656 <https://github.com/canonical/microceph/pull/656>`__: fix: add content attributes for content plugs
- `#650 <https://github.com/canonical/microceph/pull/650>`__: feat: add v2 striping to the default RBD feature set
- `#646 <https://github.com/canonical/microceph/pull/646>`__: feat: add a format flag to cluster list
- `#643 <https://github.com/canonical/microceph/pull/643>`__: docs: add HTML meta descriptions
- `#642 <https://github.com/canonical/microceph/pull/642>`__: docs: add a how-to document for MicroCeph CephFS replication
- `#641 <https://github.com/canonical/microceph/pull/641>`__: docs: use a ref target for the cluster network how-to
- `#638 <https://github.com/canonical/microceph/pull/638>`__: docs: refine the get-started tutorial
- `#637 <https://github.com/canonical/microceph/pull/637>`__: docs: add remote replication explanations
- `#635 <https://github.com/canonical/microceph/pull/635>`__: fix: pin dqlite to the LTS release
- `#633 <https://github.com/canonical/microceph/pull/633>`__: docs: create a redirect for a renamed file
- `#632 <https://github.com/canonical/microceph/pull/632>`__: docs: split up the security overview
- `#631 <https://github.com/canonical/microceph/pull/631>`__: docs: add a how-to for reporting security issues
- `#630 <https://github.com/canonical/microceph/pull/630>`__: docs: split up the full-disk encryption documentation
- `#628 <https://github.com/canonical/microceph/pull/628>`__: feat: add support for enabling and disabling CephFS replication
- `#627 <https://github.com/canonical/microceph/pull/627>`__: docs: fix a documentation title
- `#626 <https://github.com/canonical/microceph/pull/626>`__: docs: move the architecture documentation
- `#625 <https://github.com/canonical/microceph/pull/625>`__: ci: increase wait time for OSDs
- `#624 <https://github.com/canonical/microceph/pull/624>`__: ci: make the OSD check more robust
- `#622 <https://github.com/canonical/microceph/pull/622>`__: feat: adopt existing Ceph clusters using MicroCeph
- `#621 <https://github.com/canonical/microceph/pull/621>`__: fix: improve pristine disk check with Ceph BlueStore tool validation
- `#619 <https://github.com/canonical/microceph/pull/619>`__: feat: expose useful Ceph tools
- `#616 <https://github.com/canonical/microceph/pull/616>`__: fix: use the Ceph BlueStore tool for wiping disks
- `#607 <https://github.com/canonical/microceph/pull/607>`__: docs: correct command invocation in client config docs
- `#606 <https://github.com/canonical/microceph/pull/606>`__: docs: fix section headings
- `#604 <https://github.com/canonical/microceph/pull/604>`__: fix: implement structured logging with persistent configuration
- `#601 <https://github.com/canonical/microceph/pull/601>`__: feat: add support for fetching CephFS mirroring status and lists to the replication framework
- `#600 <https://github.com/canonical/microceph/pull/600>`__: fix: remove unnecessary references to the client from the command
- `#599 <https://github.com/canonical/microceph/pull/599>`__: test: speed up tests
- `#594 <https://github.com/canonical/microceph/pull/594>`__: fix: list virtio block disk devices
- `#591 <https://github.com/canonical/microceph/pull/591>`__: refactor: move subprocess handling to a common package
- `#590 <https://github.com/canonical/microceph/pull/590>`__: fix: check if disks are pristine before attempting to use them
- `#588 <https://github.com/canonical/microceph/pull/588>`__: fix: add checks before adding OSD, WAL, or DB devices
- `#585 <https://github.com/canonical/microceph/pull/585>`__: feat: create only one OSD pool for NFS Ganesha
- `#584 <https://github.com/canonical/microceph/pull/584>`__: feat: add the MicroCeph orchestrator module to the snap build
- `#583 <https://github.com/canonical/microceph/pull/583>`__: docs: update documentation to include information about enabling NFS
- `#582 <https://github.com/canonical/microceph/pull/582>`__: refactor: add an OSD manager to improve testing
- `#578 <https://github.com/canonical/microceph/pull/578>`__: feat: add CephFS mirror to the service placement interface
- `#575 <https://github.com/canonical/microceph/pull/575>`__: fix: prevent enabling snapshot replication on RBD pools
- `#574 <https://github.com/canonical/microceph/pull/574>`__: feat: add NFS support
- `#573 <https://github.com/canonical/microceph/pull/573>`__: docs: update disk add documentation
- `#572 <https://github.com/canonical/microceph/pull/572>`__: docs: migrate to the extension-based starter pack
- `#567 <https://github.com/canonical/microceph/pull/567>`__: feat: enforce v2 for monitors
- `#565 <https://github.com/canonical/microceph/pull/565>`__: feat: ensure a majority of monitor services remain available before entering maintenance mode
- `#545 <https://github.com/canonical/microceph/pull/545>`__: docs: MicroCloud integration annotations

Initial v19.2.0 release:


- `#467 <https://github.com/canonical/microceph/pull/467>`__: Fix: increase timings for osd release
- `#466 <https://github.com/canonical/microceph/pull/466>`__: Adjust ‘verify_health’ iterations
- `#464 <https://github.com/canonical/microceph/pull/464>`__: Test: upgrade update
- `#463 <https://github.com/canonical/microceph/pull/463>`__: Fix: add python3-packaging
- `#462 <https://github.com/canonical/microceph/pull/462>`__: Test: upgrade reef to local build
- `#461 <https://github.com/canonical/microceph/pull/461>`__: Test: add reef to squid upgrade test
- `#460 <https://github.com/canonical/microceph/pull/460>`__: Improve require-osd-release
- `#459 <https://github.com/canonical/microceph/pull/459>`__: Set the ‘require-osd-release’ option on startup
- `#458 <https://github.com/canonical/microceph/pull/458>`__: Updated readme.md
- `#457 <https://github.com/canonical/microceph/pull/457>`__: Modify post-refresh hook to set OSD-release
- `#456 <https://github.com/canonical/microceph/pull/456>`__: Make remote replication CLI conformant to CLI guidelines
- `#454 <https://github.com/canonical/microceph/pull/454>`__: Pin LXD and use microcluster with dqlite LTS
- `#447 <https://github.com/canonical/microceph/pull/447>`__: Update mods, build from noble
- `#443 <https://github.com/canonical/microceph/pull/443>`__: Bootstrap: wait for daemon
- `#441 <https://github.com/canonical/microceph/pull/441>`__: Build from noble-proposed
- `#440 <https://github.com/canonical/microceph/pull/440>`__: Remove tutorial section
- `#438 <https://github.com/canonical/microceph/pull/438>`__: MicroCeph Remote Replication (3/3): Site Failover/Failback
- `#437 <https://github.com/canonical/microceph/pull/437>`__: MicroCeph Remote Replication (2/3): RBD Mirroring
- `#433 <https://github.com/canonical/microceph/pull/433>`__: Docs: fix indexes
- `#432 <https://github.com/canonical/microceph/pull/432>`__: Use square brackets around IPv6 in ceph.conf
- `#430 <https://github.com/canonical/microceph/pull/430>`__: Adds support for RO cluster configs
- `#429 <https://github.com/canonical/microceph/pull/429>`__: Move mounting CephFS shares tutorial to how-to section
- `#428 <https://github.com/canonical/microceph/pull/428>`__: Move mounting RBD tutorial to how-to section
- `#427 <https://github.com/canonical/microceph/pull/427>`__: Move multi-node tutorial to how-to section
- `#426 <https://github.com/canonical/microceph/pull/426>`__: Move multi-node tutorial to how-to section
- `#422 <https://github.com/canonical/microceph/pull/422>`__: Change tutorial landing page
- `#419 <https://github.com/canonical/microceph/pull/419>`__: Change explanation landing page
- `#418 <https://github.com/canonical/microceph/pull/418>`__: Add CephFS to wordlist
- `#417 <https://github.com/canonical/microceph/pull/417>`__: Move MicroCeph charm to explanation section
- `#416 <https://github.com/canonical/microceph/pull/416>`__: Fix reference landing page
- `#415 <https://github.com/canonical/microceph/pull/415>`__: Move single-node tutorial to how-to section
- `#409 <https://github.com/canonical/microceph/pull/409>`__: Fetch current OSD pool configuration over the API
- `#407 <https://github.com/canonical/microceph/pull/407>`__: Add interfaces: rbd kernel module and support
- `#405 <https://github.com/canonical/microceph/pull/405>`__: MicroCeph Remote Replication (1/3): Remote Awareness
- `#401 <https://github.com/canonical/microceph/pull/401>`__: doc: remove ``woke-install`` as prereq for building the docs
- `#400 <https://github.com/canonical/microceph/pull/400>`__: doc: remove ``woke-install`` as prereq for building the docs
- `#398 <https://github.com/canonical/microceph/pull/398>`__: MicroCeph Remote Replication (2/3): RBD Mirroring
- `#395 <https://github.com/canonical/microceph/pull/395>`__: Use LTS microcluster

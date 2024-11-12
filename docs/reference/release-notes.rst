Overview
========

The Ceph team is happy to announce the release of MicroCeph 19.2.0
(squid). This is the first stable release in the Squid series of
releases.

The MicroCeph squid release can be installed from the squid/stable
track.

Highlights
==========

-  Uses Ceph 19.2.0 (squid)
-  Support for RBD remote replication
-  OSD support for many additional block device types such as NVMe,
   partitions, LVM volumes
-  Improved ipv6 support
-  Updated dependencies, based off of Ubuntu 24.04
-  Various fixes and documentation improvements

Known Issues
============

iSCSI users are advised that the upstream developers of Ceph encountered
a bug during an upgrade from Ceph 19.1.1 to Ceph 19.2.0. Read Tracker
Issue 68215 before attempting an upgrade to 19.2.0.

List of pull requests
=====================

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

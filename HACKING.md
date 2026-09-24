# MicroCeph Hacking Guide

Wait! No-one's "hacking" anything. The aim of this document is to enable new developers/users get familiarised with the build tools and MicroCeph codebase so that they can build and contribute to the [MicroCeph Codebase](https://github.com/canonical/microceph).

## Table of Contents

1. [⚡️ Introduction to snaps](#⚡️-introduction-to-snaps)
2. [🧰 Tools](#🧰-tools)
3. [📖 References](#📖-references)
4. [🏭 Build Guide](#🏭-build-guide)
5. [👍 Unit-Testing](#👍-unit-testing)
6. [DB Schema Updates](#db-schema-updates)
7. [RGW placement (in development)](#rgw-placement-in-development)

## ⚡️ Introduction to snaps

> A [snap](https://snapcraft.io/about) is a bundle of an app and its dependencies that works without modification across Linux distributions.

Apart from being a self-contained artifact, being a snap enables MicroCeph to be isolated from host and provide a clean and consistent building, installing and cleanup experience.

The microceph snap packages all the required ceph-binaries, [dqlite](https://dqlite.io/) and a small management daemon (microcephd) which ties all of this together. Using the light-weight distributed dqlite layer, MicroCeph enables orchestration of a ceph cluster in a centralised and easy to use manner.

## 🧰 Tools

Snaps are built and published using another snap called [snapcraft](https://snapcraft.io/snapcraft). It uses [lxd](https://snapcraft.io/lxd) to pull dependencies and build an artifact completely isolated from the host system. This makes it easier for developers to work on MicroCeph without polluting their host system with unwanted dependencies.
You can install snapcraft and lxd using snap tool.

```bash
sudo snap install snapcraft --classic
sudo snap install lxd
```

> [!NOTE]
> For a detailed how-to-use snapcraft tool guide, check-out [Snap-Tutorials](https://snapcraft.io/docs/snap-tutorials)

## 📖 References

The MicroCeph codebase resides inside the microceph sub-directory of the repo. This subdir is neatly organised into parts which make up the whole of MicroCeph. Below is a brief description of what can be found in each of those parts for reference.

### Sub Directories

1. **[api](/microceph/api)**

    Contains files related to the internal REST APIs which the microceph client uses to communicate to the microceph daemon. It also contains a types subdir which has necessary data structures for the APIs.

2. **[ceph](/microceph/ceph)**

    Contains files directly related to ceph orchestration. This includes code for ceph cluster configuration, service orchestration etc.

3. **[client](/microceph/client)**

    Contains REST client code which is used by microceph CLI for interacting with microceph daemon.

4. **[cmd](/microceph/cmd)**

    Contains microceph CLI commands written with [Cobra Commands](https://github.com/spf13/cobra)

5. **[common](/microceph/common)**

    Contains common code

6. **[database](/microceph/database)**

    Contains DQlite schema and migration definitions along with generated mappers for DB interfacing.

7. **[mocks](/microceph/mocks)**

    Contains mock definitions for unit testing

## 🏭 Build Guide

Building MicroCeph is as easy as a snap!

```bash
# v for verbose output of the build process.
snapcraft -v
...
Creating snap package
...
Created snap package microceph_0+git.ac1da26_amd64.snap
```

The newly created .snap artifact can then be installed as

```bash
# Dangerous flag for locally built snap
sudo snap install --dangerous microceph_*.snap
```

```bash
# Locally built snaps do no auto-connect the available plugs on install, they can be connected manually using;
sudo snap connect microceph:block-devices
sudo snap connect microceph:hardware-observe
sudo snap connect microceph:mount-observe
sudo snap connect microceph:load-rbd
sudo snap connect microceph:microceph-support
sudo snap connect microceph:network-bind
sudo snap connect microceph:process-control
sudo snap connect microceph:dm-crypt
sudo snap restart microceph.daemon

```

## 👍 Unit-Testing

The MicroCeph [Makefile](/microceph/Makefile) has targets for running unit tests and lint checks. However, you will need the following packages or tool to run them locally.

```bash
# Add general requirements
sudo apt install gcc make shellcheck

# Add libdqlite-dev, required for building microceph
# you may need a specific version such as libdqlite1.17-dev
sudo add-apt-repository ppa:dqlite/dev -y
sudo apt install -y libdqlite-dev

# Install go and export the binary to PATH
sudo snap install go --classic
export PATH=$PATH:$HOME/go/bin
```

Once you install the prerequisite, you can run unit tests and lint checks as follows:

```bash
cd microceph

# Run unit tests
make check-unit

# Run static checks
make check-static
```

## DB Schema Updates

MicroCeph uses [lxd-generate](https://pkg.go.dev/github.com/canonical/lxd/lxd/db/generate) to auto-generate database mapper code (SQL statements and Go CRUD functions) from struct definitions. When adding a new database entity or modifying `//go:generate` directives, you need to regenerate the corresponding `.mapper.go` file.

### Installing lxd-generate

Install the generator matching the version of `github.com/canonical/lxd` in `go.mod`:

```bash
# Check the pinned version
grep 'canonical/lxd ' go.mod

# Install it (replace the version hash as needed)
go install github.com/canonical/lxd/lxd/db/generate@v0.0.0-20241106165613-4aab50ec18c3

# The binary installs as "generate" — create a symlink with the expected name
ln -s ~/go/bin/generate ~/go/bin/lxd-generate

# Ensure ~/go/bin is in your PATH
export PATH="$HOME/go/bin:$PATH"
```

### Running the generator

Target the specific file:

```bash
cd microceph
go generate ./database/host_tag.go
```

This reads the `//go:generate mapper` directives in the source file and produces the corresponding `.mapper.go` file with SQL statements and Go functions.

## RGW placement (in development)

MicroCeph can place the RADOS Gateway (RGW) on cluster members from the placement policy sent to `PUT /1.0/placement`. Each member entry in that policy can carry an `rgw` object. This is still being built, so it has no page in the published documentation yet. Until it does, this section is the reference. Keep it current when you change the behaviour.

### The `rgw` object

`mode` is required by the placement API and is always `reconcile`.

```json
{
  "mode": "reconcile",
  "members": {
    "node-a": { "rgw": { "enabled": true, "ssl": false, "port": 8080 } },
    "node-b": { "rgw": { "enabled": true, "ssl": true, "ssl_certificate": "<base64 PEM>", "ssl_private_key": "<base64 PEM>" } },
    "node-c": { "rgw": { "enabled": false } }
  }
}
```

| Field | Meaning |
|---|---|
| `enabled` | Required. `true` runs a gateway on the member, `false` removes it. |
| `ssl` | Required when `enabled` is true. TLS is never guessed from the certificate fields. |
| `port` | Plaintext port. Defaults to 80 without TLS. With TLS it is optional: set it to serve plaintext as well. |
| `ssl_port` | TLS port. Defaults to 443. |
| `ssl_certificate`, `ssl_private_key` | Base64 PEM. Write-only: sent to the member, never stored in the database, never returned by `GET`. |

### Rules that hold today

- **A member with no `rgw` object is not touched.** Nothing reconciles between `PUT`s. An empty policy and `DELETE /1.0/placement` stop no gateway.
- **The shape is strict.** A bare boolean `rgw`, a missing `enabled` or `ssl`, a certificate without its key, and the same port for both listeners are all rejected before anything changes.
- **`ssl: true` without a certificate and key means reuse.** The member keeps the pair it already has. If it has no usable pair the request fails. It never falls back to plaintext.
- **Additions come before removals.** Within one request every gateway addition is tried, then the removals. If any addition fails, all removals wait, so a migration never stops the old gateway before the new one serves. Gateways can scale to zero.
- **`rgw_frontend` in `GET /1.0/placement` is the last applied setting,** read from the `rgw_frontends` table. It is not a health check. If it is absent, the setting is unknown, not plaintext.
- **One owner per member.** Do not use `microceph enable rgw` or `microceph disable rgw` on a member whose policy entry has an `rgw` object. The next `PUT` applies the policy again without warning. `microceph certificate set rgw` is safe and never changes the policy. After `certificate set rgw` without `--restart`, the next `PUT` that names the member restarts its gateway unless the operator has already done so.
- **Applying is safe to repeat.** The same settings cause no restart. A failed change puts the previous config and gateway back. A first gateway start can use the whole 2 minute readiness wait, and the call to each member times out after 5 minutes.

### What a `PUT` returns for RGW

| Status | Meaning |
|---|---|
| 200 | Stored, and every gateway change succeeded. |
| 400 | Invalid `rgw` object or certificate, unknown member, or a target member that does not list `placement-rgw` in `GET /1.0/cluster/capabilities`. |
| 409 | Another apply is in progress. |
| 500 | A gateway could not be enabled or disabled. The message names each member and keeps any other refusal from the same request. |
| 503 | The cluster is partly refreshed. |

### On the member

| File | Role |
|---|---|
| `/var/snap/microceph/current/conf/radosgw.conf` | Once the file exists, only the `rgw frontends` line is rewritten, so a monitor list refresh made at the same time survives. The first enable writes the whole file. |
| `/var/snap/microceph/current/conf/radosgw.conf.pending` | Holds the previous config while a change is in flight, and after `certificate set rgw` without `--restart` until the gateway is restarted. If it is present at the next apply, the gateway is asked which pair it serves. A gateway that already serves the pair in `radosgw.conf` was restarted by hand, so the file is stale and is dropped, and a failed change goes back to `radosgw.conf`. Otherwise the gateway is restarted rather than trusted, and a failed change goes back to the config the file holds. |
| `/var/snap/microceph/common/rgw-tls/<hash>/server.crt`, `server.key` | One directory per certificate and key pair, named by a hash of both. The config switches to a directory only once both files are complete. Old directories are removed once the apply commits: after the database row on a `PUT`, or right after `certificate set rgw --restart`. |

### Upgrades

- A policy stored while `rgw` was still a boolean cannot be decoded. `GET /1.0/placement` returns 500 until the next `PUT` or `DELETE` replaces it.
- A TLS gateway set up by an earlier revision keeps its pair in `server.crt` and `server.key`. The first apply that touches it moves the pair into a hashed `rgw-tls/` directory and restarts the gateway once.
- A gateway that was running before this revision has no `rgw_frontends` row until its first apply.

### Where the code lives

All paths are under `microceph/` unless noted.

| Path | Role |
|---|---|
| `api/types/placement.go` | The `rgw` object and its validation. |
| `ceph/placement.go` | The cluster pass: order, held removals, error classes. |
| `ceph/services_placement_rgw.go`, `ceph/rgw.go` | Applying on one member: TLS pair, readiness, rollback, disable. |
| `database/rgw_frontend.go` | The `rgw_frontends` table. |
| `tests/robot/rgw-placement-tests`, `tests/robot/rgw-placement-multivm-tests` (repository root) | The end-to-end suites. |

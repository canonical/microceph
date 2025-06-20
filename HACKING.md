# MicroCeph Hacking Guide

Wait! No-one's "hacking" anything. The aim of this document is to enable new developers/users get familiarised with the build tools and MicroCeph codebase so that they can build and contribute to the [MicroCeph Codebase](https://github.com/canonical/microceph).

## Table of Contents

1. [⚡️ Introduction to snaps](#⚡️-introduction-to-snaps)
2. [🧰 Tools](#🧰-tools)
3. [📖 References](#📖-references)
4. [🏭 Build Guide](#🏭-build-guide)
5. [👍 Unit-Testing](#👍-unit-testing)

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
sudo snap connect microceph:dm-crypt
sudo snap restart microceph.daemon
```

## 👍 Unit-Testing

The MicroCeph [Makefile](/microceph/Makefile) has targets for running unit tests and lint checks. However, you will need the following packages or tool to run them locally.

```bash
# Add general requirements
sudo apt install gcc make shellcheck

# Add libdqlite-dev, required for building microceph
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

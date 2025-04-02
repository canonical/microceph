# MicroCeph

[![microceph](https://snapcraft.io/microceph/badge.svg)](https://snapcraft.io/microceph)
[![microceph](https://snapcraft.io/microceph/trending.svg?name=0)](https://snapcraft.io/microceph)
[![Go Report Card](https://goreportcard.com/badge/github.com/canonical/microceph/microceph)](https://goreportcard.com/report/github.com/canonical/microceph/microceph)
[![Documentation Status](https://readthedocs.com/projects/canonical-microceph/badge/?version=latest)](https://canonical-microceph.readthedocs-hosted.com/en/latest/?badge=latest)

[![Get it from the Snap Store][snap-button]][snap-microceph]


MicroCeph is an opinionated orchestration tool for Ceph clusters at all scales.
It reduces the complexity of deploying and managing clusters by simplifying various operations like, service placement,
disk administration and remote replication via RESTful APIs and CLI commands.

Available as a snap, MicroCeph is the easiest tool for administrators, developers, and hobbyists to manage Ceph clusters.

## Installation

MicroCeph is delivered as a snap. On [snap-ready systems][snap-ready], you can install it on the command line with:

```
sudo snap install microceph
```

Disable automatic snap upgrades, so that no unexpected updates change your set-up:

```
sudo snap refresh --hold microceph
```

## Basic usage

MicroCeph can deploy a Ceph cluster on a single machine with minimal commands.

First, initialise a Ceph cluster on your machine with:

```
sudo microceph cluster bootstrap
```

> [!NOTE]  
> `cluster` is a MicroCeph subcommand for managing associated Ceph clusters.

After bootstrap, add storage to your cluster with:

```
sudo microceph disk add loop,4G,3
```

Here, you’ll add three virtual disks (“loop file” disks) of 4 GiB each. Make sure that
your root disk has 12 GiB of free storage space.

Note that there are no spaces between the `disk add` arguments.


Once your cluster is set up and running, you can monitor its status with:

```
sudo microceph status
```

If you need a comprehensive status report of your cluster, including its health and disk usage, run:

```
sudo ceph status
```
> [!NOTE]
> MicroCeph supports the usage of Ceph native tooling where snap-level commands are not yet available.

## Documentation

The [MicroCeph documentation][rtd-microceph] contains guides and learning material about
what you can do with MicroCeph and how it works.

Documentation is maintained in the [`docs`][docs-dir-microceph] directory of this repository.
It is written in reStructuredTest (reST) format, built with Sphinx, and published on [Read the Docs][rtd]. 

## Project and Community

MicroCeph is a member of the Ubuntu family. It's an open-source project that warmly welcomes community contributions,
suggestions, fixes, and constructive feedback.

If you find any errors or have suggestions for improvements, please [open an issue on GitHub][bug-microceph]

[Join our Matrix forum][matrix-microceph] to engage with our community and get support.

We abide by the [Ubuntu Code of Conduct][ubuntu-coc].

Excited about MicroCeph? See [our list of fans][stargazers-microceph].

## Contribute to MicroCeph

MicroCeph is growing rapidly, and we would love your help.

If you are interested in contributing to our code or documentation, our [contribution guide][contrib-microceph]
is the best place to start.

We are also a proud member of the [Canonical Open Documentation Academy][coda], an initiative aimed at lowering the
barrier to open-source software contributions through documentation. Find a wide range of MicroCeph documentation tasks there.

## License and copyright

MicroCeph is a free and open source software distributed under the [AGPLv3.0 license][lisense-microceph].

© 2025 Canonical Ltd.

<!-- LINKS -->

[snap-button]: https://snapcraft.io/static/images/badges/en/snap-store-black.svg
[snap-microceph]: https://snapcraft.io/microceph
[rtd-microceph]: https://canonical-microceph.readthedocs-hosted.com/en/latest/
[docs-dir-microceph]: https://github.com/canonical/microceph/tree/main/docs
[contrib-microceph]: ./CONTRIBUTING.md
[license-microceph]: ./COPYING
[ubuntu-coc]: https://ubuntu.com/community/ethos/code-of-conduct
[bug-microceph]: https://github.com/canonical/microceph/issues/new
[stargazers-microceph]: https://github.com/canonical/microceph/stargazers
[matrix-microceph]: https://matrix.to/#/#ubuntu-ceph:matrix.org
[coda]: https://canonical-open-documentation-academy.readthedocs.io/en/latest/
[snap-ready]: https://snapcraft.io/docs/installing-snapd
[rtd]: https://about.readthedocs.com/
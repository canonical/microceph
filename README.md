# MicroCeph

[![microceph](https://snapcraft.io/microceph/badge.svg)](https://snapcraft.io/microceph)
[![microceph](https://snapcraft.io/microceph/trending.svg?name=0)](https://snapcraft.io/microceph)
[![Go Report Card](https://goreportcard.com/badge/github.com/canonical/microceph/microceph)](https://goreportcard.com/report/github.com/canonical/microceph/microceph)
[![Documentation Status](https://readthedocs.com/projects/canonical-microceph/badge/?version=latest)](https://canonical-microceph.readthedocs-hosted.com/en/latest/?badge=latest)

[![Get it from the Snap Store][snap-button]][snap-microceph]


MicroCeph is a lightweight way of deploying and managing a Ceph cluster; the easiest way to get up and running with Ceph.

Deploying and operating a Ceph cluster is complex because Ceph is designed to be a general-purpose storage solution.
This is a significant overhead for small Ceph clusters. MicroCeph solves this by being _opinionated_ and _focused_ on the small scale.
With MicroCeph, you can deploy and operate a Ceph cluster in a [snap][snap-microceph] of a finger!

## Installing and using MicroCeph

Deploy a Ceph cluster on a single machine in only 4 steps! You will need about 15 GiB of available space on
your root drive.

### Install the MicroCeph snap

``sudo snap install microceph``

### Disable automatic snap upgrades

``sudo snap refresh --hold microceph``

### Bootstrap Ceph cluster

``sudo microceph cluster bootstrap``

### Add storage

``sudo microceph disk add loop,4G,3``

That's it, you're done! 

To check your Ceph cluster status:

    sudo ceph status

And, to purge the MicroCeph snap from your machine along with your cluster and all the resources contained in it: 

    sudo snap remove microceph --purge

## Documentation

The MicroCeph documentation lives in the [`docs`][docs-dir-microceph] directory. It is written in reStructuredTest format (reST), built with Sphinx,
and published on Read The Docs. To learn more about what you can do with MicroCeph, visit [our official documentation][rtd-microceph].

## Project and Community

MicroCeph is a member of the Ubuntu family. It is an open-source project that warmly welcomes community projects, contributions, suggestions,
fixes and constructive feedback. If you find any errors or have suggestions for improvements, please [open an issue][bug-microceph] or pull request against this repository,
or use the "Give feedback" link from the documentation.

* [Join our Matrix forum][matrix-microceph] to engage with our community and get support.
* We abide by the [Ubuntu Code of Conduct][ubuntu-coc].

Excited about MicroCeph? Become one of our [Stargazers][stargazers-microceph]!

## Contribute to MicroCeph

MicroCeph is growing as a project, and we would love your help.

If you are interested in contributing to our code or documentation, our [contribution guide][contrib-microceph] is the best place
to start.

We are also a proud member of the [Canonical Open Documentation Academy][coda], an initiative aimed at lowering the barrier to open-source software contributions
through documentation. Find a wide range of MicroCeph documentation tasks there!

## License and copyright

MicroCeph is a free software, distributed under the [AGPLv3.0 license][license-microceph].

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
[coda]: https://canonical.com/documentation/open-documentation-academy

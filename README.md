# MicroCeph

[![microceph](https://snapcraft.io/microceph/badge.svg)](https://snapcraft.io/microceph)
[![microceph](https://snapcraft.io/microceph/trending.svg?name=0)](https://snapcraft.io/microceph)
[![Go Report Card](https://goreportcard.com/badge/github.com/canonical/microceph/microceph)](https://goreportcard.com/report/github.com/canonical/microceph/microceph)
[![Documentation Status](https://readthedocs.com/projects/canonical-microceph/badge/?version=latest)](https://canonical-microceph.readthedocs-hosted.com/en/latest/?badge=latest)

<p align="center">
<a href="https://snapcraft.io/microceph">MicroCeph</a> is snap-deployed Ceph with built-in clustering.
</p>

[![Get it from the Snap Store](https://snapcraft.io/static/images/badges/en/snap-store-black.svg)](https://snapcraft.io/microceph)


## Table of Contents
* [💡 Philosophy](#-philosophy)
* [🎯 Features](#-features)
* [⚡️Quickstart](#-quickstart)
* [📖 Documentation](#-documentation)
* [💫 Project & community](#-project--community)
* [📰 License](#-license)

## 💡 Philosophy

Deploying and operating a Ceph cluster is complex because Ceph is designed to be a general-purpose storage solution. This is a significant overhead for small Ceph clusters. [MicroCeph](https://snapcraft.io/microceph) solves this by being _opinionated_ and _focused_ on the small scale. With MicroCeph, deploying and operating a Ceph cluster is as easy as a [Snap!](https://snapcraft.io/microceph)

## 🎯 Features

1. Quick and consistent deployment with minimal overhead.
2. Single-command operations (for bootstrapping, adding OSDs, service enablement, etc).
3. Isolated from the host and upgrade-friendly.
4. Built-in clustering so you don't have to worry about it!
5. Tailored for small scale (or just your Laptop).

## ⚡️Quickstart

The below commands will set you up with a testing environment on a single
machine using file-backed OSDs - you'll need about 15 GiB of available space on
your root drive:

    sudo snap install microceph --channel quincy/edge
    sudo snap refresh --hold microceph
    sudo microceph cluster bootstrap
    sudo microceph disk add loop,4G,3
    sudo ceph status

You're done!

You can remove everything cleanly with:

    sudo snap remove microceph

## 📖 Documentation

The documentation is found in the [`docs`][docs-dir-microceph] directory. It is
written in RST format, built with Sphinx, and published on Read The Docs:

[MicroCeph documentation][rtd-microceph]

## 💫 Project & community

* [Join our online forum][matrix-microceph] - **Ubuntu Ceph** on Matrix
* [Contributing guidelines][contrib-microceph]
* [Code of conduct][ubuntu-coc]
* [File a bug][bug-microceph]

Excited about MicroCeph? Become one of our [Stargazers][stargazers-microceph]!

## 📰 License

MicroCeph is free software, distributed under the AGPLv3 license (GNU Affero
General Public License version 3.0). Refer to the [COPYING][license-microceph]
file (the actual license) for more information.

<!-- LINKS -->

[rtd-microceph]: https://canonical-microceph.readthedocs-hosted.com/
[docs-dir-microceph]: https://github.com/canonical/microceph/tree/main/docs
[contrib-microceph]: ./CONTRIBUTING.md
[license-microceph]: ./COPYING
[ubuntu-coc]: https://ubuntu.com/community/ethos/code-of-conduct
[bug-microceph]: https://github.com/canonical/microceph/issues/new
[stargazers-microceph]: https://github.com/canonical/microceph/stargazers
[matrix-microceph]: https://matrix.to/#/#ubuntu-ceph:matrix.org

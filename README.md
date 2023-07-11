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
1. [💡 Philosophy](#💡-philosophy)
2. [🎯 Features](#🎯-features)
3. [📖 Documentation](#📖-documentation)
4. [⚡️ Quickstart](#⚡️-quickstart)
5. [👍 How Can I Contribute ?](#👍-how-can-i-contribute)

## 💡 Philosophy

Deploying and operating a Ceph cluster is complex because Ceph is designed to be a general-purpose storage solution. This is a significant overhead for small Ceph clusters. [MicroCeph](https://snapcraft.io/microceph) solves this by being _opinionated_ and _focused_ on the small scale. With MicroCeph, deploying and operating a Ceph cluster is as easy as a [Snap!](https://snapcraft.io/microceph)

## 🎯 Features

1. Quick and consistent deployment with minimal overhead.
2. Single-command operations (for bootstrapping, adding OSDs, service enablement, etc).
3. Isolated from the host and upgrade-friendly.
4. Built-in clustering so you don't have to worry about it!
5. Tailored for small scale (or just your Laptop).

## 📖 Documentation

Refer to the [QuickStart](#⚡️-quickstart) section for your first setup. If you want to read _official_ _documentation_, please visit our hosted [Docs](https://canonical-microceph.readthedocs-hosted.com/en/latest/).

## ⚡️ Quickstart

### ⚙️ Installation and Bootstrapping Ceph cluster
```bash
# Install MicroCeph
$ sudo snap install microceph

# Bootstrapping the Ceph Cluster
$ sudo microceph cluster bootstrap
$ sudo microceph.ceph status
    cluster:
        id:     c8d120af-d7dc-45db-a216-4340e88e5a0e
        health: HEALTH_WARN
                OSD count 0 < osd_pool_default_size 3
    
    services:
        mon: 1 daemons, quorum host (age 1m)
        mgr: host(active, since 1m)
        osd: 0 osds: 0 up, 0 in
    
    data:
        pools:   0 pools, 0 pgs
        objects: 0 objects, 0 B
        usage:   0 B used, 0 B / 0 B avail
        pgs: 
```

![Dashboard](/assets/bootstrap.png)

> **_NOTE:_**
You might've noticed that the Ceph cluster is not _functional_ yet, We need OSDs!<br>
But before that, if you are only interested in deploying on a single node, it would be worthwhile to change the CRUSH rules. With the below commands, we're re-creating the default rule to have a failure domain of osd (instead of the default host failure domain)

```bash
# Change Ceph failure domain to OSD
$ sudo microceph.ceph osd crush rule rm replicated_rule
$ sudo microceph.ceph osd crush rule create-replicated single default osd
```
### ⚙️ Adding OSDs and RGW
```bash
# Adding OSD Disks
$ sudo microceph disk list
    Disks configured in MicroCeph:
    +-----+----------+------+
    | OSD | LOCATION | PATH |
    +-----+----------+------+

    Available unpartitioned disks on this system:
    +-------+----------+--------+---------------------------------------------+
    | MODEL | CAPACITY |  TYPE  |                    PATH                     |
    +-------+----------+--------+---------------------------------------------+
    |       | 10.00GiB | virtio | /dev/disk/by-id/virtio-46c76c00-48fd-4f8d-9 |
    +-------+----------+--------+---------------------------------------------+
    |       | 10.00GiB | virtio | /dev/disk/by-id/virtio-2171ea8f-e8a9-44c7-8 |
    +-------+----------+--------+---------------------------------------------+
    |       | 10.00GiB | virtio | /dev/disk/by-id/virtio-cf9c6e20-306f-4296-b |
    +-------+----------+--------+---------------------------------------------+

$ sudo microceph disk add --wipe /dev/disk/by-id/virtio-46c76c00-48fd-4f8d-9
$ sudo microceph disk add --wipe /dev/disk/by-id/virtio-2171ea8f-e8a9-44c7-8
$ sudo microceph disk add --wipe /dev/disk/by-id/virtio-cf9c6e20-306f-4296-b
$ sudo microceph disk list
    Disks configured in MicroCeph:
    +-----+---------------+---------------------------------------------+
    | OSD |   LOCATION    |                    PATH                     |
    +-----+---------------+---------------------------------------------+
    | 0   | host | /dev/disk/by-id/virtio-46c76c00-48fd-4f8d-9 |
    +-----+---------------+---------------------------------------------+
    | 1   | host | /dev/disk/by-id/virtio-2171ea8f-e8a9-44c7-8 |
    +-----+---------------+---------------------------------------------+
    | 2   | host | /dev/disk/by-id/virtio-cf9c6e20-306f-4296-b |
    +-----+---------------+---------------------------------------------+

    Available unpartitioned disks on this system:
    +-------+----------+--------+------------------+
    | MODEL | CAPACITY |  TYPE  |       PATH       |
    +-------+----------+--------+------------------+
```

![Dashboard](/assets/add_osd.png)

```bash
# Adding RGW Service
$ sudo microceph enable rgw
# Perform IO and Check cluster status
$ sudo microceph.ceph status
    cluster:
        id:     a8f9b673-f3f3-4e3f-b427-a9cf0d2f2323
        health: HEALTH_OK
    
    services:
        mon: 1 daemons, quorum host (age 12m)
        mgr: host(active, since 12m)
        osd: 3 osds: 3 up (since 5m), 3 in (since 5m)
        rgw: 1 daemon active (1 hosts, 1 zones)
    
    data:
        pools:   7 pools, 193 pgs
        objects: 341 objects, 504 MiB
        usage:   1.6 GiB used, 28 GiB / 30 GiB avail
        pgs:     193 active+clean
```

![Dashboard](/assets/enable_rgw.png)

## 👍 How Can I Contribute ?

1. Excited about [MicroCeph](https://snapcraft.io/microceph) ? Join our [Stargazers](https://github.com/canonical/microceph/stargazers)
2. Write reviews or tutorials to help spread the knowledge 📖
3. Participate in [Pull Requests](https://github.com/canonical/microceph/pulls) and help fix [Issues](https://github.com/canonical/microceph/issues)

You can also find us on Matrix @[Ubuntu Ceph](https://matrix.to/#/#ubuntu-ceph:matrix.org)

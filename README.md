# MicroCeph

[![Documentation Status](https://readthedocs.com/projects/canonical-microceph/badge/?version=latest)](https://canonical-microceph.readthedocs-hosted.com/en/latest/?badge=latest)

<p align="center">
<a href="https://snapcraft.io/microceph">MicroCeph</a> is snap-deployed Ceph with built-in clustering.
</p>

## Table of Contents
1. [💡 Philosophy](#💡-philosophy)
2. [🎯 Features](#🎯-features)
3. [📖 Documentation](#📖-documentation)
4. [⚡️ Quickstart](#⚡️-quickstart)
5. [👍 How Can I Contribute ?](#👍-how-can-i-contribute)

## 💡 Philosophy

Deploying and operating a Ceph cluster is complex because Ceph is designed to be a general purpose storage solution. This is a significant overhead for smaller Ceph clusters. [MicroCeph](https://snapcraft.io/microceph) solves this by being _opinionated_ and _focused_ at small scale. With MicroCeph, deploying and operating a Ceph cluster is as easy as a [Snap!](https://snapcraft.io/microceph)

## 🎯 Features

1. Quick and Consistent deployment with minimal overhead.
2. Single-command operations (for bootstrapping, adding OSDs, service enablement etc).
3. Isolated from host and upgrade-friendly.
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

> **_NOTE:_**
You might've noticed that Ceph cluster is not _functional_ yet, We need OSDs!<br>
But before that, If you are only interested in deploying on a single node, It would be worthwhile to change the CRUSH rules.

```bash
# Change Ceph failure domain to OSD
$ sudo microceph.ceph osd crush rule rm replicated_rule
$ sudo microceph.ceph osd crush rule create-replicated single default osd
```
### ⚙️ Adding OSDs and RGW
```bash
# Adding OSD Disks
$ sudo microceph disk add --wipe "/dev/vdb"
$ sudo microceph disk add --wipe "/dev/vdc"
$ sudo microceph disk add --wipe "/dev/vdd"
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
        objects: 239 objects, 590 KiB
        usage:   258 MiB used, 30 GiB / 30 GiB avail
        pgs:     193 active+clean
```

## 👍 How Can I Contribute ?

1. Excited about [MicroCeph](https://snapcraft.io/microceph) ? Join our [Stargazers](https://github.com/canonical/microceph/stargazers)
2. Write Reviews or Tutorials to help spread the knowledge 📖
3. Participate in [Pull Requests](https://github.com/canonical/microceph/pulls) and Help fix [Issues](https://github.com/canonical/microceph/issues)

You can also find us on Matrix @[Ubuntu Ceph](https://matrix.to/#/#ubuntu-ceph:matrix.org)

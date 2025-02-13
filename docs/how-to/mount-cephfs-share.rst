====================================
Mount MicroCeph backed CephFs shares
====================================

CephFs (Ceph Filesystem) are filesystem shares backed by the Ceph storage cluster. 
This tutorial will guide you with mounting CephFs shares using MicroCeph.

The above will be achieved by creating an fs on the MicroCeph deployed
Ceph cluster, and then mounting it using the kernel driver.

MicroCeph Operations:
---------------------

Check Ceph cluster's status:

.. code-block:: none

    $ sudo microceph.ceph -s
    cluster:
        id:     90457806-a798-47f2-aca1-a8a93739941a
        health: HEALTH_OK
    
    services:
        mon: 1 daemons, quorum workbook (age 6h)
        mgr: workbook(active, since 6h)
        osd: 3 osds: 3 up (since 6h), 3 in (since 23h)
    
    data:
        pools:   4 pools, 97 pgs
        objects: 46 objects, 23 MiB
        usage:   137 MiB used, 12 GiB / 12 GiB avail
        pgs:     97 active+clean

Create data/metadata pools for CephFs:

.. code-block:: none

    $ sudo microceph.ceph osd pool create cephfs_meta 
    $ sudo microceph.ceph osd pool create cephfs_data 

Create CephFs share:

.. code-block:: none

    $ sudo microceph.ceph fs new newFs cephfs_meta cephfs_data
    new fs with metadata pool 4 and data pool 3
    $ sudo microceph.ceph fs ls
    name: newFs, metadata pool: cephfs_meta, data pools: [cephfs_data ]

Client Operations:
------------------

Download 'ceph-common' package:

.. code-block:: none

    $ sudo apt install ceph-common

This step is required for ``mount.ceph`` i.e. making mount aware of ceph device type.

Fetch the ``ceph.conf`` and ``ceph.keyring`` file :

Ideally, a keyring file for any CephX user which has access to CephFs will work.
For the sake of simplicity, we are using admin keys in this example.

.. code-block:: none

    $ pwd 
    /var/snap/microceph/current/conf
    $ ls
    ceph.client.admin.keyring  ceph.conf  ceph.keyring  metadata.yaml

The files are located at the paths shown above on any MicroCeph node.
The kernel driver, by-default looks into ``/etc/ceph`` so we will create symbolic
links to that folder.

.. code-block:: none

    $ sudo ln -s /var/snap/microceph/current/conf/ceph.keyring /etc/ceph/ceph.keyring
    $ sudo ln -s /var/snap/microceph/current/conf/ceph.conf /etc/ceph/ceph.conf
    $ ll /etc/ceph/
    ...
    lrwxrwxrwx   1 root root    42 Jun 25 16:28 ceph.conf -> /var/snap/microceph/current/conf/ceph.conf
    lrwxrwxrwx   1 root root    45 Jun 25 16:28 ceph.keyring -> /var/snap/microceph/current/conf/ceph.keyring

Mount the filesystem:

.. code-block:: none

    $ sudo mkdir /mnt/mycephfs
    $ sudo mount -t microceph.ceph :/ /mnt/mycephfs/ -o name=admin,fs=newFs

Here, we provide the CephX user (admin in our example) and the fs created earlier (newFs).

With this, you now have a CephFs mounted at ``/mnt/mycephfs`` on
your client machine that you can perform IO to.

Perform IO and observe the ceph cluster:
----------------------------------------

Write a file:

.. code-block:: none

    $ cd /mnt/mycephfs
    $ sudo dd if=/dev/zero of=random.img count=1 bs=50M
    52428800 bytes (52 MB, 50 MiB) copied, 0.0491968 s, 1.1 GB/s

    $ ll
    ...
    -rw-r--r-- 1 root root 52428800 Jun 25 16:04 random.img

Ceph cluster state post IO:

.. code-block:: none

    $ sudo microceph.ceph -s
    cluster:
        id:     90457806-a798-47f2-aca1-a8a93739941a
        health: HEALTH_OK
    
    services:
        mon: 1 daemons, quorum workbook (age 8h)
        mgr: workbook(active, since 8h)
        mds: 1/1 daemons up
        osd: 3 osds: 3 up (since 8h), 3 in (since 25h)
    
    data:
        volumes: 1/1 healthy
        pools:   4 pools, 97 pgs
        objects: 59 objects, 73 MiB
        usage:   287 MiB used, 12 GiB / 12 GiB avail
        pgs:     97 active+clean

We observe that the cluster usage grew by 150 MiB which is thrice the size of the
file written to the mounted share. This is because MicroCeph configures 3 way
replication by default.

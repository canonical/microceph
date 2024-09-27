==================================
Import a remote MicroCeph cluster
==================================

MicroCeph supports adding secondary MicroCeph clusters as remote clusters.
This creates ``$remote.conf/$remote.keyring`` files in the snap's config directory
allowing users (and microceph) to perform ceph operations on the remote clusters.

This also enables capabilities like remote replication by exposing required
remote cluster details to MicroCeph and Ceph.

Working with remote MicroCeph clusters
--------------------------------------

Assume Primary cluster (named magical) and Secondary cluster (named simple).
An operator can generate the cluster token at the secondary cluster as follows:

.. code-block:: none

   sudo microceph cluster export magical
   eyJmc2lkIjoiN2FiZmMwYmItNjIwNC00M2FmLTg4NDQtMjg3NDg2OGNiYTc0Iiwia2V5cmluZy5jbGllbnQubWFnaWNhbCI6IkFRQ0hJdmRtNG91SUNoQUFraGsvRldCUFI0WXZCRkpzUC92dDZ3PT0iLCJtb24uaG9zdC5zaW1wbGUtcmVpbmRlZXIiOiIxMC40Mi44OC42OSIsInB1YmxpY19uZXR3b3JrIjoiMTAuNDIuODguNjkvMjQifQ==

At the primary cluster, this token can be imported to create the remote record.

.. code-block:: none

   sudo microceph remote import simple eyJmc2lkIjoiN2FiZmMwYmItNjIwNC00M2FmLTg4NDQtMjg3NDg2OGNiYTc0Iiwia2V5cmluZy5jbGllbnQubWFnaWNhbCI6IkFRQ0hJdmRtNG91SUNoQUFraGsvRldCUFI0WXZCRkpzUC92dDZ3PT0iLCJtb24uaG9zdC5zaW1wbGUtcmVpbmRlZXIiOiIxMC40Mi44OC42OSIsInB1YmxpY19uZXR3b3JrIjoiMTAuNDIuODguNjkvMjQifQ== --local-name magical

This will create the required $simple.conf and $simple.keyring files.
Note: Importing a remote cluster is a uni-directional operation. For symmetric
relations both clusters should be added as remotes at each other.

Check remote ceph cluster status

.. code-block:: none

   sudo ceph -s --cluster simple --id magical
   cluster:
    id:     7abfc0bb-6204-43af-8844-2874868cba74
    health: HEALTH_OK
 
  services:
    mon: 1 daemons, quorum simple-reindeer (age 18m)
    mgr: simple-reindeer(active, since 18m)
    osd: 3 osds: 3 up (since 17m), 3 in (since 17m)
 
  data:
    pools:   4 pools, 97 pgs
    objects: 4 objects, 449 KiB
    usage:   81 MiB used, 15 GiB / 15 GiB avail
    pgs:     97 active+clean

Note: Ceph commands can be invoked on the remote cluster by providing the necessary
$cluster and $client.id names.

Similarly, configured remote clusters can be queried as follows

.. code-block:: none

   sudo microceph remote list
   ID  REMOTE NAME  LOCAL NAME 
    1  simple       magical    

and can be removed as

.. code-block:: none

   sudo microceph remote remove simple  

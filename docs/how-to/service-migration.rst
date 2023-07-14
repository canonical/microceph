Migrate Automatic Services Between Nodes
========================================

MicroCeph automatically deploys required services for Ceph (MON, MDS and MGRs). Sometimes, e.g. for maintenance reasons, it can be useful to move those automatic services from one node to another.

This is the purpose of the `microceph cluster migrate` command. It will enable automatic services on a target node and disable them on the source node.

  .. code-block:: shell

     $ sudo microceph cluster migrate <src> <dst>

Both `<src>` and `<dst>` should be node names and are required parameters.

Note:

- it's not possible (and not useful) to have more than one instance of automatic services on one node.
- RGW services are not automatic; they are always enabled and disabled specifically on a node.

Use the `microceph status` command to verify distribution of services among nodes.






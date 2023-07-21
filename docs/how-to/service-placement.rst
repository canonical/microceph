Start Ceph services on any node of the MicroCeph cluster
========================================================

MicroCeph will automatically start Mon, Mgr, and Mds services on the first three deployed nodes in the cluster. Users who wish to enable additional instances of these services for scalability or resilience can do so by utilizing the enable command and specifying a node via the --target parameter.

.. list-table:: Supported Ceph Services
   :widths: 30 70
   :header-rows: 1

   * - Name
     - Description
   * - mon
     - Ceph Monitor Service
   * - mgr
     - Ceph Manager Service
   * - mds
     - Ceph Metadata Daemon Service
   * - rgw
     - Ceph Radosgw Service

The enable command is used as follows:
  .. code-block:: shell

    $ sudo microceph enable <service> --target <node_hostname> ...

.. note:: Some services can be provided with additional parameters while enabling them, these can be listed for any service by issuing respective help command.

  .. code-block:: shell

    $ sudo microceph enable <service> --help

1. Enable radosgw service:

  .. code-block:: shell

    $ sudo microceph status
    MicroCeph deployment summary:
    - node1-2c3eb41e-14e8-465d-9877-df36f5d80922 (10.111.153.78)
      Services: mds, mgr, mon, osd
      Disks: 3
    - workbook (192.168.29.152)
      Services: mds, mgr, mon
      Disks: 0
    $ sudo microceph enable rgw --target node1 --port 8080 
    $ sudo microceph status
    MicroCeph deployment summary:
    - node1 (10.111.153.78)
      Services: mds, mgr, mon, rgw, osd
      Disks: 3
    - workbook (192.168.29.152)
      Services: mds, mgr, mon
      Disks: 0

2. Enable mon Service:

  .. code-block:: shell

    $ sudo microceph enable mon --target <node_hostname> 

3. Enable mgr Service:

  .. code-block:: shell

    $ sudo microceph enable mgr --target <node_hostname> 

4. Enable mds Service:

  .. code-block:: shell

   $ sudo microceph enable mgr --target <node_hostname> 


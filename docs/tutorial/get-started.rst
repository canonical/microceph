First steps with MicroCeph
==========================

This tutorial will guide you through your first steps with MicroCeph. You will use MicroCeph to deploy a Ceph cluster on a single node and to store
a JPEG  image, in a simple storage service (S3) bucket.

To do this, you will use the S3-compatible Ceph Object Gateway, or RADOS Gateway (RGW), to help you interact with your cluster, and ``s3cmd``, a command line tool
for interacting with MicroCeph RGW, allowing users to access Ceph object storage capabilities using familiar AWS S3 commands.

Along the way, you will also interact with your cluster in other ways, such as checking the health status of your cluster, adding disks to it and,
of course, enabling RGW on the cluster.

By the end of this tutorial, after having successfully used MicroCeph to store a graphical image, you will have a basic idea of how MicroCeph works,
and you will be ready to start exploring more advanced use cases.

Requirements
------------

You will need the following:

- The latest Ubuntu LTS version. Find Ubuntu release information `here`_.
- 2 CPU cores
- 4 GiB RAM
- 12GiB disk space
- An internet connection

.. LINKS
.. _here: https://ubuntu.com/about/release-cycle

Install MicroCeph
-----------------

- *First, install MicroCeph as a snap package from the Snap Store:*

.. code-block:: none
    
    sudo snap install microceph

- *Disable this feature to prevent MicroCeph from being auto-updated:*

.. code-block:: none
    
    sudo snap refresh --hold microceph

.. caution::
    
    Failing to set this option may lead undesired upgrades which can be fatal to your deployed cluster.

    All subsequent MicroCeph upgrades must, then, be done manually.

Initialise your cluster
-----------------------

- *Next, bootstrap your new Ceph storage cluster:*

.. code-block:: none
    
    sudo microceph cluster bootstrap

This process takes 3 to 5 seconds.

- *Check the status of the cluster:*

.. code-block:: none
    
    sudo microceph status

.. terminal::

    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
     Services: mds, mgr, mon
        Disks: 0

Your cluster deployment summary will include your node's hostname, i.e. ``ubuntu`` and IP address, along with information about the
services running and storage available. Notice that we have a healthy cluster with one node and three services running, but no storage allocated yet.

Add storage
-----------

- *Let's add storage disk devices to the node.*

We will use loop files, which are file-backed object storage daemons (OSDs) convenient for
setting up small test and development clusters. Three OSDs are required to form a minimal Ceph cluster.

.. code-block:: none
    
    sudo microceph disk add loop,4G,3

.. terminal::

    +-----------+---------+
    |   PATH    | STATUS  |
    +-----------+---------+
    | loop,4G,3 | Success |
    +-----------+---------+

Success! You have added three OSDs with 4GiB storage to your node.

- *Recheck the status of the cluster:*

.. code-block:: none
    
    sudo microceph status

.. terminal::
    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
    Services: mds, mgr, mon, osd
    Disks: 3

You have successfully deployed a Ceph cluster on a single node. Remember that we had three services running upon bootstrapping the cluster.
Note that we now have four services running, including a new ``osd`` service.

Enable RGW
----------

As mentioned before, we will use the Ceph Object Gateway as a way to interact with the object storage cluster
we just deployed.

- *Enable the RGW daemon on your node:*

.. code-block:: none

    sudo microceph enable rgw

.. note:: 
    
    By default, the ``rgw`` service uses port 80, which is not always available. If you don’t have port 80 free,
    you can set an alternative port number, say 8080, by adding the :file:`--port <port-number>` parameter.


- *Recheck status*

Another status check will show the ``rgw`` service reflected in the status output.

.. code-block:: none

    sudo microceph status

.. terminal::

    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
    Services: mds, mgr, mon, rgw, osd
    Disks: 3

MicroCeph is packaged with the standard ``radosgw-admin`` tool that manages the ``rgw`` service and users. We
will now use this tool to create a RGW user and set secrets on it.

- *Create a RGW user:*

.. code-block:: none

    sudo radosgw-admin user create --uid=user --display-name=user

The output should look something like this:

.. terminal::

     {
    "user_id": "user",
    "display_name": "user",
    "email": "",
    "suspended": 0,
    "max_buckets": 1000,
    "subusers": [],
    "keys": [
        {
            "user": "user",
            "access_key": "NJ7YZ3LYI45M4Q1A08OS",
            "secret_key": "H7OTclVbZIwhd2o0NLPu0D7Ass8ouSKmtSewuYwK",
            "active": true,
            "create_date": "2024-11-28T13:07:41.561437Z"
        }
    ],
    "swift_keys": [],
    "caps": [],
    "op_mask": "read, write, delete",
    "default_placement": "",
    "default_storage_class": "",
    "placement_tags": [],
    "bucket_quota": {
        "enabled": false,
        "check_on_raw": false,
        "max_size": -1,
        "max_size_kb": 0,
        "max_objects": -1
    },
    "user_quota": {
        "enabled": false,
        "check_on_raw": false,
        "max_size": -1,
        "max_size_kb": 0,
        "max_objects": -1
    },
    "temp_url_keys": [],
    "type": "rgw",
    "mfa_ids": [],
    "account_id": "",
    "path": "/",
    "create_date": "2024-11-28T13:07:41.561217Z",
    "tags": [],
    "group_ids": []

- *Set user secrets:*

.. code-block:: none

    sudo radosgw-admin key create --uid=user --key-type=s3 --access-key=foo --secret-key=bar

.. terminal::

    {
    "user_id": "user",
    "display_name": "user",
    "email": "",
    "suspended": 0,
    "max_buckets": 1000,
    "subusers": [],
    "keys": [
        {
            "user": "user",
            "access_key": "NJ7YZ3LYI45M4Q1A08OS",
            "secret_key": "H7OTclVbZIwhd2o0NLPu0D7Ass8ouSKmtSewuYwK",
            "active": true,
            "create_date": "2024-11-28T13:07:41.561437Z"
        },
        {
            "user": "user",
            "access_key": "foo",
            "secret_key": "bar",
            "active": true,
            "create_date": "2024-11-28T13:54:36.065214Z"
        }
    ],
    "swift_keys": [],
    "caps": [],
    "op_mask": "read, write, delete",
    "default_placement": "",
    "default_storage_class": "",
    "placement_tags": [],
    "bucket_quota": {
        "enabled": false,
        "check_on_raw": false,
        "max_size": -1,
        "max_size_kb": 0,
        "max_objects": -1
    },
    "user_quota": {
        "enabled": false,
        "check_on_raw": false,
        "max_size": -1,
        "max_size_kb": 0,
        "max_objects": -1
    },
    "temp_url_keys": [],
    "type": "rgw",
    "mfa_ids": [],
    "account_id": "",
    "path": "/",
    "create_date": "2024-11-28T13:07:41.561217Z",
    "tags": [],
    "group_ids": []

Consuming the storage
---------------------

Access RGW
~~~~~~~~~~

Before attempting to consume the object storage in the cluster, validate that you can access RGW by running :command:`curl` on your node.

- *Find the IP address of the node running the  ``rgw`` service:*

.. code-block:: none
    
    sudo microceph status

.. terminal::

    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
    Services: mds, mgr, mon, rgw, osd
    Disks: 3

- *Run* :command:`curl` *from this node:*

.. code-block:: none
    
    curl http://10.246.114.49

.. terminal::

    <?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>anonymous</ID></Owner><Buckets></Bucket

- *Create an S3 bucket:*

You have verified that your cluster is accessible via RGW. Now, let's create a bucket using the ``s3cmd`` tool:

.. code-block:: none

    s3cmd mb -P s3://mybucket

.. note::

    The ``-P`` flag ensures that the bucket is publicly visible, enabling you to access stored objects easily via a public URL.

.. terminal::

    Bucket 's3://mybucket/' created

Our bucket is successfully created.

- *Let's upload an image into it:*

.. code-block:: none

    s3cmd put -P image.jpg s3://mybucket

.. terminal::

    upload: 'image.jpg' -> 's3://mybucket/image.jpg'  [1 of 1]
    66565 of 66565   100% in    0s     4.52 MB/s  done
    Public URL of the object is: http://ubuntu/mybucket/image.jpg

Great work! You have stored your image in a publicly visible S3 bucket. You may now click on the public object URL given in the output 
to view it in your browser.

Cleaning up resources
---------------------

In case, for any reason, you want to get rid of MicroCeph, you can purge the snap from your machine this way:

.. code-block:: none

    sudo snap remove microceph --purge

This command stops all the services running, and removes the MicroCeph snap along with your cluster and all the resources contained in it.

.. note::

    The ``--purge`` option removes all the files associated with the MicroCeph package, and will also skip generating a snapshot of the package's
    running state. Skipping the :command:`purge` option is useful if you intend to re-install MicroCeph, or move your configuration to a different system.


.. terminal::

    2024-11-28T19:44:29+03:00 INFO Waiting for "snap.microceph.rgw.service" to stop.
    2024-11-28T19:45:00+03:00 INFO Waiting for "snap.microceph.mds.service" to stop.
    microceph removed

Next steps
----------

You have deployed a healthy Ceph cluster on a single-node and enabled RGW on it. Even better, you have consumed the storage in that cluster by creating
a bucket and storing an object in it. Curious to see what else you can do with MicroCeph?

See our :doc:`how-to guides <../how-to/index>`, packed with instructions to help you achieve specific goals with MicroCeph.

Or, explore our :doc:`Explanation <../explanation/index>` and
:doc:`Reference <../reference/index>` sections for additional information and quick references.
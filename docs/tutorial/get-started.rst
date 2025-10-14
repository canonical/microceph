Get started
===========

This tutorial will guide you through your first steps with MicroCeph. We will deploy a Ceph cluster on a single node using MicroCeph and store a JPEG image in an S3 bucket managed by MicroCeph.

How you'll do It
-----------------

You will install MicroCeph, initialise the cluster, and add storage. Then, you will enable the S3-compatible Ceph Object Gateway (RGW) on your node and create an S3 bucket. Finally, you will upload an image to the bucket, consuming the storage via RGW.

As we progress, you will also interact with your cluster by checking its health, adding disks, and enabling RGW.

By the end of this tutorial, after successfully using MicroCeph to store an image, you will have a foundational understanding of how MicroCeph works, and be ready to explore more advanced use cases.

What you'll need
----------------

- The latest Ubuntu LTS version. Find Ubuntu release information `here`_.
- 2 CPU cores
- 4 GiB RAM
- 12GiB disk space
- An Internet connection

.. LINKS
.. _here: https://ubuntu.com/about/release-cycle

Install MicroCeph
-----------------

First, install MicroCeph as a snap package from the Snap Store:

.. code-block:: none
    
    sudo snap install microceph

Disable the default automatic Snap upgrades to prevent MicroCeph from being updated automatically:

.. code-block:: none
    
    sudo snap refresh --hold microceph

.. caution::
    
    Failing to set this option may result in unintended upgrades, which could critically impact your deployed cluster. To prevent this, all subsequent MicroCeph upgrades must be performed manually.

Initialise your cluster
-----------------------

Next, bootstrap your new Ceph storage cluster:

.. code-block:: none
    
    sudo microceph cluster bootstrap

This process takes 3 to 5 seconds.

Check the cluster status:

.. code-block:: none
    
    sudo microceph status

The output should look somewhat as shown below:

.. terminal::

    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
     Services: mds, mgr, mon
        Disks: 0

Your cluster deployment summary contains your node's hostname (IP address). In our case, it's ``ubuntu`` (``10.246.114.49``), along with information about the services running and available storage. You'll notice that the cluster is healthy with one node and three services running, but no storage has been allocated yet. 

Now that the cluster is initialised, we'll add some storage to the node.

Add storage
-----------

Let's add storage disk devices to the node.

We will use loop files, which are file-backed Object Storage Daemons (OSDs) convenient for setting up small test and development clusters. Three OSDs are required to form a minimal Ceph cluster.

Execute the following command:

.. code-block:: none
    
    sudo microceph disk add loop,4G,3

.. terminal::

    +-----------+---------+
    |   PATH    | STATUS  |
    +-----------+---------+
    | loop,4G,3 | Success |
    +-----------+---------+

Success! You have added three OSDs with 4GiB storage to your node.

Recheck the cluster status:

.. code-block:: none
    
    sudo microceph status

.. terminal::
    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
    Services: mds, mgr, mon, osd
    Disks: 3

You have successfully deployed a Ceph cluster on a single node. 

Remember that we had three services running when the cluster was bootstrapped. Note that we now have four services running, including the newly added ``osd`` service.

Enable RGW
----------

As mentioned before, we will use the Ceph Object Gateway to interact with the object storage cluster
we just deployed.

Enable the RGW daemon on your node
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: none

    sudo microceph enable rgw

.. note:: 
    
    By default, the ``rgw`` service uses port 80, which may not always be available. If port 80 is occupied,
    you can specify an alternative port, such as 8080, by adding the :file:`--port <port-number>` parameter.

Run the status check again to confirm that the ``rgw`` service is reflected in the status output.

.. code-block:: none

    sudo microceph status

.. terminal::

    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
    Services: mds, mgr, mon, rgw, osd
    Disks: 3

Create an RGW user
~~~~~~~~~~~~~~~~~~
MicroCeph is packaged with the standard ``radosgw-admin`` tool that manages the ``rgw`` service and users. We
will now use this tool to create an RGW user called ``user``, with the display name ``user``.


.. code-block:: none

    sudo radosgw-admin user create --uid=user --display-name=user --access-key=foo --secret-key=bar

The output should include user details as shown below, if ``access-key`` or ``secret-key`` is not provided
by the user, it will be generated automatically.

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
            "access_key": "foo",
            "secret_key": "bar",
            "active": true,
            "create_date": "2024-11-28T13:07:41.561437Z"
        }
    ],
    ...

Consuming the storage
---------------------

Access RGW
~~~~~~~~~~

Before attempting to consume the object storage in the cluster, validate that you can access RGW by running :command:`curl` on your node.

Find the IP address of the node running the ``rgw`` service:

.. code-block:: none
    
    sudo microceph status

.. terminal::

    MicroCeph deployment summary:
    - ubuntu (10.246.114.49)
    Services: mds, mgr, mon, rgw, osd
    Disks: 3

Then, run :command:`curl` from this node.

.. code-block:: none
    
    curl http://10.246.114.49

.. terminal::

    <?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>anonymous</ID></Owner><Buckets></Bucket

.. note::
    Make note of the IP address used above, as it will is reused in subsequent steps.

Create an S3 bucket
~~~~~~~~~~~~~~~~~~~

You have verified that your cluster is accessible via RGW. To interact with S3, we need to make sure that the
``aws-cli`` utility is installed and configured.

Install and configure ``aws-cli``
^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

To install ``aws-cli``, run the following command:

.. code-block:: none

    sudo snap install aws-cli --classic

Run the below command to interactively populate required parameters as follows:

.. code-block:: none

    aws configure

.. terminal::

    AWS Access Key ID [****************foo]: foo
    AWS Secret Access Key [****************bar]: bar
    Default region name [default]: default
    Default output format [None]:

Create a bucket
^^^^^^^^^^^^^^^

You have verified that your cluster is accessible via RGW. Now, let's create a bucket using the ``aws s3`` command:

.. code-block:: none

    aws s3 mb s3://mybucket --endpoint=http://10.246.114.49

.. terminal::

    make_bucket: mybucket

Our bucket is successfully created.

Upload a file into the bucket
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: none

    aws s3 cp ./image.jpg s3://mybucket --endpoint=http://10.246.114.49

.. terminal::

    upload: ./image.jpg to s3://mybucket/image.jpg

The output shows that your image is now stored in a S3 bucket.

Listing the contents of a bucket
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Let's list the contents of ``s3:mybucket`` and see if our image is present there.

.. code-block::

    aws s3 ls s3://mybucket --endpoint=http://10.246.114.49

.. terminal::

    2025-10-15 12:51:03          0 image.jpg

Cleaning up resources
---------------------

If you want to remove MicroCeph, you can purge the snap from your machine using:

.. code-block:: none

    sudo snap remove microceph --purge

This command stops all running services and removes the MicroCeph snap, along with your cluster and all its contained resources.

.. note::

    Note: the ``--purge`` flag will remove all persistent state associated with MicroCeph.
    

    The ``--purge`` flag deletes all files associated with the MicroCeph package, meaning it will remove the MicroCeph snap without saving any data snapshots. Running the command without this flag will not fully remove MicroCeph; the persistent state will remain intact.

.. tip::
    Skipping the :command:`purge` option is useful if you intend to re-install MicroCeph, or move your configuration to a different system.


.. terminal::

    2024-11-28T19:44:29+03:00 INFO Waiting for "snap.microceph.rgw.service" to stop.
    2024-11-28T19:45:00+03:00 INFO Waiting for "snap.microceph.mds.service" to stop.
    microceph removed

Next steps
----------

You have deployed a healthy Ceph cluster on a single-node and enabled RGW on it. Even better, you have consumed the storage in that cluster by creating a bucket and storing an image object in it. Curious to see what else you can do with MicroCeph?

See our :doc:`how-to guides <../how-to/index>`, packed with instructions to help you achieve specific goals with MicroCeph.

Or, explore our :doc:`Explanation <../explanation/index>` and
:doc:`Reference <../reference/index>` sections for additional information and quick references.

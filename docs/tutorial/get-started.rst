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

    sudo radosgw-admin user create --uid=user --display-name=user

The output should include user details as shown below, with auto-generated access and secret keys.

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
    ...

Set user secrets
~~~~~~~~~~~~~~~~
Let's define secrets for this user, setting ``access_key`` to ``foo``, and ``--secret-key`` to ``bar``.

.. code-block:: none

    sudo radosgw-admin key create --uid=user --key-type=s3 --access-key=foo --secret-key=bar

.. terminal::

    ... 
    [
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
   ...

Consuming the storage
---------------------

Access RGW
~~~~~~~~~~

Before attempting to consume the object storage in the cluster, validate that you can access RGW by running :command:`curl` on your node.

Find the IP address of the node running the  ``rgw`` service:

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

Create an S3 bucket
~~~~~~~~~~~~~~~~~~~

You have verified that your cluster is accessible via RGW. To interact with S3, we need to make sure that the
``s3cmd`` utility is installed and configured.

Install and configure ``s3cmd``
^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

To install ``s3cmd``, run the following command:

.. code-block:: none

    sudo apt-get install s3cmd

Configure the ``s3cmd`` tool:

.. code-block:: none

    s3cmd --configure

This will invoke an interactive configuration session, and later create a file named ``.s3cfg``
in your home directory with all the settings chosen in the interactive session.

Remember that we had set secrets for our user earlier; we will use those when prompted to provide
a secret key and access key. We'll set our host name (``ubuntu``) as the S3 endpoint, and use the default
[US] region.

.. terminal::

    Enter new values or accept defaults in brackets with Enter.
    Refer to user manual for detailed description of all options.

    Access key and Secret key are your identifiers for Amazon S3. Leave them empty for using the env variables.
    Access Key: foo
    Secret Key: bar
    Default Region [US]: 

    Use "s3.amazonaws.com" for S3 Endpoint and not modify it to the target Amazon S3.
    S3 Endpoint [s3.amazonaws.com]: ubuntu

    Use "%(bucket)s.s3.amazonaws.com" to the target Amazon S3. "%(bucket)s" and "%(location)s" vars can be used
    if the target S3 system supports dns based buckets.
    DNS-style bucket+hostname:port template for accessing a bucket [%(bucket)s.s3.amazonaws.com]: 

    Encryption password is used to protect your files from reading
    by unauthorized persons while in transfer to S3
    Encryption password: Ubuntu-pass
    Path to GPG program [/usr/bin/gpg]: 

    When using secure HTTPS protocol all communication with Amazon S3
    servers is protected from 3rd party eavesdropping. This method is
    slower than plain HTTP, and can only be proxied with Python 2.7 or newer
    Use HTTPS protocol [Yes]: No

    On some networks all internet access must go through a HTTP proxy.
    Try setting it here if you can't connect to S3 directly
    HTTP Proxy server name: 

    New settings:
    Access Key: foo
    Secret Key: bar
    Default Region: US
    S3 Endpoint: ubuntu
    DNS-style bucket+hostname:port template for accessing a bucket: %(bucket)s.s3.amazonaws.com
    Encryption password: Ubuntu-pass
    Path to GPG program: /usr/bin/gpg
    Use HTTPS protocol: False
    HTTP Proxy server name: 
    HTTP Proxy server port: 0

    Test access with supplied credentials? [Y/n] y
    Please wait, attempting to list all buckets...
    Success. Your access key and secret key worked fine :-)

    Now verifying that encryption works...
    Success. Encryption and decryption worked fine :-)

    Save settings? [y/N] y
    Configuration saved to '/home/ubuntu/.s3cfg'

We have successfully configured ``s3cmd``.  To see the full configuration, inspect the config file. 

.. code-block:: none

    cat ~/.s3cfg

Create a bucket
^^^^^^^^^^^^^^^

You have verified that your cluster is accessible via RGW. Now, let's create a bucket using the ``s3cmd`` tool:

.. code-block:: none

    s3cmd mb -P s3://mybucket

.. note::

    The ``-P`` flag ensures that the bucket is publicly visible, enabling you to access stored objects easily via a public URL.

.. terminal::

    Bucket 's3://mybucket/' created

Our bucket is successfully created.

Upload an image into the  bucket
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: none

    s3cmd put -P image.jpg s3://mybucket

.. terminal::

    upload: 'image.jpg' -> 's3://mybucket/image.jpg'  [1 of 1]
    66565 of 66565   100% in    0s     4.52 MB/s  done
    Public URL of the object is: http://ubuntu/mybucket/image.jpg

The output shows that your image is stored in a publicly accessible S3 bucket. You can now click on the public object URL in the output to view the image in your browser.

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

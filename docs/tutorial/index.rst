Tutorial
--------

This tutorial will guide you through your first steps with MicroCeph. You will deploy a Ceph cluster on a single node using MicroCeph and use it to store
an object, i.e. an image, in a simple storage service (S3) bucket.

To do this, you will use the S3-compatible Ceph Object Gateway, or RADOS Gateway (RGW), to help you interact with your cluster, and ``s3cmd``, 
a command line tool for managing S3-compatible storage services, like Ceph.

Along the way, you will also interact with your cluster in other ways such as, checking the health status of your cluster, adding disks to it and,
of course, enabling RGW on the cluster.

By the end of this tutorial, you will have a basic idea of how MicroCeph works, having successfully used it to store your object, 
and you will be ready to start exploring more advanced use cases.

.. toctree::
    :maxdepth: 1

    get-started
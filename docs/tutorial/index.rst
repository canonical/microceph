Tutorial
--------

This tutorial will guide you through your first steps with MicroCeph. You will use MicroCeph to deploy a Ceph cluster on a single node and to store
a JPEG  image, in a simple storage service (S3) bucket.

To do this, you will use the S3-compatible Ceph Object Gateway, or RADOS Gateway (RGW), to help you interact with your cluster, and ``s3cmd``, a command line tool
for interacting with MicroCeph RGW, allowing users to access Ceph object storage capabilities using familiar AWS S3 commands.

Along the way, you will also interact with your cluster in other ways, such as checking the health status of your cluster, adding disks to it and,
of course, enabling RGW on the cluster.

By the end of this tutorial, after having successfully used MicroCeph to store a graphical image, you will have a basic idea of how MicroCeph works,
and you will be ready to start exploring more advanced use cases.

.. toctree::
    :maxdepth: 1

    get-started
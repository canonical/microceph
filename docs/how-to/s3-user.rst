Use S3 user management on MicroCeph
===================================

MicroCeph provides an easy to use interface for creating, viewing and deleting s3 users for interfacing with the RGW endpoint.
This enables smooth and easy access to Object Storage.

.. list-table:: Supported s3-user operations
   :widths: 30 70
   :header-rows: 1

   * - Operation
     - Description
   * - create
     - Create provided s3 (radosgw) user with optionally provided access-key and secret
   * - delete
     - Delete provided s3 (radosgw) user
   * - get
     - Fetch key information of the provided s3 (radosgw) user
   * - list
     - List all s3 (radosgw) users
.. note:: Users can additionally provide --json flag to create and get commands to dump a much detailed 

1. Create an S3 user (optionally provide --access-key --secret and --json)

  .. code-block:: shell

    $ sudo microceph s3-user create newTestUser --access-key=ThisIsAccessKey --secret=ThisIsSecret --json
    {
        "user_id": "newTestUser",
        "display_name": "newTestUser",
        "email": "",
        "suspended": 0,
        "max_buckets": 1000,
        "subusers": [],
        "keys": [
            {
                "user": "newTestUser",
                "access_key": "ThisIsAccessKey",
                "secret_key": "ThisIsSecret"
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
        "mfa_ids": []
    }

2. List all s3 users :

  .. code-block:: shell

    $ sudo microceph s3-user list
    +---+-------------+
    | # |    NAME     |
    +---+-------------+
    | 1 | newTestUser |
    +---+-------------+
    | 2 | testUser    |
    +---+-------------+

3. Get details of a an s3 user (optionally use --json flag to get complete details):

  .. code-block:: shell

    $ sudo microceph s3-user get testUser
    +----------+----------------------+---------+
    |   NAME   |    ACCESS KEY   |    SECRET    |
    +----------+----------------------+---------+
    | testUser | ThisIsAccessKey | ThisIsSecret |
    +----------+----------------------+---------+

4. Delete an s3 user:

  .. code-block:: shell

   $ sudo microceph s3-user delete newTestUser
   $ sudo microceph s3-user list
    +---+----------+
    | # |   NAME   |
    +---+----------+
    | 1 | testUser |
    +---+----------+

  .. warning:: All the related buckets+objects should be deleted before deletion of the user. 

For more fine-tuned user management use `radosgw-admin CLI <https://docs.ceph.com/en/latest/man/8/radosgw-admin/>`_


=======================================
Testing the Ceph S3-Compatible Endpoint
=======================================

Ceph provides an S3-compatible service. While the service is REST-based, it 
is tedious to test by hand with `curl` or other tools, as the S3 protocol 
requires that requests are *signed* by ordering the headers and creating a 
hash. While easy-to-use tools are available, many assume the provider is Amazon
and may be difficult to change. 
[Amazon S3 Documentation](https://docs.aws.amazon.com/AmazonS3/latest/userguide/RESTAuthentication.html#ConstructingTheAuthenticationHeader)

This how-to covers:
* Required services in microceph
* Creating authorized users
* Installing and configuring the `s3cmd` tool
* Creating a bucket, adding a file and removing the file and bucket

Important Considerations
------------------------

There are two ways to reference an S3 bucket: *path-style* and *virtual
hosted-style*. Amazon has stated a preference to deprecate *path-style*,
but as of September 2020 has decided to delay the deprecation.
[Amazon S3 Documentation](https://docs.aws.amazon.com/AmazonS3/latest/userguide/RESTAPI.html#virtual-hosted-path-style-requests)
[Amazon S3 Blog](https://aws.amazon.com/blogs/aws/amazon-s3-path-deprecation-plan-the-rest-of-the-story/)

Currently, ***Microceph supports only path-style bucket references.*** 

Prerequisites
-------------

This guide assumes that Microceph has been installed and confiugred as
specified in this guide. Also, that the [enable service instances](enable-service-instances/)
guide has been followed to create the RGW service.

Create RADOS user
-----------------

On the Microceph node, use the :command:`radosgw-admin` tool.

.. code-block:: none

   admin@ceph-lab:~$ sudo radosgw-admin user create --uid=[userid] --display-name="[displayname]"

Where [userid] and [displayname] are replaced with appropriate values.

This will return a JSON object:
.. code-block:: JSON
:emphasize-lines: 11,12

   {
      "user_id": "[userid]",
      "display_name": "[displayname]",
      "email": "",
      "suspended": 0,
      "max_buckets": 1000,
      "subusers": [],
      "keys": [
      {
         "user": "tanzu",
         "access_key": "[20-char text string]",
         "secret_key": "[40-char text string]"
      }
      ],
      "swift_keys": [],
      "caps": [],
      "op_mask": "read, write, delete",
      "default_placement": "",
      "default_storage_class": "",
      "placement_tags": [],
      "bucket_quota": 
      {
         "enabled": false,
         "check_on_raw": false,
         "max_size": -1,
         "max_size_kb": 0,
         "max_objects": -1
      },
      "user_quota": 
      {
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

Copy the **keys** data, specifically **keys.access_key** and **keys.secret_key**.

Install and Configure s3cmd
---------------------------

S3cmd is a Python-based tool created and open-sourced by s3tools.org [s3tools.org/s3cmd](https://s3tools.org/s3cmd)
and may be [downloaded here](https://sourceforge.net/projects/s3tools/files/s3cmd/).

After s3cmd is installed and verified by :command:`s3cmd --version`, configure 
s3cmd with the built-in tool that will go through a series of questions:
:command:`s3cmd --configure`

1. Access key

Enter the access key copied from **keys.access_key** above. If these keys are 
lost, they can be retrieved by an administrator with

.. code-block:: none

   admin@ceph-lab:~$ sudo radosgw-admin user info --uid=[userid]

1. Secret key

Enter the secret key copied from **keys.secret_key** above.

1. Default region

Press enter to accept the default.

1. S3 Endpoint

This is URL or IP Address to your Microceph server. Example: **ceph.lab.example.com**
or **172.16.1.100**

(Naturally, if a DNS name is used istead of an IP, there must be a DNS entry or
hosts file entry made in the appropriate place to resolve the name.)

1. DNS-style bucket+hostname:port template

***Important*** This is where the virtual-host-style requests are configured.
Since Microceph does not yet support this, enter the *same value as the S3 
endpoint*, e.g. ceph.lab.example.com or 172.16.1.100

1. Encryption, GPG, Use HTTPS, HTTP Proxy

For this test, enter blank for all, except HTTPS: enter No.

1. Test access

Press enter to test connectivity. This will check that the S3 endpoint is 
reachable, the user exists, and the access_key and secret_key are valid.
It does not exercise the bucket specification or the rights of the user.

1. Save settings

Enter Y to save the settings to ~/.s3cfg. Other parameters can be edited
in that file, but these are enough for the test.


Test Using the Bucket
---------------------

Create a bucket. Bucket names have specific rules about length, case and 
characters. Generally, they must be 3-63 characters, lowercase letters, 
numbers, dots . and hyphens -. The protocol must be specified in lower
case.

:command:`s3cmd mb s3://test`

A message that the bucket is created should appear.

:command:`s3cmd put [filename] s3://test`

Upload statistics should appear.

:command:`s3cmd del s3://test/[filename]`

Delete message should appear.

:command:`s3cmd rb s3://test`

Removed message should appear.


.. LINKS

.. _Manager service: https://docs.ceph.com/en/latest/mgr/
.. _Monitor service: https://docs.ceph.com/en/latest/man/8/ceph-mon/
.. _Metadata service: https://docs.ceph.com/en/latest/man/8/ceph-mds/
.. _RADOS Gateway service: https://docs.ceph.com/en/latest/radosgw/

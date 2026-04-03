==================================================
Configure Openstack Keystone Auth in MicroCeph RGW
==================================================

Ceph Object Gateway (RGW) can be configured to use `Openstack Keystone`_ for
providing user authentication service. A Keystone authorised user to the
gateway will also be automatically created on the Ceph Object Gateway. A
token that Keystone validates will be considered as valid by the gateway.

MicroCeph supports setting the following Keystone config keys:

.. list-table:: Supported Config Keys
   :widths: 30 70
   :header-rows: 1

   * - Key
     - Description
   * - rgw_s3_auth_use_keystone
     - Whether to use keystone auth for the S3 endpoints.
   * - rgw_keystone_url
     - Keystone server address in {url:port} format
   * - rgw_keystone_admin_token
     - Keystone admin token (not recommended in production)
   * - rgw_keystone_admin_token_path
     - Path to Keystone admin token (recommended for production)
   * - rgw_keystone_admin_user
     - Keystone service tenant user name
   * - rgw_keystone_admin_password
     - Keystone service tenant user password
   * - rgw_keystone_admin_password_path
     - Path to Keystone service tenant user password file
   * - rgw_keystone_admin_project
     - Keystone admin project name
   * - rgw_keystone_admin_domain
     - Keystone admin domain name
   * - rgw_keystone_service_token_enabled
     - Whether to allow expired tokens with service token in requests
   * - rgw_keystone_service_token_accepted_roles
     - Specify user roles accepted as service roles
   * - rgw_keystone_expired_token_cache_expiration
     - Cache expiration period for an expired token allowed with a service token
   * - rgw_keystone_api_version
     - Keystone API version
   * - rgw_keystone_accepted_roles
     - Accepted user roles for Keystone users
   * - rgw_keystone_accepted_admin_roles
     - List of roles allowing user to gain admin privileges
   * - rgw_keystone_token_cache_size
     - The maximum number of entries in each Keystone token cache
   * - rgw_keystone_verify_ssl
     - Whether to verify SSL certificates while making token requests to Keystone
   * - rgw_keystone_implicit_tenants
     - Whether to create new users in their own tenants of the same name
   * - rgw_swift_account_in_url
     - Whether the Swift account is encoded in the URL path
   * - rgw_swift_versioning_enabled
     - Enables object versioning
   * - rgw_swift_enforce_content_length
     - Whether content length header is needed when listing containers
   * - rgw_swift_custom_header
     - Enable swift custom header

A user can set/get/list/reset the above mentioned config keys as follows:

1. Supported config keys can be configured using the 'set' command:

  .. code-block:: shell

    $ sudo microceph cluster config set rgw_swift_account_in_url true

2. Config value for a particular key could be queried using the 'get' command:

  .. code-block:: shell

    $ sudo microceph cluster config get rgw_swift_account_in_url
    +---+--------------------------+-------+
    | # |           KEY            | VALUE |
    +---+--------------------------+-------+
    | 0 | rgw_swift_account_in_url | true  |
    +---+--------------------------+-------+

3. A list of all the configured keys can be fetched using the 'list' command:

  .. code-block:: shell

    $ sudo microceph cluster config list
    +---+--------------------------+-------+
    | # |           KEY            | VALUE |
    +---+--------------------------+-------+
    | 0 | rgw_swift_account_in_url | true  |
    +---+--------------------------+-------+

4. Resetting a config key (i.e. setting the key to its default value) can performed using the 'reset' command:

  .. code-block:: shell

   $ sudo microceph cluster config reset rgw_swift_account_in_url
   $ sudo microceph cluster config list
   +---+-----+-------+
   | # | KEY | VALUE |
   +---+-----+-------+

For detailed documentation of what keys should be configured, visit `Ceph Docs`_

.. LINKS

.. _Openstack Keystone: https://docs.openstack.org/keystone/latest/getting-started/architecture.html#identity
.. _Ceph Docs: https://docs.ceph.com/en/latest/radosgw/keystone/

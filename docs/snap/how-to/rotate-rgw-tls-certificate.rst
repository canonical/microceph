==============================
Rotate RGW TLS certificates
==============================

If you have RGW running with SSL enabled, you can rotate its TLS certificates
without needing to disable and re-enable the service.

Prerequisites
-------------

- RGW must already be enabled with SSL. See :doc:`enable-service-instances` for
  details on enabling RGW with ``--ssl-certificate`` and ``--ssl-private-key``.
- The replacement certificate and private key must be base64 encoded.

Rotate with immediate effect
-----------------------------

Use the ``--restart`` flag to write the new certificate and key to disk and
restart the RGW service immediately. This will drop any existing client
connections.

.. code-block:: none

   sudo microceph certificate set rgw \
     --ssl-certificate "$(base64 -w0 /path/to/new-server.crt)" \
     --ssl-private-key "$(base64 -w0 /path/to/new-server.key)" \
     --restart

Write certificate without restart
-----------------------------------

Without ``--restart``, the certificate and key are written to disk but the RGW
service continues serving the old certificate. You must restart the service
manually for the change to take effect.

.. code-block:: none

   sudo microceph certificate set rgw \
     --ssl-certificate "$(base64 -w0 /path/to/new-server.crt)" \
     --ssl-private-key "$(base64 -w0 /path/to/new-server.key)"

Rotate on a specific node
--------------------------

In a multi-node cluster, each node has its own certificate files. Use
``--target`` to rotate the certificate on a specific node. Repeat for each
node that runs RGW:

.. code-block:: none

   sudo microceph certificate set rgw \
     --ssl-certificate "$(base64 -w0 /path/to/new-server.crt)" \
     --ssl-private-key "$(base64 -w0 /path/to/new-server.key)" \
     --target node2 \
     --restart

Verify the certificate
-----------------------

After restarting the RGW service, verify that the new certificate is being
served. The SSL port is the value passed to ``microceph enable rgw --ssl-port``
(default: 443). You can confirm the port by inspecting the RGW configuration:

.. code-block:: none

   sudo grep ssl_port /var/snap/microceph/current/conf/radosgw.conf

Then verify with:

.. code-block:: none

   echo | openssl s_client -connect localhost:443 2>/dev/null \
     | openssl x509 -noout -subject -dates

Replace ``443`` with your configured SSL port if different.

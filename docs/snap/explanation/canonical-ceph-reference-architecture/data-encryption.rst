.. meta::
   :description: An overview of data encryption in Canonical Ceph, covering encryption in transit and at rest via Vault integration, and the potential impact on I/O performance.

.. _data-encryption:

Data encryption
================

Data encryption in Canonical Ceph is provided via integration with
`Vault <https://canonical-vault-charms.readthedocs-hosted.com/en/latest/>`_.
This encryption takes two forms:

* Encryption in transit
* Encryption at rest (bytes on disk)

Encryption in transit and at rest is enabled by default once Canonical Ceph is
related to a Vault ``secrets-storage`` relation. Data encryption in transit is
enabled via the `Messenger V2
<https://docs.ceph.com/en/reef/rados/configuration/msgr2/>`_ feature.

Encryption can lower the Input/Output (I/O) bandwidth of the storage layer.
The impact of this varies per workload and should be considered when deciding
whether or not encryption should be enabled.

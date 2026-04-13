.. meta::
   :description: Server hardware recommendations for Canonical Ceph deployments, including guidance on rack servers versus blade servers for private cloud storage infrastructure.

.. _server-recommendations:

Server recommendations
======================

Canonical strongly recommends using rack servers for private Ceph
implementations. Software-defined storage (SDS) platforms, like Ceph, are
designed to run on commodity hardware. Choosing rack servers for private cloud
deployment is therefore a natural move. While blade servers are very popular
for traditional virtualisation environments supported by storage area network
(SAN) storage, the overall economics for those configurations tend to be very
poor due to the inability to utilise commodity parts with multiple suppliers.
They also dramatically limit options for storage and network configuration.

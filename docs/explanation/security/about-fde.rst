=================================
Full disk encryption in MicroCeph
=================================

Overview
--------

MicroCeph supports automatic full disk encryption (FDE) on OSDs.

Full disk encryption is a security measure that protects the data on a
storage device by encrypting all the information on the disk. FDE
helps maintain data confidentiality in case the disk is lost or stolen
by rendering the data inaccessible without the correct decryption key
or password.

In the event of disk loss or theft, unauthorised individuals are
unable to access the encrypted data, as the encryption renders the
information unreadable without the proper credentials. This helps
prevent data breaches and protects sensitive information from being
misused.

FDE also eliminates the need for wiping or physically destroying a
disk when it is replaced, as the encrypted data remains secure even if
the disk is no longer in use. The data on the disk is effectively
rendered useless without the decryption key.

It's important to note that during operation of a host, the disk must
be unlocked so that programs on the machine can access it. FDE does
not protect data from exfiltration from a running system (for
instance, in case of a malware infection).


Implementation
--------------

Full disk encryption for OSDs has to be requested when adding disks.
MicroCeph will then generate a random key, store it in the Ceph
cluster configuration, and use it to encrypt the given disk via
`LUKS/cryptsetup
<https://gitlab.com/cryptsetup/cryptsetup/-/wikis/home>`_.


Prerequisites
-------------

To use FDE, the following prerequisites must be met:

- The installed snapd daemon version on the host must be >= 2.59.1
- The ``dm-crypt`` kernel module must be available. Note that some cloud-optimised kernels do not ship dm-crypt by default. Check by running ``sudo modinfo dm-crypt``
- The snap dm-crypt plug has to be connected, and ``microceph.daemon`` subsequently restarted


Limitations
-----------

.. warning::
  - It is important to note that MicroCeph FDE *only* encompasses OSDs. Other data, such as state information for monitors, logs, configuration etc., will *not* be encrypted by this mechanism.
  - Also note that the encryption key will be stored on the Ceph monitors as part of the Ceph key/value store.
  - As aluded to above, FDE protects data on disks. However while the host is running, this data will be made accessible to allow retrieval. This implies that if a malicious program were to run on the machine, it would also be able to access the data -- FDE cannot protect against this scenario.

Usage
-----

See the :doc:`how-to <../../how-to/enable-fde>` for details on enabling FDE for MicroCeph OSDs.









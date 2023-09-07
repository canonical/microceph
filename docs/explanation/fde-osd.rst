Full Disk Encryption on OSDs
============================


Overview
--------

MicroCeph supports automatic full disk encryption (FDE) on OSDs.

Full disk encryption is a security measure that protects the data on a storage device by encrypting all the information on the disk. FDE helps maintain data confidentiality in case the disk is lost or stolen by rendering the data inaccessible without the correct decryption key or password.

In the event of disk loss or theft, unauthorized individuals are unable to access the encrypted data, as the encryption renders the information unreadable without the proper credentials. This helps prevent data breaches and protects sensitive information from being misused.

FDE also eliminates the need for wiping or physically destroying a disk when it is replaced, as the encrypted data remains secure even if the disk is no longer in use. The data on the disk is effectively rendered useless without the decryption key.


Implementation
--------------

Full disk encryption for OSDs has to be requested when adding disks. MicroCeph will then generate a random key, store it in the Ceph cluster configuration, and use it to encrypt the given disk via `LUKS/cryptsetup <https://gitlab.com/cryptsetup/cryptsetup/-/wikis/home>`_.


Prerequisites
-------------

To use FDE, the following prerequisites must be met:

- The `dm-crypt` kernel module must be available. Note that some cloud-optimized kernels do not ship dm-crypt by default. Check by running `sudo modinfo dm-crypt`
- The snap dm-crypt plug has to be connected, and the microceph.daemon subsequently restarted: `sudo snap connect microceph:dm-crypt ; sudo snap restart microceph.daemon`
- The installed snapd daemon version must be >= 2.59.1


Limitations
-----------

**Warning:**

- It is important to note that MicroCeph FDE *only* encompasses OSDs. Other data, such as state information for monitors, logs, configuration etc., will *not* be encrypted by this mechanism.
- Also note that the encryption key will be stored on the Ceph monitors as part of the Ceph key/value store


Usage
-----

FDE for OSDs is activated by passing the optional ``--encrypt`` flag when adding disks:

.. code-block:: shell

    sudo microceph disk add /dev/sdx --wipe --encrypt

Note there is no facility to encrypt an OSD that is already part of the cluster. To enable encryption you will have to take the OSD disk out of the cluster, ensure data is replicated and the cluster converged and is healthy, and then re-introduce the OSD with encryption.

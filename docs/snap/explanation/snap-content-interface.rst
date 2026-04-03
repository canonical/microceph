====================================
Snap content interface for MicroCeph
====================================

Overview
--------

Snap content interfaces enable access to a particular directory from a producer snap. The MicroCeph ``ceph-conf`` content interface is designed to facilitate access to MicroCeph's configuration and credentials. This interface includes information about MON addresses, enabling a consumer snap to connect to the MicroCeph cluster using this data.

Additionally, the ``ceph-conf`` content interface also provides version information of the running Ceph software.

Usage
-----

The usage of the ``ceph-conf`` interface revolves around providing the consuming snap access to necessary configuration details. 

Here is how it can be utilised:

- Connect to the ``ceph-conf`` content interface to gain access to MicroCeph's configuration and credentials.
- The interface exposes a standard ``ceph.conf`` configuration file as well Ceph keyrings with administrative privileges. 
- Use the MON addresses included in the configuration to connect to the MicroCeph cluster.
- The interface provides version information that can be used to set up version-specific clients.

To connect the ``ceph-conf`` content interface to a consumer snap, use the following command:

::
   
  snap connect <consumer-snap-name>:ceph-conf microceph:ceph-conf


Replace ``<consumer-snap-name>`` with the name of your consumer snap. Once executed, this command establishes a connection between the consumer snap and the MicroCeph ``ceph-conf`` interface.



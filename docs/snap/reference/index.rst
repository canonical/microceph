.. meta::
   :description: MicroCeph reference information, including CLI commands and release notes. 

.. _reference:

Reference
=========

Our Reference section provides technical details about MicroCeph, such
as reference information about the command line interface and notes on
major MicroCeph releases.


CLI Commands
------------

MicroCeph has a command line interface that can be used to manage a client and the cluster, as well as query the status of any current deployment.
Each command is documented separately, or use the help argument from the command line to learn more about the commands while working with MicroCeph,
with ``microceph help``.

.. toctree::
   :maxdepth: 1

   commands/index


Release Notes
-------------

The release notes section provides details on major MicroCeph releases.

.. toctree::
   :maxdepth: 1

   release-notes

.. _ref-arch-reference-section:

Canonical Ceph hardware recommendations
-----------------------------------------

Our hardware recommendations section contains hardware specification
recommendations for Canonical Ceph clusters, basing these specifications on
cluster service placement strategy. It also includes recommendations for
infrastructure node requirements depending on the method of deployment, i.e.
via charms or snap.

.. toctree::
   :maxdepth: 1

   Canonical Ceph hardware recommendations <canonical-ceph-hardware-recommendations/index>
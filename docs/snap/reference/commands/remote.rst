.. meta::
   :description: Reference for the microceph remote command, used to import, list and remove remote MicroCeph clusters.

===========
``remote``
===========

Manage MicroCeph remotes.

Usage:

.. code-block:: none

   microceph remote [flags]
   microceph remote [command]

Available commands:

.. code-block:: none

   import      Import external MicroCeph cluster as a remote
   list        List all configured remotes for the site
   remove      Remove configured remote

Global options:

.. code-block:: none

   -d, --debug       Show all debug messages
   -h, --help        Print help
       --state-dir   Path to store state information
   -v, --verbose     Show all information messages
       --version     Print version number

``import``
----------

Import external MicroCeph cluster as a remote

The remote name and the ``--local-name`` value must each be 1 to 63 characters long and contain only lowercase letters (a-z) and digits (0-9).

Usage:

.. code-block:: none

   microceph remote import <name> <token> [flags]

Flags:

.. code-block:: none

   --local-name string   friendly local name for cluster

``list``
---------

List all configured remotes for the site

Usage:

.. code-block:: none

   microceph remote list [flags]

Flags:

.. code-block:: none

   --json   output as json string

``remove``
----------

Remove configured remote

Usage:

.. code-block:: none

   microceph remote remove <name> [flags]


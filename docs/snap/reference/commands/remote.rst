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


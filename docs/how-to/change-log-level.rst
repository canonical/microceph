===============================
Changing log level in microceph
===============================

By default, the microceph daemon runs with the log level set to DEBUG. While that is the desirable
behaviour for a good amount of use cases, there are instances when this level is prohibitely high -
for example, embedded devices where storage is much more limited. For these reasons, the microceph
daemon exposes a way to both get and set the log level.

Configuring the log level
-------------------------

Microceph includes the command `log`, with the subcommands `set-level` and `get-lvel`. When setting, we support both string and integer formats for the log level. For example:

.. code-block:: none

   sudo microceph log set-level warning
   sudo microceph log set-level 3

Both commands are equivalent. The mapping from integer to string can be consulted by querying the
help for the `set-level` subcommand. Note that any changes made to the log level take effect
immediately, and need no restarts.

On the other hand, the `get-level` subcommand takes no argument and returns an integer level only.
Any value returned by `get-level` can be used for `set-level`.

For example, after setting the level as shown in the example, we can run and verify the following:

.. code-block:: none

   sudo microceph log get-level
   3



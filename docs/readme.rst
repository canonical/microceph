Documentation starter pack
==========================

How to use this starter pack
----------------------------

Download and install
~~~~~~~~~~~~~~~~~~~~

* *Incorporate this starter pack into an existing code repository* - copy all
   the files from this repository into your project's directory structure,
   and rename this directory to ``docs`` or similar.

* *Start a standalone documentation project* - clone this locally and start
   working.

In documentation directory, run::

	make install

This invokes the ``install`` command in the ``Makefile``, and creates a
virtual environment (``sphinxenv``) and installs dependencies in
``requirements.txt``.

A complete set of pinned, known-working dependencies is included in
``pinned-requirements.txt``.


Build and serve the documentation
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Start the ``sphinx-autobuild`` documentation server::

	make run

The documentation will be available at http://127.0.0.1:8000.

The command:

* activates the virtualenv and start serving the documentation
* rebuilds the documentation each time you save a file
* sends a reload page signal to the browser when the documentation is rebuilt

(This is the most convenient way to work on the documentation, but you can still use
the more standard ``make html``.)


Configure the documentation
~~~~~~~~~~~~~~~~~~~~~~~~~~~

In ``conf.py``, you will need to check or edit several settings appropriately:

* ``project``
* ``copyright``
* ``author``
* ``release`` - only required if you're actually using release numbers
  (beyond the scope of this guide, but you can also use Python to pull this
  out of your code itself)
* ``extensions`` - add ``'sphinx-design'`` to the list
* ``html_theme`` - change to ``'furo'``

Save ``conf.py``.



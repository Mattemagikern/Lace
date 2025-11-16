Lace
====

Lace is a fast, multithreaded, asynchronous file transfer tool inspired by rsync.
It allows you to efficiently synchronize files between local and remote systems,
with support for parallel transfers and dry-run simulations.

Features
--------

- **Multithreaded Transfers**: Utilizes goroutines for parallel file operations.
- **Async Operations**: Reads, writes, and deletes are performed asynchronously.
- **Local and Remote Support**: Transfer files between local paths or remote servers via SSH.
- **Dry Run Mode**: Preview changes without modifying any files.
- **Server Mode**: Run a lightweight server instance to handle remote transfers.
- **Installation on Remote Hosts**: Easily deploy Lace to remote machines.

Installation
------------

Build from source:

.. code-block:: bash

        git clone https://github.com/Mattemagikern/lace.git
        cd lace
        ./.commands/install

Lace
====

Lace is a fast, multithreaded, asynchronous file transfer tool inspired by
rsync. It synchronizes files between local paths and remote hosts over SSH,
using a pool of workers to parallelize reads, writes, and deletes.

"Why not use Rsync?" you might ask.

No real reason. This is a hobby project I built in my spare time. Rsync is
probably the better tool, it is battle-tested, has every flag you could
imagine and is widely available.

I wrote Lace to keep my workspaces in sync between my laptop and my build
server. Verifying an already-synced tree is ~4x faster than rsync, so the
edit-on-laptop, build-on-server loop stays tight. Once files actually need
to move, throughput is bounded by the network and disk speeds rather than logic
in the tool, so the performance difference is less pronounced.

Features
--------

- **Parallel transfers**, work is sharded across goroutine workers (default:
  ``runtime.NumCPU()``, override with ``-j``).
- **Async I/O**, reads, writes, and deletes are pipelined so workers do not
  block on a single file.
- **Local and remote**, transfer between two local paths, push to a remote,
  or pull from one. Remote endpoints use the ``[user@]host[:port]:path``
  form; ``user`` and ``port`` are optional, and ``host`` may be an alias
  from ``~/.ssh/config`` (``HostName`` and ``Port`` are honored).
- **SSH-native**, remote workers are spawned over SSH; no daemon to run.
- **Dry run**, preview changes without touching the filesystem.

Installation
------------

Build from source:

.. code-block:: bash

        git clone https://github.com/Mattemagikern/lace.git
        cd lace
        ./.commands/install

This builds ``lace`` with embedded version metadata and installs it to
``~/.local/bin/lace``. Make sure that directory is on your ``PATH``.

To use ``lace`` against a remote host, install the same version of the
binary on that host as well, see `Setting up a Lace server`_ below for
the recommended setup.

Setting up a Lace server
------------------------

A "Lace server" is just an SSH-reachable host with the ``lace`` binary
installed somewhere on the default ``PATH``. There is no daemon to run and
no port to open beyond ``sshd``. The first-time setup has two parts:
placing the binary, and tuning ``sshd`` for the parallel-channel workload.

1. Place the binary on the remote host
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

The remote ``lace`` is invoked by the client as a bare ``lace --server``
command over SSH. SSH's non-interactive shells do **not** source
``~/.bashrc`` or ``~/.profile``, so the binary must live in a directory
that is on the system-default ``PATH``, in practice that means
``/usr/bin`` or ``/usr/local/bin``, both of which require root to write.

The simplest path is to install once with root access. From a workstation that
already has the lace source tree, the bundled ``./.commands/install`` script
will build the binary, ``scp`` it to the remote, and place it in
``/usr/local/bin/lace`` with ``sudo install``:

.. code-block:: bash

        # Build and install on the remote host:
        ./.commands/install user@host

        # Optional second argument overrides the destination path:
        ./.commands/install user@host /opt/bin/lace

The SSH user must be able to run ``sudo install`` on the remote (a
passwordless sudo rule for ``install`` is enough). If you prefer to do it
by hand:

.. code-block:: bash

        scp ~/.local/bin/lace user@host:/tmp/lace
        ssh user@host 'sudo install -m 0755 /tmp/lace /usr/local/bin/lace && rm /tmp/lace'

Subsequent upgrades use the same command. Both ends of the connection
must run the same Lace version, so keep them in sync.

2. Tune ``sshd`` for parallel channels
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Lace opens **one TCP connection per remote host** and multiplexes one SSH
channel per worker on top of it (the Go ``crypto/ssh`` library handles
this internally, OpenSSH ``ControlMaster`` settings in ``~/.ssh/config``
are *not* consulted). With the default of one worker per CPU core, a busy
client easily asks for more channels than a stock ``sshd`` allows.

Drop the following into ``/etc/ssh/sshd_config.d/lace.conf`` on the remote:

.. code-block:: sshd_config

        # --- Concurrency ---
        MaxSessions 128
        MaxStartups 100:30:200

        # --- Keepalives ---
        ClientAliveInterval 30
        ClientAliveCountMax 6
        TCPKeepAlive yes


3. Validate and reload ``sshd``
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Always test the config before reloading so a typo cannot lock you out of
the host:

.. code-block:: bash

        sshd -t && systemctl reload sshd

4. Verify from the client
~~~~~~~~~~~~~~~~~~~~~~~~~

Confirm the remote can find ``lace`` on the non-interactive ``PATH`` and
that the version matches your local install:

.. code-block:: bash

        ssh user@host 'lace --version'
        lace --version

If the remote command prints ``lace: command not found``, the binary is
not on the system ``PATH``, go back to step 1 and install it under
``/usr/bin`` or ``/usr/local/bin``.

Usage
-----

The general form is::

        lace [options] <source> <destination>

Either side may be a local path or a remote of the form
``user@host:port:path``. Both sides being remote is not supported.

Push a directory to a remote host:

.. code-block:: bash

        lace ./src user@host:22:/var/www/site

Pull from a remote host:

.. code-block:: bash

        lace user@host:22:/var/log/app ./logs

Local-to-local copy:

.. code-block:: bash

        lace ./src ./dest

Options
-------

============================  ===========================================
``-v``, ``--verbose``         Enable debug logging.
``-j``, ``--jobs <n>``        Number of concurrent transfer workers.
``--dryrun``                  Simulate the transfer; make no changes.
``--server``                  Run as a server instance (used internally
                              by the client over SSH).
``--version``                 Print version and exit.
``-h``, ``--help``            Print help and exit.
============================  ===========================================

How it works
------------

The client walks the source tree and dispatches per-file work to a pool of
workers. Each worker holds an ``exec.Pipe``, either a local pipe
(``osexec``) or an SSH pipe that runs ``lace --server`` on the far end.
Server instances speak the same wire protocol as local workers, so the same
client logic drives both local and remote transfers.

Because the remote side is just ``lace`` invoked with ``--server``, the
binary on both ends must match, keep them in sync when upgrading.

Example use cases
-----------------

The ``tether`` script wraps lace to provide a push-build-pull workflow for
remote execution / development.
It syncs a local tree to a remote host, runs a command there, and optionally
pulls the results back.
In addition to the use cases mentioned below, see ``tether --help`` for more options.

Push source to a build server and compile
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Push your local source tree to the remote host and run a build command:

.. code-block:: bash

        tether make -j8

This runs ``lace`` to sync the current directory to the remote, then
executes ``make -j8`` over SSH.

Push, build, and pull results back
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Use ``--pull`` to sync the remote tree back after the command finishes:

.. code-block:: bash

        tether --pull make -j8

After the build completes, the updated remote tree (e.g. compiled objects,
artifacts) is synced back to the local directory.

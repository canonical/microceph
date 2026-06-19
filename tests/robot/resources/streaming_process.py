"""Robot Framework library: run a shell command with real-time console output."""

import os
import signal
import subprocess
import sys
import threading


def run_streaming_process(cmd, timeout=None, xtrace=False):
    """Run *cmd*, printing each line to the console immediately.

    *cmd* may be either a shell command string (run via the shell) or an argv
    list (run directly with ``shell=False``). The argv form is for callers that
    build a command from dynamic components -- a VM name, a script path, args --
    so that spaces or shell metacharacters in those components cannot be
    reinterpreted by a shell. *xtrace* applies only to the string form.

    If *xtrace* is True (string form) the command is prefixed with ``bash -x`` so
    every sub-command is echoed as it executes (equivalent to ``set -x``).

    *xtrace* only works when *cmd* is a direct script invocation
    (``/path/script.sh args``): bash takes the first word as a script file
    operand, so the whole script body runs traced. It must not be used with
    nested-shell commands such as ``lxc exec ... -- bash -c "..."`` --
    ``bash -x lxc ...`` would misparse the ``lxc`` binary as a script file.
    For those, put ``-x`` on the shell that executes the script (see the
    ``Run Script In VM With Trace`` keyword in microceph_harness.py, which passes
    an argv list with ``-x`` on the script's bash).

    Returns a two-element list ``[rc, combined_output]`` so Robot callers
    can unpack it with the multi-assignment syntax::

        ${rc}    ${out}=    Run Streaming Process    ${cmd}

    *timeout* is in seconds; None means no limit.
    """
    use_shell = isinstance(cmd, str)
    if use_shell and str(xtrace).upper() in ("TRUE", "YES", "1"):
        cmd = f"bash -x {cmd}"

    timeout_int = int(timeout) if timeout is not None else None

    # start_new_session=True places the shell and all its children in a new
    # process group (PGID == proc.pid).  On timeout we kill the whole group so
    # grandchild processes (e.g. "sleep N" spawned by a shell one-liner) cannot
    # keep the stdout pipe open and block the reader thread.
    # stdin=DEVNULL: scripts run here contain lxc launch/exec calls, which read
    # stdin to EOF when it is not a tty; inheriting Robot's stdin can block
    # forever (e.g. a pipe that never closes).  /dev/null gives instant EOF.
    proc = subprocess.Popen(
        cmd,
        shell=use_shell,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
        start_new_session=True,
    )

    # Robot Framework redirects sys.stdout to its log buffer; write to the
    # original stdout so lines appear on the console as they are produced.
    console = sys.__stdout__ or sys.stdout
    lines = []

    def _reader():
        for line in proc.stdout:
            console.write(line)
            console.flush()
            lines.append(line)

    # The reader's "for line in proc.stdout" loop only ends when the pipe
    # closes, which only happens once the process (and its group) exits -- so
    # the reader thread on its own offers no protection against a hung child.
    # That is why the reader runs as a daemon thread and proc.wait(timeout=) on
    # the main thread is the actual hang guard: wait() enforces the deadline
    # independently of the reader, returning/raising whether or not stdout has
    # closed.  On timeout we kill the whole process group, which closes the
    # pipe and ends the reader loop; join() then drains any final buffered
    # lines.
    thread = threading.Thread(target=_reader, daemon=True)
    thread.start()

    try:
        proc.wait(timeout=timeout_int)
    except subprocess.TimeoutExpired:
        os.killpg(proc.pid, signal.SIGKILL)
        proc.wait()
        thread.join(timeout=5)
        raise RuntimeError(f"Process timed out after {timeout}s: {cmd}")

    thread.join()
    return [proc.returncode, "".join(lines)]

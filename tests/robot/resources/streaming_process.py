"""Robot Framework library: run a shell command with real-time console output."""

import os
import signal
import subprocess
import sys
import threading


def run_streaming_process(cmd, timeout=None, xtrace=False):
    """Run *cmd* in a shell, printing each line to the console immediately.

    If *xtrace* is True the command is prefixed with ``bash -x`` so every
    sub-command is echoed as it executes (equivalent to ``set -x``).

    Returns a two-element list ``[rc, combined_output]`` so Robot callers
    can unpack it with the multi-assignment syntax::

        ${rc}    ${out}=    Run Streaming Process    ${cmd}

    *timeout* is in seconds; None means no limit.
    """
    if str(xtrace).upper() in ("TRUE", "YES", "1"):
        cmd = f"bash -x {cmd}"

    timeout_int = int(timeout) if timeout is not None else None

    # start_new_session=True places the shell and all its children in a new
    # process group (PGID == proc.pid).  On timeout we kill the whole group so
    # grandchild processes (e.g. "sleep N" spawned by a shell one-liner) cannot
    # keep the stdout pipe open and block the reader thread.
    proc = subprocess.Popen(
        cmd,
        shell=True,
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

    # The reader runs in a daemon thread so proc.wait(timeout=) on the main
    # thread is the actual hang guard.  Killing the process group closes the
    # pipe, which ends the reader loop; join() then drains any final buffered
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

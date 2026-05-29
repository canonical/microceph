"""Robot Framework library: run a shell command with real-time console output."""

import subprocess
import sys


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

    proc = subprocess.Popen(
        cmd,
        shell=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )

    # Robot Framework redirects sys.stdout to its log buffer; write to the
    # original stdout so lines appear on the console as they are produced.
    console = sys.__stdout__ or sys.stdout
    lines = []
    try:
        for line in proc.stdout:
            console.write(line)
            console.flush()
            lines.append(line)
        proc.wait(timeout=timeout_int)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
        raise RuntimeError(f"Process timed out after {timeout}s: {cmd}")

    return [proc.returncode, "".join(lines)]

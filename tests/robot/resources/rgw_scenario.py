"""Run RGW placement scenarios inside one guest and print their results as JSON.

Robot copies this script to the coordinating guest and runs it in the foreground.
The migration scenario has a PUT in the caller thread and one sampler worker;
the contention scenario has two PUT workers. Each PUT owns a bounded curl child.
All workers finish before the script returns, so there are no detached jobs,
completion-marker files, or separate result-collection commands.
"""

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import subprocess
import threading
from urllib.parse import urlencode
from urllib.request import urlopen


def put_placement(socket_path, body, target=None, timeout=600):
    """Send a policy to the guest's MicroCeph daemon and return its response text.

    --unix-socket selects the local control socket, not a TCP connection to
    localhost. An optional target query asks that daemon to forward the request
    to another member. body is passed to curl on stdin; @- reads that input
    directly, without a request file or shell interpolation.

    Deliberately omit --fail: HTTP 409/500 bodies are results for Robot to check,
    not curl process failures. check=True still raises on transport failures.
    curl enforces the request deadline; subprocess.run has a one-second margin
    before killing and waiting for the child if curl itself fails to exit.
    """
    url = "http://localhost/1.0/placement"
    if target:
        url += "?" + urlencode({"target": target})
    return subprocess.run(
        ["curl", "--silent", "--show-error", "--max-time", str(timeout),
         "--unix-socket", socket_path, "-X", "PUT", "-H", "Content-Type: application/json",
         "--data-binary", "@-", url],
        input=body, capture_output=True, text=True, check=True, timeout=timeout + 1,
    ).stdout


def serves_object(host, port, path, expected):
    """Fetch the actual object from a gateway's public HTTP listener.

    This is a normal network read, separate from the PUT's Unix control socket.
    Require the expected content, not merely a successful connection. Socket
    operations have a one-second timeout; HTTP/network errors or an unreadable
    response count as a failed read, allowing the other gateway to be checked.
    """
    address = f"[{host}]" if ":" in host else host
    try:
        with urlopen(f"http://{address}:{port}{path}", timeout=1) as response:
            return response.read().decode().strip() == expected
    except (OSError, ValueError):
        return False


def run_migration(socket_path, body, old, new, port, path, expected, timeout=600):
    """Run a blocking PUT beside one sampler, returning the response and verdict.

    The startup barrier brings the caller and sampler to the same starting
    point; it does not synchronize either with the daemon's internal work.
    While the caller waits for the PUT, the worker owns all observation counters
    and repeatedly reads old then new. No shared result files or counter locks
    are needed: the caller only takes the verdict after the worker has finished.

    A stop event ends sampling when the PUT returns or raises. Exiting the
    executor waits for the worker, including any object read already in progress.
    """
    ready = threading.Barrier(2)
    stop = threading.Event()

    def sample():
        """Count read pairs begun before stop; a final pair may finish after the PUT.

        These are ordered observations, not an atomic snapshot of both gateways.
        Zero observations cannot establish availability and must not pass.
        """
        count = 0
        available = True
        replacement_ready = False
        ready.wait()
        while not stop.is_set():
            # Read in handover order. New-first can miss both sides of a
            # successful add-before-remove transition between the two reads.
            old_ok = serves_object(old, port, path, expected)
            new_ok = serves_object(new, port, path, expected)
            count += 1
            available = available and (old_ok or new_ok)
            replacement_ready = replacement_ready or new_ok
            # Wait between samples, but wake immediately if the PUT has finished.
            stop.wait(0.1)
        return {
            "samples": count,
            "available": count > 0 and available,
            "replacement_ready": replacement_ready,
        }

    with ThreadPoolExecutor(max_workers=1) as workers:
        # submit schedules the sampler and returns a future for its eventual verdict.
        sampler = workers.submit(sample)
        ready.wait()
        try:
            response = put_placement(socket_path, body, timeout=timeout)
        finally:
            # Also stop the sampler on transport errors or request timeouts.
            # The executor joins it before any return or exception escapes.
            stop.set()
        # result waits for the verdict and propagates any sampler exception.
        observation = sampler.result()
    return {"response": response, "observation": observation}


def run_concurrent(socket_path, body_a, body_b, target_b, timeout=600):
    """Send two PUTs from this guest, returning their responses in A, B order.

    Each worker owns a blocking curl call. A two-party barrier prevents either
    worker from issuing its request before the other has reached the start gate.
    Network and daemon scheduling still decide whether they overlap and who wins
    the lock, so Robot must accept either ordering of the 200/409 responses.

    The executor waits for both workers even if retrieving one result raises;
    neither request is left running after this function exits.
    """
    ready = threading.Barrier(2)

    def request(body, target):
        """Rendezvous with the other worker, then perform this worker's PUT."""
        ready.wait()
        return put_placement(socket_path, body, target, timeout)

    with ThreadPoolExecutor(max_workers=2) as workers:
        first = workers.submit(request, body_a, None)
        second = workers.submit(request, body_b, target_b)
        # Both workers have been submitted. Waiting for A's future first does
        # not delay B; it only keeps the returned list in submission order.
        return {"responses": [first.result(), second.result()]}


def main():
    """Dispatch one foreground scenario and print its complete JSON result.

    The host supplies policy JSON as arguments, not paths to temporary files.
    Only the outer result is encoded here; API responses remain raw JSON strings
    for Robot's response parser. Uncaught transport/worker errors exit non-zero
    with diagnostics on stderr instead of printing a successful-looking result.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--socket", required=True)
    parser.add_argument("--timeout", type=float, default=600)
    scenarios = parser.add_subparsers(dest="scenario", required=True)
    migration = scenarios.add_parser("migration")
    migration.add_argument("body")
    migration.add_argument("old")
    migration.add_argument("new")
    migration.add_argument("port", type=int)
    migration.add_argument("path")
    migration.add_argument("expected")
    concurrent = scenarios.add_parser("concurrent")
    concurrent.add_argument("body_a")
    concurrent.add_argument("body_b")
    concurrent.add_argument("target_b")
    args = parser.parse_args()
    if args.scenario == "migration":
        result = run_migration(
            args.socket, args.body, args.old, args.new, args.port, args.path, args.expected, args.timeout,
        )
    else:
        result = run_concurrent(args.socket, args.body_a, args.body_b, args.target_b, args.timeout)
    print(json.dumps(result))


if __name__ == "__main__":
    main()

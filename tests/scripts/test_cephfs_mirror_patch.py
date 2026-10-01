#!/usr/bin/env python3
"""Check the mirror callback patch against the staged Ceph source, without a cluster."""
import ast
import logging
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
from types import MethodType, SimpleNamespace


def callback_does_not_block(source):
    tree = ast.parse(source.read_text())
    policy = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == "FSPolicy")
    methods = [node for node in policy.body if isinstance(node, ast.FunctionDef)
               and node.name in ("handle_peer_ack", "_handle_peer_ack")]
    namespace = {"log": logging.getLogger(__name__)}
    exec(compile(ast.Module(body=methods, type_ignores=[]), str(source), "exec"), namespace)
    queued, completed = [], []
    instance = SimpleNamespace(
        lock=threading.Lock(), stopping=threading.Event(),
        finisher=SimpleNamespace(queue=lambda fn, args: queued.append((fn, args))),
        continue_action=lambda *args: completed.append(args),
        op_tracker=SimpleNamespace(finish_async_op=lambda: completed.append("finished")),
    )
    for method in methods:
        setattr(instance, method.name, MethodType(namespace[method.name], instance))
    returned = threading.Event()

    def acknowledge():
        instance.handle_peer_ack("/dir", 0)
        returned.set()

    # remove_dir holds this lock while waiting for another librados callback.
    instance.lock.acquire()
    worker = threading.Thread(target=acknowledge, daemon=True)
    worker.start()
    try:
        nonblocking = returned.wait(1)
    finally:
        instance.lock.release()
    worker.join(2)
    assert not worker.is_alive(), "callback did not finish after releasing the lock"
    if nonblocking:
        assert len(queued) == 1, "acknowledgement was not queued on the finisher"
        fn, args = queued[0]
        fn(*args)
    assert completed == [(["/dir"], [], 0), "finished"]
    return nonblocking


def main():
    stage = Path(sys.argv[1])
    relative = Path("share/ceph/mgr/mirroring/fs/snapshot_mirror.py")
    patch = Path(__file__).resolve().parents[2] / "patches/0003-cephfs-mirror-queue-peer-ack.patch"
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        source = root / relative
        source.parent.mkdir(parents=True)
        shutil.copyfile(stage / relative, source)
        assert not callback_does_not_block(source), "upstream callback changed; reevaluate the patch"
        with patch.open() as data:
            subprocess.run(["patch", "--fuzz=0", "-p1", "-d", str(root)], stdin=data, check=True)
        assert callback_does_not_block(source), "patched callback still blocks librados"
    print("CephFS mirror callback regression check passed")


if __name__ == "__main__":
    main()

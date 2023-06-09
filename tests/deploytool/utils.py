import uuid
import time
import re


def create_node(client, log, image):
    name = "microceph-" + str(uuid.uuid4())[0:5]
    osd = create_osd(client, log)
    config = {
        "name": name,
        "description": "microceph_managed",
        "source": {
            "type": "image",
            "mode": "pull",
            "server": "https://images.linuxcontainers.org",
            "protocol": "simplestreams",
            "alias": image,
        },
        "config": {"limits.cpu": "2", "limits.memory": "4GB"},
        "type": "virtual-machine",
        "devices": {
            "root": {"path": "/", "pool": "default", "size": "8GB", "type": "disk"},
            "osd": {"pool": "default", "source": osd, "type": "disk"},
        },
    }
    log.info("creating node " + name)
    inst = client.instances.create(config, wait=True)
    inst.start(wait=True)
    instance_ready(inst, log)

    # the default kvm-clock doesnt appear to be good enough for ceph's
    # clock-skew detection.
    wrap_cmd(inst, "apt install chrony -y", log)

    snapd_ready(inst, log)
    return inst


def create_osd(client, log):
    """
    creates a block device for use as osd
    """
    rnd = str(uuid.uuid4())[0:5]
    name = "microceph-osd-{}".format(rnd)
    storage_pool = client.storage_pools.get("default")
    storage_pool.volumes.create(
        {
            "name": name,
            "type": "custom",
            "content_type": "block",
            "description": "microceph-managed",
            "config": {"size": "8GiB"},
        }
    )
    return name


def snapd_ready(instance, log):
    """
    configures a given Instance
    blocks until snapd is "ready"
    """
    wrap_cmd(instance, "apt install snapd -y", log)
    """
    snapd appears to perform some bootstrapping asynchronously with
    'apt install', so subsequent 'snap install's can fail for a moment.
    therefore we run this 'snap refesh' as a dummy-op to ensure snapd is
    up before continuing.
    """
    poll_cmd(instance, "snap refresh", log)
    return


def instance_ready(instance, log):
    """
    waits until an instance is executable.
    pylxd's wait=True parameter alone does not guarantee the agent is running.
    """
    poll_cmd(instance, "hostname", log)
    return


def poll_cmd(instance, cmd, log):
    """
    given a shell string, retries for up to a minute for return code == 1.
    """
    count = 30
    for i in range(count):
        if i == count - 1:
            raise RuntimeError(
                "timed out waiting for command `{}` on {}".format(cmd, instance.name)
            )

        log.debug("waiting for command `{}` on {}".format(cmd, instance.name))
        time.sleep(2)
        try:
            res = wrap_cmd(instance, cmd, log)
            if res.exit_code == 0:
                return res
        except RuntimeError:
            continue
        except BrokenPipeError:
            continue
        except ConnectionResetError:
            continue


def wrap_cmd(instance, cmd, log):
    log.debug("executing `{}` on {}".format(cmd, instance.name))
    res = instance.execute(cmd.split())
    if res.exit_code != 0:
        raise RuntimeError(res.stderr)
    if res.stdout:
        log.debug(res.stdout)
    return res


def install_snap(instance, snap, log):
    """
    given an Instance, install the Snap
    """
    instance.files.put("/root/{}.snap".format(snap.name), snap.snap)
    if snap.local:
        wrap_cmd(
            instance, "snap install /root/{}.snap --dangerous".format(snap.name), log
        )
    else:
        instance.files.put("/root/{}.assert".format(snap.name), snap.assertion)
        wrap_cmd(instance, "snap ack /root/{}.assert".format(snap.name), log)
        wrap_cmd(instance, "snap install /root/{}.snap".format(snap.name), log)


def microceph_running(instance, log):
    """
    microceph takes some amount of time to start up immediately after 'snap install'
    """
    poll_cmd(instance, "test -e /var/snap/microceph/common/state/control.socket", log)
    return


def microceph_ready(node, log):
    """
    waits for and asserts microceph is 'ready' on given instance
    """
    count = 30
    for i in range(count):
        cmd = node.execute(["/snap/bin/microceph", "cluster", "list"])
        if cmd.exit_code != 0:
            raise RuntimeError(cmd.stderr)
        if re.search("{}.*ONLINE".format(node.name), cmd.stdout):
            log.info("{} is ready".format(node.name))
            return
        if i == count - 1:
            raise RuntimeError(
                "timed out waiting for microceph to become ready on {}".format(
                    node.name
                )
            )
        time.sleep(2)


def join_cluster(leader, node, log):
    join_key = wrap_cmd(
        leader, "/snap/bin/microceph cluster add {}".format(node.name), log
    )
    wrap_cmd(node, "/snap/bin/microceph cluster join {}".format(join_key.stdout), log)
    return


def cleanup(client, log):
    for i in client.instances.all():
        if re.search("^microceph-[a-f0-9]{5}$", i.name):
            if "microceph_managed" in i.description:
                if i.status == "Running":
                    i.stop(wait=True)
                i.delete(wait=True)
                log.info(i.name + " deleted")

    for v in client.storage_pools.get("default").volumes.all():
        # todo: pylxd doesnt appear to be propogating v.description
        if re.search("^microceph-osd-[a-f0-9]{5}$", v.name):
            v.delete()
            log.info("block device {} deleted".format(v.name))

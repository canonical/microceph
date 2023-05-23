import uuid
import time
import re


def create_node(client, log, image):
    name = 'microceph-' + str(uuid.uuid4())[0:5]
    config = {'name': name,
              'source': {'type': 'image',
                         'mode': 'pull',
                         'server': 'https://images.linuxcontainers.org',
                         'protocol': 'simplestreams',
                         'alias': image},
              'config': {'limits.cpu': '2',
                         'limits.memory': '4GB'},
              'type': 'virtual-machine',
              'devices': {'root': {'path': '/',
                                   'pool': 'default',
                                   'size': '8GB',
                                   'type': 'disk'}}}
    log.info('creating node ' + name)
    inst = client.instances.create(config, wait=True)
    inst.description = 'microceph_managed'
    inst.save(wait=True)
    inst.start(wait=True)
    instance_ready(inst, log)
    snapd_ready(inst, log)
    return inst


def snapd_ready(instance, log):
    '''
    configures a given Instance
    blocks until snapd is "ready"
    '''
    # "(execute) returns a tuple of (exit_code, stdout, stderr).
    #  This method will block while the command is executed"
    # snapd appears to perform some bootstrapping actions after this exits
    cmd = instance.execute(['apt', 'install', 'snapd', '-y'])
    if cmd.exit_code != 0:
        log.info(cmd.stderr)
        exit(1)
    log.info('snapd installed')

    '''
    snapd appears to perform some bootstrapping asynchronously with
    'apt install', so subsequent 'snap install's can fail for a moment.
    therefore we run this 'snap refesh' as a dummy-op to ensure snapd is
    up before continuing.
    '''
    count = 30
    for i in range(count):
        out = instance.execute(['snap', 'refresh'])
        if out.exit_code == 0:
            log.info(out.stdout)
            break
        if i == count - 1:
            log.info('timed out waiting for snapd on {}'.format(instance.name))
            exit(1)
        log.info(out.stderr)
        time.sleep(2)


def instance_ready(instance, log):
    '''
    waits until an instance is executable.
    pylxd's wait=True parameter alone does not guarantee the agent is running.
    '''
    count = 30
    for i in range(count):
        try:
            if instance.execute(['hostname']).exit_code == 0:
                return
        except BrokenPipeError:
            continue
        if i == count - 1:
            log.info('timed out waiting for lxd agent on {}'.format(instance.name))
            exit(1)
        log.info('waiting for lxd agent on ' + instance.name)
        time.sleep(2)


def install_snap(instance, snap, log):
    '''
    given an Instance, install the Snap
    '''
    instance.files.put('/root/{}.snap'.format(snap.name), snap.snap)
    if snap.local:
        cmd = instance.execute(['snap', 'install', '/root/{}.snap'.format(snap.name), '--dangerous'])
        if cmd.exit_code != 0:
            log.info(cmd.stderr)
            exit(1)
        log.info(cmd.stdout)
    else:
        instance.files.put('/root/{}.assert'.format(snap.name), snap.assertion)
        cmd = instance.execute(['snap', 'ack', '/root/{}.assert'.format(snap.name)])
        if cmd.exit_code != 0:
            log.info(cmd.stderr)
            exit(1)
        log.info(cmd.stdout)

        cmd = instance.execute(['snap', 'install', '/root/{}.snap'.format(snap.name)])
        if cmd.exit_code != 0:
            log.info(cmd.stderr)
            exit(1)
        log.info(cmd.stdout)


def microceph_running(node, log):
    count = 30
    for i in range(count):
        cmd = node.execute(['stat', '/var/snap/microceph/common/state/control.socket'])
        if cmd.exit_code == 0:
            return
        else:
            log.info('waiting for microceph to start on {}'.format(node.name))
        if i == count - 1:
            log.info('timed out waiting for microceph to start on {}'.format(node.name))
            exit(1)


def bootstrap_microceph(node, log):
    cmd = node.execute(['/snap/bin/microceph', 'cluster', 'bootstrap'])
    if cmd.exit_code != 0:
        log.info(cmd.stderr)
        exit(1)
    log.info(cmd.stdout)


def microceph_ready(node, log):
    count = 30
    for i in range(count):
        cmd = node.execute(['/snap/bin/microceph', 'cluster', 'list'])
        if cmd.exit_code != 0:
            log.info(cmd.stderr)
            exit(1)
        if re.search('{}.*ONLINE'.format(node.name), cmd.stdout):
            log.info('{} is ready'.format(node.name))
            return
        if i == count - 1:
            log.info('timed out waiting for microceph to become readt on {}'.format(node.name))
            exit(1)


def join_cluster(leader, node, log):
    join_key = leader.execute(['/snap/bin/microceph', 'cluster', 'add', node.name])
    if join_key.exit_code != 0:
        log.info(join_key.stderr)
        exit(1)

    join = node.execute(['/snap/bin/microceph', 'cluster', 'join', join_key.stdout])
    if join.exit_code != 0:
        log.info(join.stderr)
        exit(1)
    else:
        log.info(join.stdout)
        return


def cleanup(client, log):
    for i in client.instances.all():
        if 'microceph_managed' in i.description:
            log.info('found ' + i.name)
            i.stop(wait=True)
            i.delete()
            log.info(i.name + ' deleted')

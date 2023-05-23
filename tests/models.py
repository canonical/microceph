import utils


class Cluster:
    '''
    a microceph Cluster is made up of lxd Instances.
    a Cluster can exist without having been bootstrapped.
    '''
    def __init__(self, size):
        self.size = size
        self.members = []

    def bootstrap(self, channel, client, log, image):
        '''
        given a cluster, create the nodes in lxd.
        '''

        for i in range(self.size):
            node = utils.create_node(client, log, image)
            self.members.append(node)

            # use the first node to download the Snap, then pass it along.
            if i == 0:
                microceph = Snap('microceph', channel, node, log)

            utils.install_snap(node, microceph, log)
            utils.microceph_running(node, log)

            if i == 0:
                utils.bootstrap_microceph(node, log)
            else:
                utils.join_cluster(self.members[0], node, log)

            utils.microceph_ready(node, log)


class Snap:
    '''
    a snap assertion and data file
    '''
    def __init__(self, name, channel, inst, log):
        '''
        to initialize a Snap, we need a name, channel, and surrogate Instance
        '''
        self.name = name

        log.info('downloading snap {} {}'.format(name, channel))
        err = inst.execute(['snap', 'download', self.name,
                            '--channel={}'.format(channel),
                            '--target-directory=/tmp/',
                            '--basename={}'.format(self.name)])
        if err.exit_code != 0:
            log.info(err.stderr)
            log.info(err.stdout)
            log.info('snap install failed')
            exit(1)
        log.info(err.stdout)

        log.info('retrieving initial snap')
        self.snap = inst.files.get('/tmp/{}.snap'.format(self.name))
        self.assertion = inst.files.get('/tmp/{}.assert'.format(self.name))

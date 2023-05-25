import argparse
import pylxd
import logging

import models
import utils


def main():

    logger = logging.getLogger(__name__)
    logging.basicConfig(level=logging.INFO)
    logging.getLogger('ws4py').setLevel(logging.WARNING)

    parser = argparse.ArgumentParser()
    parser.add_argument('--create', action='store_true', help='Create a cluster')
    parser.add_argument('-n', type=int, default=3, help='Node count.  Defaults to 3.')
    parser.add_argument('--channel', default='latest/stable',
                        help='Snap channel.  Defaults to latest/stable.  If value is a local path, an offline installation will be attempted.')
    parser.add_argument('--image', default='ubuntu/22.04/cloud', help='lxd image to use for cluster nodes.  Defaults to ubuntu/22.04/cloud.')
    parser.add_argument('--cleanup', action='store_true', help='Remove all microceph lxd instances')
    args = parser.parse_args()

    client = pylxd.Client()

    if args.cleanup:
        utils.cleanup(client, logger)
        exit(0)

    if args.create:
        ceph = models.Cluster(args.n)
        ceph.bootstrap(args.channel, client, logger, args.image)
        logger.info('cluster created with members:')
        for m in ceph.members:
            logger.info(m.name)


if __name__ == '__main__':
    main()

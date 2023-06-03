from deploytool import utils
import os.path
import subprocess


class Cluster:
    """
    a microceph Cluster is made up of lxd Instances.
    a Cluster can exist without having been bootstrapped.
    """

    def __init__(self, size):
        self.size = size
        self.members = []

    def bootstrap(self, channel, client, log, image):
        """
        given a cluster, create the nodes in lxd.
        """
        microceph = Snap("microceph", channel, log)
        for i in range(self.size):
            if i == 0:
                self.add_node(channel, client, log, image, microceph, initial=True)
            else:
                self.add_node(channel, client, log, image, microceph, initial=False)

    def add_node(self, channel, client, log, image, microceph, initial):
        node = utils.create_node(client, log, image)
        self.members.append(node)
        utils.install_snap(node, microceph, log)
        utils.microceph_running(node, log)

        if initial:
            utils.wrap_cmd(node, "/snap/bin/microceph cluster bootstrap", log)
        else:
            utils.join_cluster(self.members[0], node, log)

        utils.microceph_ready(node, log)
        utils.wrap_cmd(node, "snap connect microceph:block-devices", log)
        utils.wrap_cmd(node, "snap connect microceph:hardware-observe", log)
        try:
            utils.wrap_cmd(node, "snap connect microceph:dm-crypt", log)
        except RuntimeError as err:
            log.info("snap may not implement microceph:dm-crypt : {}".format(err))
        utils.wrap_cmd(node, "/snap/bin/microceph disk add /dev/sdb", log)


class Snap:
    """
    a snap assertion and data file
    """

    def __init__(self, name, channel, log):
        """
        A Snap may potentially be initialized with a host file path rather than a channel.
        """
        self.name = name
        self.local = os.path.exists(channel)

        if self.local:
            log.info("importing snap {} from {}".format(name, channel))
            self.snap = open(channel, mode="rb").read()
        else:
            if not os.path.exists(".cache"):
                os.mkdir(".cache")
            log.info("downloading snap {} {}".format(name, channel))
            subprocess.run(
                [
                    "snap",
                    "download",
                    self.name,
                    "--channel={}".format(channel),
                    "--target-directory=.cache",
                    "--basename={}".format(self.name),
                ]
            )
            with open(".cache/{}.snap".format(name), mode="rb") as f:
                self.snap = f.read()
            with open(".cache/{}.assert".format(name), mode="rb") as f:
                self.assertion = f.read()

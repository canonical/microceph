import json
import pylxd
from time import sleep

from deploytool import utils


def main(cluster, log):
    assert_ceph_healthy(cluster, log, 10)
    grow_cluster(cluster, log)


def assert_ceph_healthy(cluster, log, timeout):
    if timeout == 0:
        raise RuntimeError("ceph did not reach HEALTH_OK within timeout")

    leader = cluster.members[0]
    res = utils.wrap_cmd(leader, "/snap/bin/microceph.ceph status --format json", log)
    ceph_status_json = res.stdout
    ceph_status = json.loads(ceph_status_json)["health"]["status"]
    log.info("ceph status is: {}".format(ceph_status))

    if ceph_status != "HEALTH_OK":
        sleep(1)
        assert_ceph_healthy(cluster, log, timeout - 1)


def grow_cluster(cluster, log):
    """
    a grown cluster should return to HEALTH_OK in reasonable time
    """
    client = pylxd.Client()
    cluster.add_node(client, log, initial=False)
    assert_ceph_healthy(cluster, log, 30)

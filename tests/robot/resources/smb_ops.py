"""Robot Framework library: pure helpers for the smb-tests suite.

Follows the "fetch raw, decide in Python" harness rule: the suite runs the
minimum remote command (ctdb -X status, ctdb ip) and every parse/decision
lives here, unit-tested in test_harness_helpers.py with no LXD.
"""

import ipaddress


def smb_vip_addresses(cidr, count, base_offset=200):
    """Return *count* CTDB public addresses ("IP/prefix") from *cidr*.

    VIPs are taken from a high host offset (default .200 upwards) so they do
    not collide with the DHCP range LXD hands to the inner containers. Raises
    ValueError when the request walks past the last usable host address.
    """
    network = ipaddress.ip_network(cidr, strict=False)
    base = int(network.network_address)
    addresses = []
    for i in range(count):
        candidate = ipaddress.ip_address(base + base_offset + i)
        if candidate not in network or candidate == network.broadcast_address:
            raise ValueError(f"VIP offset {base_offset + i} is outside {cidr}")
        addresses.append(f"{candidate}/{network.prefixlen}")
    return addresses


def ctdb_ok_node_count(xstatus_text):
    """Return the number of healthy nodes in ``ctdb -X status`` output.

    The machine-readable format is one header row plus one row per node:
    |Node|IP|Disconnected|Unknown|Banned|Disabled|Unhealthy|Stopped|Inactive|PartiallyOnline|ThisNode|
    A node is healthy when every flag column (Disconnected through
    PartiallyOnline) is 0.
    """
    count = 0
    for line in xstatus_text.splitlines():
        cols = line.strip().strip("|").split("|")
        if len(cols) < 11 or cols[0] == "Node":
            continue
        if all(flag == "0" for flag in cols[2:10]):
            count += 1
    return count


def ctdb_vip_pnn(ip_output, vip):
    """Return the node number hosting *vip* from ``ctdb ip`` output, or -1.

    The plain format is a "Public IPs on node N" header followed by
    "<address> <pnn>" lines; *vip* may be given with or without a /prefix.
    """
    address = vip.split("/")[0]
    for line in ip_output.splitlines():
        cols = line.split()
        if len(cols) == 2 and cols[0] == address:
            try:
                return int(cols[1])
            except ValueError:
                return -1
    return -1

Create a cluster for end-to-end testing.

```
python3 main.py -h
usage: main.py [-h] [--create] [-n N] [--channel CHANNEL] [--image IMAGE] [--cleanup]

optional arguments:
  -h, --help         show this help message and exit
  --create           Create a cluster
  -n N               Node count. Defaults to 3.
  --channel CHANNEL  Snap channel. Defaults to latest/stable.
  --image IMAGE      lxd image to use for cluster nodes. Defaults to ubuntu/22.04/cloud.
  --cleanup          Remove all microceph lxd instances
```

Example:

```
python3 main.py --create -n 3 --image ubuntu/lunar
...truncated...
INFO:__main__:cluster created with members:
INFO:__main__:microceph-1178e
INFO:__main__:microceph-c5db2
INFO:__main__:microceph-a02ec

lxc exec microceph-1178e -- /snap/bin/microceph cluster list
+-----------------+--------------------+-------+------------------------------------------------------------------+--------+
|      NAME       |      ADDRESS       | ROLE  |                           FINGERPRINT                            | STATUS |
+-----------------+--------------------+-------+------------------------------------------------------------------+--------+
| microceph-1178e | 10.11.228.107:7443 | voter | 283f1996dd1e5b8f8a288b85141677078a0ba2fc2d519aca6af17c2e8633bfde | ONLINE |
+-----------------+--------------------+-------+------------------------------------------------------------------+--------+
| microceph-a02ec | 10.11.228.150:7443 | voter | 113b9c30de77e3501add27615617449ef154a3d8bf9343cbfd628bb6979c59cd | ONLINE |
+-----------------+--------------------+-------+------------------------------------------------------------------+--------+
| microceph-c5db2 | 10.11.228.155:7443 | voter | dd24adb9d6169e1cd9f4adae022a901850808665f4fef696a62d13acaa035eac | ONLINE |
+-----------------+--------------------+-------+------------------------------------------------------------------+--------+
```

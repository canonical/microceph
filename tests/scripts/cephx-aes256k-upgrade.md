# CephX aes256k upgrade + rotation (CVE-2025-30156)

19.2.6 adds the `aes256k` cephx key type. Upgrade alone does not fix it — rotate every key, then disallow `aes`.

Upgrade is a per-node `snap refresh`; an upgraded cluster keeps `aes` allowed until you cut over.

Kernel clients need Linux 7.0+ (Noble HWE 7.0). Do not rotate a kernel-client key, or cut over, while a client is on an older kernel.

## Run the test

```
sudo -E tests/scripts/cephx-aes256k-upgrade.sh
# snap under test defaults to revision 1939; override with a revision, channel, or local .snap:
sudo MICROCEPH_SNAP=/path/to/microceph_19.2.6.snap -E tests/scripts/cephx-aes256k-upgrade.sh
```

## Manual sequence

Pre:

```
sudo microceph.ceph -s                 # HEALTH_OK
sudo microceph.ceph osd set noout
```

Upgrade — every node, one at a time, wait for OSDs between:

```
sudo snap refresh microceph --channel squid/stable     # the channel once it carries 19.2.6
sudo snap restart microceph.daemon
sudo microceph.ceph osd stat           # wait: all up
sudo microceph.ceph osd unset noout
sudo microceph.ceph versions           # all 19.2.6
```

Confirm:

```
sudo microceph.ceph mon dump | grep auth_    # aes, aes256k
sudo microceph.ceph health detail            # AUTH_INSECURE_* = expected
```

Rotate:

```
sudo microceph.ceph mon set auth_preferred_cipher aes256k
# mon.: rotate, import the new key into each node's mon keyring, restart mons
sudo microceph.ceph auth rotate --key-type=aes256k mon.
# mgr/osd/mds: rotate, write the new key to the daemon keyring; osd rotates in place, no restart:
sudo microceph.ceph auth rotate --key-type=aes256k osd.N
printf '%s' <raw-key> | sudo microceph.ceph tell osd.N rotate-key -i -   # -i - reads the raw key from stdin
sudo microceph.ceph mon set auth_service_cipher aes256k
sudo microceph.ceph config set mon mon_auth_allow_insecure_key false
# admin = 3 places: both conf keyring files + the dqlite row. Back up client.admin first (recovery is painful):
sudo microceph.ceph auth rotate --key-type=aes256k client.admin
sudo microceph cluster sql "UPDATE config SET value='<raw-key>' WHERE key='keyring.client.admin'"
sudo microceph.ceph auth rotate --key-type=aes256k client.X
```

Cut over:

```
sudo microceph.ceph mon set auth_allowed_ciphers aes256k
```

Rescue: `mon_auth_emergency_allowed_ciphers` in a mon's local config re-admits `aes` temporarily.

## Upstream

- CVE-2025-30156: https://docs.ceph.com/en/latest/security/CVE-2025-30156/
- Rotation procedure: https://docs.ceph.com/en/latest/rados/configuration/auth-config-ref/#upgrading-and-rotating-cephx-keys
- Health checks: https://docs.ceph.com/en/latest/rados/operations/health-checks/
- Emergency allowed ciphers: https://docs.ceph.com/en/latest/rados/configuration/auth-config-ref/#emergency-allowed-ciphers
- Squid 19.2.6 notes: https://docs.ceph.com/en/latest/releases/squid/#v19-2-6-squid
- MicroCeph upgrade: `docs/how-to/major-upgrade.rst`

# Single-System Tests

This test suite covers the single-system tests from the GitHub Actions workflow. It installs the locally-built snap inside an LXD container, bootstraps a single-node cluster, and exercises:

- The `waitready` CLI (pre/post-bootstrap, storage threshold)
- The Orchestrator module
- Encrypted OSDs with dm-crypt
- An LVM volume OSD
- RGW (plain and SSL)
- SSL certificate rotation (with and without `--restart`, with `--target`)
- Cluster config set/reset
- Pool replication-factor operations
- Log level control
- IPv6 monitor address formatting
- Snap disable/enable service restoration

## Test Tags

- `single-node`
- `osd`
- `rgw`
- `mon`
- `mgr`
- `disk-management`
- `snap-packaging`
- `waitready`
- `e2e`
- `integration`
- `lxd`
- `loop-devices`
- `slow`

## Running the Tests

```bash
# Build the snap first
snapcraft

# Export the snap path
export SNAP_PATH="$(ls microceph_*.snap | head -1)"

# Run the tests
robot --variable SNAP_PATH:"$SNAP_PATH" tests/robot/single-system-tests/
```
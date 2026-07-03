package database

// cluster_lifecycle is a singleton table (CHECK(id=1)) that tracks Ceph
// bootstrap lifecycle state for role-managed deployments (CE142).
//
// This table uses intentional hand-rolled SQL helpers (see
// cluster_lifecycle_extras.go) rather than lxd-generate mapper codegen, because
// it is a single-row table and the codegen toolchain does not support the
// singleton pattern in this environment.

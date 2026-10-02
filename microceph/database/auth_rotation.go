package database

// auth_rotation is a singleton table (CHECK(id=1)) that tracks CephX authentication
// key rotation state, stage progress, blockers, and concurrency locks.
//
// This table uses intentional hand-rolled SQL helpers (see auth_rotation_extras.go)
// rather than lxd-generate mapper codegen, because it is a single-row table and the
// codegen toolchain does not support the singleton pattern in this environment.

package database

// placement_policy is a singleton table (CHECK(id=1)) that stores the last
// accepted role-managed declarative placement policy as a JSON blob (CE142).
//
// This table uses intentional hand-rolled SQL helpers (see
// placement_policy_extras.go) rather than lxd-generate mapper codegen, because
// it is a single-row table and the codegen toolchain does not support the
// singleton pattern in this environment.

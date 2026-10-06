package database

// placement_policy is a singleton table (CHECK(id=1)) that stores the canonical
// desired role-managed declarative placement policy as a JSON blob (CE142).
//
// The blob is a complete desired-state snapshot, not an accumulation of
// requests: each accepted PUT /1.0/placement overwrites it wholesale, so the
// row always reads as the full current intent rather than the most recent
// partial edit.
//
// This table uses intentional hand-rolled SQL helpers (see
// placement_policy_extras.go) rather than lxd-generate mapper codegen, because
// it is a single-row table and the codegen toolchain does not support the
// singleton pattern in this environment.

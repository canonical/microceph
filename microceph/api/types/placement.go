// Package types provides shared types and structs.
package types

// NFSPlacement describes a single role-driven NFS gateway placement entry for a member.
type NFSPlacement struct {
	GroupID     string `json:"group_id" yaml:"group_id"`
	BindAddress string `json:"bind_address" yaml:"bind_address"`
}

// MemberPlacement describes the desired placement for a single MicroCeph member.
// This is the generic, non-OS106 payload consumed by the snap placement engine
// (CE142).
//
// Every field is read from the submitted document alone. Because PUT
// /1.0/placement replaces the whole policy (see PlacementPolicy), a nil field
// is unmanaged by the new policy; it never inherits the declaration the
// replaced policy made for that field. Pointer fields distinguish "explicitly
// false/empty" (remove) from "omitted" (unmanaged).
type MemberPlacement struct {
	// Control governs MON, MGR, and MDS placement. nil means unmanaged:
	// reconciliation neither adds nor removes control services on this member.
	Control *bool `json:"control,omitempty" yaml:"control,omitempty"`
	// Rgw governs RGW placement. nil means unmanaged: reconciliation does not
	// change RGW on this member.
	Rgw *bool `json:"rgw,omitempty" yaml:"rgw,omitempty"`
	// Nfs governs role-driven NFS placement. nil means unmanaged; an empty
	// (non-nil) slice means remove role-driven NFS on that member. The json
	// tag intentionally omits the omitempty modifier so that an empty slice
	// (remove intent) round-trips through json.Marshal/Unmarshal as "nfs":[]
	// rather than being silently dropped.
	Nfs []NFSPlacement `json:"nfs" yaml:"nfs"`
	// StorageEligible governs OSD enrollment eligibility. It is the only
	// fail-closed dimension: while a policy is active, this member may enroll
	// new OSDs only on an explicit storage_eligible:true. nil means unmanaged,
	// and unmanaged storage denies enrollment rather than leaving it as it
	// was, so a replacement policy that drops a previous grant revokes it.
	// Existing OSDs are never removed by placement.
	StorageEligible *bool `json:"storage_eligible,omitempty" yaml:"storage_eligible,omitempty"`
}

// PlacementModeReconcile is the only supported placement policy mode. The
// mode must be set explicitly on every PUT; an omitted mode is rejected, and
// unknown modes are rejected, so a future mode (e.g. dry-run) sent to an older
// snap fails loudly instead of being silently applied as a reconcile.
const PlacementModeReconcile = "reconcile"

// PlacementPolicy is the body of PUT /1.0/placement and the canonical desired
// placement policy the snap stores. PUT is a full replacement, never a delta:
// the submitted document becomes the complete desired policy, and no member
// entry or field value survives from the policy it replaces. A second PUT that
// omits a declaration therefore revokes it rather than inheriting it.
//
// Members maps MicroCeph member names to their desired placement. A member
// absent from the map is unmanaged: reconciliation neither adds nor removes
// role-managed services on it. Absence is not a grant, though -- an absent
// member is not on the storage_eligible allow-list, so while the policy is
// active it cannot enroll new OSDs.
//
// An empty or absent Members map is the CE142 waiting policy: no service
// operations on any member, and an empty storage allow-list, so no member may
// enroll new OSDs until a policy naming it is installed. It is accepted before
// Ceph is bootstrapped. DELETE /1.0/placement is the way to stand down
// entirely: it clears the policy, leaves services untouched, and returns
// storage eligibility to unmanaged (allowed).
type PlacementPolicy struct {
	Mode    string                     `json:"mode" yaml:"mode"`
	Members map[string]MemberPlacement `json:"members" yaml:"members"`
}

// PlacementObservedMember captures the observed service placement for a member.
// Control is true when the member hosts any of MON, MGR, or MDS. Nfs lists the
// NFS group IDs placed on the member (from the grouped-services records).
type PlacementObservedMember struct {
	Member  string   `json:"member" yaml:"member"`
	Control bool     `json:"control" yaml:"control"`
	Rgw     bool     `json:"rgw" yaml:"rgw"`
	Nfs     []string `json:"nfs" yaml:"nfs"`
}

// PlacementStatus is the response body of GET /1.0/placement. It returns the
// canonical desired policy (Policy, the complete snapshot installed by the
// last accepted PUT), the current observed placement, lifecycle state, and any
// blocked or in-progress reasons. Policy is the whole desired state, so a
// declaration missing from it is not in effect.
//
// BootstrapState is one of: "not_bootstrapped", "in_progress",
// "bootstrapped", or "failed" (see database.CephState* constants).
// BlockedReason is populated only when BootstrapState is "failed"; it carries
// the error detail from the failed Ceph-only bootstrap attempt (with cephx
// key material redacted).
type PlacementStatus struct {
	Active           bool                      `json:"active" yaml:"active"`
	Policy           *PlacementPolicy          `json:"policy,omitempty" yaml:"policy,omitempty"`
	Observed         []PlacementObservedMember `json:"observed" yaml:"observed"`
	BootstrapState   string                    `json:"bootstrap_state" yaml:"bootstrap_state"`
	BootstrapTarget  string                    `json:"bootstrap_target,omitempty" yaml:"bootstrap_target,omitempty"`
	BlockedReason    string                    `json:"blocked_reason,omitempty" yaml:"blocked_reason,omitempty"`
	PlacementRefusal string                    `json:"placement_refusal,omitempty" yaml:"placement_refusal,omitempty"`
}

// CephBootstrapRequest is the body of PUT /1.0/ceph/bootstrap (CE142 Ceph-only
// bootstrap on an existing MicroCluster member).
type CephBootstrapRequest struct {
	Target           string `json:"target" yaml:"target"`
	MonIp            string `json:"mon_ip,omitempty" yaml:"mon_ip,omitempty"`
	PublicNet        string `json:"public_network,omitempty" yaml:"public_network,omitempty"`
	ClusterNet       string `json:"cluster_network,omitempty" yaml:"cluster_network,omitempty"`
	V2Only           bool   `json:"v2_only,omitempty" yaml:"v2_only,omitempty"`
	AvailabilityZone string `json:"availability_zone,omitempty" yaml:"availability_zone,omitempty"`
	// Force recovers from a stale in_progress lifecycle state left by a
	// crashed or stuck bootstrap. When true, a stale in_progress row is reset
	// to failed before the normal retry proceeds. Not for normal use.
	Force bool `json:"force,omitempty" yaml:"force,omitempty"`
}

// Capabilities lists the snap capability/API-extension markers supported by
// this revision (CE142). The charm checks these to block clearly when
// role-managed=true is requested with an unsupported snap revision.
type Capabilities struct {
	Supported []string `json:"supported" yaml:"supported"`
}

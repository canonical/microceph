// Package types provides shared types and structs.
package types

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
)

// NFSPlacement describes a single role-driven NFS gateway placement entry for a member.
type NFSPlacement struct {
	GroupID     string `json:"group_id" yaml:"group_id"`
	BindAddress string `json:"bind_address" yaml:"bind_address"`
}

// RgwPlacement declares whether a member should run RGW and its frontend settings.
// Certificate material is write-only; only the TLS flag and ports are stored.
type RgwPlacement struct {
	Enabled        *bool  `json:"enabled" yaml:"enabled"`
	SSL            *bool  `json:"ssl,omitempty" yaml:"ssl,omitempty"`
	Port           int    `json:"port,omitempty" yaml:"port,omitempty"`
	SSLPort        int    `json:"ssl_port,omitempty" yaml:"ssl_port,omitempty"`
	SSLCertificate string `json:"ssl_certificate,omitempty" yaml:"ssl_certificate,omitempty"`
	SSLPrivateKey  string `json:"ssl_private_key,omitempty" yaml:"ssl_private_key,omitempty"`
}

// Normalized validates RGW intent and fills in the effective listener ports.
func (r RgwPlacement) Normalized() (RgwPlacement, error) {
	if r.Enabled == nil {
		return r, errors.New("rgw.enabled is required")
	}
	if !*r.Enabled {
		return RgwPlacement{Enabled: r.Enabled}, nil
	}
	if r.SSL == nil {
		return r, errors.New("rgw.ssl is required when RGW is enabled")
	}
	if !*r.SSL && (r.SSLCertificate != "" || r.SSLPrivateKey != "") {
		return r, errors.New("TLS material requires rgw.ssl=true")
	}

	var err error
	r.Port, r.SSLPort, err = NormalizeRGWPorts(r.Port, r.SSLPort, *r.SSL)
	if err != nil {
		return r, err
	}
	if r.SSLCertificate != "" || r.SSLPrivateKey != "" {
		_, _, err = DecodeRGWCertificate(r.SSLCertificate, r.SSLPrivateKey)
		if err != nil {
			return r, err
		}
	}

	// No supplied material means reuse the member's pair, never disable TLS.
	// PUT replaces the whole policy, so an edit elsewhere resends this entry
	// too, and GET never gave the caller material to attach.
	return r, nil
}

// NormalizeRGWPorts returns the ports used by plaintext or TLS listeners.
func NormalizeRGWPorts(port, sslPort int, ssl bool) (int, int, error) {
	if port < 0 || port > 65535 || sslPort < 0 || sslPort > 65535 {
		return 0, 0, errors.New("RGW ports must be between 0 and 65535")
	}
	if !ssl {
		if port == 0 {
			port = 80
		}
		return port, 0, nil
	}
	if sslPort == 0 {
		sslPort = 443
	}
	if port == sslPort {
		return 0, 0, errors.New("HTTP and TLS listeners must use different ports")
	}
	return port, sslPort, nil
}

// DecodeRGWCertificate decodes a base64 certificate and matching private key.
func DecodeRGWCertificate(certificate, privateKey string) ([]byte, []byte, error) {
	if certificate == "" || privateKey == "" {
		return nil, nil, errors.New("TLS requires both a certificate and a private key")
	}
	cert, err := base64.StdEncoding.DecodeString(certificate)
	if err != nil {
		return nil, nil, errors.New("invalid base64 TLS certificate")
	}
	key, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return nil, nil, errors.New("invalid base64 TLS private key")
	}
	_, err = tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, nil, errors.New("invalid or mismatched TLS certificate and private key")
	}
	return cert, key, nil
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

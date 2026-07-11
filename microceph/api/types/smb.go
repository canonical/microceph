package types

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// SMBClusterIDRegex mirrors the upstream mgr/smb ID validation
// (src/pybind/mgr/smb/validation.py _name_re): 1-18 characters,
// alphanumeric with inner hyphens.
var SMBClusterIDRegex = regexp.MustCompile(`^[a-zA-Z0-9]($|[a-zA-Z0-9-]{0,16}[a-zA-Z0-9]$)`)

// SMBSpec mirrors the JSON serialization of the upstream mgr/smb service
// spec (ceph src/python-common service_spec.py, SMBSpec). Field names match
// the python spec exactly; unknown fields are tolerated so newer mgr
// versions do not break decoding. Phase-1-unsupported fields are rejected
// at validation time from the raw payload, not modeled here.
type SMBSpec struct {
	ServiceType        string              `json:"service_type" yaml:"service_type"`
	ServiceID          string              `json:"service_id" yaml:"service_id"`
	Placement          SMBPlacementSpec    `json:"placement" yaml:"placement"`
	ClusterID          string              `json:"cluster_id" yaml:"cluster_id"`
	Features           []string            `json:"features" yaml:"features"`
	ConfigURI          string              `json:"config_uri" yaml:"config_uri"`
	UserSources        []string            `json:"user_sources" yaml:"user_sources"`
	ClusterMetaURI     string              `json:"cluster_meta_uri" yaml:"cluster_meta_uri"`
	ClusterLockURI     string              `json:"cluster_lock_uri" yaml:"cluster_lock_uri"`
	ClusterPublicAddrs []SMBPublicAddrSpec `json:"cluster_public_addrs" yaml:"cluster_public_addrs"`
	IncludeCephUsers   []string            `json:"include_ceph_users" yaml:"include_ceph_users"`
}

// SMBPlacementSpec is the subset of the ceph PlacementSpec serialization
// honored in Phase 1. Hosts entries are plain hostnames. CountPerHost and
// HostPattern are parsed only so validation can reject them explicitly.
type SMBPlacementSpec struct {
	Hosts        []string        `json:"hosts" yaml:"hosts"`
	Count        int             `json:"count" yaml:"count"`
	Label        string          `json:"label" yaml:"label"`
	CountPerHost int             `json:"count_per_host" yaml:"count_per_host"`
	HostPattern  json.RawMessage `json:"host_pattern" yaml:"host_pattern"`
}

// SMBService identifies an SMB cluster by its cluster id.
type SMBService struct {
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
}

// SMBServiceStatus describes one SMB cluster: its stored spec and the
// nodes it is currently placed on.
type SMBServiceStatus struct {
	ClusterID string          `json:"cluster_id" yaml:"cluster_id"`
	Spec      json.RawMessage `json:"spec" yaml:"spec"`
	PlacedOn  []string        `json:"placed_on" yaml:"placed_on"`
}

// SMBPublicAddrSpec mirrors SMBClusterPublicIPSpec: a CTDB public address
// with optional destination networks.
type SMBPublicAddrSpec struct {
	Address     string         `json:"address" yaml:"address"`
	Destination SMBDestination `json:"destination" yaml:"destination"`
}

// SMBDestination decodes the python Union[str, List[str], None] shape of
// SMBClusterPublicIPSpec.destination into a flat string slice.
type SMBDestination []string

// UnmarshalJSON accepts null, a single string, or a list of strings.
func (d *SMBDestination) UnmarshalJSON(data []byte) error {
	// json.Unmarshal(null, &string) is a no-op success, so null must be
	// handled before the single-string attempt.
	if string(data) == "null" {
		*d = nil
		return nil
	}

	var single string
	err := json.Unmarshal(data, &single)
	if err == nil {
		*d = SMBDestination{single}
		return nil
	}

	var many []string
	err = json.Unmarshal(data, &many)
	if err == nil {
		*d = many
		return nil
	}

	return fmt.Errorf("destination must be a string, list of strings, or null: %s", string(data))
}

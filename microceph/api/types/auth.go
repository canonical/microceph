package types

// AuthRotateRequest holds parameters for rotating CephX authentication keys.
type AuthRotateRequest struct {
	KeyType string `json:"key_type" yaml:"key_type"`
	Client  string `json:"client" yaml:"client"`
}

// AuthRotateResponse represents the outcome of an auth rotate command.
type AuthRotateResponse struct {
	TargetKeyType string `json:"target_key_type" yaml:"target_key_type"`
	State         string `json:"state" yaml:"state"`
	Stage         string `json:"stage" yaml:"stage"`
	ClientName    string `json:"client_name,omitempty" yaml:"client_name,omitempty"`
	Blocker       string `json:"blocker,omitempty" yaml:"blocker,omitempty"`
	Detail        string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// AuthStatusResponse represents the status and client cipher distribution of CephX authentication.
type AuthStatusResponse struct {
	Status              string              `json:"status" yaml:"status"`
	State               string              `json:"state" yaml:"state"`
	Stage               string              `json:"stage,omitempty" yaml:"stage,omitempty"`
	Blocker             string              `json:"blocker,omitempty" yaml:"blocker,omitempty"`
	TargetKeyType       string              `json:"target_key_type,omitempty" yaml:"target_key_type,omitempty"`
	Detail              string              `json:"detail,omitempty" yaml:"detail,omitempty"`
	ServiceDistribution map[string][]string `json:"service_distribution,omitempty" yaml:"service_distribution,omitempty"`
	ClientDistribution  map[string][]string `json:"client_distribution,omitempty" yaml:"client_distribution,omitempty"`
	HealthWarnings      []string            `json:"health_warnings,omitempty" yaml:"health_warnings,omitempty"`
}

// MemberAuthRotateRequest is the per-member daemon rotation request the coordinator
// sends to every member (one at a time). KeyType is the target cipher; MonKeyring is
// the shared mon. keyring content rotated once by the coordinator, deployed by any
// member that runs a mon before restarting it.
type MemberAuthRotateRequest struct {
	KeyType    string `json:"key_type" yaml:"key_type"`
	MonKeyring string `json:"mon_keyring,omitempty" yaml:"mon_keyring,omitempty"`
}

// MemberAuthRotateResponse reports which of a member's own daemons were rotated.
type MemberAuthRotateResponse struct {
	Hostname     string   `json:"hostname" yaml:"hostname"`
	MonRestarted bool     `json:"mon_restarted" yaml:"mon_restarted"`
	RotatedMgrs  []string `json:"rotated_mgrs,omitempty" yaml:"rotated_mgrs,omitempty"`
	RotatedOSDs  []string `json:"rotated_osds,omitempty" yaml:"rotated_osds,omitempty"`
	RotatedMDSs  []string `json:"rotated_mdss,omitempty" yaml:"rotated_mdss,omitempty"`
}

package types

import "regexp"

// SMBClusterIDRegex is the regular expression for acceptable SMB cluster IDs.
var SMBClusterIDRegex = regexp.MustCompile(`^[\w][\w.-]{1,61}[\w]$`)

// ManagedSMBService describes cluster-level SMB service configuration.
type ManagedSMBService struct {
	ClusterID      string   `json:"cluster_id" yaml:"cluster_id"`
	DefineUserPass []string `json:"define_user_pass,omitempty" yaml:"define_user_pass,omitempty"`
	UserGroupRefs  []string `json:"user_group_refs,omitempty" yaml:"user_group_refs,omitempty"`
	BindAddresses  []string `json:"bind_addresses,omitempty" yaml:"bind_addresses,omitempty"`
	BindNetworks   []string `json:"bind_networks,omitempty" yaml:"bind_networks,omitempty"`
	Port           int      `json:"port,omitempty" yaml:"port,omitempty"`
	Wait           bool     `json:"wait" yaml:"wait"`
}

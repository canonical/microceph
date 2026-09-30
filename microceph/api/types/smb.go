package types

import (
	"fmt"
	"regexp"
	"strings"
)

// SMBClusterIDRegex matches Tentacle SMB resource IDs: 1–18 ASCII alphanumeric
// or hyphen characters, with alphanumeric endpoints.
var SMBClusterIDRegex = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,16}[A-Za-z0-9])?$`)

// SMBUser is a local SMB account. Never log its password or serialized contents.
type SMBUser struct {
	Name     string `json:"name" yaml:"name"`
	Password string `json:"password" yaml:"password"`
}

// SMBCredentials holds creation-only input read from a file or stdin.
type SMBCredentials struct {
	Users []SMBUser `json:"users" yaml:"users"`
}

// Validate rejects unusable or reserved identities without echoing secret input.
func (c *SMBCredentials) Validate() error {
	if c == nil {
		return nil
	}
	if len(c.Users) == 0 {
		return fmt.Errorf("SMB credentials require at least one user")
	}
	seen := make(map[string]bool)
	for _, user := range c.Users {
		if user.Name == "" || strings.ContainsAny(user.Name, ":\r\n\x00") || user.Name == "root" || user.Name == "nobody" || seen[user.Name] {
			return fmt.Errorf("SMB credentials contain an invalid, duplicate, or reserved username")
		}
		if user.Password == "" || strings.ContainsRune(user.Password, '\x00') {
			return fmt.Errorf("SMB credentials contain an empty or invalid password")
		}
		seen[user.Name] = true
	}
	return nil
}

// String protects ordinary formatted diagnostics; never log serialized credentials.
func (c SMBCredentials) String() string { return "[SMB credentials redacted]" }

// ManagedSMBService describes cluster-level SMB service configuration.
type ManagedSMBService struct {
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
	// Clustering is optional: creation defaults to always; updates inherit.
	Clustering    *string         `json:"clustering,omitempty" yaml:"clustering,omitempty"`
	Credentials   *SMBCredentials `json:"credentials,omitempty" yaml:"credentials,omitempty"`
	UserGroupRefs []string        `json:"user_group_refs,omitempty" yaml:"user_group_refs,omitempty"`
	BindAddresses []string        `json:"bind_addresses,omitempty" yaml:"bind_addresses,omitempty"`
	BindNetworks  []string        `json:"bind_networks,omitempty" yaml:"bind_networks,omitempty"`
	Port          int             `json:"port,omitempty" yaml:"port,omitempty"`
	Wait          bool            `json:"wait" yaml:"wait"`
}

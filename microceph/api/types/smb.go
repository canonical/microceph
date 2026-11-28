package types

// SMBService identifies the logical cluster for a targeted native operation.
// Purge affects only confirmed-stopped local state, never logical resources.
type SMBService struct {
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
	Purge     bool   `json:"purge,omitempty" yaml:"purge,omitempty"`
	// Orchestrated requests asynchronous retirement and fences queued placement.
	Orchestrated bool `json:"orchestrated,omitempty" yaml:"orchestrated,omitempty"`
}

// SMBStatus is a credential-free local application diagnostic.
type SMBStatus struct {
	// AuthMode is the prepared authentication mode, not a join success assertion.
	AuthMode string `json:"auth_mode,omitempty"`
	// Realm is the prepared external directory realm; credentials are never exposed.
	Realm string `json:"realm,omitempty"`
	// Clustered identifies a prepared CTDB-backed gateway.
	Clustered bool `json:"clustered,omitempty"`
	// CTDBPNN is the assigned stable member rank, including zero.
	CTDBPNN       *uint64 `json:"ctdb_pnn,omitempty"`
	ClusterID     string  `json:"cluster_id"`
	Ready         bool    `json:"ready"`
	StopRequested bool    `json:"stop_requested"`
	Error         string  `json:"error,omitempty"`
	PolicyWarning string  `json:"policy_warning,omitempty"`
}

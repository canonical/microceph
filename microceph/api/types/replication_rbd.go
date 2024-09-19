package types

// Types for RBD Pool status table.
type RbdPoolStatusImageBrief struct {
	Name            string `json:"name" yaml:"name"`
	IsPrimary       bool   `json:"is_primary" yaml:"is_primary"`
	LastLocalUpdate string `json:"last_local_update" yaml:"last_local_update"`
}

type RbdPoolStatusRemoteBrief struct {
	Name      string `json:"name" yaml:"name"`
	UUID      string `json:"uuid" yaml:"uuid"`
	Direction string `json:"direction" yaml:"direction"`
}

type RbdPoolStatus struct {
	Name              string                     `json:"name" yaml:"name"`
	Type              string                     `json:"type" yaml:"type"`
	HealthReplication string                     `json:"rep_health" yaml:"rep_health"`
	HealthDaemon      string                     `json:"daemon_health" yaml:"daemon_health"`
	HealthImages      string                     `json:"image_health" yaml:"image_health"`
	ImageCount        int                        `json:"image_count" yaml:"image_count"`
	Images            []RbdPoolStatusImageBrief  `json:"images" yaml:"images"`
	Remotes           []RbdPoolStatusRemoteBrief `json:"remotes" yaml:"remotes"`
}

// Types for RBD Image status table.
type RbdImageStatusRemoteBrief struct {
	Name             string `json:"name" yaml:"name"`
	Status           string `json:"status" yaml:"status"`
	LastRemoteUpdate string `json:"last_remote_update" yaml:"last_remote_update"`
}

type RbdImageStatus struct {
	Name            string                      `json:"name" yaml:"name"`
	ID              string                      `json:"id" yaml:"id"`
	Type            string                      `json:"type" yaml:"type"`
	IsPrimary       bool                        `json:"is_primary" yaml:"is_primary"`
	Status          string                      `json:"status" yaml:"status"`
	LastLocalUpdate string                      `json:"last_local_update" yaml:"last_local_update"`
	Remotes         []RbdImageStatusRemoteBrief `json:"remotes" yaml:"remotes"`
}

// Types for Rbd List

type RbdPoolListImageBrief struct {
	Name            string `json:"name" yaml:"name"`
	Type            string `json:"Type" yaml:"Type"`
	IsPrimary       bool   `json:"is_primary" yaml:"is_primary"`
	LastLocalUpdate string `json:"last_local_update" yaml:"last_local_update"`
}

type RbdPoolBrief struct {
	Name   string `json:"name" yaml:"name"`
	Images []RbdPoolListImageBrief
}

type RbdPoolList []RbdPoolBrief

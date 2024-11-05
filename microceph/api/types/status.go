package types

type ServiceStatus struct {
	// a service shortname rgw/mon/mds etc.
	Kind string `json:"kind" yaml:"kind"`
	// Addresses the service listens on.
	Addresses []string `json:"addresses" yaml:"addresses"`
}

type Member struct {
	// List of addresses (public_network/cluster_network) on host.
	Addresses []string `json:"addresses" yaml:"addresses"`
	// List of OSD IDs that exist on a particular member.
	Disks []int `json:"disks" yaml:"disks"`
	// List of services spawned on that host.
	Services []ServiceStatus `json:"services" yaml:"services"`
}

type Cluster struct {
	Members []Member `json:"members" yaml:"members"`
}

package main

// AdoptBootstraper bootstraps microceph with an adopted/existing ceph cluster.
type AdoptBootstraper struct {
	FSID       string   // fsid of the existing ceph cluster.
	MonHosts   []string // slice of exisiting monitor addresses.
	AdminKey   string   // Admin key for providing microceph with privileges.
	PublicNet  string   // Public Network subnet.
	ClusterNet string   // Cluster Network subnet.
}

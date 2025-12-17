package database

//go:generate -command mapper lxd-generate db mapper -t grouped_service.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService objects table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService objects-by-Member table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService objects-by-Service-and-GroupID table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService objects-by-Member-and-Service-and-GroupID table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService id table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService create table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService delete-by-Member table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService delete-by-Member-and-Service-and-GroupID table=grouped_services
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e GroupedService update table=grouped_services
//
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService GetMany table=grouped_services
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService GetOne table=grouped_services
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService ID table=grouped_services
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService Exists table=grouped_services
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService Create table=grouped_services
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService DeleteOne-by-Member-and-Service-and-GroupID table=grouped_services
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e GroupedService Update table=grouped_services

// GroupedService is used to track clustered services running on a particular server.
type GroupedService struct {
	ID      int
	Service string `db:"primary=yes&join=service_groups.service&joinon=grouped_services.service_group_id"`
	GroupID string `db:"primary=yes&sql=service_groups.group_id"`
	Member  string `db:"primary=yes&join=core_cluster_members.name&joinon=grouped_services.member_id"`
	Info    string
}

// GroupedServiceFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type GroupedServiceFilter struct {
	Service *string
	GroupID *string
	Member  *string
}

// NFSServiceInfo is a struct containing GroupedService information.
type NFSServiceInfo struct {
	BindAddress string `json:"bind_address"`
	BindPort    uint   `json:"bind_port"`
}

// RGWServiceInfo is a struct containing GroupedService information.
type RGWServiceInfo struct {
	Port           int    `json:"port"`
	SSLPort        int    `json:"ssl_port"`
	SSLCertificate string `json:"ssl_certificate"`
	SSLPrivateKey  string `json:"ssl_private_key"`
}

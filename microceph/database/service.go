package database

//go:generate -command mapper lxd-generate db mapper -t service.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service objects table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service objects-by-Member table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service objects-by-Service table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service objects-by-Member-and-Service table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service id table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service create table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service delete-by-Member table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service delete-by-Member-and-Service table=services
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e service update table=services
//
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service GetMany
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service GetOne
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service ID
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service Exists
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service Create
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service DeleteOne-by-Member-and-Service
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service DeleteMany-by-Member
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e service Update

// Service is used to track the Ceph services running on a particular server.
type Service struct {
	ID      int
	Member  string `db:"primary=yes&join=internal_cluster_members.name&joinon=services.member_id"`
	Service string `db:"primary=yes"`
}

// ServiceFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type ServiceFilter struct {
	Member  *string
	Service *string
}

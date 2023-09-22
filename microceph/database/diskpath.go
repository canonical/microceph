package database

//go:generate -command mapper lxd-generate db mapper -t diskpath.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath objects table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath objects-by-Member table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath objects-by-Member-and-Path table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath id table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath create table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath delete-by-Member table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath delete-by-Member-and-Path table=diskpaths
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e DiskPath update table=diskpaths
//
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath GetMany
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath GetOne
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath ID
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath Exists
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath Create
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath DeleteOne-by-Member-and-Path
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath DeleteMany-by-Member
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e DiskPath Update

// DiskPath is used to track the Ceph disks on a particular server.
type DiskPath struct {
	ID     int
	Member string `db:"primary=yes&join=internal_cluster_members.name&joinon=diskpaths.member_id"`
	Path   string `db:"primary=yes"`
}

// DiskPathFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type DiskPathFilter struct {
	Member *string
	Path   *string
}

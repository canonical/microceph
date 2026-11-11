package database

//go:generate -command mapper lxd-generate db mapper -t disk.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk objects table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk objects-by-Member table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk objects-by-Member-and-Path table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk id table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk create table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk delete-by-Member table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk delete-by-Member-and-Path table=Disks
//go:generate mapper stmt -d github.com/canonical/microcluster/v2/cluster -e Disk update table=Disks
//
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk GetMany
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk GetOne
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk ID
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk Exists
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk Create
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk DeleteOne-by-Member-and-Path
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk DeleteMany-by-Member
//go:generate mapper method -i -d github.com/canonical/microcluster/v2/cluster -e Disk Update

// Disk is used to track the Ceph disks on a particular server.
type Disk struct {
	ID     int
	Member string `db:"primary=yes&join=core_cluster_members.name&joinon=Disks.member_id"`
	Path   string `db:"primary=yes"`
}

// DiskFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type DiskFilter struct {
	Member *string
	Path   *string
}

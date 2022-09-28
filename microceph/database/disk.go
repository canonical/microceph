package database

//go:generate -command mapper lxd-generate db mapper -t disk.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk objects table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk objects-by-Member table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk objects-by-Member-and-Path table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk id table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk create table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk delete-by-Member table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk delete-by-Member-and-Path table=disks
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e disk update table=disks
//
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk GetMany
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk GetOne
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk ID
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk Exists
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk Create
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk DeleteOne-by-Member-and-Path
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk DeleteMany-by-Member
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e disk Update

// Disk is used to track the Ceph disks on a particular server.
type Disk struct {
	ID     int
	Member string `db:"primary=yes&join=internal_cluster_members.name&joinon=disks.member_id"`
	OSD    int    `db:"primary=yes"`
	Path   string
}

// DiskFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type DiskFilter struct {
	Member *string
	Path   *string
	OSD    *int
}

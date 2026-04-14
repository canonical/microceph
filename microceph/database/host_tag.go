package database

//go:generate -command mapper lxd-generate db mapper -t host_tag.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag objects table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag objects-by-Member table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag objects-by-Key table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag objects-by-Member-and-Key table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag id table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag create table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag delete-by-Member table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag delete-by-Member-and-Key table=host_tags
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag update table=host_tags
//
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag GetMany
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag GetOne
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag ID
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag Exists
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag Create
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag DeleteOne-by-Member-and-Key
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag DeleteMany-by-Member
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e host_tag Update

// HostTag is used to track key/value tags associated with a particular cluster member.
type HostTag struct {
	ID     int
	Member string `db:"primary=yes&join=core_cluster_members.name&joinon=host_tags.member_id"`
	Key    string `db:"primary=yes"`
	Value  string
}

// HostTagFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type HostTagFilter struct {
	Member *string
	Key    *string
}

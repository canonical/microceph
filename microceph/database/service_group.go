package database

//go:generate -command mapper lxd-generate db mapper -t service_group.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup objects table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup objects-by-Service table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup objects-by-GroupID table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup objects-by-Service-and-GroupID table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup id table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup create table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup delete-by-Service-and-GroupID table=service_groups
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup update table=service_groups
//
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup GetMany
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup GetOne
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup ID
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup Exists
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup Create
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup DeleteOne-by-Service-and-GroupID
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ServiceGroup Update

// ServiceGroup is used to track microceph service clusters.
type ServiceGroup struct {
	Service string `db:"primary=yes"`
	GroupID string `db:"group_id&primary=yes"`
	Config  string
}

// ServiceGroupFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type ServiceGroupFilter struct {
	GroupID *string
	Service *string
}

// NFSServiceGroupConfig is a struct containing a ServiceGroup's configuration.
type NFSServiceGroupConfig struct {
	V4MinVersion uint `json:"v4_min_version"`
}

package database

//go:generate -command mapper lxd-generate db mapper -t client_config.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem objects table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem objects-by-Key table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem objects-by-Host table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem objects-by-Key-and-Host table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem id table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem create table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem delete-by-Key table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem delete-by-Host table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem delete-by-Key-and-Host table=client_config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem update table=client_config

//
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem GetMany table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem GetOne table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem ID table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem Exists table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem Create table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem DeleteOne-by-Key-and-Host table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem DeleteMany-by-Key table=client_config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ClientConfigItem Update table=client_config

type ClientConfigItem struct {
	ID    int
	Host  string `db:"primary=yes&join=core_cluster_members.name&joinon=client_config.member_id"`
	Key   string `db:"primary=yes"`
	Value string
}

type ClientConfigItemFilter struct {
	Host *string
	Key  *string
}

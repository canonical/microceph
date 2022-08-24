package database

//go:generate -command mapper lxd-generate db mapper -t config.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e ConfigItem objects table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e ConfigItem objects-by-Key table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e ConfigItem id table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e ConfigItem create table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e ConfigItem delete-by-Key table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e ConfigItem update table=config

//
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem GetMany table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem GetOne table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem ID table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem Exists table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem Create table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem DeleteOne-by-Key table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e ConfigItem Update table=config

// ConfigItem is used to track the Ceph configuration.
type ConfigItem struct {
	ID    int
	Key   string `db:"primary=yes"`
	Value string
}

// ConfigItemFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type ConfigItemFilter struct {
	Key *string
}

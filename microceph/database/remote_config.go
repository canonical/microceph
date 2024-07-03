package database

//go:generate -command mapper lxd-generate db mapper -t remote_config.mapper.go
//go:generate mapper reset

//
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig objects table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig objects-by-Key table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig objects-by-Remote table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig objects-by-Key-and-Remote table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig id table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig create table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig delete-by-Key table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig delete-by-Remote table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig delete-by-Key-and-Remote table=remote_config
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e RemoteConfig update table=remote_config

//
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig GetMany table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig GetOne table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig ID table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig Exists table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig Create table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig DeleteOne-by-Key-and-Remote table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig DeleteMany-by-Remote table=remote_config
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e RemoteConfig Update table=remote_config

// RemoteConfig is used to track the Ceph configuration.
type RemoteConfig struct {
	ID     int
	Remote string `db:"primary=yes&join=remote.name&joinon=remote_config.remote_id"`
	Key    string `db:"primary=yes"`
	Value  string
}

// RemoteItemFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type RemoteConfigFilter struct {
	Key    *string
	Remote *string
}

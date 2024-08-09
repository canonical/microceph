package database

//go:generate -command mapper lxd-generate db mapper -t remote.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e Remote objects table=remote
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e Remote objects-by-Name table=remote
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e Remote id table=remote
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e Remote create table=remote
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e Remote delete-by-Name table=remote
//go:generate mapper stmt -d github.com/canonical/microcluster/cluster -e Remote update table=remote

//
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote GetMany table=remote
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote GetOne table=remote
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote ID table=remote
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote Exists table=remote
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote Create table=remote
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote DeleteOne-by-Name table=remote
//go:generate mapper method -i -d github.com/canonical/microcluster/cluster -e Remote Update table=remote

// Remote is used to track the Remotes.
type Remote struct {
	ID        int
	Name      string `db:"primary=yes"`
	LocalName string // friendly local cluster name
}

// RemoteItemFilter is a required struct for use with lxd-generate. It is used for filtering fields on database fetches.
type RemoteFilter struct {
	Name *string
}

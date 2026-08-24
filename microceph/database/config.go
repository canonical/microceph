package database

import (
	"context"
	"database/sql"
	"fmt"
)

//go:generate -command mapper lxd-generate db mapper -t config.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem objects table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem objects-by-Key table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem id table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem create table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem delete-by-Key table=config
//go:generate mapper stmt -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem update table=config

//
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem GetMany table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem GetOne table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem ID table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem Exists table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem Create table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem DeleteOne-by-Key table=config
//go:generate mapper method -i -d github.com/canonical/microcluster/v3/microcluster/db -e ConfigItem Update table=config

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

// UpsertConfigItem creates or updates a configuration item identified by key.
func UpsertConfigItem(ctx context.Context, tx *sql.Tx, object ConfigItem) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO config (key, value)
VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
`, object.Key, object.Value)
	if err != nil {
		return fmt.Errorf("failed to upsert config item %q: %w", object.Key, err)
	}

	return nil
}

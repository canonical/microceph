package ceph

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	lxdApi "github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertMonHostConfigItemCreateWhenMissing(t *testing.T) {
	origGet := getConfigItemOp
	origCreate := createConfigItemOp
	origUpdate := updateConfigItemOp
	defer func() {
		getConfigItemOp = origGet
		createConfigItemOp = origCreate
		updateConfigItemOp = origUpdate
	}()

	getConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string) (*database.ConfigItem, error) {
		return nil, lxdApi.StatusErrorf(http.StatusNotFound, "ConfigItem not found")
	}

	created := false
	createConfigItemOp = func(ctx context.Context, tx *sql.Tx, object database.ConfigItem) (int64, error) {
		created = true
		assert.Equal(t, "mon.host.node1", object.Key)
		assert.Equal(t, "10.0.0.1", object.Value)
		return 1, nil
	}

	updateConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string, object database.ConfigItem) error {
		t.Fatalf("update should not be called when key is missing")
		return nil
	}

	err := upsertMonHostConfigItem(context.Background(), nil, "node1", "10.0.0.1")
	require.NoError(t, err)
	assert.True(t, created)
}

func TestUpsertMonHostConfigItemUpdatesChangedValue(t *testing.T) {
	origGet := getConfigItemOp
	origCreate := createConfigItemOp
	origUpdate := updateConfigItemOp
	defer func() {
		getConfigItemOp = origGet
		createConfigItemOp = origCreate
		updateConfigItemOp = origUpdate
	}()

	getConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string) (*database.ConfigItem, error) {
		return &database.ConfigItem{Key: key, Value: "10.0.0.1"}, nil
	}

	createConfigItemOp = func(ctx context.Context, tx *sql.Tx, object database.ConfigItem) (int64, error) {
		t.Fatalf("create should not be called when key exists")
		return 0, nil
	}

	updated := false
	updateConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string, object database.ConfigItem) error {
		updated = true
		assert.Equal(t, "mon.host.node1", key)
		assert.Equal(t, "10.0.0.2", object.Value)
		return nil
	}

	err := upsertMonHostConfigItem(context.Background(), nil, "node1", "10.0.0.2")
	require.NoError(t, err)
	assert.True(t, updated)
}

func TestUpsertMonHostConfigItemNoopWhenValueUnchanged(t *testing.T) {
	origGet := getConfigItemOp
	origCreate := createConfigItemOp
	origUpdate := updateConfigItemOp
	defer func() {
		getConfigItemOp = origGet
		createConfigItemOp = origCreate
		updateConfigItemOp = origUpdate
	}()

	getConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string) (*database.ConfigItem, error) {
		return &database.ConfigItem{Key: key, Value: "10.0.0.1"}, nil
	}

	createConfigItemOp = func(ctx context.Context, tx *sql.Tx, object database.ConfigItem) (int64, error) {
		t.Fatalf("create should not be called when key exists")
		return 0, nil
	}

	updateConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string, object database.ConfigItem) error {
		t.Fatalf("update should not be called when value is unchanged")
		return nil
	}

	err := upsertMonHostConfigItem(context.Background(), nil, "node1", "10.0.0.1")
	require.NoError(t, err)
}

func TestUpsertMonHostConfigItemReturnsGetError(t *testing.T) {
	origGet := getConfigItemOp
	origCreate := createConfigItemOp
	origUpdate := updateConfigItemOp
	defer func() {
		getConfigItemOp = origGet
		createConfigItemOp = origCreate
		updateConfigItemOp = origUpdate
	}()

	expectedErr := errors.New("db unavailable")
	getConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string) (*database.ConfigItem, error) {
		return nil, expectedErr
	}

	createConfigItemOp = func(ctx context.Context, tx *sql.Tx, object database.ConfigItem) (int64, error) {
		t.Fatalf("create should not be called on get error")
		return 0, nil
	}

	updateConfigItemOp = func(ctx context.Context, tx *sql.Tx, key string, object database.ConfigItem) error {
		t.Fatalf("update should not be called on get error")
		return nil
	}

	err := upsertMonHostConfigItem(context.Background(), nil, "node1", "10.0.0.1")
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

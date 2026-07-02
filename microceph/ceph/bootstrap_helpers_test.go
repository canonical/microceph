package ceph

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
)

func TestRecordHostTagRejectsValueMismatch(t *testing.T) {
	origExists := hostTagExistsFunc
	origGet := getHostTagFunc
	origCreate := createHostTagFunc
	t.Cleanup(func() {
		hostTagExistsFunc = origExists
		getHostTagFunc = origGet
		createHostTagFunc = origCreate
	})

	createCalled := false
	hostTagExistsFunc = func(_ context.Context, _ *sql.Tx, _ string, _ string) (bool, error) {
		return true, nil
	}
	getHostTagFunc = func(_ context.Context, _ *sql.Tx, member string, key string) (*database.HostTag, error) {
		return &database.HostTag{Member: member, Key: key, Value: "az-0"}, nil
	}
	createHostTagFunc = func(_ context.Context, _ *sql.Tx, _ database.HostTag) (int64, error) {
		createCalled = true
		return 1, nil
	}

	err := RecordHostTagFunc(context.Background(), nil, "node-a", "availability-zone", "az-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host tag mismatch")
	assert.False(t, createCalled)
}

func TestRecordHostTagAllowsSameValue(t *testing.T) {
	origExists := hostTagExistsFunc
	origGet := getHostTagFunc
	origCreate := createHostTagFunc
	t.Cleanup(func() {
		hostTagExistsFunc = origExists
		getHostTagFunc = origGet
		createHostTagFunc = origCreate
	})

	createCalled := false
	hostTagExistsFunc = func(_ context.Context, _ *sql.Tx, _ string, _ string) (bool, error) {
		return true, nil
	}
	getHostTagFunc = func(_ context.Context, _ *sql.Tx, member string, key string) (*database.HostTag, error) {
		return &database.HostTag{Member: member, Key: key, Value: "az-0"}, nil
	}
	createHostTagFunc = func(_ context.Context, _ *sql.Tx, _ database.HostTag) (int64, error) {
		createCalled = true
		return 1, nil
	}

	err := RecordHostTagFunc(context.Background(), nil, "node-a", "availability-zone", "az-0")
	require.NoError(t, err)
	assert.False(t, createCalled)
}

func TestBootstrapDBAddHostTagOpUsesIdempotentRecorder(t *testing.T) {
	orig := RecordHostTagFunc
	t.Cleanup(func() { RecordHostTagFunc = orig })

	var gotMember, gotKey, gotValue string
	RecordHostTagFunc = func(_ context.Context, _ *sql.Tx, member string, key string, value string) error {
		gotMember = member
		gotKey = key
		gotValue = value
		return nil
	}

	err := bootstrapDBAddHostTagOp(context.Background(), nil, "node-a", "availability-zone", "az-0")
	require.NoError(t, err)
	assert.Equal(t, "node-a", gotMember)
	assert.Equal(t, "availability-zone", gotKey)
	assert.Equal(t, "az-0", gotValue)
}

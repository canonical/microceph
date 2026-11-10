package clusterstate

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type databaseStub struct {
	ctx context.Context
	err error
}

func (d *databaseStub) IsOpen(ctx context.Context) error {
	d.ctx = ctx
	return d.err
}

func (d *databaseStub) Transaction(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
	d.ctx = ctx
	return f(ctx, nil)
}

func TestDatabaseReadinessUsesLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	native := &databaseStub{}
	db := adaptDatabase(ctx, native)
	if !db.IsOpen() || native.ctx != ctx {
		t.Fatal("database readiness must use the lifecycle context and translate success to true")
	}
	native.err = errors.New("not ready")
	if db.IsOpen() {
		t.Fatal("database readiness errors must remain false")
	}
	if adaptDatabase(ctx, nil) != nil {
		t.Fatal("missing database must remain nil")
	}
}

func TestDatabaseTransactionPreservesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	query, stop := context.WithCancel(ctx)
	defer stop()
	native := &databaseStub{}
	db := adaptDatabase(ctx, native)
	want := errors.New("transaction failure")
	err := db.Transaction(query, func(got context.Context, _ *sql.Tx) error {
		if got != query {
			t.Fatal("transaction callback lost caller context")
		}
		return want
	})
	if native.ctx != query || !errors.Is(err, want) {
		t.Fatal("transaction context or result was not preserved")
	}
}

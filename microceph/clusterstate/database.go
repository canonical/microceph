package clusterstate

import (
	"context"
	"database/sql"
)

type nativeDatabase interface {
	IsOpen(context.Context) error
	Transaction(context.Context, func(context.Context, *sql.Tx) error) error
}

type applicationDatabase struct {
	ctx    context.Context
	native nativeDatabase
}

func adaptDatabase(ctx context.Context, native nativeDatabase) Database {
	if native == nil {
		return nil
	}
	return &applicationDatabase{ctx: ctx, native: native}
}

func (d *applicationDatabase) IsOpen() bool {
	return d.native.IsOpen(d.ctx) == nil
}

func (d *applicationDatabase) Transaction(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
	return d.native.Transaction(ctx, f)
}

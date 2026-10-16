package ceph

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestServicePlacementHandlerRequestContext(t *testing.T) {
	// Keep these serial with the placement suite's package seams. Exercise
	// the real handler, UpdateConfig and getMonitorCountFunc, unlike the
	// placement suite's config-render bypass. Only the DB transaction is stopped
	// at entry, before any configuration files or services can be changed.
	for _, wait := range []bool{false, true} {
		name := "async"
		if wait {
			name = "sync"
		}
		t.Run(name, func(t *testing.T) {
			type requestKey struct{}
			requestValue := &struct{}{}
			requestLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := mcTypes.ContextWithLogger(context.Background(), requestLogger)
			ctx = context.WithValue(ctx, requestKey{}, requestValue)
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Hour))
			defer cancel()

			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			type observation struct {
				err    error
				logger any
				value  any
			}
			observed := make(chan observation, 1)
			returned := make(chan error, 1)
			stop := errors.New("stop at monitor-count transaction")
			db := &mocks.MockDB{TxFn: func(ctx context.Context, _ func(context.Context, *sql.Tx) error) error {
				entered <- ctx
				<-release
				observed <- observation{ctx.Err(), ctx.Value(mcTypes.CtxLogger), ctx.Value(requestKey{})}
				return stop
			}}
			state := interfaces.CephState{State: &mocks.MockState{DBObj: db}}
			go func() {
				returned <- ServicePlacementHandler(ctx, state, types.EnableService{Name: "smb", Payload: `{"cluster_id":"alpha"}`, Wait: wait})
			}()
			// Always release and drain the placement critical section before this
			// subtest ends, including on assertion failure. The async error path
			// uses no test state after releasing serviceStartMu.
			t.Cleanup(func() {
				cancel()
				close(release)
				select {
				case got := <-observed:
					assert.Equal(t, requestLogger, got.logger)
					assert.Equal(t, requestValue, got.value)
					if wait {
						assert.ErrorIs(t, got.err, context.Canceled)
					} else {
						assert.NoError(t, got.err, "async DB work must continue after the request is cancelled")
					}
				case <-time.After(5 * time.Second):
					t.Error("monitor-count transaction did not finish")
					return
				}
				// Acquiring the placement mutex drains work before cleanup returns.
				serviceStartMu.Lock()
				defer serviceStartMu.Unlock()
				if wait {
					select {
					case err := <-returned:
						assert.ErrorContains(t, err, stop.Error())
					case <-time.After(5 * time.Second):
						t.Error("synchronous handler did not finish")
					}
				}
			})

			var placementCtx context.Context
			select {
			case placementCtx = <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not enter the real config monitor-count query")
			}
			if !wait {
				select {
				case err := <-returned:
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("async handler did not return while placement was blocked")
				}
			}
			// The DB boundary remains blocked until cleanup, so cancellation is
			// observed deterministically, never via scheduling sleeps.
			cancel()
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			gotLogger, ok := placementCtx.Value(mcTypes.CtxLogger).(*slog.Logger)
			assert.True(t, ok, "microcluster v3.1.1 query.Retry requires a *slog.Logger")
			assert.Same(t, requestLogger, gotLogger)
			assert.Equal(t, requestValue, placementCtx.Value(requestKey{}))
			if wait {
				assert.Same(t, ctx, placementCtx)
				assert.ErrorIs(t, placementCtx.Err(), context.Canceled)
				assert.ErrorIs(t, context.Cause(placementCtx), context.Canceled)
				_, deadline := placementCtx.Deadline()
				assert.True(t, deadline)
				assert.NotNil(t, placementCtx.Done())
			} else {
				assert.NoError(t, placementCtx.Err())
				assert.NoError(t, context.Cause(placementCtx))
				_, deadline := placementCtx.Deadline()
				assert.False(t, deadline)
				assert.Nil(t, placementCtx.Done())
			}
		})
	}
}

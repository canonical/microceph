package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

func prepareSMBADRuntime(ctx context.Context, s interfaces.StateInterface, root, clusterID string) error {
	var state database.SMBADState
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		state, err = database.GetSMBADState(ctx, tx, clusterID, s.ClusterState().Name())
		return err
	})
	if err != nil {
		return err
	}
	if state.JoinOwner != "" && !state.Joined {
		return fmt.Errorf("AD initial join outcome is uncertain; retain ownership/state and reconcile in manual maintenance, never retry on timeout")
	}
	// The snap-started wrapper prepares safe mountpoint stubs before waiting
	// for Go-owned startup data. Confirm the context before consuming the sole
	// join attempt; there is no helper socket or live daemon handshake.
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = smbADProbeFunc(waitCtx, root)
		if err == nil {
			break
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("AD runtime context is unavailable")
		case <-ticker.C:
		}
	}
	if !state.Joined {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			return database.BeginSMBADJoin(ctx, tx, clusterID, s.ClusterState().Name())
		})
		if err != nil {
			return err
		}
		// Every failure from this point retains the consumed SQL attempt. A lost
		// response is not evidence that AD rejected or did not change the account.
		err = smbADJoinFunc(ctx, root)
		if err != nil {
			return fmt.Errorf("AD initial join is not confirmed; manual maintenance required, no automatic retry")
		}
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			return database.CompleteSMBADJoin(ctx, tx, clusterID, s.ClusterState().Name())
		})
		if err != nil {
			return err
		}
	}
	// A peer cannot initialize fresh mapping state during the first actor's
	// incomplete setup. Later peers/reboots require the existing CTDB records.
	if state.Joined && !state.Initialized {
		var identity string
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			view, err := database.GetSMBCTDBView(ctx, tx, clusterID, s.ClusterState().Name())
			if err == nil {
				identity = view.Member.Identity
			}
			return err
		})
		if err != nil {
			return err
		}
		if state.JoinOwner != identity {
			return fmt.Errorf("initial AD member has not completed trust/idmap readiness; retain state and retry after initialization")
		}
	}
	return smbADPrepareFunc(ctx, root, !state.Initialized)
}

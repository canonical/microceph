package ceph

import (
	"context"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/looplab/fsm"
)

func CreateReplicationFSM(initialState string, metadata types.RbdReplicationRequest) *fsm.FSM {
	newFsm := fsm.NewFSM(
		initialState,
		fsm.Events{
			fsm.EventDesc{
				// Enable event can be handled at both enabled and disabled state.
				Name: constants.EnableReplication,
				Src:  []string{constants.DisabledRBDReplication, constants.EnabledRBDReplication},
				Dst:  constants.EnabledRBDReplication},
			fsm.EventDesc{
				// Disable event can be handled at both enabled and disabled state.
				Name: constants.DisableReplication,
				Src:  []string{constants.DisabledRBDReplication, constants.EnabledRBDReplication},
				Dst:  constants.DisabledRBDReplication},
			fsm.EventDesc{
				// Configure event can only be handled from enabled state.
				Name: constants.ConfigureReplication,
				Src:  []string{constants.EnabledRBDReplication},
				Dst:  constants.EnabledRBDReplication},
			fsm.EventDesc{
				// List event can only be handled from enabled state.
				Name: constants.ListReplication,
				Src:  []string{constants.EnabledRBDReplication},
				Dst:  constants.EnabledRBDReplication},
			fsm.EventDesc{
				// Status event can only be handled from enabled state.
				Name: constants.StatusReplication,
				Src:  []string{constants.EnabledRBDReplication},
				Dst:  constants.EnabledRBDReplication},
		},
		fsm.Callbacks{
			// callback for all transitions
			"enter_state": genericLoggerHandler,
			// enable event handler
			constants.EnableReplication: enableHandler,
			// disable event handler
			constants.DisableReplication: disableHandler,
			// configure event handler
			constants.ConfigureReplication: configureHandler,
			// list event handler
			constants.ListReplication: listHandler,
			// status event handler
			constants.StatusReplication: statusHandler,
		},
	)

	// prepare metadata and state.
	newFsm.SetMetadata("metadata", metadata)
	newFsm.SetState(initialState)

	return newFsm
}

func genericLoggerHandler(_ context.Context, e *fsm.Event) {
	metadata, ok := e.FSM.Metadata("metadata")
	if !ok {
		logger.Errorf("Unable to fetch RBD request metadata. state(%s) event(%s)", e.Src, e.Event)
	}
	logger.Infof("Replication: RBD Event(%s), SrcState(%s), Metadata(%v)", e.Event, e.Src, &metadata)
}

func enableHandler(_ context.Context, e *fsm.Event)    { placeHolder("enable") }
func disableHandler(_ context.Context, e *fsm.Event)   { placeHolder("disable") }
func configureHandler(_ context.Context, e *fsm.Event) { placeHolder("configure") }
func listHandler(_ context.Context, e *fsm.Event)      { placeHolder("list") }
func statusHandler(_ context.Context, e *fsm.Event)    { placeHolder("status") }

func placeHolder(msg string) {
	logger.Infof("BAZINGA: %s handler", msg)
}

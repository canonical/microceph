package ceph

import (
	"context"
	"fmt"
	"reflect"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/qmuntal/stateless"
)

func CreateReplicationFSM(initialState string, metadata types.RbdReplicationRequest) *stateless.StateMachine {
	newFsm := stateless.NewStateMachine(initialState)
	// Configure transitions from disabled state.
	newFsm.Configure(constants.StateDisabledReplication).
		Permit(constants.EventEnableReplication, constants.StateEnabledReplication).
		OnEntryFrom(constants.EventEnableReplication, enableHandler).
		InternalTransition(constants.EventDisableReplication, disableHandler)

	// Configure transitions from enabled state.
	newFsm.Configure(constants.StateEnabledReplication).
		Permit(constants.EventDisableReplication, constants.StateDisabledReplication).
		OnEntryFrom(constants.EventDisableReplication, disableHandler).
		InternalTransition(constants.EventEnableReplication, enableHandler).
		InternalTransition(constants.EventConfigureReplication, configureHandler).
		InternalTransition(constants.EventListReplication, listHandler).
		InternalTransition(constants.EventStatusReplication, statusHandler)

	// Check Event params type.
	var output *string
	inputType := reflect.TypeOf(metadata)
	outputType := reflect.TypeOf(output)
	newFsm.SetTriggerParameters(constants.EventEnableReplication, inputType, outputType)
	newFsm.SetTriggerParameters(constants.EventDisableReplication, inputType, outputType)
	newFsm.SetTriggerParameters(constants.EventConfigureReplication, inputType, outputType)
	newFsm.SetTriggerParameters(constants.EventListReplication, inputType, outputType)
	newFsm.SetTriggerParameters(constants.EventStatusReplication, inputType, outputType)

	// Add logger callback for all transitions
	newFsm.OnTransitioning(logTransitionHandler)

	// Add handler for unhandled transitions.
	newFsm.OnUnhandledTrigger(unhandledTransitionHandler)

	logger.Infof("BAZINGA: Created new FSM from state: %s", initialState)

	return newFsm
}

func logTransitionHandler(_ context.Context, t stateless.Transition) {
	logger.Infof("Replication: RBD Event(%s), SrcState(%s), DstState(%s)", t.Trigger, t.Source, t.Destination)
}

func unhandledTransitionHandler(_ context.Context, state stateless.State, trigger stateless.Trigger, _ []string) error {
	return fmt.Errorf("operation: %s is not permitted at %s state", trigger, state)
}

func enableHandler(ctx context.Context, args ...any) error {
	// TODO: Implement
	logger.Infof("BAZINGA: %v", args)
	ph := "BAZINGA RESPONSE"
	*args[1].(*string) = ph
	return placeHolder("enable")
}
func disableHandler(ctx context.Context, args ...any) error {
	// TODO: Implement
	ph := "BAZINGA RESPONSE"
	*args[1].(*string) = ph
	return placeHolder("disable")
}
func configureHandler(ctx context.Context, args ...any) error {
	// TODO: Implement
	ph := "BAZINGA RESPONSE"
	*args[1].(*string) = ph
	return placeHolder("configure")
}
func listHandler(ctx context.Context, args ...any) error {
	// TODO: Implement
	ph := "BAZINGA RESPONSE"
	*args[1].(*string) = ph
	return placeHolder("list")
}
func statusHandler(ctx context.Context, args ...any) error {
	// TODO: Implement
	ph := "BAZINGA RESPONSE"
	*args[1].(*string) = ph
	return placeHolder("status")
}

func placeHolder(msg string) error {
	logger.Infof("BAZINGA: %s handler", msg)
	return nil
}

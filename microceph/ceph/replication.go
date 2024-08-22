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

type ReplicationState string

const (
	StateDisabledReplication ReplicationState = "replication_disabled"
	StateEnabledReplication  ReplicationState = "replication_enabled"
)

type ReplicationHandlerInterface interface {
	PreFill(ctx context.Context, request types.ReplicationRequest) error
	GetResourceState() ReplicationState
	EnableHandler(ctx context.Context, args ...any) error
	DisableHandler(ctx context.Context, args ...any) error
	ConfigureHandler(ctx context.Context, args ...any) error
	ListHandler(ctx context.Context, args ...any) error
	StatusHandler(ctx context.Context, args ...any) error
}

func GetReplicationHandler(name string) ReplicationHandlerInterface {
	// Add RGW and CephFs Replication handlers here.
	// TODO: Check
	table := map[string]ReplicationHandlerInterface{
		"rbd": &RbdReplicationHandler{},
	}

	rh, ok := table[name]
	if !ok {
		return nil
	}

	return rh
}

func GetReplicationStateMachine(initialState ReplicationState) *stateless.StateMachine {
	newFsm := stateless.NewStateMachine(initialState)
	// Configure transitions from disabled state.
	newFsm.Configure(StateDisabledReplication).
		Permit(constants.EventEnableReplication, StateEnabledReplication).
		OnEntryFrom(constants.EventEnableReplication, enableHandler).
		InternalTransition(constants.EventDisableReplication, disableHandler)

	// Configure transitions from enabled state.
	newFsm.Configure(StateEnabledReplication).
		Permit(constants.EventDisableReplication, StateDisabledReplication).
		OnEntryFrom(constants.EventDisableReplication, disableHandler).
		InternalTransition(constants.EventEnableReplication, enableHandler).
		InternalTransition(constants.EventConfigureReplication, configureHandler).
		InternalTransition(constants.EventListReplication, listHandler).
		InternalTransition(constants.EventStatusReplication, statusHandler)

	// Check Event params type.
	var output *string
	var eventHandler ReplicationHandlerInterface
	inputType := reflect.TypeOf(&eventHandler).Elem()
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
	rh := args[0].(ReplicationHandlerInterface)
	return rh.EnableHandler(ctx, args...)
}
func disableHandler(ctx context.Context, args ...any) error {
	rh := args[0].(ReplicationHandlerInterface)
	return rh.DisableHandler(ctx, args...)
}
func configureHandler(ctx context.Context, args ...any) error {
	rh := args[0].(ReplicationHandlerInterface)
	return rh.ConfigureHandler(ctx, args...)
}
func listHandler(ctx context.Context, args ...any) error {
	rh := args[0].(ReplicationHandlerInterface)
	return rh.ListHandler(ctx, args...)
}
func statusHandler(ctx context.Context, args ...any) error {
	rh := args[0].(ReplicationHandlerInterface)
	return rh.StatusHandler(ctx, args...)
}

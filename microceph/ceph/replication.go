package ceph

import (
	"context"
	"fmt"
	"reflect"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/qmuntal/stateless"
)

type repArgIndex int
type ReplicationState string

const (
	StateDisabledReplication ReplicationState = "replication_disabled"
	StateEnabledReplication  ReplicationState = "replication_enabled"
)

const (
	repArgHandler  repArgIndex = 0
	repArgResponse repArgIndex = 1
	repArgState    repArgIndex = 2
)

type ReplicationHandlerInterface interface {
	PreFill(ctx context.Context, request types.ReplicationRequest) error
	GetResourceState() ReplicationState
	EnableHandler(ctx context.Context, args ...any) error
	DisableHandler(ctx context.Context, args ...any) error
	ConfigureHandler(ctx context.Context, args ...any) error
	StatusHandler(ctx context.Context, args ...any) error
	// Cluster wide Operations (don't require any pool/image info.)
	ListHandler(ctx context.Context, args ...any) error
	PromoteHandler(ctx context.Context, args ...any) error
	DemoteHandler(ctx context.Context, args ...any) error
}

func GetReplicationHandler(name string) ReplicationHandlerInterface {
	// Add RGW and CephFs Replication handlers here.
	table := map[string]ReplicationHandlerInterface{
		"rbd": &RbdReplicationHandler{},
	}

	rh, ok := table[name]
	if !ok {
		return nil
	}

	return rh
}

func getAllEvents() []stateless.Trigger {
	return []stateless.Trigger{
		constants.EventEnableReplication,
		constants.EventDisableReplication,
		constants.EventConfigureReplication,
		constants.EventListReplication,
		constants.EventStatusReplication,
		constants.EventPromoteReplication,
		constants.EventDemoteReplication,
	}
}

func GetReplicationStateMachine(initialState ReplicationState) *stateless.StateMachine {
	newFsm := stateless.NewStateMachine(initialState)
	// Configure transitions for disabled state.
	newFsm.Configure(StateDisabledReplication).
		Permit(constants.EventEnableReplication, StateEnabledReplication).
		OnEntryFrom(constants.EventDisableReplication, disableHandler).
		InternalTransition(constants.EventListReplication, listHandler).
		InternalTransition(constants.EventDisableReplication, disableHandler).
		InternalTransition(constants.EventPromoteReplication, promoteHandler).
		InternalTransition(constants.EventDemoteReplication, demoteHandler)

	// Configure transitions for enabled state.
	newFsm.Configure(StateEnabledReplication).
		Permit(constants.EventDisableReplication, StateDisabledReplication).
		OnEntryFrom(constants.EventEnableReplication, enableHandler).
		InternalTransition(constants.EventConfigureReplication, configureHandler).
		InternalTransition(constants.EventListReplication, listHandler).
		InternalTransition(constants.EventStatusReplication, statusHandler).
		InternalTransition(constants.EventPromoteReplication, promoteHandler).
		InternalTransition(constants.EventDemoteReplication, demoteHandler)

	// Check Event params type.
	var outputType *string
	var stateType interfaces.CephState
	var inputType ReplicationHandlerInterface
	for _, event := range getAllEvents() {
		newFsm.SetTriggerParameters(event, reflect.TypeOf(&inputType).Elem(), reflect.TypeOf(outputType), reflect.TypeOf(stateType))
	}

	// Add logger callback for all transitions
	newFsm.OnTransitioning(logTransitionHandler)

	// Add handler for unhandled transitions.
	newFsm.OnUnhandledTrigger(unhandledTransitionHandler)

	logger.Debugf("REPFSM: Created from state: %s", initialState)
	return newFsm
}

func logTransitionHandler(_ context.Context, t stateless.Transition) {
	logger.Infof("REPFSM: Event(%s), SrcState(%s), DstState(%s)", t.Trigger, t.Source, t.Destination)
}

func unhandledTransitionHandler(_ context.Context, state stateless.State, trigger stateless.Trigger, _ []string) error {
	return fmt.Errorf("REPFSM: operation: %s is not permitted at %s state", trigger, state)
}

func enableHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	return rh.EnableHandler(ctx, args...)
}
func disableHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	return rh.DisableHandler(ctx, args...)
}
func configureHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	return rh.ConfigureHandler(ctx, args...)
}
func listHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	return rh.ListHandler(ctx, args...)
}
func statusHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	return rh.StatusHandler(ctx, args...)
}
func promoteHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	logger.Infof("REPFSM: Entered Status Handler")
	return rh.PromoteHandler(ctx, args...)
}
func demoteHandler(ctx context.Context, args ...any) error {
	rh := args[repArgHandler].(ReplicationHandlerInterface)
	logger.Infof("REPFSM: Entered Status Handler")
	return rh.DemoteHandler(ctx, args...)
}

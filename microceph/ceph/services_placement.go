package ceph

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
)

// PlacementIntf is the interface used for running various services in a MicroCeph cluster.
type PlacementIntf interface {
	// Populate json payload data to the service object.
	PopulateParams(interfaces.StateInterface, string) error
	// Check if host is hospitable to the new service to be enabled.
	HospitalityCheck(interfaces.StateInterface) error
	// Initialise the new service.
	ServiceInit(context.Context, interfaces.StateInterface) error
	// Perform Post Placement checks for the service
	PostPlacementCheck(interfaces.StateInterface) error
	// Perform DB updates to persist the service enablement changes.
	DbUpdate(context.Context, interfaces.StateInterface) error
}

func GetServicePlacementTable() map[string](PlacementIntf) {
	return map[string](PlacementIntf){
		"mon":           &MonServicePlacement{"mon"},
		"mgr":           &GenericServicePlacement{"mgr"},
		"mds":           &GenericServicePlacement{"mds"},
		"nfs":           &NFSServicePlacement{},
		"rgw":           &RgwServicePlacement{},
		"rbd-mirror":    &ClientServicePlacement{"rbd-mirror"},
		"cephfs-mirror": &ClientServicePlacement{"cephfs-mirror"},
	}
}

func ServicePlacementHandler(ctx context.Context, s interfaces.StateInterface, payload types.EnableService) error {
	var ok bool
	var spt = GetServicePlacementTable()
	var sp PlacementIntf

	logger.Debugf("Enabling %s service, payload: %v", payload.Name, payload.Payload)
	sp, ok = spt[payload.Name]
	if !ok {
		err := fmt.Errorf("%s enablement is not supported", payload.Name)
		logger.Error(err.Error())
		return err
	}

	if payload.Wait {
		err := EnableService(ctx, s, payload, sp)
		if err != nil {
			logger.Errorf("failed %s service enablement request: %v", payload.Name, err)
			return err
		}
	} else {
		go func() {
			// Async call to Enable service.
			err := EnableService(context.Background(), s, payload, sp)
			if err != nil {
				logger.Errorf("failed %s service enablement request: %v", payload.Name, err)
			}
		}()
	}

	return nil
}

// EnableService renders ceph.conf and the admin keyring from the shared
// cluster database before enabling a service. The config rendering uses the
// package-level updateConfigFunc injectable var (declared in join.go) so unit
// tests of the placement pipeline can bypass database-dependent config
// rendering.
func EnableService(ctx context.Context, s interfaces.StateInterface, payload types.EnableService, item PlacementIntf) error {

	// Ensure ceph.conf and the admin keyring are rendered from the shared
	// cluster database before enabling any service. This is intentional for
	// every EnableService call, not just deferred members: on already-configured
	// nodes it is an idempotent no-op rewrite (WriteConfig overwrites the same
	// content), and on deferred members (CE142) it is required because they have
	// no ceph.conf until Ceph is bootstrapped elsewhere. Rendering here realises
	// the "pre-joined members activate after Ceph bootstrap" step so role-managed
	// placement can add services to deferred members.
	err := updateConfigFunc(ctx, s)
	if err != nil {
		retErr := fmt.Errorf("failed to render ceph config before %s enablement: %v", payload.Name, err)
		logger.Error(retErr.Error())
		return retErr
	}

	// Populate json payload data to the service object.
	err = item.PopulateParams(s, payload.Payload)
	if err != nil {
		retErr := fmt.Errorf("failed to populate the payload for %s enablement: %v", payload.Name, err)
		logger.Error(retErr.Error())
		return retErr
	}

	// Check if host is hospitable to the new service to be enabled.
	err = item.HospitalityCheck(s)
	if err != nil {
		retErr := fmt.Errorf("host failed hospitality check for %s enablement: %v", payload.Name, err)
		logger.Error(retErr.Error())
		return retErr
	}

	// Initialise the new service.
	err = item.ServiceInit(ctx, s)
	if err != nil {
		retErr := fmt.Errorf("failed to initialise %s service at host: %v", payload.Name, err)
		logger.Error(retErr.Error())
		return retErr
	}

	// Perform Post Placement checks for the service
	err = item.PostPlacementCheck(s)
	if err != nil {
		retErr := fmt.Errorf("%s service unable to sustain on host: %v", payload.Name, err)
		logger.Error(retErr.Error())
		return retErr
	}

	// Perform DB updates to persist the service enablement changes.
	err = item.DbUpdate(ctx, s)
	if err != nil {
		retErr := fmt.Errorf("failed to add DB record for %s: %v", payload.Name, err)
		logger.Error(retErr.Error())
		return retErr
	}

	return nil
}

package ceph

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
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
		"mon":        &MonServicePlacement{"mon"},
		"mgr":        &GenericServicePlacement{"mgr", false},
		"mds":        &GenericServicePlacement{"mds", false},
		"rgw":        &RgwServicePlacement{},
		"rbd-mirror": &GenericServicePlacement{"rbd-mirror", true},
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

func EnableService(ctx context.Context, s interfaces.StateInterface, payload types.EnableService, item PlacementIntf) error {

	// Populate json payload data to the service object.
	err := item.PopulateParams(s, payload.Payload)
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

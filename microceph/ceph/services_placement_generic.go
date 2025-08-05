package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/database"
)

// Maps the addService function to respective services.
func GetServiceKeyringTable() map[string](func(string, string) error) {
	return map[string](func(string, string) error){
		"mon":           joinMon,
		"mgr":           bootstrapMgr,
		"mds":           bootstrapMds,
		"rbd-mirror":    bootstrapRbdMirror,
		"cephfs-mirror": bootstrapFsMirror,
		// Add more services here, for using the generic Interface implementation.
	}
}

// Used by services: mon, mgr, mds
type GenericServicePlacement struct {
	Name string
}

func (gsp *GenericServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	// No params needed to initialise generic service
	return nil
}

func (gsp *GenericServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	return genericHospitalityCheck(gsp.Name)
}

func (gsp *GenericServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	return genericServiceInit(s, gsp.Name)
}

func (gsp *GenericServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck(gsp.Name)
}

func (gsp *GenericServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return genericDbUpdate(ctx, s, gsp.Name)
}

// Generic Method Implementations
func genericHospitalityCheck(service string) error {
	// Check if service already exists on host.
	err := snapCheckActive(service)
	if err == nil {
		retErr := fmt.Errorf("%s service already active on host", service)
		logger.Error(retErr.Error())
		return retErr
	}

	return nil
}

func genericServiceInit(s interfaces.StateInterface, name string) error {
	var ok bool
	var bootstrapServiceKeyring func(string, string) error
	hostname := s.ClusterState().Name()
	pathConsts := constants.GetPathConst()
	pathFileMode := constants.GetPathFileMode()
	serviceDataPath := filepath.Join(pathConsts.DataPath, name, fmt.Sprintf("ceph-%s", hostname))

	// Fetch addService handler for name service
	bootstrapServiceKeyring, ok = GetServiceKeyringTable()[name]

	if !ok {
		err := fmt.Errorf("%s is not registered in the generic implementation", name)
		logger.Error(err.Error())
		return err
	}

	// Make required directories
	err := os.MkdirAll(serviceDataPath, pathFileMode[pathConsts.DataPath])
	if err != nil {
		logger.Error(err.Error())
		return fmt.Errorf("failed to add datapath %s for service %s: %w", serviceDataPath, name, err)
	}

	err = bootstrapServiceKeyring(hostname, serviceDataPath)
	if err != nil {
		logger.Error(err.Error())
		return fmt.Errorf("failed to add service %s: %w", name, err)
	}

	err = snapStart(name, true)
	if err != nil {
		logger.Error(err.Error())
		return fmt.Errorf("failed to perform snap start for service %s: %w", name, err)
	}

	return nil
}

func genericPostPlacementCheck(service string) error {
	// Check in a loop if the service stays up.
	attempts := 4

	for attempts > 0 {
		ret := snapCheckActive(service)
		if ret != nil {
			return ret
		}

		// simple delay, since only checking if the service stays up.
		time.Sleep(time.Duration(attempts) * time.Second)
		attempts-- // Decrease attempt by one.
	}

	return nil
}

func genericDbUpdate(ctx context.Context, s interfaces.StateInterface, service string) error {
	// Update the database.
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: service})
		if err != nil {
			return fmt.Errorf("failed to record role: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

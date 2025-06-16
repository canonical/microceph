package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/api"
)

var _ = api.ServerEnvironment{}

//go:generate mockery --name GroupedServiceQueryIntf
type GroupedServiceQueryIntf interface {
	// Add Method
	AddNew(ctx context.Context, s interfaces.StateInterface, service, groupID string, groupConfig, serviceInfo any) error

	// Get Methods
	GetGroupedServices(ctx context.Context, s interfaces.StateInterface) ([]GroupedService, error)
	GetGroupedServicesOnHost(ctx context.Context, s interfaces.StateInterface) ([]GroupedService, error)

	// Exists Methods
	ExistsOnHost(ctx context.Context, s interfaces.StateInterface, service, groupID string) (bool, error)

	// Delete Methods
	RemoveForHost(ctx context.Context, s interfaces.StateInterface, service, groupID string) error
}

type GroupedServiceQueryImpl struct{}

// AddNew creates a service record in the service_groups database if it doesn't exist already,
// and creates a record referencing it in the grouped_services database.
func (g GroupedServiceQueryImpl) AddNew(ctx context.Context, s interfaces.StateInterface, service, groupID string, groupConfig, serviceInfo any) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	bytes, err := json.Marshal(groupConfig)
	if err != nil {
		return fmt.Errorf("error while marshalling group config: %w", err)
	}
	groupConfigStr := string(bytes)

	bytes, err = json.Marshal(serviceInfo)
	if err != nil {
		return fmt.Errorf("error while marshalling group service info: %w", err)
	}
	serviceInfoStr := string(bytes)

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Ensure that the ServiceGroup exists. If it doesn't, create it.
		serviceGroup, err := GetServiceGroup(ctx, tx, service, groupID)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("failed to get service group record: %w", err)
		}

		if serviceGroup != nil {
			// If it exists, make sure the config matches.
			if serviceGroup.Config != groupConfigStr {
				return fmt.Errorf("conflicting service group configurations")
			}
		} else {
			// Create the ServiceGroup record.
			_, err = CreateServiceGroup(ctx, tx, ServiceGroup{GroupID: groupID, Service: service, Config: groupConfigStr})
			if err != nil {
				return fmt.Errorf("failed to record service group: %w", err)
			}
		}

		// Create the GroupedService record.
		_, err = CreateGroupedService(ctx, tx, GroupedService{Member: s.ClusterState().Name(), GroupID: groupID, Service: service, Info: serviceInfoStr})
		if err != nil {
			return fmt.Errorf("failed to record grouped service: %w", err)
		}

		return nil
	})

	return err
}

// GetGroupedServices returns an array of grouped services.
func (g GroupedServiceQueryImpl) GetGroupedServices(ctx context.Context, s interfaces.StateInterface) ([]GroupedService, error) {
	if s.ClusterState().ServerCert() == nil {
		return []GroupedService{}, fmt.Errorf("no server certificate")
	}

	var services []GroupedService
	var err error

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		services, err = GetGroupedServices(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to get grouped services records: %w", err)
		}

		return nil
	})

	return services, err
}

// GetGroupedServicesOnHost returns an array of grouped services present on the host.
func (g GroupedServiceQueryImpl) GetGroupedServicesOnHost(ctx context.Context, s interfaces.StateInterface) ([]GroupedService, error) {
	if s.ClusterState().ServerCert() == nil {
		return []GroupedService{}, fmt.Errorf("no server certificate")
	}

	var services []GroupedService
	var err error

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		member := s.ClusterState().Name()
		filter := GroupedServiceFilter{
			Member: &member,
		}

		services, err = GetGroupedServices(ctx, tx, filter)
		if err != nil {
			return fmt.Errorf("failed to get grouped services records: %w", err)
		}

		return nil
	})

	return services, err
}

// ExistsOnHost checks if a given grouped service exists on this host or not.
func (g GroupedServiceQueryImpl) ExistsOnHost(ctx context.Context, s interfaces.StateInterface, service, groupID string) (bool, error) {
	if s.ClusterState().ServerCert() == nil {
		return false, fmt.Errorf("no server certificate")
	}

	var exists bool
	var err error

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		exists, err = GroupedServiceExists(ctx, tx, service, groupID, s.ClusterState().Name())
		if err != nil {
			return fmt.Errorf("failed to check if grouped service record exists: %w", err)
		}

		return nil
	})

	return exists, err
}

// RemoveForHost deletes the given service record in the grouped_service database, and deletes the
// service record from the service_groups database if there is no grouped_service referencing it.
func (g GroupedServiceQueryImpl) RemoveForHost(ctx context.Context, s interfaces.StateInterface, service, groupID string) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Delete the GroupedService record.
		err := DeleteGroupedService(ctx, tx, s.ClusterState().Name(), service, groupID)
		if err != nil {
			return fmt.Errorf("failed to delete grouped service record: %w", err)
		}

		// Check if there is any GroupedService referencing this ServiceGroup.
		filter := GroupedServiceFilter{
			Service: &service,
			GroupID: &groupID,
		}
		groupedServices, err := GetGroupedServices(ctx, tx, filter)
		if err != nil {
			return fmt.Errorf("failed to get grouped services records: %w", err)
		}

		if len(groupedServices) > 0 {
			// There's still at least one GroupedService referencing this ServiceGroup.
			return nil
		}

		// Delete the ServiceGroup record.
		err = DeleteServiceGroup(ctx, tx, service, groupID)
		if err != nil {
			return fmt.Errorf("failed to delete service group record: %w", err)
		}

		return nil
	})

	return err
}

var GroupedServicesQuery GroupedServiceQueryIntf = GroupedServiceQueryImpl{}

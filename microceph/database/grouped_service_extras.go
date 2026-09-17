package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/api"
)

var _ = api.ServerEnvironment{}

//go:generate mockery --name GroupedServiceQueryIntf
type GroupedServiceQueryIntf interface {
	// Add Methods
	AddNew(ctx context.Context, s interfaces.StateInterface, service, groupID string, groupConfig, serviceInfo any) error
	AddOrUpdate(ctx context.Context, s interfaces.StateInterface, service, groupID string, groupConfig, serviceInfo any) error

	// Get Methods
	GetGroupedServices(ctx context.Context, s interfaces.StateInterface) ([]GroupedService, error)
	GetGroupedServicesWithGroupConfig(ctx context.Context, s interfaces.StateInterface) ([]GroupedServiceWithGroupConfig, error)
	GetGroupedServicesOnHost(ctx context.Context, s interfaces.StateInterface) ([]GroupedService, error)

	// Exists Methods
	ExistsOnHost(ctx context.Context, s interfaces.StateInterface, service, groupID string) (bool, error)

	// Delete Methods
	RemoveForHost(ctx context.Context, s interfaces.StateInterface, service, groupID string) error
}

type GroupedServiceQueryImpl struct{}

// GroupedServiceWithGroupConfig combines a member placement with its shared
// non-secret service-group configuration.
type GroupedServiceWithGroupConfig struct {
	GroupedService
	GroupConfig string
}

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

// AddOrUpdate atomically creates or updates a service group and its local member record.
func (g GroupedServiceQueryImpl) AddOrUpdate(ctx context.Context, s interfaces.StateInterface, service, groupID string, groupConfig, serviceInfo any) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	groupConfigBytes, err := json.Marshal(groupConfig)
	if err != nil {
		return fmt.Errorf("error while marshalling group config: %w", err)
	}
	serviceInfoBytes, err := json.Marshal(serviceInfo)
	if err != nil {
		return fmt.Errorf("error while marshalling group service info: %w", err)
	}

	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return addOrUpdateGroupedService(
			ctx,
			tx,
			s.ClusterState().Name(),
			service,
			groupID,
			string(groupConfigBytes),
			string(serviceInfoBytes),
		)
	})
}

func addOrUpdateGroupedService(ctx context.Context, tx *sql.Tx, member, service, groupID, groupConfig, serviceInfo string) error {
	var serviceGroupID int64
	err := tx.QueryRowContext(ctx, `
SELECT id FROM service_groups WHERE service = ? AND group_id = ?
`, service, groupID).Scan(&serviceGroupID)
	if errors.Is(err, sql.ErrNoRows) {
		result, createErr := tx.ExecContext(ctx, `
INSERT INTO service_groups (service, group_id, config) VALUES (?, ?, ?)
`, service, groupID, groupConfig)
		if createErr != nil {
			return fmt.Errorf("failed to create service group: %w", createErr)
		}
		serviceGroupID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get service group ID: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get service group record: %w", err)
	} else {
		_, err = tx.ExecContext(ctx, `
UPDATE service_groups SET config = ? WHERE id = ?
`, groupConfig, serviceGroupID)
		if err != nil {
			return fmt.Errorf("failed to update service group: %w", err)
		}
	}

	var groupedServiceID int64
	err = tx.QueryRowContext(ctx, `
SELECT id FROM grouped_services WHERE service_group_id = ? AND member_id = (
  SELECT id FROM core_cluster_members WHERE name = ?
)
`, serviceGroupID, member).Scan(&groupedServiceID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
INSERT INTO grouped_services (service_group_id, member_id, info)
VALUES (?, (SELECT id FROM core_cluster_members WHERE name = ?), ?)
`, serviceGroupID, member, serviceInfo)
		if err != nil {
			return fmt.Errorf("failed to create grouped service: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get grouped service record: %w", err)
	} else {
		_, err = tx.ExecContext(ctx, `
UPDATE grouped_services SET info = ? WHERE id = ?
`, serviceInfo, groupedServiceID)
		if err != nil {
			return fmt.Errorf("failed to update grouped service: %w", err)
		}
	}

	return nil
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

// GetGroupedServicesWithGroupConfig returns grouped services with their shared configuration.
func (g GroupedServiceQueryImpl) GetGroupedServicesWithGroupConfig(ctx context.Context, s interfaces.StateInterface) ([]GroupedServiceWithGroupConfig, error) {
	if s.ClusterState().ServerCert() == nil {
		return []GroupedServiceWithGroupConfig{}, fmt.Errorf("no server certificate")
	}

	var services []GroupedServiceWithGroupConfig
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		services, err = getGroupedServicesWithGroupConfig(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to get grouped services with group configuration: %w", err)
		}

		return nil
	})

	return services, err
}

func getGroupedServicesWithGroupConfig(ctx context.Context, tx *sql.Tx) ([]GroupedServiceWithGroupConfig, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT grouped_services.id,
       service_groups.service,
       service_groups.group_id,
       core_cluster_members.name,
       grouped_services.info,
       service_groups.config
  FROM grouped_services
  JOIN service_groups ON grouped_services.service_group_id = service_groups.id
  JOIN core_cluster_members ON grouped_services.member_id = core_cluster_members.id
 ORDER BY service_groups.id, service_groups.group_id, core_cluster_members.id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	services := make([]GroupedServiceWithGroupConfig, 0)
	for rows.Next() {
		service := GroupedServiceWithGroupConfig{}
		err = rows.Scan(
			&service.ID,
			&service.Service,
			&service.GroupID,
			&service.Member,
			&service.Info,
			&service.GroupConfig,
		)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return services, nil
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

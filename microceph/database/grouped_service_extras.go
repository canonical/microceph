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

// ReserveSMBCTDBRank atomically records a CTDB rank reservation before local
// SMB deployment. It does not create a grouped service receipt.
func ReserveSMBCTDBRank(ctx context.Context, tx *sql.Tx, groupID string, desiredSpec []byte, identity string) (SMBServiceGroupConfig, int, error) {
	if identity == "" {
		return SMBServiceGroupConfig{}, 0, fmt.Errorf("CTDB identity is required")
	}
	if len(desiredSpec) > 0 && !json.Valid(desiredSpec) {
		return SMBServiceGroupConfig{}, 0, fmt.Errorf("invalid SMB desired spec")
	}

	var stored string
	err := tx.QueryRowContext(ctx, `
SELECT config FROM service_groups WHERE service = 'smb' AND group_id = ?
`, groupID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		config := SMBServiceGroupConfig{
			DesiredSpec:  append(json.RawMessage(nil), desiredSpec...),
			CTDBRanks:    map[string]int{identity: 0},
			NextCTDBRank: 1,
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return SMBServiceGroupConfig{}, 0, fmt.Errorf("failed to encode SMB group configuration: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO service_groups (service, group_id, config) VALUES ('smb', ?, ?)
`, groupID, string(encoded))
		if err != nil {
			return SMBServiceGroupConfig{}, 0, fmt.Errorf("failed to reserve SMB CTDB rank: %w", err)
		}
		return config, 0, nil
	}
	if err != nil {
		return SMBServiceGroupConfig{}, 0, fmt.Errorf("failed to read SMB group configuration: %w", err)
	}

	var config SMBServiceGroupConfig
	err = json.Unmarshal([]byte(stored), &config)
	if err != nil {
		return SMBServiceGroupConfig{}, 0, fmt.Errorf("invalid stored SMB group config: %w", err)
	}
	err = validateSMBCTDBRanks(config)
	if err != nil {
		return SMBServiceGroupConfig{}, 0, err
	}
	config.DesiredSpec = append(json.RawMessage(nil), desiredSpec...)
	if config.CTDBRanks == nil {
		config.CTDBRanks = make(map[string]int)
	}
	rank, found := config.CTDBRanks[identity]
	if !found {
		rank = config.NextCTDBRank
		config.CTDBRanks[identity] = rank
		config.NextCTDBRank++
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return SMBServiceGroupConfig{}, 0, fmt.Errorf("failed to encode SMB group configuration: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE service_groups SET config = ? WHERE service = 'smb' AND group_id = ?`, string(encoded), groupID)
	if err != nil {
		return SMBServiceGroupConfig{}, 0, fmt.Errorf("failed to update SMB CTDB reservation: %w", err)
	}
	return config, rank, nil
}

func validateSMBCTDBRanks(config SMBServiceGroupConfig) error {
	if config.NextCTDBRank < 0 {
		return fmt.Errorf("negative next CTDB rank")
	}
	used := make(map[int]string)
	for identity, rank := range config.CTDBRanks {
		if identity == "" || rank < 0 {
			return fmt.Errorf("invalid CTDB rank allocation for %q: %d", identity, rank)
		}
		if other, found := used[rank]; found {
			return fmt.Errorf("CTDB rank %d already belongs to %q, not %q", rank, other, identity)
		}
		used[rank] = identity
		if rank >= config.NextCTDBRank {
			return fmt.Errorf("CTDB rank %d is not below next CTDB rank", rank)
		}
	}
	return nil
}

func addOrUpdateGroupedService(ctx context.Context, tx *sql.Tx, member, service, groupID, groupConfig, serviceInfo string) error {
	var serviceGroupID int64
	var existingConfig string
	err := tx.QueryRowContext(ctx, `
SELECT id, config FROM service_groups WHERE service = ? AND group_id = ?
`, service, groupID).Scan(&serviceGroupID, &existingConfig)
	if errors.Is(err, sql.ErrNoRows) {
		if service == "smb" {
			groupConfig, err = mergeSMBGroupConfig(`{}`, groupConfig)
			if err != nil {
				return fmt.Errorf("failed to validate SMB group configuration: %w", err)
			}
		}
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
		if service == "smb" {
			groupConfig, err = mergeSMBGroupConfig(existingConfig, groupConfig)
			if err != nil {
				return fmt.Errorf("failed to merge SMB group configuration: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `
UPDATE service_groups SET config = ? WHERE id = ?
`, groupConfig, serviceGroupID)
		if err != nil {
			return fmt.Errorf("failed to update service group: %w", err)
		}
	}

	if service == "smb" {
		err = validateSMBServiceReceipt(groupConfig, serviceInfo)
		if err != nil {
			return err
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

func validateSMBServiceReceipt(groupConfig, serviceInfo string) error {
	var config SMBServiceGroupConfig
	err := json.Unmarshal([]byte(groupConfig), &config)
	if err != nil {
		return fmt.Errorf("invalid SMB group configuration: %w", err)
	}
	var info SMBServiceInfo
	err = json.Unmarshal([]byte(serviceInfo), &info)
	if err != nil {
		return fmt.Errorf("invalid SMB service receipt: %w", err)
	}
	if info.CTDBRank == nil && info.CTDBIdentity == "" {
		return nil
	}
	// Older receipts recorded the rank without the identity. They cannot prove
	// a conflicting identity, so retain their established compatibility.
	if info.CTDBRank == nil || info.CTDBIdentity == "" {
		return nil
	}
	rank, found := config.CTDBRanks[info.CTDBIdentity]
	if !found || rank != *info.CTDBRank {
		return fmt.Errorf("CTDB receipt rank is not reserved for %q", info.CTDBIdentity)
	}
	return nil
}

// mergeSMBGroupConfig keeps CTDB allocations even when a member sends a stale
// snapshot of the shared config. New identities may only claim unused ranks.
func mergeSMBGroupConfig(existingJSON, incomingJSON string) (string, error) {
	var existing, incoming SMBServiceGroupConfig
	err := json.Unmarshal([]byte(existingJSON), &existing)
	if err != nil {
		return "", fmt.Errorf("invalid stored SMB group config: %w", err)
	}
	err = json.Unmarshal([]byte(incomingJSON), &incoming)
	if err != nil {
		return "", fmt.Errorf("invalid incoming SMB group config: %w", err)
	}

	if existing.NextCTDBRank < 0 || incoming.NextCTDBRank < 0 {
		return "", fmt.Errorf("negative next CTDB rank")
	}
	if incoming.CTDBRanks == nil {
		incoming.CTDBRanks = make(map[string]int)
	}
	for identity, rank := range existing.CTDBRanks {
		if previous, ok := incoming.CTDBRanks[identity]; ok && previous != rank {
			return "", fmt.Errorf("CTDB rank changed for %q from %d to %d", identity, rank, previous)
		}
		incoming.CTDBRanks[identity] = rank
	}
	used := make(map[int]string)
	for identity, rank := range incoming.CTDBRanks {
		if _, known := existing.CTDBRanks[identity]; !known && rank < existing.NextCTDBRank {
			return "", fmt.Errorf("CTDB rank %d is retired and cannot be assigned to %q", rank, identity)
		}
		if identity == "" || rank < 0 {
			return "", fmt.Errorf("invalid CTDB rank allocation for %q: %d", identity, rank)
		}
		if other, ok := used[rank]; ok {
			return "", fmt.Errorf("CTDB rank %d already belongs to %q, not %q", rank, other, identity)
		}
		used[rank] = identity
		if incoming.NextCTDBRank <= rank {
			incoming.NextCTDBRank = rank + 1
		}
	}
	if incoming.NextCTDBRank < existing.NextCTDBRank {
		incoming.NextCTDBRank = existing.NextCTDBRank
	}
	merged, err := json.Marshal(incoming)
	if err != nil {
		return "", err
	}
	return string(merged), nil
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
		return removeGroupedServiceForHost(ctx, tx, s.ClusterState().Name(), service, groupID)
	})

	return err
}

func removeGroupedServiceForHost(ctx context.Context, tx *sql.Tx, member, service, groupID string) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM grouped_services
 WHERE service_group_id = (
   SELECT id FROM service_groups WHERE service = ? AND group_id = ?
 )
 AND member_id = (
   SELECT id FROM core_cluster_members WHERE name = ?
 )
`, service, groupID, member)
	if err != nil {
		return fmt.Errorf("failed to delete grouped service record: %w", err)
	}

	var count int
	err = tx.QueryRowContext(ctx, `
SELECT count(*) FROM grouped_services
 WHERE service_group_id = (
   SELECT id FROM service_groups WHERE service = ? AND group_id = ?
 )
`, service, groupID).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count grouped service records: %w", err)
	}
	if count > 0 || service == "smb" {
		return nil
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM service_groups WHERE service = ? AND group_id = ?`, service, groupID)
	if err != nil {
		return fmt.Errorf("failed to delete service group record: %w", err)
	}

	return nil
}

// GetSMBServiceGroupConfig returns the persisted SMB group configuration,
// including reservations that have not produced a member receipt.
func GetSMBServiceGroupConfig(ctx context.Context, s interfaces.StateInterface, groupID string) (string, bool, error) {
	if s.ClusterState().ServerCert() == nil {
		return "", false, fmt.Errorf("no server certificate")
	}
	var config string
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT config FROM service_groups WHERE service = 'smb' AND group_id = ?`, groupID).Scan(&config)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return "", false, fmt.Errorf("failed to read SMB service group: %w", err)
	}
	return config, config != "", nil
}

// FinalizeSMBServiceGroup clears local SMB rank reservations after upstream
// deletion and local teardown. If expectedConfig is supplied, it must still
// match the group state captured before teardown.
func FinalizeSMBServiceGroup(ctx context.Context, s interfaces.StateInterface, groupID string, expectedConfig ...string) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}
	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var expected *string
		if len(expectedConfig) > 0 {
			expected = &expectedConfig[0]
		}
		return finalizeSMBServiceGroup(ctx, tx, groupID, expected)
	})
}

func finalizeSMBServiceGroup(ctx context.Context, tx *sql.Tx, groupID string, expectedConfig ...*string) error {
	var config string
	err := tx.QueryRowContext(ctx, `SELECT config FROM service_groups WHERE service = 'smb' AND group_id = ?`, groupID).Scan(&config)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read SMB service group: %w", err)
	}
	if len(expectedConfig) > 0 && expectedConfig[0] != nil && config != *expectedConfig[0] {
		return fmt.Errorf("SMB service group state changed during teardown")
	}
	var count int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM grouped_services WHERE service_group_id = (SELECT id FROM service_groups WHERE service = 'smb' AND group_id = ?)`, groupID).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to count SMB service records: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("SMB service records remain after teardown")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM service_groups WHERE service = 'smb' AND group_id = ?`, groupID)
	if err != nil {
		return fmt.Errorf("failed to delete SMB service group: %w", err)
	}
	return nil
}

var GroupedServicesQuery GroupedServiceQueryIntf = GroupedServiceQueryImpl{}

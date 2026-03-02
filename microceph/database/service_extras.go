package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/v2/state"
)

// ServiceQueryInterface is for querying services. Introduced for mocking.
//
//go:generate mockery --name ServiceQueryInterface
type ServiceQueryInterface interface {
	List(ctx context.Context, s state.State) (types.Services, error)
}

type ServiceQueryImpl struct{}

// List retrieves all services from the database.
func (sq ServiceQueryImpl) List(ctx context.Context, s state.State) (types.Services, error) {
	services := types.Services{}

	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		records, err := GetServices(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch service: %w", err)
		}

		for _, service := range records {
			services = append(services, types.Service{
				Location: service.Member,
				Service:  service.Service,
				Info:     "",
				GroupID:  "",
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return services, nil
}

// ServiceQuery is a singleton for the ServiceQueryImpl, to be mocked in unit testing.
var ServiceQuery ServiceQueryInterface = ServiceQueryImpl{}

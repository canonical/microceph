// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/microcluster/client"
	"github.com/lxc/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
)

type ApiDisksGetter interface {
	GetDisks(ctx context.Context) (types.Disks, error)
}

type ApiResourcesGetter interface {
	GetResources(ctx context.Context) (*api.ResourcesStorage, error)
}

type ApiServicesGetter interface {
	GetServices(ctx context.Context) (types.Services, error)
}

type ApiDisksAppender interface {
	AddDisk(ctx context.Context, data *types.DisksPost) error
}

type ApiRGWEnabler interface {
	EnableRGW(ctx context.Context, data *types.RGWService) error
}

type ApiReader interface {
	ApiDisksGetter
	ApiResourcesGetter
	ApiServicesGetter
}

type ApiWriter interface {
	ApiDisksAppender
	ApiRGWEnabler
}

type ApiClient interface {
	ApiReader
	ApiWriter
}

type microCli struct {
	restClient *client.Client
}

func NewClient(c *client.Client) ApiClient {
	return microCli{restClient: c}
}

// AddDisk requests Ceph sets up a new OSD.
func (m microCli) AddDisk(ctx context.Context, data *types.DisksPost) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := m.restClient.Query(queryCtx, "POST", api.NewURL().Path("disks"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed adding new disk: %w", err)
	}

	return nil
}

// GetDisks returns the list of configured disks.
func (m microCli) GetDisks(ctx context.Context) (types.Disks, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	disks := types.Disks{}

	err := m.restClient.Query(queryCtx, "GET", api.NewURL().Path("disks"), nil, &disks)
	if err != nil {
		return nil, fmt.Errorf("Failed listing disks: %w", err)
	}

	return disks, nil
}

// GetResources returns the list of storage devices on the system.
func (m microCli) GetResources(ctx context.Context) (*api.ResourcesStorage, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	storage := api.ResourcesStorage{}

	err := m.restClient.Query(queryCtx, "GET", api.NewURL().Path("resources"), nil, &storage)
	if err != nil {
		return nil, fmt.Errorf("Failed listing storage devices: %w", err)
	}

	return &storage, nil
}

// GetServices returns the list of configured ceph services.
func (m microCli) GetServices(ctx context.Context) (types.Services, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	services := types.Services{}

	err := m.restClient.Query(queryCtx, "GET", api.NewURL().Path("services"), nil, &services)
	if err != nil {
		return nil, fmt.Errorf("Failed listing services: %w", err)
	}

	return services, nil
}

// EnableRGW requests Ceph configures the RGW service.
func (m microCli) EnableRGW(ctx context.Context, data *types.RGWService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()
	err := m.restClient.Query(queryCtx, "PUT", api.NewURL().Path("services", "rgw"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed enabling RGW: %w", err)
	}

	return nil
}

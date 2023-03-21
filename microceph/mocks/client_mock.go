package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/lxc/lxd/shared/api"
)

type ApiMock struct {
	mock.Mock
}

func (m *ApiMock) GetDisks(ctx context.Context) (types.Disks, error) {
	mockArgs := m.Called(ctx)
	return mockArgs.Get(0).(types.Disks), mockArgs.Error(1)
}

func (m *ApiMock) GetResources(ctx context.Context) (*api.ResourcesStorage, error) {
	mockArgs := m.Called(ctx)
	if mockArgs.Get(0) != nil {
		return mockArgs.Get(0).(*api.ResourcesStorage), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

func (m *ApiMock) GetServices(ctx context.Context) (types.Services, error) {
	mockArgs := m.Called(ctx)
	return mockArgs.Get(0).(types.Services), mockArgs.Error(1)
}

func (m *ApiMock) AddDisk(ctx context.Context, data *types.DisksPost) error {
	mockArgs := m.Called(ctx, data)
	return mockArgs.Error(0)
}

func (m *ApiMock) EnableRGW(ctx context.Context, data *types.RGWService) error {
	mockArgs := m.Called(ctx, data)
	return mockArgs.Error(0)
}

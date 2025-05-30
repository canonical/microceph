package ceph

import (
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/interfaces"
)

// StorageImpl provides the default implementation of StorageInterface.
type StorageImpl struct{}

// GetStorage returns system storage information.
func (s StorageImpl) GetStorage() (*api.ResourcesStorage, error) {
	return resources.GetStorage()
}

// Ensure StorageImpl implements StorageInterface.
var _ interfaces.StorageInterface = StorageImpl{}

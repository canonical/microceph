package interfaces

import "github.com/canonical/lxd/shared/api"

// StorageInterface abstracts storage operations for testing.
type StorageInterface interface {
	GetStorage() (*api.ResourcesStorage, error)
}

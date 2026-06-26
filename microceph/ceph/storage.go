package ceph

import (
	"time"

	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// StorageImpl provides the default implementation of StorageInterface.
type StorageImpl struct{}

// GetStorage returns system storage information.
func (s StorageImpl) GetStorage() (*api.ResourcesStorage, error) {
	return resources.GetStorage()
}

// Ensure StorageImpl implements StorageInterface.
var _ interfaces.StorageInterface = StorageImpl{}

// storageRetrySleepFunc is the sleep function used between GetStorage retry
// attempts. It is a package-level variable so tests can replace it with a no-op
// for fast execution without changing retry logic.
var storageRetrySleepFunc = time.Sleep

// GetStorageWithRetry calls a GetStorage function up to 3 times, retrying on
// any error.
//
// LXD's resources.GetStorage() enumerates /dev/disk/by-id and can hit a
// TOCTOU race: udevd atomically replaces symlinks by writing a .#-prefixed
// temp entry then renaming it, so GetStorage() can see the .# entry in
// readdir and then get ENOENT on lstat because the rename completed between
// the two calls. The error is transient — the temp entry is gone by the next
// attempt — so a short retry absorbs it.
//
// getStorageFunc is passed in (rather than calling resources.GetStorage()
// directly) so the retry is unit-testable without touching the live block
// device layer. storageRetrySleepFunc is the package-level
// sleep func that tests can swap for a no-op.
func GetStorageWithRetry(getStorageFunc func() (*api.ResourcesStorage, error)) (*api.ResourcesStorage, error) {
	const maxAttempts = 3
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var storage *api.ResourcesStorage
		storage, err = getStorageFunc()
		if err == nil {
			return storage, nil
		}
		if attempt < maxAttempts {
			logger.Warnf("Transient error enumerating storage (attempt %d/%d): %v; retrying",
				attempt, maxAttempts, err)
			storageRetrySleepFunc(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	return nil, err
}

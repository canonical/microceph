package ceph

import (
	"fmt"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetStorageWithRetryAbsorbsTransientError verifies that GetStorageWithRetry
// retries on a transient error (the udevd TOCTOU race in /dev/disk/by-id) and
// succeeds on the next attempt. This is the regression path for the daemon
// /resources endpoint, which previously called resources.GetStorage() once with
// no retry and failed when a .#-prefixed temp entry vanished mid-enumeration.
func TestGetStorageWithRetryAbsorbsTransientError(t *testing.T) {
	storageRetrySleepFunc = func(_ time.Duration) {}
	t.Cleanup(func() { storageRetrySleepFunc = time.Sleep })

	goodStorage := &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{{Device: "sda"}},
	}

	calls := 0
	getStorage := func() (*api.ResourcesStorage, error) {
		calls++
		if calls == 1 {
			// Exact error shape from the CI failure: udevd renamed a
			// .#scsi-... entry out from under LXD's EvalSymlinks call.
			return nil, fmt.Errorf(`Failed to find "/dev/disk/by-id/.#scsi-0QEMU_QEMU_HARDDISK_lxd_microceph--dsl--rm1x--part1": lstat /dev/disk/by-id/.#scsi-0QEMU_QEMU_HARDDISK_lxd_microceph--dsl--rm1x--part1: no such file or directory`)
		}
		return goodStorage, nil
	}

	storage, err := GetStorageWithRetry(getStorage)

	require.NoError(t, err)
	assert.Equal(t, goodStorage, storage)
	assert.Equal(t, 2, calls, "should succeed on the second attempt")
}

// TestGetStorageWithRetryExhausted verifies that the last error is returned
// after all retry attempts are exhausted, and that GetStorage() is called
// exactly maxAttempts (3) times before giving up.
func TestGetStorageWithRetryExhausted(t *testing.T) {
	storageRetrySleepFunc = func(_ time.Duration) {}
	t.Cleanup(func() { storageRetrySleepFunc = time.Sleep })

	persistentErr := fmt.Errorf("persistent enumeration failure")
	calls := 0
	getStorage := func() (*api.ResourcesStorage, error) {
		calls++
		return nil, persistentErr
	}

	storage, err := GetStorageWithRetry(getStorage)

	require.Error(t, err)
	assert.Equal(t, persistentErr, err)
	assert.Nil(t, storage)
	assert.Equal(t, 3, calls, "should retry up to maxAttempts (3) times")
}

// TestGetStorageWithRetryFirstTrySuccess verifies that no retry occurs when
// GetStorage() succeeds immediately.
func TestGetStorageWithRetryFirstTrySuccess(t *testing.T) {
	goodStorage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{{Device: "sda"}}}

	calls := 0
	getStorage := func() (*api.ResourcesStorage, error) {
		calls++
		return goodStorage, nil
	}

	storage, err := GetStorageWithRetry(getStorage)

	require.NoError(t, err)
	assert.Equal(t, goodStorage, storage)
	assert.Equal(t, 1, calls, "should not retry on success")
}

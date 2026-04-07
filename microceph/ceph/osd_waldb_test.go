package ceph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microcluster/v2/state"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type staticStorage struct {
	storage *api.ResourcesStorage
	err     error
}

func (s staticStorage) GetStorage() (*api.ResourcesStorage, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.storage, nil
}

type staticMountChecker struct{}
type staticCephDeviceChecker struct{}
type staticPristineChecker struct{}

func (staticMountChecker) IsMounted(string) (bool, error) {
	return false, nil
}

func (staticCephDeviceChecker) IsCephDevice(string) (bool, error) {
	return false, nil
}

func (staticPristineChecker) IsPristineDisk(string) (bool, error) {
	return true, nil
}

func makeTestDisk(id, path string, sizeGiB uint64) api.ResourcesStorageDisk {
	return api.ResourcesStorageDisk{
		ID:         id,
		DevicePath: path,
		Size:       sizeGiB * 1024 * 1024 * 1024,
		Type:       "virtio",
		Model:      "QEMU HARDDISK",
		Partitions: []api.ResourcesStorageDiskPartition{},
	}
}

func makeTestDiskWithPartition(id, path string, sizeGiB uint64, partID string, partNo uint64, partSizeGiB uint64) api.ResourcesStorageDisk {
	disk := makeTestDisk(id, path, sizeGiB)
	disk.Partitions = []api.ResourcesStorageDiskPartition{{
		ID:        partID,
		Partition: partNo,
		Size:      partSizeGiB * 1024 * 1024 * 1024,
	}}
	return disk
}

func newDryRunManagerWithConfiguredDisks(t *testing.T, storage *api.ResourcesStorage, configuredDisks types.Disks) (*OSDManager, *mocks.OSDQueryInterface) {
	t.Helper()
	var st state.State
	mgr := NewOSDManager(st)
	mgr.storage = staticStorage{storage: storage}
	mgr.mountChecker = staticMountChecker{}
	mgr.cephDeviceChecker = staticCephDeviceChecker{}
	mgr.pristineChecker = staticPristineChecker{}

	osdQuery := mocks.NewOSDQueryInterface(t)
	osdQuery.On("List", mock.Anything, mock.Anything).Return(configuredDisks, nil).Maybe()

	original := database.OSDQuery
	database.OSDQuery = osdQuery
	t.Cleanup(func() {
		database.OSDQuery = original
	})

	return mgr, osdQuery
}

func newDryRunManager(t *testing.T, storage *api.ResourcesStorage) (*OSDManager, *mocks.OSDQueryInterface) {
	return newDryRunManagerWithConfiguredDisks(t, storage, types.Disks{})
}

func TestAddDisksWithDSLRequestDryRunPlan(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("osd2", "virtio-pci-0000:02:00.0", 11),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
		makeTestDisk("wal2", "virtio-pci-0000:04:00.0", 21),
		makeTestDisk("db1", "virtio-pci-0000:05:00.0", 30),
	}}
	mgr, _ := newDryRunManager(t, storage)

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "or(eq(@size, 10GiB), eq(@size, 11GiB))",
		WALMatch: "or(eq(@size, 20GiB), eq(@size, 21GiB))",
		WALSize:  "1GiB",
		DBMatch:  "eq(@size, 30GiB)",
		DBSize:   "2GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 2)
	require.Empty(t, resp.Warnings)

	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:01:00.0", resp.DryRunPlan[0].OSDPath)
	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:02:00.0", resp.DryRunPlan[1].OSDPath)

	require.NotNil(t, resp.DryRunPlan[0].WAL)
	require.NotNil(t, resp.DryRunPlan[1].WAL)
	assert.NotEqual(t, resp.DryRunPlan[0].WAL.ParentPath, resp.DryRunPlan[1].WAL.ParentPath)
	assert.Equal(t, uint64(1), resp.DryRunPlan[0].WAL.Partition)
	assert.Equal(t, uint64(1), resp.DryRunPlan[1].WAL.Partition)
	assert.Equal(t, "1.00 GiB", resp.DryRunPlan[0].WAL.Size)

	require.NotNil(t, resp.DryRunPlan[0].DB)
	require.NotNil(t, resp.DryRunPlan[1].DB)
	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:05:00.0", resp.DryRunPlan[0].DB.ParentPath)
	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:05:00.0", resp.DryRunPlan[1].DB.ParentPath)
	assert.Equal(t, uint64(1), resp.DryRunPlan[0].DB.Partition)
	assert.Equal(t, uint64(2), resp.DryRunPlan[1].DB.Partition)
}

func TestAddDisksWithDSLRequestDryRunWarnings(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("db1", "virtio-pci-0000:05:00.0", 30),
	}}
	mgr, _ := newDryRunManager(t, storage)

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 999GiB)",
		WALSize:  "1GiB",
		DBMatch:  "eq(@size, 30GiB)",
		DBSize:   "2GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL match expression resolved to no devices")
	assert.Nil(t, resp.DryRunPlan[0].WAL)
	require.NotNil(t, resp.DryRunPlan[0].DB)
}

func TestAddDisksWithDSLRequestDryRunRejectsNonPristineWholeAuxDiskWithoutWipe(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
	}}
	mgr, _ := newDryRunManager(t, storage)
	mockPristineChecker := &MockPristineChecker{}
	mgr.pristineChecker = mockPristineChecker
	mockPristineChecker.On("IsPristineDisk", "/dev/disk/by-path/virtio-pci-0000:03:00.0").Return(false, nil).Once()

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL match expression resolved to no devices")
	assert.Nil(t, resp.DryRunPlan[0].WAL)
	mockPristineChecker.AssertExpectations(t)
}

func TestAddDisksWithDSLRequestDryRunAllowsNonPristineWholeAuxDiskWithWipe(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
	}}
	mgr, _ := newDryRunManager(t, storage)
	mockPristineChecker := &MockPristineChecker{}
	mgr.pristineChecker = mockPristineChecker

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		WALWipe:  true,
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL carrier /dev/disk/by-path/virtio-pci-0000:03:00.0 will be wiped/reset before partitioning")
	require.NotNil(t, resp.DryRunPlan[0].WAL)
	assert.Equal(t, uint64(1), resp.DryRunPlan[0].WAL.Partition)
	assert.True(t, resp.DryRunPlan[0].WAL.ResetBeforeUse)
	mockPristineChecker.AssertNotCalled(t, "IsPristineDisk", "/dev/disk/by-path/virtio-pci-0000:03:00.0")
}

func TestAddDisksWithDSLRequestDryRunAllowsPartitionedForeignAuxDiskWithWipe(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDiskWithPartition("wal1", "virtio-pci-0000:03:00.0", 20, "vdc1", 1, 1),
	}}
	mgr, _ := newDryRunManager(t, storage)
	mockPristineChecker := &MockPristineChecker{}
	mgr.pristineChecker = mockPristineChecker

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		WALWipe:  true,
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL carrier /dev/disk/by-path/virtio-pci-0000:03:00.0 will be wiped/reset before partitioning")
	require.NotNil(t, resp.DryRunPlan[0].WAL)
	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:03:00.0", resp.DryRunPlan[0].WAL.ParentPath)
	assert.Equal(t, uint64(1), resp.DryRunPlan[0].WAL.Partition)
	assert.True(t, resp.DryRunPlan[0].WAL.ResetBeforeUse)
	mockPristineChecker.AssertNotCalled(t, "IsPristineDisk", "/dev/disk/by-path/virtio-pci-0000:03:00.0")
}

func TestAddDisksWithDSLRequestDryRunRejectsPartitionedNonCephAuxDisk(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDiskWithPartition("wal1", "virtio-pci-0000:03:00.0", 20, "vdc1", 1, 1),
	}}
	mgr, _ := newDryRunManager(t, storage)

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL match expression resolved to no devices")
	assert.Nil(t, resp.DryRunPlan[0].WAL)
}

func TestAddDisksWithDSLRequestDryRunRejectsWholeDiskCephAuxDevice(t *testing.T) {
	snapCommon := t.TempDir()
	t.Setenv("SNAP_COMMON", snapCommon)

	usedWholeDiskPath := filepath.Join(t.TempDir(), "used-whole-wal-disk")
	require.NoError(t, os.WriteFile(usedWholeDiskPath, []byte("wal"), 0644))

	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		{
			ID:         filepath.ToSlash(filepath.Join("..", "..", strings.TrimPrefix(usedWholeDiskPath, "/"))),
			Size:       20 * 1024 * 1024 * 1024,
			Type:       "virtio",
			Model:      "QEMU HARDDISK",
			Partitions: []api.ResourcesStorageDiskPartition{},
		},
	}}
	mgr, _ := newDryRunManager(t, storage)

	osdDir := filepath.Join(snapCommon, "data", "osd", "ceph-0")
	require.NoError(t, os.MkdirAll(osdDir, 0755))
	require.NoError(t, os.Symlink(usedWholeDiskPath, filepath.Join(osdDir, "block.wal")))

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL match expression resolved to no devices")
	assert.Nil(t, resp.DryRunPlan[0].WAL)
}

func TestAddDisksWithDSLRequestDryRunRejectsEncryptedWholeDiskCephAuxDevice(t *testing.T) {
	snapCommon := t.TempDir()
	t.Setenv("SNAP_COMMON", snapCommon)

	usedWholeDiskPath := filepath.Join(t.TempDir(), "used-encrypted-whole-wal-disk")
	require.NoError(t, os.WriteFile(usedWholeDiskPath, []byte("wal"), 0644))

	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		{
			ID:         filepath.ToSlash(filepath.Join("..", "..", strings.TrimPrefix(usedWholeDiskPath, "/"))),
			Size:       20 * 1024 * 1024 * 1024,
			Type:       "virtio",
			Model:      "QEMU HARDDISK",
			Partitions: []api.ResourcesStorageDiskPartition{},
		},
	}}
	mgr, _ := newDryRunManager(t, storage)

	osdDir := filepath.Join(snapCommon, "data", "osd", "ceph-0")
	require.NoError(t, os.MkdirAll(osdDir, 0755))
	require.NoError(t, os.Symlink(usedWholeDiskPath, filepath.Join(osdDir, "unencrypted.wal")))

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "WAL match expression resolved to no devices")
	assert.Nil(t, resp.DryRunPlan[0].WAL)
}

func TestAddDisksWithDSLRequestDryRunAllowsPartitionedEncryptedCurrentClusterAuxCarrier(t *testing.T) {
	snapCommon := t.TempDir()
	t.Setenv("SNAP_COMMON", snapCommon)

	usedPartitionPath := filepath.Join(t.TempDir(), "used-encrypted-wal-partition")
	require.NoError(t, os.WriteFile(usedPartitionPath, []byte("wal-partition"), 0644))
	partitionID := filepath.ToSlash(filepath.Join("..", "..", strings.TrimPrefix(usedPartitionPath, "/")))

	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		{
			ID:         "wal1",
			DevicePath: "virtio-pci-0000:03:00.0",
			Size:       20 * 1024 * 1024 * 1024,
			Type:       "virtio",
			Model:      "QEMU HARDDISK",
			Partitions: []api.ResourcesStorageDiskPartition{{
				ID:        partitionID,
				Partition: 1,
				Size:      1 * 1024 * 1024 * 1024,
			}},
		},
	}}
	mgr, _ := newDryRunManager(t, storage)

	osdDir := filepath.Join(snapCommon, "data", "osd", "ceph-0")
	require.NoError(t, os.MkdirAll(osdDir, 0755))
	require.NoError(t, os.Symlink(usedPartitionPath, filepath.Join(osdDir, "unencrypted.wal")))

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.Empty(t, resp.Warnings)
	require.NotNil(t, resp.DryRunPlan[0].WAL)
	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:03:00.0", resp.DryRunPlan[0].WAL.ParentPath)
	assert.Equal(t, uint64(2), resp.DryRunPlan[0].WAL.Partition)
}

func TestAddDisksWithDSLRequestDryRunIgnoresConfiguredDiskOnOtherHost(t *testing.T) {
	hostname, err := os.Hostname()
	require.NoError(t, err)

	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
	}}
	remoteConfigured := types.Disks{{
		Path:     "/dev/disk/by-path/virtio-pci-0000:03:00.0",
		Location: hostname + "-other",
	}}
	mgr, _ := newDryRunManagerWithConfiguredDisks(t, storage, remoteConfigured)

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.DryRunPlan, 1)
	require.Empty(t, resp.Warnings)
	require.NotNil(t, resp.DryRunPlan[0].WAL)
	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:03:00.0", resp.DryRunPlan[0].WAL.ParentPath)
}

func TestAddDisksWithDSLRequestDryRunOverlapRejected(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
	}}
	mgr, _ := newDryRunManager(t, storage)

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 10GiB)",
		WALSize:  "1GiB",
		DryRun:   true,
	})

	assert.Contains(t, resp.ValidationError, "OSD and WAL match sets overlap")
}

func TestAddDisksWithDSLRequestNonDryRunNoNewOSDs(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
	}}
	mgr, _ := newDryRunManager(t, storage)
	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
	})
	assert.Empty(t, resp.ValidationError)
	assert.Contains(t, resp.Warnings[0], "no new OSDs")
}

func TestAddDisksWithDSLRequestNonDryRunUsesIndependentAuxFlags(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
		makeTestDisk("db1", "virtio-pci-0000:05:00.0", 30),
	}}
	mgr, _ := newDryRunManager(t, storage)

	originalCreatePlannedAuxPartitionFn := createPlannedAuxPartitionFn
	originalDoAddOSDWithStorageFn := doAddOSDWithStorageFn
	t.Cleanup(func() {
		createPlannedAuxPartitionFn = originalCreatePlannedAuxPartitionFn
		doAddOSDWithStorageFn = originalDoAddOSDWithStorageFn
	})

	createPlannedAuxPartitionFn = func(m *OSDManager, plan *plannedAuxPartition) (string, error) {
		switch plan.Kind {
		case "wal":
			return "/dev/mock-wal1", nil
		case "db":
			return "/dev/mock-db1", nil
		default:
			return "", assert.AnError
		}
	}

	var capturedData types.DiskParameter
	var capturedWAL *types.DiskParameter
	var capturedDB *types.DiskParameter
	var capturedManifest *generatedAuxDevicesManifest
	var invocationCount int
	doAddOSDWithStorageFn = func(m *OSDManager, ctx context.Context, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter, storage *api.ResourcesStorage, generatedAux *generatedAuxDevicesManifest) error {
		invocationCount++
		capturedData = data
		capturedWAL = wal
		capturedDB = db
		capturedManifest = generatedAux
		require.NotNil(t, storage)
		return nil
	}

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch:   "eq(@size, 10GiB)",
		Encrypt:    true,
		Wipe:       false,
		WALMatch:   "eq(@size, 20GiB)",
		WALSize:    "1GiB",
		WALEncrypt: true,
		WALWipe:    true,
		DBMatch:    "eq(@size, 30GiB)",
		DBSize:     "2GiB",
		DBEncrypt:  false,
		DBWipe:     true,
	})

	require.Empty(t, resp.ValidationError)
	require.Len(t, resp.Reports, 1)
	assert.Equal(t, "Success", resp.Reports[0].Report)
	assert.Equal(t, 1, invocationCount)

	assert.Equal(t, "/dev/disk/by-path/virtio-pci-0000:01:00.0", capturedData.Path)
	assert.True(t, capturedData.Encrypt)
	assert.False(t, capturedData.Wipe)

	require.NotNil(t, capturedWAL)
	assert.Equal(t, "/dev/mock-wal1", capturedWAL.Path)
	assert.True(t, capturedWAL.Encrypt)
	assert.True(t, capturedWAL.Wipe)
	assert.True(t, capturedWAL.SkipPristineCheck)

	require.NotNil(t, capturedDB)
	assert.Equal(t, "/dev/mock-db1", capturedDB.Path)
	assert.False(t, capturedDB.Encrypt)
	assert.True(t, capturedDB.Wipe)
	assert.True(t, capturedDB.SkipPristineCheck)

	require.NotNil(t, capturedManifest)
	require.NotNil(t, capturedManifest.WAL)
	assert.True(t, capturedManifest.WAL.Encrypted)
	assert.Equal(t, "/dev/mock-wal1", capturedManifest.WAL.PartitionPath)
	require.NotNil(t, capturedManifest.DB)
	assert.False(t, capturedManifest.DB.Encrypted)
	assert.Equal(t, "/dev/mock-db1", capturedManifest.DB.PartitionPath)
}

func TestBuildGeneratedAuxDiskParameter(t *testing.T) {
	plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/disk/by-id/wal", Partition: 3}

	param, generated := buildGeneratedAuxDiskParameter(plan, "/dev/sde3", true, true)
	require.NotNil(t, param)
	require.NotNil(t, generated)
	assert.Equal(t, "/dev/sde3", param.Path)
	assert.True(t, param.Encrypt)
	assert.True(t, param.Wipe)
	assert.Equal(t, "/dev/disk/by-id/wal", generated.ParentPath)
	assert.Equal(t, uint64(3), generated.Partition)
	assert.Equal(t, "/dev/sde3", generated.PartitionPath)
	assert.True(t, generated.Encrypted)

	param, generated = buildGeneratedAuxDiskParameter(plan, "/dev/sde3", false, false)
	require.NotNil(t, param)
	require.NotNil(t, generated)
	assert.False(t, param.Encrypt)
	assert.False(t, param.Wipe)
	assert.False(t, generated.Encrypted)

	param, generated = buildGeneratedAuxDiskParameter(nil, "/dev/sde3", true, true)
	assert.Nil(t, param)
	assert.Nil(t, generated)
}

func TestWriteAndReadGeneratedAuxManifest(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()

	osdDataPath := "/var/snap/microceph/common/data/osd/ceph-1"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))

	manifest := &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/disk/by-id/wal", Partition: 1, PartitionPath: "/dev/sde1", Encrypted: false},
		DB:  &generatedAuxDevice{ParentPath: "/dev/disk/by-id/db", Partition: 2, PartitionPath: "/dev/sdf2", Encrypted: true},
	}

	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, manifest))

	loaded, err := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, manifest, loaded)

	require.NoError(t, mgr.persistGeneratedAuxManifest(osdDataPath, &generatedAuxDevicesManifest{DB: manifest.DB}))
	loaded, err = mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Nil(t, loaded.WAL)
	assert.Equal(t, manifest.DB, loaded.DB)

	require.NoError(t, mgr.persistGeneratedAuxManifest(osdDataPath, &generatedAuxDevicesManifest{}))
	loaded, err = mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestCleanupGeneratedAuxDevicesMissingManifest(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	require.NoError(t, mgr.fs.MkdirAll("/var/snap/microceph/common/data/osd/ceph-9", 0755))

	err := mgr.cleanupGeneratedAuxDevices(context.Background(), "/var/snap/microceph/common/data/osd/ceph-9", 9)
	require.NoError(t, err)
}

func TestCreatePlannedAuxPartitionResetBeforeUseWipesCarrierFirst(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	runner := mocks.NewRunner(t)
	mgr.runner = runner

	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("part"), 0644))

	plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/sde", Partition: 1, SizeBytes: 1024 * 1024 * 1024, ResetBeforeUse: true}
	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sde", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-u", "/dev/sde").Return("", nil).Twice()
	runner.On("RunCommand", "sh", "-c", "printf 'label: gpt\n' | sfdisk \"/dev/sde\"").Return("", nil).Once()
	runner.On("RunCommand", "sh", "-c", "printf ',+1024MiB\n' | sfdisk --append \"/dev/sde\"").Return("", nil).Once()

	path, err := mgr.createPlannedAuxPartition(plan)
	require.NoError(t, err)
	assert.Equal(t, "/dev/sde1", path)
}

func TestCleanupGeneratedAuxDevicesDeletesGeneratedPartitions(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	runner := mocks.NewRunner(t)
	mgr.runner = runner

	osdDataPath := "/var/snap/microceph/common/data/osd/ceph-3"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev/disk/by-id", 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev/mapper", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("wal"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sdf2", []byte("db"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/disk/by-id/wal-part1", []byte("wal-part"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/disk/by-id/db-part2", []byte("db-part"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/mapper/luksosd.db-3", []byte("mapper"), 0644))

	manifest := &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/disk/by-id/wal", Partition: 1, PartitionPath: "/dev/sde1", Encrypted: false},
		DB:  &generatedAuxDevice{ParentPath: "/dev/disk/by-id/db", Partition: 2, PartitionPath: "/dev/sdf2", Encrypted: true},
	}
	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, manifest))

	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sde1", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/wal", "1").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-d", "--nr", "1:1", "/dev/disk/by-id/wal").Return("", nil).Once()
	runner.On("RunCommand", "cryptsetup", "close", "luksosd.db-3").Return("", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sdf2", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/db", "2").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-d", "--nr", "2:2", "/dev/disk/by-id/db").Return("", nil).Once()

	err := mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 3)
	require.NoError(t, err)
	exists, statErr := afero.Exists(mgr.fs, generatedAuxManifestPath(osdDataPath))
	require.NoError(t, statErr)
	assert.False(t, exists)
}

func TestCleanupGeneratedAuxDevicesAfterRestartUsesPersistedManifest(t *testing.T) {
	var st state.State
	fs := afero.NewMemMapFs()
	mgr := NewOSDManager(st)
	mgr.fs = fs

	osdDataPath := "/var/snap/microceph/common/data/osd/ceph-33"
	require.NoError(t, fs.MkdirAll(osdDataPath, 0755))
	require.NoError(t, fs.MkdirAll("/dev/disk/by-id", 0755))
	require.NoError(t, afero.WriteFile(fs, "/dev/disk/by-id/wal-part1", []byte("wal-part"), 0644))
	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/disk/by-id/wal", Partition: 1, Encrypted: false},
	}))

	restartedMgr := NewOSDManager(st)
	restartedMgr.fs = fs
	runner := mocks.NewRunner(t)
	restartedMgr.runner = runner

	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/disk/by-id/wal-part1", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/wal", "1").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-d", "--nr", "1:1", "/dev/disk/by-id/wal").Return("", nil).Once()

	err := restartedMgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 33)
	require.NoError(t, err)
	exists, statErr := afero.Exists(fs, generatedAuxManifestPath(osdDataPath))
	require.NoError(t, statErr)
	assert.False(t, exists)
}

func TestCleanupGeneratedAuxDevicesFailurePreservesManifest(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	runner := mocks.NewRunner(t)
	mgr.runner = runner

	osdDataPath := "/var/snap/microceph/common/data/osd/ceph-4"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev/disk/by-id", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("wal"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/disk/by-id/wal-part1", []byte("wal-part"), 0644))

	manifest := &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/disk/by-id/wal", Partition: 1, PartitionPath: "/dev/sde1", Encrypted: false},
	}
	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, manifest))

	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sde1", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/wal", "1").Return("", assert.AnError).Once()

	err := mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 4)
	require.Error(t, err)
	exists, statErr := afero.Exists(mgr.fs, generatedAuxManifestPath(osdDataPath))
	require.NoError(t, statErr)
	assert.True(t, exists)
	loaded, readErr := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, readErr)
	require.NotNil(t, loaded)
	assert.NotNil(t, loaded.WAL)
}

func TestCleanupGeneratedAuxDevicesRetryResumesFromRemainingEntry(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	runner := mocks.NewRunner(t)
	mgr.runner = runner

	osdDataPath := "/var/snap/microceph/common/data/osd/ceph-5"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev/disk/by-id", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("wal"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sdf2", []byte("db"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/disk/by-id/wal-part1", []byte("wal-part"), 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/disk/by-id/db-part2", []byte("db-part"), 0644))

	manifest := &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/disk/by-id/wal", Partition: 1, PartitionPath: "/dev/sde1", Encrypted: false},
		DB:  &generatedAuxDevice{ParentPath: "/dev/disk/by-id/db", Partition: 2, PartitionPath: "/dev/sdf2", Encrypted: false},
	}
	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, manifest))

	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sde1", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/wal", "1").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-d", "--nr", "1:1", "/dev/disk/by-id/wal").Return("", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sdf2", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/db", "2").Return("", assert.AnError).Once()

	err := mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 5)
	require.Error(t, err)
	loaded, readErr := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, readErr)
	require.NotNil(t, loaded)
	assert.Nil(t, loaded.WAL)
	require.NotNil(t, loaded.DB)

	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sdf2", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-id/db", "2").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-d", "--nr", "2:2", "/dev/disk/by-id/db").Return("", nil).Once()

	err = mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 5)
	require.NoError(t, err)
	exists, statErr := afero.Exists(mgr.fs, generatedAuxManifestPath(osdDataPath))
	require.NoError(t, statErr)
	assert.False(t, exists)
}

func TestExecuteDSLProvisionPlanCleansCreatedAuxOnPartitionCreationFailure(t *testing.T) {
	storage := &api.ResourcesStorage{Disks: []api.ResourcesStorageDisk{
		makeTestDisk("osd1", "virtio-pci-0000:01:00.0", 10),
		makeTestDisk("wal1", "virtio-pci-0000:03:00.0", 20),
		makeTestDisk("db1", "virtio-pci-0000:05:00.0", 30),
	}}
	mgr, _ := newDryRunManager(t, storage)
	mgr.fs = afero.NewMemMapFs()
	runner := mocks.NewRunner(t)
	mgr.runner = runner

	originalCreatePlannedAuxPartitionFn := createPlannedAuxPartitionFn
	t.Cleanup(func() {
		createPlannedAuxPartitionFn = originalCreatePlannedAuxPartitionFn
	})

	require.NoError(t, mgr.fs.MkdirAll("/dev/disk/by-path", 0755))
	walPartitionPath := "/dev/disk/by-path/virtio-pci-0000:03:00.0-part1"
	require.NoError(t, afero.WriteFile(mgr.fs, walPartitionPath, []byte("wal"), 0644))

	createCalls := 0
	createPlannedAuxPartitionFn = func(m *OSDManager, plan *plannedAuxPartition) (string, error) {
		createCalls++
		if createCalls == 1 {
			return walPartitionPath, nil
		}
		return "", assert.AnError
	}

	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", walPartitionPath, "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sfdisk", "--delete", "/dev/disk/by-path/virtio-pci-0000:03:00.0", "1").Return("", nil).Once()
	runner.On("RunCommand", "partx", "-d", "--nr", "1:1", "/dev/disk/by-path/virtio-pci-0000:03:00.0").Return("", nil).Once()

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch: "eq(@size, 10GiB)",
		WALMatch: "eq(@size, 20GiB)",
		WALSize:  "1GiB",
		DBMatch:  "eq(@size, 30GiB)",
		DBSize:   "2GiB",
	})

	require.Len(t, resp.Reports, 1)
	assert.Equal(t, "Failure", resp.Reports[0].Report)
	assert.Contains(t, resp.Reports[0].Error, assert.AnError.Error())
	assert.Empty(t, resp.Warnings)
}

func TestResolvePartitionStablePathFallsBackToRawDeviceWhenStorageUnavailable(t *testing.T) {
	var st state.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	mgr.storage = staticStorage{err: assert.AnError}

	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("part"), 0644))

	path, err := mgr.resolvePartitionStablePath("/dev/sde", 1)
	require.NoError(t, err)
	assert.Equal(t, "/dev/sde1", path)
}

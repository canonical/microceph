package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
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
	var st mcTypes.State
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
	require.NotNil(t, capturedManifest.DB)
	assert.False(t, capturedManifest.DB.Encrypted)
}

func TestWriteAndReadGeneratedAuxManifest(t *testing.T) {
	var st mcTypes.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()

	osdDataPath := "/var/snap/microceph/common/data/osd/ceph-1"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))

	manifest := &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/disk/by-id/wal", Partition: 1, PartUUID: "wal-uuid", Encrypted: false},
		DB:  &generatedAuxDevice{ParentPath: "/dev/disk/by-id/db", Partition: 2, PartUUID: "db-uuid", Encrypted: true},
	}

	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, manifest))

	loaded, err := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, manifest, loaded)
	assert.Equal(t, generatedAuxManifestVersion, loaded.Version)

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

func TestMigrateLegacyGeneratedAuxManifestBeforeCleanup(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	mgr.fs = afero.NewOsFs()
	root := t.TempDir()
	osdDataPath := filepath.Join(root, "osd")
	require.NoError(t, os.Mkdir(osdDataPath, 0700))
	parent := filepath.Join(root, "carrier")
	child := parent + "1"
	require.NoError(t, os.WriteFile(parent, nil, 0600))
	require.NoError(t, os.WriteFile(child, nil, 0600))
	require.NoError(t, os.Symlink(child, filepath.Join(osdDataPath, "block.wal")))
	require.NoError(t, os.WriteFile(filepath.Join(osdDataPath, "fsid"), []byte("osd-identity\n"), 0600))
	legacy := fmt.Sprintf(`{"wal":{"parent_path":%q,"partition":1,"partition_path":"/dev/old-node"}}`, parent)
	require.NoError(t, os.WriteFile(generatedAuxManifestPath(osdDataPath), []byte(legacy), 0600))
	expectAuxTable(runner, parent, 1, "owned-uuid")
	runner.On("RunCommand", "lsblk", "-no", "PARTUUID", child).Return("owned-uuid\n", nil).Once()
	runner.On("RunCommand", "ceph-bluestore-tool", "show-label", "--dev", filepath.Join(osdDataPath, "block.wal")).Return(fmt.Sprintf(`{%q:{"osd_uuid":"osd-identity"}}`, filepath.Join(osdDataPath, "block.wal")), nil).Once()

	require.NoError(t, mgr.migrateGeneratedAuxManifest(osdDataPath))
	manifest, err := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	require.Equal(t, generatedAuxManifestVersion, manifest.Version)
	require.Equal(t, "owned-uuid", manifest.WAL.PartUUID)
	assert.NoFileExists(t, generatedAuxManifestPath(osdDataPath)+".tmp")
	expectAuxTable(runner, parent, 1, "owned-uuid")
	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", child, "--yes-i-really-really-mean-it").Return("", nil).Once()
	expectAuxDelete(runner, parent, 1)
	require.NoError(t, mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 5))
	assert.NoFileExists(t, generatedAuxManifestPath(osdDataPath))
}

func TestMigrateEncryptedLegacyAuxUsesUnderlyingPartitionAndMapperLabel(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	mgr.fs = afero.NewOsFs()
	root := t.TempDir()
	osdDataPath := filepath.Join(root, "osd")
	require.NoError(t, os.Mkdir(osdDataPath, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(osdDataPath, "fsid"), []byte("encrypted-osd"), 0600))
	parent := filepath.Join(root, "carrier")
	child := parent + "1"
	mapper := filepath.Join(root, "mapper")
	require.NoError(t, os.WriteFile(parent, nil, 0600))
	require.NoError(t, os.WriteFile(child, nil, 0600))
	require.NoError(t, os.WriteFile(mapper, nil, 0600))
	require.NoError(t, os.Symlink(child, filepath.Join(osdDataPath, "unencrypted.db")))
	require.NoError(t, os.Symlink(mapper, filepath.Join(osdDataPath, "block.db")))
	legacy := fmt.Sprintf(`{"db":{"parent_path":%q,"partition":1,"encrypted":true}}`, parent)
	require.NoError(t, os.WriteFile(generatedAuxManifestPath(osdDataPath), []byte(legacy), 0600))
	expectAuxTable(runner, parent, 1, "db-uuid")
	runner.On("RunCommand", "lsblk", "-no", "PARTUUID", child).Return("db-uuid", nil).Once()
	runner.On("RunCommand", "ceph-bluestore-tool", "show-label", "--dev", filepath.Join(osdDataPath, "block.db")).Return(`{"mapper":{"osd_uuid":"encrypted-osd"}}`, nil).Once()

	require.NoError(t, mgr.migrateGeneratedAuxManifest(osdDataPath))
	manifest, err := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	assert.Equal(t, "db-uuid", manifest.DB.PartUUID)
	assert.Equal(t, generatedAuxManifestVersion, manifest.Version)
}

func TestMigrateLegacyGeneratedAuxManifestRejectsUnverifiedOwnership(t *testing.T) {
	for _, tc := range []struct {
		name       string
		linkTarget string
		kernelUUID string
		labelUUID  string
	}{
		{name: "missing OSD link"},
		{name: "reassigned OSD link", linkTarget: "other"},
		{name: "kernel GPT disagreement", linkTarget: "child", kernelUUID: "replacement"},
		{name: "reused slot with another OSD label", linkTarget: "child", kernelUUID: "owned-uuid", labelUUID: "another-osd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runner := newAuxTestManager(t)
			mgr.fs = afero.NewOsFs()
			root := t.TempDir()
			osdDataPath := filepath.Join(root, "osd")
			require.NoError(t, os.Mkdir(osdDataPath, 0700))
			parent := filepath.Join(root, "carrier")
			require.NoError(t, os.WriteFile(filepath.Join(osdDataPath, "fsid"), []byte("osd-identity\n"), 0600))
			child := parent + "1"
			require.NoError(t, os.WriteFile(parent, nil, 0600))
			require.NoError(t, os.WriteFile(child, nil, 0600))
			if tc.linkTarget != "" {
				target := child
				if tc.linkTarget == "other" {
					target = filepath.Join(root, "other")
					require.NoError(t, os.WriteFile(target, nil, 0600))
				}
				require.NoError(t, os.Symlink(target, filepath.Join(osdDataPath, "block.wal")))
			}
			legacy := fmt.Sprintf(`{"wal":{"parent_path":%q,"partition":1,"partition_path":"/dev/old-node"}}`, parent)
			require.NoError(t, os.WriteFile(generatedAuxManifestPath(osdDataPath), []byte(legacy), 0600))
			expectAuxTable(runner, parent, 1, "owned-uuid")
			if tc.kernelUUID != "" {
				runner.On("RunCommand", "lsblk", "-no", "PARTUUID", child).Return(tc.kernelUUID, nil).Once()
			}
			if tc.labelUUID != "" {
				runner.On("RunCommand", "ceph-bluestore-tool", "show-label", "--dev", filepath.Join(osdDataPath, "block.wal")).Return(fmt.Sprintf(`{%q:{"osd_uuid":%q}}`, filepath.Join(osdDataPath, "block.wal"), tc.labelUUID), nil).Once()
			}

			err := mgr.migrateGeneratedAuxManifest(osdDataPath)
			require.ErrorContains(t, err, "manual cleanup required")
			data, err := os.ReadFile(generatedAuxManifestPath(osdDataPath))
			require.NoError(t, err)
			assert.Equal(t, legacy, string(data))
		})
	}
}

func TestMigrateLegacyManifestAllowsAbsentPartitions(t *testing.T) {
	const parent = "/dev/sde"
	for _, tc := range []struct {
		name       string
		missing    bool
		table      string
		readErr    error
		cleanupErr error
	}{
		{name: "missing carrier", missing: true},
		{name: "wiped table", readErr: fmt.Errorf("sfdisk: does not contain a recognized partition table")},
		{name: "missing slot", table: auxTableJSON(parent, 2, "sibling")},
		{name: "busy stale kernel entry", table: auxTableJSON(parent, 2, "sibling"), cleanupErr: assert.AnError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runner := newAuxTestManager(t)
			require.NoError(t, mgr.fs.MkdirAll("/osd", 0700))
			require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
			if !tc.missing {
				require.NoError(t, afero.WriteFile(mgr.fs, parent, nil, 0600))
				runner.On("RunCommand", "env", "LC_ALL=C", "sfdisk", "--json", parent).Return(tc.table, tc.readErr).Twice()
				attempts := 1
				if tc.cleanupErr != nil {
					attempts = kernelPartitionDeleteAttempts
				}
				runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", parent).Return("", tc.cleanupErr).Times(attempts)
			}
			// A cached node does not authorize a zap, even after migration.
			require.NoError(t, afero.WriteFile(mgr.fs, parent+"1", []byte("unverified data"), 0600))
			legacy := fmt.Sprintf(`{"wal":{"parent_path":%q,"partition":1,"partition_path":%q}}`, parent, parent+"1")
			require.NoError(t, afero.WriteFile(mgr.fs, generatedAuxManifestPath("/osd"), []byte(legacy), 0600))

			require.NoError(t, mgr.migrateGeneratedAuxManifest("/osd"))
			err := mgr.cleanupGeneratedAuxDevices(context.Background(), "/osd", 3)
			if tc.cleanupErr != nil {
				require.ErrorIs(t, err, tc.cleanupErr)
			} else {
				require.NoError(t, err)
			}
			manifest, err := mgr.readGeneratedAuxManifest("/osd")
			require.NoError(t, err)
			if tc.cleanupErr != nil {
				require.NotNil(t, manifest)
				require.NotNil(t, manifest.WAL)
				assert.Equal(t, parent, manifest.WAL.ParentPath)
				assert.Equal(t, uint64(1), manifest.WAL.Partition)
				assert.Empty(t, manifest.WAL.PartUUID)
			} else {
				assert.Nil(t, manifest)
			}
			data, err := afero.ReadFile(mgr.fs, parent+"1")
			require.NoError(t, err)
			assert.Equal(t, "unverified data", string(data))
		})
	}
}

func TestCleanupLegacyManifestRejectsSlotReusedAfterMigration(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	require.NoError(t, mgr.fs.MkdirAll("/osd", 0700))
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde", nil, 0600))
	legacy := `{"wal":{"parent_path":"/dev/sde","partition":1}}`
	require.NoError(t, afero.WriteFile(mgr.fs, generatedAuxManifestPath("/osd"), []byte(legacy), 0600))
	expectAuxTable(runner, "/dev/sde", 2, "sibling")
	require.NoError(t, mgr.migrateGeneratedAuxManifest("/osd"))
	before, err := mgr.readGeneratedAuxManifest("/osd")
	require.NoError(t, err)

	// Cleanup must recheck absence, not trust the preflight snapshot.
	expectAuxTable(runner, "/dev/sde", 1, "replacement")
	err = mgr.cleanupGeneratedAuxDevices(context.Background(), "/osd", 3)
	require.ErrorContains(t, err, "no verified PARTUUID")
	after, err := mgr.readGeneratedAuxManifest("/osd")
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestReadGeneratedAuxManifestRejectsUnknownVersion(t *testing.T) {
	mgr, _ := newAuxTestManager(t)
	require.NoError(t, mgr.fs.MkdirAll("/osd", 0700))
	require.NoError(t, afero.WriteFile(mgr.fs, generatedAuxManifestPath("/osd"), []byte(`{"version":2,"wal":{"parent_path":"/dev/sde","partition":1}}`), 0600))
	_, err := mgr.readGeneratedAuxManifest("/osd")
	require.ErrorContains(t, err, "unsupported generated aux manifest version 2")
}

func TestCleanupGeneratedAuxDevicesMissingManifest(t *testing.T) {
	var st mcTypes.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	require.NoError(t, mgr.fs.MkdirAll("/var/snap/microceph/common/data/osd/ceph-9", 0755))

	err := mgr.cleanupGeneratedAuxDevices(context.Background(), "/var/snap/microceph/common/data/osd/ceph-9", 9)
	require.NoError(t, err)
}

func newAuxTestManager(t *testing.T) (*OSDManager, *mocks.Runner) {
	t.Helper()
	var st mcTypes.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	mgr.lockCarrierFunc = func(path string, operation func(string) error) error { return operation(path) }
	runner := mocks.NewRunner(t)
	mgr.runner = runner
	originalSleep := partxDeleteSleepFunc
	partxDeleteSleepFunc = func(time.Duration) {}
	t.Cleanup(func() { partxDeleteSleepFunc = originalSleep })
	return mgr, runner
}

func auxTableJSON(parent string, number uint64, uuid string) string {
	parts := "[]"
	if number != 0 {
		parts = fmt.Sprintf(`[{"node":%q,"uuid":%q,"start":2048,"size":2097152}]`, partitionPathFromParentPath(parent, number), uuid)
	}
	return fmt.Sprintf(`{"partitiontable":{"label":"gpt","firstlba":2048,"lastlba":41943006,"sectorsize":512,"partitions":%s}}`, parts)
}

func expectAuxTable(runner *mocks.Runner, parent string, number uint64, uuid string) *mock.Call {
	return runner.On("RunCommand", "env", "LC_ALL=C", "sfdisk", "--json", parent).Return(auxTableJSON(parent, number, uuid), nil).Once()
}

func expectAuxDelete(runner *mocks.Runner, parent string, number uint64) {
	partitionRange := fmt.Sprintf("%d:%d", number, number)
	first := runner.On("RunCommand", "partx", "-v", "-d", "--nr", partitionRange, parent).Return("", nil).Once()
	gpt := runner.On("RunCommand", "sfdisk", "--delete", "--lock=no", "--no-reread", "--no-tell-kernel", parent, fmt.Sprint(number)).Return("", nil).Once().NotBefore(first)
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", partitionRange, parent).Return("", nil).Once().NotBefore(gpt)
}

func TestCreatePlannedAuxPartitionResetBeforeUseWipesCarrierFirst(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("part"), 0644))
	plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/sde", Partition: 1, SizeBytes: 1024 * 1024 * 1024, ResetBeforeUse: true}
	expectAuxTable(runner, "/dev/sde", 2, "foreign")
	wipe := runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sde", "--yes-i-really-really-mean-it").Return("", nil).Once()
	runner.On("RunCommand", "sh", "-c", "printf 'label: gpt\n' | sfdisk --lock=no \"/dev/sde\"").Return("", nil).Once().NotBefore(wipe)
	expectAuxTable(runner, "/dev/sde", 0, "")
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", "/dev/sde").Return("", nil).Once()
	runner.On("RunCommand", "sh", "-c", "printf ',2097152\n' | sfdisk --lock=no --append --force --no-reread --no-tell-kernel \"/dev/sde\"").Return("", nil).Once()
	expectAuxTable(runner, "/dev/sde", 1, "new-uuid")
	runner.On("RunCommand", "partx", "-u", "--nr", "1:1", "/dev/sde").Return("", nil).Once()
	path, err := mgr.createPlannedAuxPartition(plan)
	require.NoError(t, err)
	assert.Equal(t, "/dev/sde1", path)
	assert.Equal(t, "new-uuid", plan.PartUUID)
}

func TestCleanupGeneratedAuxDevicesDeletesGeneratedPartitions(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	osdDataPath := "/osd/ceph-3"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev/disk/by-id", 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev/mapper", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/mapper/luksosd.db-3", nil, 0644))
	for _, kind := range []string{"wal", "db"} {
		parent := "/dev/disk/by-id/" + kind
		require.NoError(t, afero.WriteFile(mgr.fs, parent, nil, 0644))
		require.NoError(t, afero.WriteFile(mgr.fs, parent+"-part1", []byte("owned"), 0644))
		// This cached raw name now belongs to another disk. It must not be zapped.
		cachedPath := "/dev/old-" + kind
		require.NoError(t, afero.WriteFile(mgr.fs, cachedPath, []byte("unrelated"), 0644))
		if kind == "db" {
			runner.On("RunCommand", "cryptsetup", "close", "luksosd.db-3").Return("", nil).Once()
		}
		table := expectAuxTable(runner, parent, 1, kind+"-uuid")
		runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", parent+"-part1", "--yes-i-really-really-mean-it").Return("", nil).Once().NotBefore(table)
		expectAuxDelete(runner, parent, 1)
	}
	legacyManifest := `{"wal":{"parent_path":"/dev/disk/by-id/wal","partition":1,"partition_path":"/dev/old-wal","part_uuid":"wal-uuid"},"db":{"parent_path":"/dev/disk/by-id/db","partition":1,"partition_path":"/dev/old-db","part_uuid":"db-uuid","encrypted":true}}`
	require.NoError(t, afero.WriteFile(mgr.fs, generatedAuxManifestPath(osdDataPath), []byte(legacyManifest), 0600))
	require.NoError(t, mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 3))
	loaded, err := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestCleanupGeneratedAuxDevicesRetryResumesFromRemainingEntry(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	osdDataPath := "/osd/ceph-5"
	require.NoError(t, mgr.fs.MkdirAll(osdDataPath, 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	wal := &generatedAuxDevice{ParentPath: "/dev/sde", Partition: 1, PartUUID: "wal-uuid"}
	db := &generatedAuxDevice{ParentPath: "/dev/sdf", Partition: 1, PartUUID: "db-uuid"}
	for _, entry := range []*generatedAuxDevice{wal, db} {
		require.NoError(t, afero.WriteFile(mgr.fs, entry.ParentPath, nil, 0644))
		// No partition node exists, but its GPT entry still requires deletion.
		expectAuxTable(runner, entry.ParentPath, 1, entry.PartUUID)
	}
	require.NoError(t, mgr.writeGeneratedAuxManifest(osdDataPath, &generatedAuxDevicesManifest{WAL: wal, DB: db}))
	expectAuxDelete(runner, wal.ParentPath, 1)
	first := runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", db.ParentPath).Return("", nil).Once()
	gpt := runner.On("RunCommand", "sfdisk", "--delete", "--lock=no", "--no-reread", "--no-tell-kernel", db.ParentPath, "1").Return("", nil).Once().NotBefore(first)
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", db.ParentPath).Return("", assert.AnError).Times(kernelPartitionDeleteAttempts).NotBefore(gpt)
	require.ErrorIs(t, mgr.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 5), assert.AnError)
	loaded, err := mgr.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Nil(t, loaded.WAL)
	assert.Equal(t, db, loaded.DB)

	// Restart with only the persisted DB entry. GPT is gone: no zap or second
	// GPT deletion is permitted, only the idempotent targeted kernel cleanup.
	restarted, retryRunner := newAuxTestManager(t)
	restarted.fs = mgr.fs
	expectAuxTable(retryRunner, db.ParentPath, 0, "")
	retryRunner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", db.ParentPath).Return("", nil).Once()
	require.NoError(t, restarted.cleanupGeneratedAuxDevices(context.Background(), osdDataPath, 5))
	loaded, err = restarted.readGeneratedAuxManifest(osdDataPath)
	require.NoError(t, err)
	assert.Nil(t, loaded)
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
	mgr.lockCarrierFunc = func(path string, operation func(string) error) error { return operation(path) }

	originalCreatePlannedAuxPartitionFn := createPlannedAuxPartitionFn
	t.Cleanup(func() {
		createPlannedAuxPartitionFn = originalCreatePlannedAuxPartitionFn
	})

	require.NoError(t, mgr.fs.MkdirAll("/dev/disk/by-path", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/disk/by-path/virtio-pci-0000:03:00.0", nil, 0644))
	walPartitionPath := "/dev/disk/by-path/virtio-pci-0000:03:00.0-part1"
	require.NoError(t, afero.WriteFile(mgr.fs, walPartitionPath, []byte("wal"), 0644))
	// No OSD ID has been allocated yet. OSD 0's mapper belongs to someone else.
	require.NoError(t, mgr.fs.MkdirAll("/dev/mapper", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/mapper/luksosd.wal-0", nil, 0644))

	createCalls := 0
	createPlannedAuxPartitionFn = func(m *OSDManager, plan *plannedAuxPartition) (string, error) {
		createCalls++
		if createCalls == 1 {
			plan.PartUUID = "wal-uuid"
			return walPartitionPath, nil
		}
		return "", assert.AnError
	}

	parentPath := "/dev/disk/by-path/virtio-pci-0000:03:00.0"
	expectAuxTable(runner, parentPath, 1, "wal-uuid")
	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", walPartitionPath, "--yes-i-really-really-mean-it").Return("", nil).Once()
	expectAuxDelete(runner, parentPath, 1)

	resp := mgr.AddDisksWithDSLRequest(context.Background(), types.DisksPost{
		OSDMatch:   "eq(@size, 10GiB)",
		WALMatch:   "eq(@size, 20GiB)",
		WALSize:    "1GiB",
		WALEncrypt: true,
		DBMatch:    "eq(@size, 30GiB)",
		DBSize:     "2GiB",
	})

	require.Len(t, resp.Reports, 1)
	assert.Equal(t, "Failure", resp.Reports[0].Report)
	assert.Contains(t, resp.Reports[0].Error, assert.AnError.Error())
	assert.Empty(t, resp.Warnings)
}

func TestCleanupBusyKernelEntryPreservesManifestAndGPT(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	require.NoError(t, mgr.fs.MkdirAll("/osd", 0755))
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde", nil, 0644))
	manifest := &generatedAuxDevicesManifest{WAL: &generatedAuxDevice{ParentPath: "/dev/sde", Partition: 1, PartUUID: "owned"}}
	require.NoError(t, mgr.writeGeneratedAuxManifest("/osd", manifest))
	expectAuxTable(runner, "/dev/sde", 1, "owned")
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", "/dev/sde").Return("", assert.AnError).Times(kernelPartitionDeleteAttempts)
	// No GPT write is allowed when the first kernel deletion stays busy.
	require.ErrorIs(t, mgr.cleanupGeneratedAuxDevices(context.Background(), "/osd", 1), assert.AnError)
	loaded, err := mgr.readGeneratedAuxManifest("/osd")
	require.NoError(t, err)
	assert.Equal(t, manifest, loaded)
}

func TestResolvePartitionStablePathFallsBackToRawDeviceWhenStorageUnavailable(t *testing.T) {
	var st mcTypes.State
	mgr := NewOSDManager(st)
	mgr.fs = afero.NewMemMapFs()
	mgr.storage = staticStorage{err: assert.AnError}

	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("part"), 0644))

	path, err := mgr.resolvePartitionStablePath("/dev/sde", 1)
	require.NoError(t, err)
	assert.Equal(t, "/dev/sde1", path)
}

func TestPlanAuxPartitionsReusesHolesWithoutOverwritingSurvivors(t *testing.T) {
	disk := makeTestDiskWithPartition("wal", "wal", 20, "sde2", 2, 1)
	plans, err := planAuxiliaryPartitionsDetailed([]string{"osd-a", "osd-b"}, []api.ResourcesStorageDisk{disk}, nil, 1024*1024*1024, "wal")
	require.NoError(t, err)
	require.Len(t, plans, 2)
	assert.Equal(t, uint64(1), plans[0].Partition)
	assert.Equal(t, uint64(3), plans[1].Partition)
	assert.False(t, plans[0].InitializeGPT, "a vacant slot 1 is not an empty disk")
	assert.False(t, plans[1].InitializeGPT)
}

func TestCleanupGeneratedAuxDeviceOwnershipAndReadFailures(t *testing.T) {
	const parent = "/dev/sde"
	for _, tc := range []struct {
		name       string
		missing    bool
		disappears bool
		legacy     bool
		table      string
		readErr    error
		kernelOnly bool
		wantErr    bool
	}{
		{name: "missing carrier", missing: true},
		{name: "carrier disappears before lock", disappears: true},
		{name: "legacy identity", legacy: true, table: auxTableJSON(parent, 1, "unverified"), wantErr: true},
		{name: "reused slot", table: auxTableJSON(parent, 1, "replacement")},
		{name: "missing slot", table: auxTableJSON(parent, 2, "sibling"), kernelOnly: true},
		{name: "wiped table", readErr: fmt.Errorf("sfdisk: does not contain a recognized partition table"), kernelOnly: true},
		{name: "I/O failure", readErr: assert.AnError, wantErr: true},
		{name: "malformed table", table: "{", wantErr: true},
		{name: "incomplete table", table: "{}", wantErr: true},
		{name: "missing on-disk UUID", table: auxTableJSON(parent, 1, ""), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runner := newAuxTestManager(t)
			require.NoError(t, mgr.fs.MkdirAll("/osd", 0755))
			require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
			if !tc.missing {
				require.NoError(t, afero.WriteFile(mgr.fs, parent, nil, 0644))
			}
			require.NoError(t, afero.WriteFile(mgr.fs, parent+"1", []byte("unverified data"), 0644))
			entry := &generatedAuxDevice{ParentPath: parent, Partition: 1, PartUUID: "owned"}
			if tc.legacy {
				entry.PartUUID = ""
			}
			manifest := &generatedAuxDevicesManifest{WAL: entry}
			require.NoError(t, mgr.writeGeneratedAuxManifest("/osd", manifest))
			if tc.disappears {
				mgr.lockCarrierFunc = func(path string, operation func(string) error) error {
					require.NoError(t, mgr.fs.Remove(path))
					return os.ErrNotExist
				}
			} else if !tc.missing {
				runner.On("RunCommand", "env", "LC_ALL=C", "sfdisk", "--json", parent).Return(tc.table, tc.readErr).Once()
			}
			if tc.kernelOnly {
				runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", parent).Return("", nil).Once()
			}
			// None of these states establishes ownership of a live GPT entry:
			// no zap or GPT mutation is allowed, even when a cached node exists.
			err := mgr.cleanupGeneratedAuxDevices(context.Background(), "/osd", 1)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			loaded, err := mgr.readGeneratedAuxManifest("/osd")
			require.NoError(t, err)
			if tc.wantErr {
				assert.Equal(t, manifest, loaded)
			} else {
				assert.Nil(t, loaded)
			}
		})
	}
}

func TestDeletePartitionRetriesTransientKernelFailure(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	var pauses []time.Duration
	partxDeleteSleepFunc = func(d time.Duration) { pauses = append(pauses, d) }
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", "/dev/sde").Return("", assert.AnError).Once()
	expectAuxDelete(runner, "/dev/sde", 1)
	require.NoError(t, mgr.deletePartition("/dev/sde", 1))
	assert.Equal(t, []time.Duration{kernelPartitionDeleteBackoff}, pauses)
}

func TestCleanupZapFailureStillDeletesOwnedPartition(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde", nil, 0644))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", nil, 0644))
	expectAuxTable(runner, "/dev/sde", 1, "owned")
	runner.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sde1", "--yes-i-really-really-mean-it").Run(func(args mock.Arguments) {
		deadline, ok := args.Get(0).(context.Context).Deadline()
		require.True(t, ok, "zap must not hold the carrier lock indefinitely")
		assert.LessOrEqual(t, time.Until(deadline), 30*time.Second)
	}).Return("", assert.AnError).Once()
	expectAuxDelete(runner, "/dev/sde", 1)
	err := mgr.cleanupGeneratedAuxDevice(context.Background(), "wal", &generatedAuxDevice{ParentPath: "/dev/sde", Partition: 1, PartUUID: "owned"}, 1)
	require.NoError(t, err)
}

func TestCreatePlannedAuxPartitionFillsHoleWithoutReset(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
	require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", nil, 0644))
	plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/sde", Partition: 1, SizeBytes: 1024 * 1024 * 1024}
	expectAuxTable(runner, "/dev/sde", 2, "sibling")
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", "/dev/sde").Return("", nil).Once()
	runner.On("RunCommand", "sh", "-c", "printf ',2097152\n' | sfdisk --lock=no --append --force --no-reread --no-tell-kernel \"/dev/sde\"").Return("", nil).Once()
	expectAuxTable(runner, "/dev/sde", 1, "new-uuid")
	locked := false
	mgr.lockCarrierFunc = func(path string, operation func(string) error) error {
		locked = true
		defer func() { locked = false }()
		return operation(path)
	}
	runner.On("RunCommand", "partx", "-u", "--nr", "1:1", "/dev/sde").Run(func(mock.Arguments) {
		assert.True(t, locked, "udev must not probe a half-published partition")
	}).Return("", nil).Once()
	path, err := mgr.createPlannedAuxPartition(plan)
	require.NoError(t, err)
	assert.Equal(t, "/dev/sde1", path)
	assert.Equal(t, "new-uuid", plan.PartUUID)
}

func TestCreatePlannedAuxPartitionRejectsUnsafeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		number     uint64
		initialize bool
		size       uint64
	}{
		{name: "wrong free slot", number: 2, size: 1024 * 1024},
		{name: "live sibling during initialization", number: 1, initialize: true, size: 1024 * 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runner := newAuxTestManager(t)
			expectAuxTable(runner, "/dev/sde", 2, "sibling")
			plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/sde", Partition: tc.number, InitializeGPT: tc.initialize, SizeBytes: tc.size}
			_, err := mgr.createPlannedAuxPartition(plan)
			require.Error(t, err)
			// The strict runner allows only the table read: no wipe, init or append.
			assert.Empty(t, plan.PartUUID)
		})
	}
}

func TestExclusiveCarrierLockWaitsAndReleasesAfterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "carrier")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	holder, err := os.Open(path)
	require.NoError(t, err)
	defer holder.Close()
	require.NoError(t, unix.Flock(int(holder.Fd()), unix.LOCK_SH))
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withExclusiveCarrierLock(path, func(string) error {
			close(entered)
			return assert.AnError
		})
	}()
	select {
	case <-entered:
		t.Fatal("entered while a probe held the shared carrier lock")
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, unix.Flock(int(holder.Fd()), unix.LOCK_UN))
	select {
	case err = <-done:
		require.ErrorIs(t, err, assert.AnError)
	case <-time.After(2 * time.Second):
		t.Fatal("carrier lock was not acquired after the probe finished")
	}
	// Even a failed mutation must release the lock so udev can probe again.
	require.NoError(t, unix.Flock(int(holder.Fd()), unix.LOCK_SH|unix.LOCK_NB))
}

type failingAuxManifestFS struct {
	afero.Fs
	failWrite bool
}

// Rename fails the final swap after a successful temporary-file write.
func (fs failingAuxManifestFS) Rename(string, string) error { return assert.AnError }

// OpenFile optionally injects a partial write into the temporary manifest.
func (fs failingAuxManifestFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil || !fs.failWrite || !strings.HasSuffix(name, ".tmp") {
		return f, err
	}
	return failingAuxManifestFile{File: f}, nil
}

type failingAuxManifestFile struct{ afero.File }

// Write leaves truncated data behind before reporting the simulated I/O error.
func (f failingAuxManifestFile) Write(data []byte) (int, error) {
	n, _ := f.File.Write(data[:len(data)/2])
	return n, assert.AnError
}

func TestGeneratedAuxManifestFailedReplacementPreservesOriginal(t *testing.T) {
	for _, failWrite := range []bool{true, false} {
		t.Run(fmt.Sprintf("partial-write=%t", failWrite), func(t *testing.T) {
			mgr, _ := newAuxTestManager(t)
			require.NoError(t, mgr.fs.MkdirAll("/osd", 0755))
			manifest := &generatedAuxDevicesManifest{
				WAL: &generatedAuxDevice{ParentPath: "/dev/sde", Partition: 1, PartUUID: "wal-uuid"},
				DB:  &generatedAuxDevice{ParentPath: "/dev/sdf", Partition: 1, PartUUID: "db-uuid"},
			}
			require.NoError(t, mgr.writeGeneratedAuxManifest("/osd", manifest))
			mgr.fs = failingAuxManifestFS{Fs: mgr.fs, failWrite: failWrite}
			err := mgr.persistGeneratedAuxManifest("/osd", &generatedAuxDevicesManifest{DB: manifest.DB})
			require.ErrorIs(t, err, assert.AnError)
			loaded, err := mgr.readGeneratedAuxManifest("/osd")
			require.NoError(t, err)
			assert.Equal(t, manifest, loaded)
			_, err = mgr.fs.Stat(generatedAuxManifestPath("/osd") + ".tmp")
			assert.True(t, os.IsNotExist(err))
		})
	}
}

func TestCreatePlannedAuxPartitionRejectsBusyStaleKernelEntry(t *testing.T) {
	mgr, runner := newAuxTestManager(t)
	expectAuxTable(runner, "/dev/sde", 0, "")
	runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", "/dev/sde").Return("", assert.AnError).Times(kernelPartitionDeleteAttempts)
	plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/sde", Partition: 1, SizeBytes: 1024 * 1024 * 1024}
	path, err := mgr.createPlannedAuxPartition(plan)
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, path)
	assert.Empty(t, plan.PartUUID)
	// No append is permitted until the old kernel slot has been removed.
}

func TestCreatePlannedAuxPartitionRollsBackIncompleteCreation(t *testing.T) {
	for _, failure := range []string{"append", "readback", "missing UUID", "wrong size", "kernel update"} {
		t.Run(failure, func(t *testing.T) {
			mgr, runner := newAuxTestManager(t)
			require.NoError(t, mgr.fs.MkdirAll("/dev", 0755))
			// A stale path is deliberately present: failure must not return it.
			require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde1", []byte("old node"), 0644))
			expectAuxTable(runner, "/dev/sde", 0, "")
			runner.On("RunCommand", "partx", "-v", "-d", "--nr", "1:1", "/dev/sde").Return("", nil).Once()
			var appendErr error
			if failure == "append" {
				appendErr = assert.AnError
			}
			runner.On("RunCommand", "sh", "-c", "printf ',2097152\n' | sfdisk --lock=no --append --force --no-reread --no-tell-kernel \"/dev/sde\"").Return("", appendErr).Once()
			if failure != "append" {
				output := auxTableJSON("/dev/sde", 1, "created")
				var readErr error
				switch failure {
				case "readback":
					readErr = assert.AnError
				case "missing UUID":
					output = auxTableJSON("/dev/sde", 1, "")
				case "wrong size":
					output = strings.Replace(output, `"size":2097152`, `"size":1024`, 1)
				case "kernel update":
					runner.On("RunCommand", "partx", "-u", "--nr", "1:1", "/dev/sde").Return("", assert.AnError).Once()
				}
				runner.On("RunCommand", "env", "LC_ALL=C", "sfdisk", "--json", "/dev/sde").Return(output, readErr).Once()
				expectAuxDelete(runner, "/dev/sde", 1)
			}
			plan := &plannedAuxPartition{Kind: "wal", ParentPath: "/dev/sde", Partition: 1, SizeBytes: 1024 * 1024 * 1024}
			path, err := mgr.createPlannedAuxPartition(plan)
			require.Error(t, err)
			assert.Empty(t, path)
			assert.Empty(t, plan.PartUUID)
		})
	}
}

func TestCleanupEncryptedCarrierClosesMapperEvenWhenCarrierIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		missing    bool
		osdID      int64
		closeError error
	}{
		{name: "missing carrier", missing: true, osdID: 3},
		{name: "missing carrier for OSD zero", missing: true, osdID: 0},
		{name: "missing carrier with busy mapper", missing: true, osdID: 3, closeError: assert.AnError},
		{name: "present carrier with busy mapper", osdID: 3, closeError: assert.AnError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runner := newAuxTestManager(t)
			require.NoError(t, mgr.fs.MkdirAll("/osd", 0755))
			require.NoError(t, mgr.fs.MkdirAll("/dev/mapper", 0755))
			mapper := fmt.Sprintf("luksosd.wal-%d", tc.osdID)
			require.NoError(t, afero.WriteFile(mgr.fs, "/dev/mapper/"+mapper, nil, 0644))
			if !tc.missing {
				require.NoError(t, afero.WriteFile(mgr.fs, "/dev/sde", nil, 0644))
			}
			manifest := &generatedAuxDevicesManifest{WAL: &generatedAuxDevice{ParentPath: "/dev/sde", Partition: 1, PartUUID: "owned", Encrypted: true}}
			require.NoError(t, mgr.writeGeneratedAuxManifest("/osd", manifest))
			runner.On("RunCommand", "cryptsetup", "close", mapper).Return("", tc.closeError).Once()
			err := mgr.cleanupGeneratedAuxDevices(context.Background(), "/osd", tc.osdID)
			if tc.missing {
				require.NoError(t, err, "a missing carrier must not prevent OSD removal")
			} else {
				require.ErrorIs(t, err, tc.closeError)
			}
			loaded, err := mgr.readGeneratedAuxManifest("/osd")
			require.NoError(t, err)
			if tc.missing {
				assert.Nil(t, loaded)
			} else {
				assert.Equal(t, manifest, loaded)
			}
		})
	}
}

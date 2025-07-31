package main

import (
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

func TestFilterLocalDisks(t *testing.T) {
	// Mock functions that always return false (not mounted, not ceph device)
	mockIsMounted := func(device string) (bool, error) {
		return false, nil
	}
	mockIsCephDevice := func(device string) (bool, error) {
		return false, nil
	}

	// Test cases for doFilterLocalDisks function
	tests := []struct {
		name          string
		resources     *api.ResourcesStorage
		disks         types.Disks
		expectedCount int
		expectedPaths []string
		description   string
	}{
		{
			name: "virtio device with DevicePath only",
			resources: &api.ResourcesStorage{
				Disks: []api.ResourcesStorageDisk{
					{
						ID:         "vdc",
						DeviceID:   "", // Empty DeviceID for virtio-blk
						DevicePath: "virtio-pci-0000:06:00.0",
						Size:       16 * 1024 * 1024 * 1024, // 16GB
						Type:       "virtio",
						Model:      "QEMU HARDDISK",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
				},
			},
			disks:         types.Disks{},
			expectedCount: 1,
			expectedPaths: []string{"/dev/disk/by-path/virtio-pci-0000:06:00.0"},
			description:   "Should include virtio device using DevicePath",
		},
		{
			name: "virtio device with ID only",
			resources: &api.ResourcesStorage{
				Disks: []api.ResourcesStorageDisk{
					{
						ID:         "vdd",
						DeviceID:   "",                     // Empty DeviceID
						DevicePath: "",                     // Empty DevicePath
						Size:       8 * 1024 * 1024 * 1024, // 8GB
						Type:       "virtio",
						Model:      "QEMU HARDDISK",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
				},
			},
			disks:         types.Disks{},
			expectedCount: 1,
			expectedPaths: []string{"/dev/vdd"},
			description:   "Should include virtio device using ID fallback",
		},
		{
			name: "SATA device with DeviceID",
			resources: &api.ResourcesStorage{
				Disks: []api.ResourcesStorageDisk{
					{
						ID:         "sda",
						DeviceID:   "scsi-SATA_QEMU_HARDDISK_QM00001",
						DevicePath: "pci-0000:00:1f.2-ata-1",
						Size:       128 * 1024 * 1024 * 1024, // 128GB
						Type:       "sata",
						Model:      "QEMU HARDDISK",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
				},
			},
			disks:         types.Disks{},
			expectedCount: 1,
			expectedPaths: []string{"/dev/disk/by-id/scsi-SATA_QEMU_HARDDISK_QM00001"},
			description:   "Should include SATA device using DeviceID",
		},
		{
			name: "mixed device types",
			resources: &api.ResourcesStorage{
				Disks: []api.ResourcesStorageDisk{
					{
						ID:         "vdc",
						DeviceID:   "",
						DevicePath: "virtio-pci-0000:06:00.0",
						Size:       16 * 1024 * 1024 * 1024,
						Type:       "virtio",
						Model:      "QEMU HARDDISK",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
					{
						ID:         "sda",
						DeviceID:   "scsi-SATA_QEMU_HARDDISK_QM00001",
						DevicePath: "pci-0000:00:1f.2-ata-1",
						Size:       128 * 1024 * 1024 * 1024,
						Type:       "sata",
						Model:      "QEMU HARDDISK",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
				},
			},
			disks:         types.Disks{},
			expectedCount: 2,
			expectedPaths: []string{
				"/dev/disk/by-path/virtio-pci-0000:06:00.0",
				"/dev/disk/by-id/scsi-SATA_QEMU_HARDDISK_QM00001",
			},
			description: "Should include both virtio and SATA devices with appropriate paths",
		},
		{
			name: "filter out partitioned disks",
			resources: &api.ResourcesStorage{
				Disks: []api.ResourcesStorageDisk{
					{
						ID:       "vda",
						DeviceID: "",
						Size:     8 * 1024 * 1024 * 1024,
						Type:     "virtio",
						Partitions: []api.ResourcesStorageDiskPartition{
							{ID: "vda1", Size: 7 * 1024 * 1024 * 1024},
						},
					},
					{
						ID:         "vdc",
						DeviceID:   "",
						DevicePath: "virtio-pci-0000:06:00.0",
						Size:       16 * 1024 * 1024 * 1024,
						Type:       "virtio",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
				},
			},
			disks:         types.Disks{},
			expectedCount: 1,
			expectedPaths: []string{"/dev/disk/by-path/virtio-pci-0000:06:00.0"},
			description:   "Should filter out partitioned disks",
		},
		{
			name: "filter out small disks",
			resources: &api.ResourcesStorage{
				Disks: []api.ResourcesStorageDisk{
					{
						ID:         "vdc",
						DeviceID:   "",
						DevicePath: "virtio-pci-0000:06:00.0",
						Size:       1 * 1024 * 1024 * 1024, // too small
						Type:       "virtio",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
					{
						ID:         "vdd",
						DeviceID:   "",
						DevicePath: "virtio-pci-0000:07:00.0",
						Size:       4 * 1024 * 1024 * 1024, // large enough
						Type:       "virtio",
						Partitions: []api.ResourcesStorageDiskPartition{},
					},
				},
			},
			disks:         types.Disks{},
			expectedCount: 1,
			expectedPaths: []string{"/dev/disk/by-path/virtio-pci-0000:07:00.0"},
			description:   "Should filter out disks smaller than 2GB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := doFilterLocalDisks(tt.resources, tt.disks, mockIsMounted, mockIsCephDevice)
			require.NoError(t, err, tt.description)

			assert.Equal(t, tt.expectedCount, len(result), tt.description)

			if tt.expectedCount > 0 {
				actualPaths := make([]string, len(result))
				for i, disk := range result {
					actualPaths[i] = disk.Path
				}
				assert.ElementsMatch(t, tt.expectedPaths, actualPaths, tt.description)
			}
		})
	}
}

func TestFilterLocalDisksPathPriority(t *testing.T) {
	// Mock functions that always return false (not mounted, not ceph device)
	mockIsMounted := func(device string) (bool, error) {
		return false, nil
	}
	mockIsCephDevice := func(device string) (bool, error) {
		return false, nil
	}

	// Test that DeviceID takes priority over DevicePath
	resources := &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{
			{
				ID:         "nvme0n1",
				DeviceID:   "nvme-eui.0000000001000000e4d25cafae2e4c00",
				DevicePath: "pci-0000:05:00.0-nvme-1",
				Size:       256 * 1024 * 1024 * 1024, // 256GB
				Type:       "nvme",
				Model:      "INTEL SSDPEKKW256G7",
				Partitions: []api.ResourcesStorageDiskPartition{},
			},
		},
	}

	result, err := doFilterLocalDisks(resources, types.Disks{}, mockIsMounted, mockIsCephDevice)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Should use DeviceID path not by-path
	expectedPath := "/dev/disk/by-id/nvme-eui.0000000001000000e4d25cafae2e4c00"
	assert.Equal(t, expectedPath, result[0].Path)
}

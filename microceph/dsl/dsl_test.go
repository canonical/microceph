package dsl

import (
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid expression",
			input:   "eq(@type, 'nvme')",
			wantErr: false,
		},
		{
			name:    "invalid expression",
			input:   "eq(@type, 'nvme'",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "known function and variable",
			input:   "eq(@type, 'nvme')",
			wantErr: false,
		},
		{
			name:    "unknown function",
			input:   "unknown(@type)",
			wantErr: true,
		},
		{
			name:    "unknown variable",
			input:   "eq(@unknown, 'nvme')",
			wantErr: true,
		},
		{
			name:    "nested unknown variable",
			input:   "and(eq(@type, 'nvme'), eq(@invalid, 'test'))",
			wantErr: true,
		},
		{
			name:    "all known functions",
			input:   "and(or(not(eq(@type, 'nvme')), ne(@size, 100)), in(@vendor, 'a', 'b'), re('pat', @model), gt(@size, 1), ge(@size, 1), lt(@size, 1), le(@size, 1))",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			err = Validate(expr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMatchDevice(t *testing.T) {
	disk := api.ResourcesStorageDisk{
		ID:       "nvme0n1",
		DeviceID: "nvme-Samsung_970_EVO",
		Model:    "Samsung 970 EVO",
		Size:     256 * uint64(GiB),
		Type:     "nvme",
	}

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "matching type",
			input:    "eq(@type, 'nvme')",
			expected: true,
		},
		{
			name:     "non-matching type",
			input:    "eq(@type, 'sata')",
			expected: false,
		},
		{
			name:     "size comparison",
			input:    "gt(@size, 100GiB)",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			result, err := MatchDevice(expr, disk, "node-01")
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchDevices(t *testing.T) {
	disks := []api.ResourcesStorageDisk{
		{
			ID:       "nvme0n1",
			DeviceID: "nvme-Samsung_970_EVO",
			Model:    "Samsung 970 EVO",
			Size:     256 * uint64(GiB),
			Type:     "nvme",
		},
		{
			ID:       "sda",
			DeviceID: "scsi-SATA_WDC_WD10EZEX",
			Model:    "WDC WD10EZEX",
			Size:     1 * uint64(TiB),
			Type:     "sata",
		},
		{
			ID:       "sdb",
			DeviceID: "scsi-SATA_Seagate_ST2000",
			Model:    "Seagate ST2000DM008",
			Size:     2 * uint64(TiB),
			Type:     "sata",
		},
	}

	tests := []struct {
		name          string
		input         string
		expectedCount int
	}{
		{
			name:          "match nvme only",
			input:         "eq(@type, 'nvme')",
			expectedCount: 1,
		},
		{
			name:          "match sata only",
			input:         "eq(@type, 'sata')",
			expectedCount: 2,
		},
		{
			name:          "match all",
			input:         "or(eq(@type, 'nvme'), eq(@type, 'sata'))",
			expectedCount: 3,
		},
		{
			name:          "match none",
			input:         "eq(@type, 'virtio')",
			expectedCount: 0,
		},
		{
			name:          "match by size",
			input:         "ge(@size, 1TiB)",
			expectedCount: 2,
		},
		{
			name:          "match by vendor",
			input:         "re('seagate', @vendor)",
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			matched, err := MatchDevices(expr, disks, "node-01")
			require.NoError(t, err)
			assert.Len(t, matched, tt.expectedCount)
		})
	}
}

func TestGetDevicePath(t *testing.T) {
	tests := []struct {
		name     string
		disk     api.ResourcesStorageDisk
		expected string
	}{
		{
			name: "with DeviceID",
			disk: api.ResourcesStorageDisk{
				ID:       "nvme0n1",
				DeviceID: "nvme-Samsung_970_EVO",
			},
			expected: "/dev/disk/by-id/nvme-Samsung_970_EVO",
		},
		{
			name: "with DevicePath only",
			disk: api.ResourcesStorageDisk{
				ID:         "vdc",
				DevicePath: "pci-0000:00:1f.2-virtio-1",
			},
			expected: "/dev/disk/by-path/pci-0000:00:1f.2-virtio-1",
		},
		{
			name: "fallback to ID",
			disk: api.ResourcesStorageDisk{
				ID: "vdc",
			},
			expected: "/dev/vdc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := GetDevicePath(tt.disk)
			assert.Equal(t, tt.expected, path)
		})
	}
}

func TestKnownFunctions(t *testing.T) {
	funcs := KnownFunctions()
	assert.Contains(t, funcs, "and")
	assert.Contains(t, funcs, "or")
	assert.Contains(t, funcs, "not")
	assert.Contains(t, funcs, "in")
	assert.Contains(t, funcs, "re")
	assert.Contains(t, funcs, "eq")
	assert.Contains(t, funcs, "ne")
	assert.Contains(t, funcs, "gt")
	assert.Contains(t, funcs, "ge")
	assert.Contains(t, funcs, "lt")
	assert.Contains(t, funcs, "le")
}

func TestKnownVariables(t *testing.T) {
	vars := KnownVariables()
	assert.Contains(t, vars, "type")
	assert.Contains(t, vars, "vendor")
	assert.Contains(t, vars, "model")
	assert.Contains(t, vars, "size")
	assert.Contains(t, vars, "devnode")
	assert.Contains(t, vars, "host")
}

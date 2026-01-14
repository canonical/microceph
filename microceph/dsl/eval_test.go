package dsl

import (
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestDisk() api.ResourcesStorageDisk {
	return api.ResourcesStorageDisk{
		ID:         "nvme0n1",
		DeviceID:   "nvme-Samsung_970_EVO_Plus_S4EVNJ0N123456",
		DevicePath: "pci-0000:00:1f.2-nvme-1",
		Model:      "Samsung 970 EVO Plus 500GB",
		Size:       500 * uint64(GB),
		Type:       "nvme",
	}
}

func TestEvaluatorEquality(t *testing.T) {
	disk := createTestDisk()
	ctx := NewDeviceContext(disk, "node-01")

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "type equals nvme",
			input:    "eq(@type, 'nvme')",
			expected: true,
		},
		{
			name:     "type equals sata (false)",
			input:    "eq(@type, 'sata')",
			expected: false,
		},
		{
			name:     "type not equals sata",
			input:    "ne(@type, 'sata')",
			expected: true,
		},
		{
			name:     "host equals node-01",
			input:    "eq(@host, 'node-01')",
			expected: true,
		},
		{
			name:     "case insensitive comparison",
			input:    "eq(@type, 'NVME')",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			eval := NewEvaluator(ctx)
			result, err := eval.Eval(expr)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Bool())
		})
	}
}

func TestEvaluatorComparisons(t *testing.T) {
	disk := createTestDisk()
	ctx := NewDeviceContext(disk, "node-01")

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "size greater than 100GiB",
			input:    "gt(@size, 100GiB)",
			expected: true,
		},
		{
			name:     "size greater than 1TiB (false)",
			input:    "gt(@size, 1TiB)",
			expected: false,
		},
		{
			name:     "size less than 1TiB",
			input:    "lt(@size, 1TiB)",
			expected: true,
		},
		{
			name:     "size greater or equal",
			input:    "ge(@size, 500GB)",
			expected: true,
		},
		{
			name:     "size less or equal",
			input:    "le(@size, 500GB)",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			eval := NewEvaluator(ctx)
			result, err := eval.Eval(expr)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Bool())
		})
	}
}

func TestEvaluatorLogicalOperators(t *testing.T) {
	disk := createTestDisk()
	ctx := NewDeviceContext(disk, "node-01")

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "and with all true",
			input:    "and(eq(@type, 'nvme'), gt(@size, 100GiB))",
			expected: true,
		},
		{
			name:     "and with one false",
			input:    "and(eq(@type, 'nvme'), eq(@type, 'sata'))",
			expected: false,
		},
		{
			name:     "or with one true",
			input:    "or(eq(@type, 'sata'), eq(@type, 'nvme'))",
			expected: true,
		},
		{
			name:     "or with all false",
			input:    "or(eq(@type, 'sata'), eq(@type, 'hdd'))",
			expected: false,
		},
		{
			name:     "not true becomes false",
			input:    "not(eq(@type, 'nvme'))",
			expected: false,
		},
		{
			name:     "not false becomes true",
			input:    "not(eq(@type, 'sata'))",
			expected: true,
		},
		{
			name:     "empty and is true",
			input:    "and()",
			expected: true,
		},
		{
			name:     "empty or is false",
			input:    "or()",
			expected: false,
		},
		{
			name:     "variadic and",
			input:    "and(eq(@type, 'nvme'), gt(@size, 100GiB), eq(@host, 'node-01'))",
			expected: true,
		},
		{
			name:     "variadic or",
			input:    "or(eq(@type, 'sata'), eq(@type, 'hdd'), eq(@type, 'nvme'))",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			eval := NewEvaluator(ctx)
			result, err := eval.Eval(expr)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Bool())
		})
	}
}

func TestEvaluatorIn(t *testing.T) {
	disk := createTestDisk()
	ctx := NewDeviceContext(disk, "node-01")

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "in with match",
			input:    "in(@type, 'sata', 'nvme', 'ssd')",
			expected: true,
		},
		{
			name:     "in without match",
			input:    "in(@type, 'sata', 'hdd', 'virtio')",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			eval := NewEvaluator(ctx)
			result, err := eval.Eval(expr)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Bool())
		})
	}
}

func TestEvaluatorRegex(t *testing.T) {
	disk := createTestDisk()
	ctx := NewDeviceContext(disk, "node-01")

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "regex match vendor",
			input:    "re('samsung', @model)",
			expected: true,
		},
		{
			name:     "regex no match",
			input:    "re('seagate', @model)",
			expected: false,
		},
		{
			name:     "regex devnode pattern",
			input:    "re('^/dev/disk/by-id/nvme', @devnode)",
			expected: true,
		},
		{
			name:     "regex host pattern",
			input:    "re('^node-', @host)",
			expected: true,
		},
		{
			name:     "regex case insensitive",
			input:    "re('SAMSUNG', @model)",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			eval := NewEvaluator(ctx)
			result, err := eval.Eval(expr)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Bool())
		})
	}
}

func TestEvaluatorVariables(t *testing.T) {
	disk := api.ResourcesStorageDisk{
		ID:         "sda",
		DeviceID:   "scsi-SATA_QEMU_HARDDISK_QM00001",
		DevicePath: "pci-0000:00:1f.2-ata-1",
		Model:      "QEMU HARDDISK",
		Size:       100 * uint64(GiB),
		Type:       "sata",
	}
	ctx := NewDeviceContext(disk, "compute-01")

	// Test @type
	expr, _ := Parse("eq(@type, 'sata')")
	eval := NewEvaluator(ctx)
	result, err := eval.Eval(expr)
	require.NoError(t, err)
	assert.True(t, result.Bool())

	// Test @vendor (extracted from model)
	expr, _ = Parse("eq(@vendor, 'qemu')")
	result, err = eval.Eval(expr)
	require.NoError(t, err)
	assert.True(t, result.Bool())

	// Test @model (lowercased)
	expr, _ = Parse("re('harddisk', @model)")
	result, err = eval.Eval(expr)
	require.NoError(t, err)
	assert.True(t, result.Bool())

	// Test @host
	expr, _ = Parse("re('^compute-', @host)")
	result, err = eval.Eval(expr)
	require.NoError(t, err)
	assert.True(t, result.Bool())

	// Test @devnode
	expr, _ = Parse("re('^/dev/disk/by-id/', @devnode)")
	result, err = eval.Eval(expr)
	require.NoError(t, err)
	assert.True(t, result.Bool())
}

func TestEvaluatorErrors(t *testing.T) {
	disk := createTestDisk()
	ctx := NewDeviceContext(disk, "node-01")
	eval := NewEvaluator(ctx)

	// Test unknown function
	expr, _ := Parse("unknown(@type)")
	_, err := eval.Eval(expr)
	assert.Error(t, err)
	_, ok := err.(*UnknownFunctionError)
	assert.True(t, ok)

	// Test wrong number of arguments for not()
	expr, _ = Parse("not(@type, @size)")
	_, err = eval.Eval(expr)
	assert.Error(t, err)

	// Test wrong number of arguments for eq()
	expr, _ = Parse("eq(@type)")
	_, err = eval.Eval(expr)
	assert.Error(t, err)

	// Test wrong number of arguments for in()
	expr, _ = Parse("in(@type)")
	_, err = eval.Eval(expr)
	assert.Error(t, err)

	// Test invalid regex
	expr, _ = Parse("re('[invalid', @type)")
	_, err = eval.Eval(expr)
	assert.Error(t, err)
}

func TestEvaluatorComplexExpressions(t *testing.T) {
	// Test the example expressions from the spec
	disk := api.ResourcesStorageDisk{
		ID:         "nvme0n1",
		DeviceID:   "nvme-Samsung_970_EVO_Plus_S4EVNJ0N123456",
		DevicePath: "pci-0000:00:1f.2-nvme-1",
		Model:      "Samsung 970 EVO Plus",
		Size:       256 * uint64(GiB),
		Type:       "nvme",
	}

	tests := []struct {
		name     string
		hostname string
		input    string
		expected bool
	}{
		{
			name:     "spec example 1: NVMes larger than 100GiB",
			hostname: "stor-01",
			input:    "and(eq(@type,'nvme'), ge(@size,100GiB), re('^/dev', @devnode), ne(@vendor,'Seagate'))",
			expected: true,
		},
		{
			name:     "spec example 2: SSD or NVMe",
			hostname: "node-01",
			input:    "or(eq(@type, 'ssd'), eq(@type, 'nvme'))",
			expected: true,
		},
		{
			name:     "spec example 3: host-based selection (compute)",
			hostname: "compute-01",
			input:    "or(and(re('^compute-', @host), re('Samsung', @vendor)), and(re('^stor-', @host), re('Seagate', @vendor)))",
			expected: true,
		},
		{
			name:     "spec example 3: host-based selection (stor)",
			hostname: "stor-01",
			input:    "or(and(re('^compute-', @host), re('Samsung', @vendor)), and(re('^stor-', @host), re('Seagate', @vendor)))",
			expected: false, // Samsung on stor- host doesn't match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewDeviceContext(disk, tt.hostname)
			expr, err := Parse(tt.input)
			require.NoError(t, err)

			eval := NewEvaluator(ctx)
			result, err := eval.Eval(expr)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Bool())
		})
	}
}

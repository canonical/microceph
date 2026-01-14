package dsl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNumberWithUnit(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		unit     string
		wantErr  bool
	}{
		// Plain numbers
		{
			name:     "plain integer",
			input:    "100",
			expected: 100,
			unit:     "",
		},
		{
			name:     "plain float",
			input:    "100.5",
			expected: 100.5,
			unit:     "",
		},
		{
			name:     "negative number",
			input:    "-100",
			expected: -100,
			unit:     "",
		},

		// IEC units (1024-based)
		{
			name:     "KiB",
			input:    "100KiB",
			expected: 100 * KiB,
			unit:     "KiB",
		},
		{
			name:     "MiB",
			input:    "100MiB",
			expected: 100 * MiB,
			unit:     "MiB",
		},
		{
			name:     "GiB",
			input:    "100GiB",
			expected: 100 * GiB,
			unit:     "GiB",
		},
		{
			name:     "TiB",
			input:    "2TiB",
			expected: 2 * TiB,
			unit:     "TiB",
		},
		{
			name:     "PiB",
			input:    "1PiB",
			expected: 1 * PiB,
			unit:     "PiB",
		},

		// SI units (1000-based)
		{
			name:     "KB",
			input:    "100KB",
			expected: 100 * KB,
			unit:     "KB",
		},
		{
			name:     "MB",
			input:    "100MB",
			expected: 100 * MB,
			unit:     "MB",
		},
		{
			name:     "GB",
			input:    "100GB",
			expected: 100 * GB,
			unit:     "GB",
		},
		{
			name:     "TB",
			input:    "2TB",
			expected: 2 * TB,
			unit:     "TB",
		},
		{
			name:     "PB",
			input:    "1PB",
			expected: 1 * PB,
			unit:     "PB",
		},

		// Case insensitivity
		{
			name:     "lowercase gib",
			input:    "100gib",
			expected: 100 * GiB,
			unit:     "gib",
		},
		{
			name:     "mixed case GiB",
			input:    "100Gib",
			expected: 100 * GiB,
			unit:     "Gib",
		},

		// Bytes
		{
			name:     "explicit bytes",
			input:    "1024B",
			expected: 1024,
			unit:     "B",
		},

		// Float with units
		{
			name:     "float with GiB",
			input:    "1.5GiB",
			expected: 1.5 * GiB,
			unit:     "GiB",
		},

		// Error cases
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "unknown unit",
			input:   "100XYZ",
			wantErr: true,
		},
		{
			name:    "only letters",
			input:   "GiB",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, unit, err := ParseNumberWithUnit(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, value)
				assert.Equal(t, tt.unit, unit)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected string
	}{
		{
			name:     "bytes",
			input:    500,
			expected: "500 B",
		},
		{
			name:     "KiB",
			input:    2 * KiB,
			expected: "2.00 KiB",
		},
		{
			name:     "MiB",
			input:    100 * MiB,
			expected: "100.00 MiB",
		},
		{
			name:     "GiB",
			input:    256 * GiB,
			expected: "256.00 GiB",
		},
		{
			name:     "TiB",
			input:    2 * TiB,
			expected: "2.00 TiB",
		},
		{
			name:     "PiB",
			input:    1 * PiB,
			expected: "1.00 PiB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatBytes(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUnitConstants(t *testing.T) {
	// Verify IEC constants are powers of 1024
	assert.Equal(t, float64(1024), float64(KiB))
	assert.Equal(t, float64(1024*1024), float64(MiB))
	assert.Equal(t, float64(1024*1024*1024), float64(GiB))
	assert.Equal(t, float64(1024*1024*1024*1024), float64(TiB))

	// Verify SI constants are powers of 1000
	assert.Equal(t, float64(1000), float64(KB))
	assert.Equal(t, float64(1000*1000), float64(MB))
	assert.Equal(t, float64(1000*1000*1000), float64(GB))
	assert.Equal(t, float64(1000*1000*1000*1000), float64(TB))
}

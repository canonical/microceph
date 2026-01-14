package dsl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Unit multipliers for size conversions.
const (
	// IEC units (1024-based)
	KiB = 1024
	MiB = 1024 * KiB
	GiB = 1024 * MiB
	TiB = 1024 * GiB
	PiB = 1024 * TiB

	// SI units (1000-based)
	KB = 1000
	MB = 1000 * KB
	GB = 1000 * MB
	TB = 1000 * GB
	PB = 1000 * TB
)

// unitMultipliers maps unit suffixes to their byte multipliers.
var unitMultipliers = map[string]float64{
	// IEC units (binary, 1024-based)
	"B":   1,
	"KIB": KiB,
	"MIB": MiB,
	"GIB": GiB,
	"TIB": TiB,
	"PIB": PiB,

	// SI units (decimal, 1000-based)
	"KB": KB,
	"MB": MB,
	"GB": GB,
	"TB": TB,
	"PB": PB,

	// Common shorthand (treat as SI for compatibility)
	"K": KB,
	"M": MB,
	"G": GB,
	"T": TB,
	"P": PB,
}

// ParseNumberWithUnit parses a number string with an optional unit suffix.
// Returns the value in bytes (for size units), the unit string, and any error.
// Examples: "100GiB" -> (107374182400, "GiB", nil)
//
//	"500MB" -> (500000000, "MB", nil)
//	"42" -> (42, "", nil)
func ParseNumberWithUnit(s string) (float64, string, error) {
	if s == "" {
		return 0, "", fmt.Errorf("empty number string")
	}

	// Find where the numeric part ends and unit begins
	numEnd := 0
	for i, r := range s {
		if unicode.IsDigit(r) || r == '.' || r == '-' || r == '+' {
			numEnd = i + 1
		} else {
			break
		}
	}

	if numEnd == 0 {
		return 0, "", fmt.Errorf("invalid number format: %s", s)
	}

	numPart := s[:numEnd]
	unitPart := strings.TrimSpace(s[numEnd:])

	// Parse the numeric part
	value, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid number: %s", numPart)
	}

	// If no unit, return the raw value
	if unitPart == "" {
		return value, "", nil
	}

	// Look up the unit multiplier
	multiplier, ok := unitMultipliers[strings.ToUpper(unitPart)]
	if !ok {
		return 0, "", fmt.Errorf("unknown unit: %s", unitPart)
	}

	return value * multiplier, unitPart, nil
}

// FormatBytes formats a byte count as a human-readable string.
func FormatBytes(bytes float64) string {
	if bytes >= PiB {
		return fmt.Sprintf("%.2f PiB", bytes/PiB)
	}
	if bytes >= TiB {
		return fmt.Sprintf("%.2f TiB", bytes/TiB)
	}
	if bytes >= GiB {
		return fmt.Sprintf("%.2f GiB", bytes/GiB)
	}
	if bytes >= MiB {
		return fmt.Sprintf("%.2f MiB", bytes/MiB)
	}
	if bytes >= KiB {
		return fmt.Sprintf("%.2f KiB", bytes/KiB)
	}
	return fmt.Sprintf("%.0f B", bytes)
}

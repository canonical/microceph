package dsl

import (
	"fmt"
	"os"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// DeviceContext holds the context for evaluating DSL expressions against a device.
type DeviceContext struct {
	Disk     api.ResourcesStorageDisk
	Hostname string
	Path     string // computed device path
}

// NewDeviceContext creates a new DeviceContext from a disk resource.
// It computes the device path using the same logic as disk_list.go.
func NewDeviceContext(disk api.ResourcesStorageDisk, hostname string) *DeviceContext {
	// Compute device path using multiple fallback methods
	// (matches logic in disk_list.go:doFilterLocalDisks)
	var devicePath string
	if len(disk.DeviceID) > 0 {
		// First preference: use DeviceID with prefix (e.g., /dev/disk/by-id/...)
		devicePath = fmt.Sprintf("/dev/disk/by-id/%s", disk.DeviceID)
	} else if len(disk.DevicePath) > 0 {
		// Second preference: use DevicePath (e.g., /dev/disk/by-path/...)
		devicePath = fmt.Sprintf("/dev/disk/by-path/%s", disk.DevicePath)
	} else {
		// Final fallback: use device ID directly (e.g., /dev/vdc)
		devicePath = fmt.Sprintf("/dev/%s", disk.ID)
	}

	return &DeviceContext{
		Disk:     disk,
		Hostname: hostname,
		Path:     devicePath,
	}
}

// GetHostname returns the short hostname (without domain).
func GetHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	// Return short hostname (before first dot)
	if idx := strings.Index(hostname, "."); idx > 0 {
		return hostname[:idx]
	}
	return hostname
}

// ResolveVariable returns the value for a DSL variable.
// Supported variables:
//   - @type: disk type (raw LXD value: sata, virtio, nvme, etc.)
//   - @vendor: vendor name extracted from model, lowercased
//   - @model: full model string, lowercased
//   - @size: disk size in bytes
//   - @devnode: device path (e.g., /dev/sda or /dev/disk/by-id/...)
//   - @host: short hostname
func (dc *DeviceContext) ResolveVariable(name string) (Value, error) {
	switch strings.ToLower(name) {
	case "type":
		return StringValue(strings.ToLower(dc.Disk.Type)), nil

	case "vendor":
		vendor := extractVendor(dc.Disk.Model)
		return StringValue(strings.ToLower(vendor)), nil

	case "model":
		return StringValue(strings.ToLower(dc.Disk.Model)), nil

	case "size":
		return NumberValue(float64(dc.Disk.Size)), nil

	case "devnode":
		return StringValue(dc.Path), nil

	case "host":
		return StringValue(dc.Hostname), nil

	default:
		return nil, &UnknownVariableError{Name: "@" + name}
	}
}

// extractVendor extracts the vendor name from a model string.
// Typically the vendor is the first word of the model string.
// Examples:
//
//	"Samsung 970 EVO Plus" -> "Samsung"
//	"QEMU HARDDISK" -> "QEMU"
//	"WDC WD10EZEX" -> "WDC"
func extractVendor(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

	// Find first space
	if idx := strings.IndexAny(model, " \t"); idx > 0 {
		return model[:idx]
	}

	// If no space, check for common patterns like "WDC_WD10EZEX"
	if idx := strings.Index(model, "_"); idx > 0 {
		return model[:idx]
	}

	// Return full string if no separator found
	return model
}

// Value represents a runtime value in the DSL evaluation.
type Value interface {
	// Type returns the type of this value.
	Type() ValueType
	// Bool returns the value as a boolean.
	Bool() bool
	// String returns the value as a string.
	String() string
	// Number returns the value as a number.
	Number() float64
	// IsNil returns true if this is a nil value.
	IsNil() bool
}

// ValueType represents the type of a Value.
type ValueType int

const (
	ValueTypeNil ValueType = iota
	ValueTypeBool
	ValueTypeString
	ValueTypeNumber
)

func (t ValueType) String() string {
	switch t {
	case ValueTypeNil:
		return "nil"
	case ValueTypeBool:
		return "bool"
	case ValueTypeString:
		return "string"
	case ValueTypeNumber:
		return "number"
	default:
		return "unknown"
	}
}

// BoolValue represents a boolean value.
type BoolValue bool

func (v BoolValue) Type() ValueType { return ValueTypeBool }
func (v BoolValue) Bool() bool      { return bool(v) }
func (v BoolValue) String() string {
	if v {
		return "true"
	}
	return "false"
}
func (v BoolValue) Number() float64 {
	if v {
		return 1
	}
	return 0
}
func (v BoolValue) IsNil() bool { return false }

// StringValue represents a string value.
type StringValue string

func (v StringValue) Type() ValueType { return ValueTypeString }
func (v StringValue) Bool() bool      { return string(v) != "" }
func (v StringValue) String() string  { return string(v) }
func (v StringValue) Number() float64 { return 0 }
func (v StringValue) IsNil() bool     { return false }

// NumberValue represents a numeric value.
type NumberValue float64

func (v NumberValue) Type() ValueType { return ValueTypeNumber }
func (v NumberValue) Bool() bool      { return v != 0 }
func (v NumberValue) String() string  { return fmt.Sprintf("%g", float64(v)) }
func (v NumberValue) Number() float64 { return float64(v) }
func (v NumberValue) IsNil() bool     { return false }

// NilValue represents a nil/null value.
type NilValue struct{}

func (v NilValue) Type() ValueType { return ValueTypeNil }
func (v NilValue) Bool() bool      { return false }
func (v NilValue) String() string  { return "" }
func (v NilValue) Number() float64 { return 0 }
func (v NilValue) IsNil() bool     { return true }

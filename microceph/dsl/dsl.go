package dsl

import (
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// Parse parses a DSL expression string and returns the AST.
func Parse(input string) (Expression, error) {
	parser, err := NewParser(input)
	if err != nil {
		return nil, err
	}
	return parser.Parse()
}

// Validate checks an expression for semantic errors without evaluating it.
// This includes checking for unknown functions and variables.
func Validate(expr Expression) error {
	return validateExpr(expr)
}

// validateExpr recursively validates an expression.
func validateExpr(expr Expression) error {
	switch node := expr.(type) {
	case *FunctionCall:
		// Check if function is known
		if !isKnownFunction(node.Name) {
			return &UnknownFunctionError{Pos: node.Pos(), Name: node.Name}
		}
		// Validate arguments
		for _, arg := range node.Args {
			if err := validateExpr(arg); err != nil {
				return err
			}
		}
	case *Variable:
		// Check if variable is known
		if !isKnownVariable(node.Name) {
			return &UnknownVariableError{Pos: node.Pos(), Name: "@" + node.Name}
		}
	}
	return nil
}

// isKnownFunction returns true if the function name is a known predicate.
// Function names are case-insensitive.
func isKnownFunction(name string) bool {
	switch strings.ToLower(name) {
	case "and", "or", "not", "in", "re", "eq", "ne", "gt", "ge", "lt", "le":
		return true
	default:
		return false
	}
}

// isKnownVariable returns true if the variable name is known.
func isKnownVariable(name string) bool {
	switch name {
	case "type", "vendor", "model", "size", "devnode", "host":
		return true
	default:
		return false
	}
}

// MatchDevice evaluates an expression against a single device.
// Returns true if the device matches the expression.
func MatchDevice(expr Expression, disk api.ResourcesStorageDisk, hostname string) (bool, error) {
	ctx := NewDeviceContext(disk, hostname)
	eval := NewEvaluator(ctx)

	result, err := eval.Eval(expr)
	if err != nil {
		return false, err
	}

	return result.Bool(), nil
}

// MatchDevices filters a list of disks using the expression.
// Returns only the disks that match the expression.
func MatchDevices(expr Expression, disks []api.ResourcesStorageDisk, hostname string) ([]api.ResourcesStorageDisk, error) {
	var matched []api.ResourcesStorageDisk

	for _, disk := range disks {
		match, err := MatchDevice(expr, disk, hostname)
		if err != nil {
			return nil, err
		}
		if match {
			matched = append(matched, disk)
		}
	}

	return matched, nil
}

// GetDevicePath computes the device path for a disk using the same logic
// as the device context. This is useful for getting the path that will
// be passed to the OSD creation.
func GetDevicePath(disk api.ResourcesStorageDisk) string {
	ctx := NewDeviceContext(disk, "")
	return ctx.Path
}

// KnownFunctions returns a list of all known function names.
func KnownFunctions() []string {
	return []string{"and", "or", "not", "in", "re", "eq", "ne", "gt", "ge", "lt", "le"}
}

// KnownVariables returns a list of all known variable names (without @ prefix).
func KnownVariables() []string {
	return []string{"type", "vendor", "model", "size", "devnode", "host"}
}

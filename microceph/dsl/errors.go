// Package dsl provides a domain-specific language for matching storage devices.
package dsl

import "fmt"

// Position represents a location in the source input.
type Position struct {
	Line   int
	Column int
	Offset int
}

// String returns a human-readable position string.
func (p Position) String() string {
	return fmt.Sprintf("line %d, column %d", p.Line, p.Column)
}

// ParseError represents an error that occurred during parsing.
type ParseError struct {
	Pos     Position
	Message string
	Context string // surrounding source text for context
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	if e.Context != "" {
		return fmt.Sprintf("parse error at %s: %s\n  %s", e.Pos, e.Message, e.Context)
	}
	return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Message)
}

// EvalError represents an error that occurred during expression evaluation.
type EvalError struct {
	Pos     Position
	Message string
}

// Error implements the error interface.
func (e *EvalError) Error() string {
	return fmt.Sprintf("evaluation error at %s: %s", e.Pos, e.Message)
}

// TypeError represents a type mismatch error during evaluation.
type TypeError struct {
	Pos      Position
	Expected string
	Got      string
}

// Error implements the error interface.
func (e *TypeError) Error() string {
	return fmt.Sprintf("type error at %s: expected %s, got %s", e.Pos, e.Expected, e.Got)
}

// UnknownFunctionError represents an unknown function name error.
type UnknownFunctionError struct {
	Pos  Position
	Name string
}

// Error implements the error interface.
func (e *UnknownFunctionError) Error() string {
	return fmt.Sprintf("unknown function '%s' at %s", e.Name, e.Pos)
}

// UnknownVariableError represents an unknown variable name error.
type UnknownVariableError struct {
	Pos  Position
	Name string
}

// Error implements the error interface.
func (e *UnknownVariableError) Error() string {
	return fmt.Sprintf("unknown variable '%s' at %s", e.Name, e.Pos)
}

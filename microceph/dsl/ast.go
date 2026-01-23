package dsl

// Node is the interface for all AST nodes.
type Node interface {
	// Pos returns the position of this node in the source.
	Pos() Position
}

// Expression represents any expression that can be evaluated.
type Expression interface {
	Node
	exprNode() // marker method to distinguish expressions
}

// StringLiteral represents a string value in the DSL.
type StringLiteral struct {
	pos   Position
	Value string
	Raw   bool // true for single-quoted raw strings
}

func (s *StringLiteral) Pos() Position { return s.pos }
func (s *StringLiteral) exprNode()     {}

// NumberLiteral represents a numeric value, optionally with a unit.
type NumberLiteral struct {
	pos   Position
	Value float64 // the numeric value in base units (bytes for sizes)
	Raw   string  // original string representation
	Unit  string  // unit suffix if present (e.g., "GiB", "MB")
}

func (n *NumberLiteral) Pos() Position { return n.pos }
func (n *NumberLiteral) exprNode()     {}

// BoolLiteral represents a boolean value.
type BoolLiteral struct {
	pos   Position
	Value bool
}

func (b *BoolLiteral) Pos() Position { return b.pos }
func (b *BoolLiteral) exprNode()     {}

// Variable represents a variable reference (e.g., @type, @size).
type Variable struct {
	pos  Position
	Name string // variable name without the @ prefix
}

func (v *Variable) Pos() Position { return v.pos }
func (v *Variable) exprNode()     {}

// FunctionCall represents a function/predicate call.
type FunctionCall struct {
	pos  Position
	Name string       // function name (e.g., "and", "eq", "re")
	Args []Expression // function arguments
}

func (f *FunctionCall) Pos() Position { return f.pos }
func (f *FunctionCall) exprNode()     {}

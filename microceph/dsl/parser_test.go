package dsl

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParserValidExpressions(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "simple equality",
			input: "eq(@type, 'nvme')",
		},
		{
			name:  "nested and",
			input: "and(eq(@type, 'nvme'), gt(@size, 100GiB))",
		},
		{
			name:  "nested or",
			input: "or(eq(@type, 'sata'), eq(@type, 'nvme'))",
		},
		{
			name:  "not expression",
			input: "not(eq(@type, 'hdd'))",
		},
		{
			name:  "in expression",
			input: "in(@type, 'nvme', 'sata', 'ssd')",
		},
		{
			name:  "regex expression",
			input: "re('^/dev/nvme', @devnode)",
		},
		{
			name:  "complex nested expression",
			input: "and(eq(@type, 'nvme'), ge(@size, 100GiB), re('^/dev/nvme', @devnode), ne(@vendor, 'seagate'))",
		},
		{
			name:  "empty and",
			input: "and()",
		},
		{
			name:  "empty or",
			input: "or()",
		},
		{
			name:  "comparison operators",
			input: "and(gt(@size, 100GiB), lt(@size, 1TiB), ge(@size, 50GiB), le(@size, 500GiB))",
		},
		{
			name:  "host-based selection",
			input: "and(re('^compute-', @host), re('samsung', @vendor))",
		},
		{
			name:  "string with spaces",
			input: `eq(@model, "Samsung 970 EVO")`,
		},
		{
			name:  "boolean literals",
			input: "and(true, not(false))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			expr, err := parser.Parse()
			require.NoError(t, err)
			require.NotNil(t, expr)
		})
	}
}

func TestParserInvalidExpressions(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "missing closing paren",
			input: "eq(@type, 'nvme'",
		},
		{
			name:  "missing opening paren",
			input: "eq @type, 'nvme')",
		},
		{
			name:  "missing comma",
			input: "eq(@type 'nvme')",
		},
		{
			name:  "unexpected token",
			input: "eq(@type, 'nvme') extra",
		},
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "variable without name",
			input: "eq(@, 'nvme')",
		},
		{
			name:  "invalid number",
			input: "gt(@size, abc)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			if err != nil {
				// Input size error is also a parse error
				return
			}
			_, err = parser.Parse()
			assert.Error(t, err)
		})
	}
}

func TestParserASTStructure(t *testing.T) {
	// Test that parsed AST has correct structure
	input := "and(eq(@type, 'nvme'), gt(@size, 100GiB))"
	parser, err := NewParser(input)
	require.NoError(t, err)
	expr, err := parser.Parse()
	require.NoError(t, err)

	// Root should be a function call
	call, ok := expr.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "and", call.Name)
	assert.Len(t, call.Args, 2)

	// First arg: eq(@type, 'nvme')
	eqCall, ok := call.Args[0].(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "eq", eqCall.Name)
	assert.Len(t, eqCall.Args, 2)

	// @type variable
	typeVar, ok := eqCall.Args[0].(*Variable)
	require.True(t, ok)
	assert.Equal(t, "type", typeVar.Name)

	// 'nvme' string
	nvmeStr, ok := eqCall.Args[1].(*StringLiteral)
	require.True(t, ok)
	assert.Equal(t, "nvme", nvmeStr.Value)

	// Second arg: gt(@size, 100GiB)
	gtCall, ok := call.Args[1].(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "gt", gtCall.Name)
	assert.Len(t, gtCall.Args, 2)

	// @size variable
	sizeVar, ok := gtCall.Args[0].(*Variable)
	require.True(t, ok)
	assert.Equal(t, "size", sizeVar.Name)

	// 100GiB number
	sizeNum, ok := gtCall.Args[1].(*NumberLiteral)
	require.True(t, ok)
	assert.Equal(t, float64(100*GiB), sizeNum.Value)
	assert.Equal(t, "GiB", sizeNum.Unit)
}

func TestParserNumberParsing(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		unit     string
	}{
		{
			name:     "plain number",
			input:    "eq(@size, 100)",
			expected: 100,
			unit:     "",
		},
		{
			name:     "GiB",
			input:    "eq(@size, 100GiB)",
			expected: 100 * GiB,
			unit:     "GiB",
		},
		{
			name:     "MiB",
			input:    "eq(@size, 512MiB)",
			expected: 512 * MiB,
			unit:     "MiB",
		},
		{
			name:     "TiB",
			input:    "eq(@size, 2TiB)",
			expected: 2 * TiB,
			unit:     "TiB",
		},
		{
			name:     "GB (SI)",
			input:    "eq(@size, 100GB)",
			expected: 100 * GB,
			unit:     "GB",
		},
		{
			name:     "MB (SI)",
			input:    "eq(@size, 500MB)",
			expected: 500 * MB,
			unit:     "MB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			expr, err := parser.Parse()
			require.NoError(t, err)

			call, ok := expr.(*FunctionCall)
			require.True(t, ok)

			num, ok := call.Args[1].(*NumberLiteral)
			require.True(t, ok)
			assert.Equal(t, tt.expected, num.Value)
			assert.Equal(t, tt.unit, num.Unit)
		})
	}
}

func TestParserErrorMessages(t *testing.T) {
	input := "eq(@type, 'nvme'"
	parser, err := NewParser(input)
	require.NoError(t, err)
	_, err = parser.Parse()

	require.Error(t, err)
	parseErr, ok := err.(*ParseError)
	require.True(t, ok)
	assert.Contains(t, parseErr.Message, "expected")
}

func TestParserRecursionDepthLimit(t *testing.T) {
	// Build a deeply nested expression that exceeds the limit
	// and(and(and(and(... 'value' ...))))
	depth := MaxRecursionDepth + 10
	var builder strings.Builder
	for i := 0; i < depth; i++ {
		builder.WriteString("and(")
	}
	builder.WriteString("true")
	for i := 0; i < depth; i++ {
		builder.WriteString(")")
	}

	parser, err := NewParser(builder.String())
	require.NoError(t, err)
	_, err = parser.Parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum depth")
}

func TestParserRecursionWithinLimit(t *testing.T) {
	// Build a nested expression within the limit
	depth := 10
	var builder strings.Builder
	for i := 0; i < depth; i++ {
		builder.WriteString("and(")
	}
	builder.WriteString("true")
	for i := 0; i < depth; i++ {
		builder.WriteString(")")
	}

	parser, err := NewParser(builder.String())
	require.NoError(t, err)
	expr, err := parser.Parse()
	require.NoError(t, err)
	require.NotNil(t, expr)
}

// Edge case tests for the parser
func TestParserEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// Whitespace handling
		{name: "excessive whitespace", input: "  and(   eq(  @type  ,   'nvme'  )   )  "},
		{name: "tabs and newlines", input: "and(\n\teq(@type,\n\t\t'nvme')\n)"},
		{name: "no whitespace", input: "and(eq(@type,'nvme'),gt(@size,100GiB))"},

		// String edge cases
		{name: "empty string argument", input: "eq(@vendor, '')"},
		{name: "string with special chars", input: `eq(@model, "Samsung 970 EVO Plus")`},
		{name: "string with unicode", input: `eq(@vendor, '日本語')`},
		{name: "string with path", input: `eq(@devnode, '/dev/disk/by-id/nvme-Samsung_SSD')`},
		{name: "string with regex special chars", input: `re('^/dev/nvme[0-9]+n[0-9]+$', @devnode)`},

		// Number edge cases
		{name: "zero", input: "eq(@size, 0)"},
		{name: "large number", input: "gt(@size, 999999999999999)"},
		{name: "decimal number", input: "gt(@size, 1.5TiB)"},
		{name: "small decimal", input: "gt(@size, 0.001GiB)"},

		// Boolean edge cases
		{name: "boolean true standalone", input: "true"},
		{name: "boolean false standalone", input: "false"},
		{name: "mixed case boolean", input: "and(True, FALSE, true)"},

		// Function call edge cases
		{name: "single arg and", input: "and(true)"},
		{name: "single arg or", input: "or(false)"},
		{name: "many args and", input: "and(true, false, true, false, true, false, true, false)"},
		{name: "many args or", input: "or(true, false, true, false, true, false, true, false)"},
		{name: "many args in", input: "in(@type, 'nvme', 'sata', 'ssd', 'hdd', 'virtio', 'scsi')"},

		// Variable edge cases
		{name: "all known variables", input: "and(eq(@type, 'x'), eq(@vendor, 'x'), eq(@model, 'x'), eq(@devnode, 'x'), eq(@host, 'x'), gt(@size, 0))"},

		// Case insensitivity
		{name: "uppercase function names", input: "AND(EQ(@type, 'nvme'), GT(@size, 100GiB))"},
		{name: "mixed case function names", input: "And(Eq(@type, 'nvme'), Gt(@size, 100GiB))"},

		// Deeply nested but valid
		{name: "triple nested not", input: "not(not(not(true)))"},
		{name: "alternating and/or", input: "and(or(true, false), or(false, true))"},
		{name: "complex real-world expression", input: "and(or(eq(@type, 'nvme'), eq(@type, 'ssd')), gt(@size, 100GiB), not(re('slow', @model)), in(@vendor, 'samsung', 'intel', 'western digital'))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err, "NewParser failed for: %s", tt.input)
			expr, err := parser.Parse()
			require.NoError(t, err, "Parse failed for: %s", tt.input)
			require.NotNil(t, expr, "Expression is nil for: %s", tt.input)
		})
	}
}

// Pathological and malformed input tests
func TestParserPathologicalInputs(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		// Unbalanced parentheses
		{name: "extra open paren", input: "and((eq(@type, 'nvme'))", expectError: true},
		{name: "extra close paren", input: "and(eq(@type, 'nvme')))", expectError: true},
		{name: "mismatched parens", input: "and(eq(@type, 'nvme'", expectError: true},
		{name: "only open parens", input: "(((", expectError: true},
		{name: "only close parens", input: ")))", expectError: true},

		// Missing required elements
		{name: "function without parens", input: "and", expectError: true},
		{name: "function with empty parens followed by extra", input: "and() extra", expectError: true},
		{name: "comma without args", input: "and(,)", expectError: true},
		{name: "leading comma", input: "and(, true)", expectError: true},
		{name: "trailing comma", input: "and(true,)", expectError: true},
		{name: "double comma", input: "and(true,, false)", expectError: true},

		// Invalid tokens
		{name: "unknown operator", input: "eq(@type + 'nvme')", expectError: true},
		{name: "semicolon", input: "eq(@type, 'nvme');", expectError: true},
		{name: "brackets", input: "eq[@type, 'nvme']", expectError: true},
		{name: "curly braces", input: "{eq(@type, 'nvme')}", expectError: true},

		// Invalid variable syntax
		{name: "double at sign", input: "eq(@@type, 'nvme')", expectError: true},
		{name: "at sign alone", input: "eq(@, 'nvme')", expectError: true},
		{name: "at sign with number", input: "eq(@123, 'nvme')", expectError: true},

		// String issues
		{name: "unclosed single quote", input: "eq(@type, 'nvme)", expectError: true},
		{name: "unclosed double quote", input: `eq(@type, "nvme)`, expectError: true},
		{name: "mismatched quotes", input: `eq(@type, 'nvme")`, expectError: true},

		// Number issues
		{name: "invalid number format", input: "gt(@size, 100.100.100)", expectError: true},
		{name: "number with invalid unit", input: "gt(@size, 100XYZ)", expectError: true},
		{name: "just minus sign", input: "gt(@size, -)", expectError: true},
		{name: "minus dot", input: "gt(@size, -.)", expectError: true},

		// Whitespace only
		{name: "only spaces", input: "   ", expectError: true},
		{name: "only tabs", input: "\t\t\t", expectError: true},
		{name: "only newlines", input: "\n\n\n", expectError: true},

		// Gibberish
		{name: "random characters", input: "!@#$%^&*", expectError: true},
		{name: "sql injection attempt", input: "'; DROP TABLE devices; --", expectError: true},
		{name: "script injection", input: "<script>alert('xss')</script>", expectError: true},

		// Partial expressions
		{name: "just at sign", input: "@", expectError: true},
		{name: "just comma", input: ",", expectError: true},
		{name: "just paren", input: "(", expectError: true},

		// Reserved/confusing
		{name: "null keyword", input: "null", expectError: true},
		{name: "undefined keyword", input: "undefined", expectError: true},
		{name: "nil keyword", input: "nil", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			if err != nil {
				// Input rejected at lexer level (e.g., size limit)
				if tt.expectError {
					return
				}
				t.Fatalf("Unexpected NewParser error: %v", err)
			}
			_, err = parser.Parse()
			if tt.expectError {
				assert.Error(t, err, "Expected error for input: %s", tt.input)
			} else {
				assert.NoError(t, err, "Unexpected error for input: %s", tt.input)
			}
		})
	}
}

// Tests for wide nesting (many siblings) rather than deep nesting
func TestParserWideExpressions(t *testing.T) {
	// Build expression with many arguments: and(true, true, true, ..., true)
	numArgs := 50
	var builder strings.Builder
	builder.WriteString("and(")
	for i := 0; i < numArgs; i++ {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString("true")
	}
	builder.WriteString(")")

	parser, err := NewParser(builder.String())
	require.NoError(t, err)
	expr, err := parser.Parse()
	require.NoError(t, err)

	call, ok := expr.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "and", call.Name)
	assert.Len(t, call.Args, numArgs)
}

// Tests for complex nested structures
func TestParserComplexNesting(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "or inside and inside not",
			input: "not(and(or(true, false), or(false, true)))",
		},
		{
			name:  "multiple levels of alternation",
			input: "and(or(and(true, false), and(false, true)), or(and(true, true), and(false, false)))",
		},
		{
			name:  "in with function results",
			input: "and(in(@type, 'nvme', 'ssd'), not(in(@vendor, 'unknown', 'generic')))",
		},
		{
			name:  "real world: complex storage selection",
			input: "and(or(eq(@type, 'nvme'), and(eq(@type, 'ssd'), gt(@size, 500GiB))), not(re('SLOW|OLD', @model)), ge(@size, 100GiB), le(@size, 10TiB))",
		},
		{
			name:  "real world: host-based partitioning",
			input: "or(and(re('^storage-', @host), eq(@type, 'nvme')), and(re('^compute-', @host), gt(@size, 1TiB)))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			expr, err := parser.Parse()
			require.NoError(t, err)
			require.NotNil(t, expr)
		})
	}
}

// Test that parser correctly rejects expressions after valid expression
func TestParserTrailingContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "extra identifier", input: "true false"},
		{name: "extra function", input: "true and()"},
		{name: "extra paren", input: "and(true) )"},
		{name: "extra text", input: "eq(@type, 'nvme') hello"},
		{name: "duplicate expression", input: "eq(@type, 'nvme') eq(@type, 'ssd')"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			_, err = parser.Parse()
			assert.Error(t, err, "Expected error for input with trailing content: %s", tt.input)
		})
	}
}

// Test argument count validation for specific functions
func TestParserFunctionArgCounts(t *testing.T) {
	// These should parse successfully (arg count is validated at eval time, not parse time)
	validTests := []struct {
		name  string
		input string
	}{
		{name: "eq with 2 args", input: "eq(@type, 'nvme')"},
		{name: "ne with 2 args", input: "ne(@type, 'nvme')"},
		{name: "gt with 2 args", input: "gt(@size, 100GiB)"},
		{name: "ge with 2 args", input: "ge(@size, 100GiB)"},
		{name: "lt with 2 args", input: "lt(@size, 100GiB)"},
		{name: "le with 2 args", input: "le(@size, 100GiB)"},
		{name: "not with 1 arg", input: "not(true)"},
		{name: "re with 2 args", input: "re('pattern', @type)"},
		{name: "in with multiple args", input: "in(@type, 'a', 'b', 'c')"},
		{name: "and with 0 args", input: "and()"},
		{name: "or with 0 args", input: "or()"},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			_, err = parser.Parse()
			assert.NoError(t, err)
		})
	}
}

// Test boundary conditions for numbers
func TestParserNumberBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
	}{
		{name: "zero bytes", input: "eq(@size, 0)", expected: 0},
		{name: "one byte", input: "eq(@size, 1)", expected: 1},
		{name: "max safe integer", input: "eq(@size, 9007199254740991)", expected: 9007199254740991},
		{name: "1 KiB", input: "eq(@size, 1KiB)", expected: 1024},
		{name: "1 MiB", input: "eq(@size, 1MiB)", expected: 1024 * 1024},
		{name: "1 GiB", input: "eq(@size, 1GiB)", expected: 1024 * 1024 * 1024},
		{name: "1 TiB", input: "eq(@size, 1TiB)", expected: 1024 * 1024 * 1024 * 1024},
		{name: "1 PiB", input: "eq(@size, 1PiB)", expected: 1024 * 1024 * 1024 * 1024 * 1024},
		{name: "fractional GiB", input: "eq(@size, 0.5GiB)", expected: 0.5 * 1024 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			expr, err := parser.Parse()
			require.NoError(t, err)

			call, ok := expr.(*FunctionCall)
			require.True(t, ok)
			num, ok := call.Args[1].(*NumberLiteral)
			require.True(t, ok)
			assert.Equal(t, tt.expected, num.Value)
		})
	}
}

// Test string escape sequences
func TestParserStringEscapes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "newline escape", input: `eq(@model, "line1\nline2")`, expected: "line1\nline2"},
		{name: "tab escape", input: `eq(@model, "col1\tcol2")`, expected: "col1\tcol2"},
		{name: "carriage return", input: `eq(@model, "a\rb")`, expected: "a\rb"},
		{name: "escaped quote", input: `eq(@model, "say \"hi\"")`, expected: `say "hi"`},
		{name: "escaped backslash", input: `eq(@model, "path\\file")`, expected: `path\file`},
		{name: "raw string with escaped single quote", input: `eq(@model, 'it''s fine')`, expected: "it's fine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := NewParser(tt.input)
			require.NoError(t, err)
			expr, err := parser.Parse()
			require.NoError(t, err)

			call, ok := expr.(*FunctionCall)
			require.True(t, ok)
			str, ok := call.Args[1].(*StringLiteral)
			require.True(t, ok)
			assert.Equal(t, tt.expected, str.Value)
		})
	}
}

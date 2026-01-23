package dsl

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLexerTokenTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Token
	}{
		{
			name:  "simple function call",
			input: "eq(@type, 'nvme')",
			expected: []Token{
				{Type: TokenIdent, Value: "eq"},
				{Type: TokenLParen, Value: "("},
				{Type: TokenAt, Value: "@"},
				{Type: TokenIdent, Value: "type"},
				{Type: TokenComma, Value: ","},
				{Type: TokenString, Value: "nvme"},
				{Type: TokenRParen, Value: ")"},
				{Type: TokenEOF},
			},
		},
		{
			name:  "boolean keywords",
			input: "true false TRUE FALSE",
			expected: []Token{
				{Type: TokenTrue, Value: "true"},
				{Type: TokenFalse, Value: "false"},
				{Type: TokenTrue, Value: "TRUE"},
				{Type: TokenFalse, Value: "FALSE"},
				{Type: TokenEOF},
			},
		},
		{
			name:  "number with units",
			input: "100GiB 500MB 2TB 42",
			expected: []Token{
				{Type: TokenNumber, Value: "100GiB"},
				{Type: TokenNumber, Value: "500MB"},
				{Type: TokenNumber, Value: "2TB"},
				{Type: TokenNumber, Value: "42"},
				{Type: TokenEOF},
			},
		},
		{
			name:  "nested function calls",
			input: "and(eq(@type, 'nvme'), gt(@size, 100GiB))",
			expected: []Token{
				{Type: TokenIdent, Value: "and"},
				{Type: TokenLParen, Value: "("},
				{Type: TokenIdent, Value: "eq"},
				{Type: TokenLParen, Value: "("},
				{Type: TokenAt, Value: "@"},
				{Type: TokenIdent, Value: "type"},
				{Type: TokenComma, Value: ","},
				{Type: TokenString, Value: "nvme"},
				{Type: TokenRParen, Value: ")"},
				{Type: TokenComma, Value: ","},
				{Type: TokenIdent, Value: "gt"},
				{Type: TokenLParen, Value: "("},
				{Type: TokenAt, Value: "@"},
				{Type: TokenIdent, Value: "size"},
				{Type: TokenComma, Value: ","},
				{Type: TokenNumber, Value: "100GiB"},
				{Type: TokenRParen, Value: ")"},
				{Type: TokenRParen, Value: ")"},
				{Type: TokenEOF},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer, err := NewLexer(tt.input)
			require.NoError(t, err)
			for i, expected := range tt.expected {
				token := lexer.NextToken()
				assert.Equal(t, expected.Type, token.Type, "token %d type mismatch", i)
				if expected.Value != "" {
					assert.Equal(t, expected.Value, token.Value, "token %d value mismatch", i)
				}
			}
		})
	}
}

func TestLexerRawStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple raw string",
			input:    "'hello'",
			expected: "hello",
		},
		{
			name:     "raw string with escaped quote",
			input:    "'it''s working'",
			expected: "it's working",
		},
		{
			name:     "empty raw string",
			input:    "''",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer, err := NewLexer(tt.input)
			require.NoError(t, err)
			token := lexer.NextToken()
			assert.Equal(t, TokenString, token.Type)
			assert.Equal(t, tt.expected, token.Value)
		})
	}
}

func TestLexerEscapedStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple escaped string",
			input:    `"hello"`,
			expected: "hello",
		},
		{
			name:     "string with newline escape",
			input:    `"hello\nworld"`,
			expected: "hello\nworld",
		},
		{
			name:     "string with tab escape",
			input:    `"hello\tworld"`,
			expected: "hello\tworld",
		},
		{
			name:     "string with escaped quote",
			input:    `"say \"hello\""`,
			expected: `say "hello"`,
		},
		{
			name:     "string with escaped backslash",
			input:    `"path\\to\\file"`,
			expected: `path\to\file`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer, err := NewLexer(tt.input)
			require.NoError(t, err)
			token := lexer.NextToken()
			assert.Equal(t, TokenString, token.Type)
			assert.Equal(t, tt.expected, token.Value)
		})
	}
}

func TestLexerErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unterminated raw string",
			input: "'hello",
		},
		{
			name:  "unterminated escaped string",
			input: `"hello`,
		},
		{
			name:  "invalid escape sequence",
			input: `"hello\x"`,
		},
		{
			name:  "invalid number - just minus",
			input: "-",
		},
		{
			name:  "invalid number - minus dot",
			input: "-.",
		},
		{
			name:  "invalid number - just dot",
			input: ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer, err := NewLexer(tt.input)
			require.NoError(t, err)
			token := lexer.NextToken()
			assert.Equal(t, TokenError, token.Type)
		})
	}
}

func TestLexerPositionTracking(t *testing.T) {
	input := "eq(@type,\n'nvme')"
	lexer, err := NewLexer(input)
	require.NoError(t, err)

	// eq
	token := lexer.NextToken()
	assert.Equal(t, 1, token.Pos.Line)
	assert.Equal(t, 1, token.Pos.Column)

	// (
	token = lexer.NextToken()
	assert.Equal(t, 1, token.Pos.Line)
	assert.Equal(t, 3, token.Pos.Column)

	// @
	token = lexer.NextToken()
	assert.Equal(t, 1, token.Pos.Line)
	assert.Equal(t, 4, token.Pos.Column)

	// type
	token = lexer.NextToken()
	assert.Equal(t, 1, token.Pos.Line)
	assert.Equal(t, 5, token.Pos.Column)

	// ,
	token = lexer.NextToken()
	assert.Equal(t, 1, token.Pos.Line)
	assert.Equal(t, 9, token.Pos.Column)

	// 'nvme' (on line 2)
	token = lexer.NextToken()
	assert.Equal(t, 2, token.Pos.Line)
	assert.Equal(t, 1, token.Pos.Column)
}

func TestLexerPeek(t *testing.T) {
	lexer, err := NewLexer("eq(@type)")
	require.NoError(t, err)

	// Peek should return the same token multiple times
	peek1 := lexer.Peek()
	peek2 := lexer.Peek()
	assert.Equal(t, peek1.Type, peek2.Type)
	assert.Equal(t, peek1.Value, peek2.Value)

	// NextToken should return the peeked token
	next := lexer.NextToken()
	assert.Equal(t, peek1.Type, next.Type)
	assert.Equal(t, peek1.Value, next.Value)

	// Next peek should be different
	peek3 := lexer.Peek()
	assert.NotEqual(t, peek1.Value, peek3.Value)
}

func TestLexerValidNumbers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "integer",
			input:    "42",
			expected: "42",
		},
		{
			name:     "negative integer",
			input:    "-42",
			expected: "-42",
		},
		{
			name:     "decimal",
			input:    "3.14",
			expected: "3.14",
		},
		{
			name:     "negative decimal",
			input:    "-3.14",
			expected: "-3.14",
		},
		{
			name:     "leading decimal",
			input:    ".5",
			expected: ".5",
		},
		{
			name:     "number with unit",
			input:    "100GiB",
			expected: "100GiB",
		},
		{
			name:     "negative with unit",
			input:    "-100GiB",
			expected: "-100GiB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lexer, err := NewLexer(tt.input)
			require.NoError(t, err)
			token := lexer.NextToken()
			assert.Equal(t, TokenNumber, token.Type)
			assert.Equal(t, tt.expected, token.Value)
		})
	}
}

func TestLexerInputSizeLimit(t *testing.T) {
	// Input within limit should succeed
	smallInput := strings.Repeat("a", MaxInputSize)
	_, err := NewLexer(smallInput)
	assert.NoError(t, err)

	// Input exceeding limit should fail
	largeInput := strings.Repeat("a", MaxInputSize+1)
	_, err = NewLexer(largeInput)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum allowed size")
}

func TestLexerGetContextLineOutOfBounds(t *testing.T) {
	lexer, err := NewLexer("test")
	require.NoError(t, err)

	// Test with offset beyond input length
	result := lexer.GetContextLine(Position{Offset: 100})
	assert.Equal(t, "test", result) // Should clamp and return the line

	// Test with negative offset
	result = lexer.GetContextLine(Position{Offset: -5})
	assert.Equal(t, "test", result) // Should clamp and return the line

	// Test with empty input
	emptyLexer, err := NewLexer("")
	require.NoError(t, err)
	result = emptyLexer.GetContextLine(Position{Offset: 0})
	assert.Equal(t, "", result)
}

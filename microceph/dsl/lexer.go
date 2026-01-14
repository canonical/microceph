package dsl

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	// MaxInputSize is the maximum allowed size for DSL input to prevent memory exhaustion.
	MaxInputSize = 10 * 1024 // 10KB
)

// TokenType represents the type of a lexical token.
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenIdent
	TokenNumber
	TokenString
	TokenTrue
	TokenFalse
	TokenAt
	TokenLParen
	TokenRParen
	TokenComma
	TokenError
)

// String returns a human-readable token type name.
func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenIdent:
		return "IDENT"
	case TokenNumber:
		return "NUMBER"
	case TokenString:
		return "STRING"
	case TokenTrue:
		return "TRUE"
	case TokenFalse:
		return "FALSE"
	case TokenAt:
		return "@"
	case TokenLParen:
		return "("
	case TokenRParen:
		return ")"
	case TokenComma:
		return ","
	case TokenError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Token represents a lexical token.
type Token struct {
	Type  TokenType
	Value string
	Pos   Position
}

// Lexer tokenizes DSL input.
type Lexer struct {
	input  string
	pos    int  // current position in input
	line   int  // current line number (1-based)
	column int  // current column number (1-based)
	start  int  // start position of current token
}

// NewLexer creates a new Lexer for the given input.
// Returns an error if the input exceeds MaxInputSize.
func NewLexer(input string) (*Lexer, error) {
	if len(input) > MaxInputSize {
		return nil, fmt.Errorf("input size %d exceeds maximum allowed size %d", len(input), MaxInputSize)
	}
	return &Lexer{
		input:  input,
		pos:    0,
		line:   1,
		column: 1,
	}, nil
}

// NextToken returns the next token from the input.
func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	if l.pos >= len(l.input) {
		return Token{Type: TokenEOF, Pos: l.currentPos()}
	}

	l.start = l.pos
	startPos := l.currentPos()
	ch := l.input[l.pos]

	switch ch {
	case '(':
		l.advance()
		return Token{Type: TokenLParen, Value: "(", Pos: startPos}
	case ')':
		l.advance()
		return Token{Type: TokenRParen, Value: ")", Pos: startPos}
	case ',':
		l.advance()
		return Token{Type: TokenComma, Value: ",", Pos: startPos}
	case '@':
		l.advance()
		return Token{Type: TokenAt, Value: "@", Pos: startPos}
	case '\'':
		return l.scanRawString()
	case '"':
		return l.scanEscapedString()
	default:
		if unicode.IsLetter(rune(ch)) || ch == '_' {
			return l.scanIdentOrKeyword()
		}
		if unicode.IsDigit(rune(ch)) || ch == '.' || ch == '-' {
			return l.scanNumber()
		}
		l.advance()
		return Token{
			Type:  TokenError,
			Value: fmt.Sprintf("unexpected character '%c'", ch),
			Pos:   startPos,
		}
	}
}

// Peek returns the next token without consuming it.
func (l *Lexer) Peek() Token {
	// Save state
	pos := l.pos
	line := l.line
	column := l.column

	tok := l.NextToken()

	// Restore state
	l.pos = pos
	l.line = line
	l.column = column

	return tok
}

// currentPos returns the current position.
func (l *Lexer) currentPos() Position {
	return Position{Line: l.line, Column: l.column, Offset: l.pos}
}

// advance moves to the next character.
func (l *Lexer) advance() {
	if l.pos < len(l.input) {
		if l.input[l.pos] == '\n' {
			l.line++
			l.column = 1
		} else {
			l.column++
		}
		l.pos++
	}
}

// skipWhitespace skips whitespace characters.
func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			l.advance()
		} else {
			break
		}
	}
}

// scanIdentOrKeyword scans an identifier or keyword.
func (l *Lexer) scanIdentOrKeyword() Token {
	startPos := l.currentPos()
	start := l.pos

	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == '_' {
			l.advance()
		} else {
			break
		}
	}

	value := l.input[start:l.pos]

	// Check for keywords
	switch strings.ToLower(value) {
	case "true":
		return Token{Type: TokenTrue, Value: value, Pos: startPos}
	case "false":
		return Token{Type: TokenFalse, Value: value, Pos: startPos}
	default:
		return Token{Type: TokenIdent, Value: value, Pos: startPos}
	}
}

// scanNumber scans a number with optional unit suffix.
func (l *Lexer) scanNumber() Token {
	startPos := l.currentPos()
	start := l.pos
	hasDigits := false

	// Handle optional negative sign
	if l.pos < len(l.input) && l.input[l.pos] == '-' {
		l.advance()
	}

	// Scan digits before decimal point
	for l.pos < len(l.input) && unicode.IsDigit(rune(l.input[l.pos])) {
		hasDigits = true
		l.advance()
	}

	// Handle decimal point
	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		l.advance()
		// Scan digits after decimal point
		for l.pos < len(l.input) && unicode.IsDigit(rune(l.input[l.pos])) {
			hasDigits = true
			l.advance()
		}
	}

	// Validate that we have at least some digits
	if !hasDigits {
		value := l.input[start:l.pos]
		return Token{
			Type:  TokenError,
			Value: fmt.Sprintf("invalid number: '%s' (no digits)", value),
			Pos:   startPos,
		}
	}

	// Handle unit suffix (e.g., GiB, MB, TB)
	// Units start with a letter and can contain letters
	if l.pos < len(l.input) && unicode.IsLetter(rune(l.input[l.pos])) {
		for l.pos < len(l.input) && unicode.IsLetter(rune(l.input[l.pos])) {
			l.advance()
		}
	}

	value := l.input[start:l.pos]
	return Token{Type: TokenNumber, Value: value, Pos: startPos}
}

// scanRawString scans a single-quoted raw string.
// In raw strings, '' is used to represent a literal single quote.
func (l *Lexer) scanRawString() Token {
	startPos := l.currentPos()
	l.advance() // consume opening quote

	var sb strings.Builder

	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\'' {
			// Check for escaped quote ('')
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
				sb.WriteByte('\'')
				l.advance()
				l.advance()
			} else {
				// End of string
				l.advance()
				return Token{Type: TokenString, Value: sb.String(), Pos: startPos}
			}
		} else {
			sb.WriteByte(ch)
			l.advance()
		}
	}

	return Token{
		Type:  TokenError,
		Value: "unterminated string literal",
		Pos:   startPos,
	}
}

// scanEscapedString scans a double-quoted string with escape sequences.
func (l *Lexer) scanEscapedString() Token {
	startPos := l.currentPos()
	l.advance() // consume opening quote

	var sb strings.Builder

	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '"' {
			l.advance()
			return Token{Type: TokenString, Value: sb.String(), Pos: startPos}
		} else if ch == '\\' {
			if l.pos+1 >= len(l.input) {
				return Token{
					Type:  TokenError,
					Value: "unterminated escape sequence",
					Pos:   startPos,
				}
			}
			l.advance()
			escaped := l.input[l.pos]
			switch escaped {
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			default:
				return Token{
					Type:  TokenError,
					Value: fmt.Sprintf("invalid escape sequence '\\%c'", escaped),
					Pos:   l.currentPos(),
				}
			}
			l.advance()
		} else if ch == '\n' {
			return Token{
				Type:  TokenError,
				Value: "unterminated string literal",
				Pos:   startPos,
			}
		} else {
			sb.WriteByte(ch)
			l.advance()
		}
	}

	return Token{
		Type:  TokenError,
		Value: "unterminated string literal",
		Pos:   startPos,
	}
}

// GetContextLine returns the line of input containing the given position
// for error reporting purposes.
func (l *Lexer) GetContextLine(pos Position) string {
	// Validate offset is within bounds
	if pos.Offset < 0 || pos.Offset >= len(l.input) {
		if len(l.input) == 0 {
			return ""
		}
		// Clamp to valid range for best-effort context
		if pos.Offset < 0 {
			pos.Offset = 0
		} else {
			pos.Offset = len(l.input) - 1
		}
	}

	// Find start of line
	start := pos.Offset
	for start > 0 && l.input[start-1] != '\n' {
		start--
	}

	// Find end of line
	end := pos.Offset
	for end < len(l.input) && l.input[end] != '\n' {
		end++
	}

	return l.input[start:end]
}

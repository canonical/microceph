package dsl

import (
	"fmt"
)

const (
	// MaxRecursionDepth limits the nesting depth of DSL expressions to prevent stack overflow.
	MaxRecursionDepth = 100
)

// Parser parses DSL expressions into an AST.
type Parser struct {
	lexer   *Lexer
	current Token
	input   string
	depth   int // current recursion depth
}

// NewParser creates a new Parser for the given input.
// Returns an error if the input exceeds size limits.
func NewParser(input string) (*Parser, error) {
	lexer, err := NewLexer(input)
	if err != nil {
		return nil, err
	}
	p := &Parser{
		lexer: lexer,
		input: input,
		depth: 0,
	}
	p.advance() // prime the parser with first token
	return p, nil
}

// Parse parses the input and returns the root expression.
func (p *Parser) Parse() (Expression, error) {
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	// Ensure we've consumed all input
	if p.current.Type != TokenEOF {
		return nil, p.error(fmt.Sprintf("unexpected token '%s', expected end of input", p.current.Value))
	}

	return expr, nil
}

// advance moves to the next token.
func (p *Parser) advance() {
	p.current = p.lexer.NextToken()
}

// expect checks that the current token has the expected type and advances.
func (p *Parser) expect(typ TokenType) error {
	if p.current.Type != typ {
		return p.error(fmt.Sprintf("expected %s, got %s", typ, p.current.Type))
	}
	p.advance()
	return nil
}

// error creates a ParseError at the current position.
func (p *Parser) error(msg string) *ParseError {
	context := p.lexer.GetContextLine(p.current.Pos)
	return &ParseError{
		Pos:     p.current.Pos,
		Message: msg,
		Context: context,
	}
}

// parseExpression parses any expression.
func (p *Parser) parseExpression() (Expression, error) {
	// Check recursion depth to prevent stack overflow
	p.depth++
	if p.depth > MaxRecursionDepth {
		return nil, p.error(fmt.Sprintf("expression nesting exceeds maximum depth of %d", MaxRecursionDepth))
	}
	defer func() { p.depth-- }()

	switch p.current.Type {
	case TokenIdent:
		return p.parseFunctionCall()
	case TokenAt:
		return p.parseVariable()
	case TokenString:
		return p.parseString()
	case TokenNumber:
		return p.parseNumber()
	case TokenTrue, TokenFalse:
		return p.parseBool()
	case TokenError:
		return nil, p.error(p.current.Value)
	default:
		return nil, p.error(fmt.Sprintf("unexpected token '%s'", p.current.Value))
	}
}

// parseFunctionCall parses a function call: ident(args...)
func (p *Parser) parseFunctionCall() (Expression, error) {
	pos := p.current.Pos
	name := p.current.Value
	p.advance() // consume identifier

	if p.current.Type != TokenLParen {
		return nil, p.error(fmt.Sprintf("expected '(' after function name '%s'", name))
	}
	p.advance() // consume '('

	var args []Expression

	// Parse arguments
	if p.current.Type != TokenRParen {
		for {
			arg, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)

			if p.current.Type == TokenComma {
				p.advance() // consume ','
			} else {
				break
			}
		}
	}

	if err := p.expect(TokenRParen); err != nil {
		return nil, err
	}

	return &FunctionCall{
		pos:  pos,
		Name: name,
		Args: args,
	}, nil
}

// parseVariable parses a variable reference: @name
func (p *Parser) parseVariable() (Expression, error) {
	pos := p.current.Pos
	p.advance() // consume '@'

	if p.current.Type != TokenIdent {
		return nil, p.error("expected variable name after '@'")
	}

	name := p.current.Value
	p.advance() // consume identifier

	return &Variable{
		pos:  pos,
		Name: name,
	}, nil
}

// parseString parses a string literal.
func (p *Parser) parseString() (Expression, error) {
	pos := p.current.Pos
	value := p.current.Value
	p.advance()

	return &StringLiteral{
		pos:   pos,
		Value: value,
	}, nil
}

// parseNumber parses a number literal with optional unit.
func (p *Parser) parseNumber() (Expression, error) {
	pos := p.current.Pos
	raw := p.current.Value

	value, unit, err := ParseNumberWithUnit(raw)
	if err != nil {
		return nil, &ParseError{
			Pos:     pos,
			Message: err.Error(),
		}
	}

	p.advance()

	return &NumberLiteral{
		pos:   pos,
		Value: value,
		Raw:   raw,
		Unit:  unit,
	}, nil
}

// parseBool parses a boolean literal.
func (p *Parser) parseBool() (Expression, error) {
	pos := p.current.Pos
	value := p.current.Type == TokenTrue
	p.advance()

	return &BoolLiteral{
		pos:   pos,
		Value: value,
	}, nil
}

package dsl

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// MaxRegexPatternLength limits regex pattern size to prevent DoS.
	MaxRegexPatternLength = 1000
	// RegexTimeout limits regex execution time to prevent ReDoS.
	RegexTimeout = 100 * time.Millisecond
)

// Evaluator evaluates DSL expressions against a device context.
type Evaluator struct {
	ctx *DeviceContext
}

// NewEvaluator creates a new Evaluator with the given device context.
func NewEvaluator(ctx *DeviceContext) *Evaluator {
	return &Evaluator{ctx: ctx}
}

// Eval evaluates an expression and returns the result.
func (e *Evaluator) Eval(expr Expression) (Value, error) {
	switch node := expr.(type) {
	case *StringLiteral:
		return StringValue(node.Value), nil
	case *NumberLiteral:
		return NumberValue(node.Value), nil
	case *BoolLiteral:
		return BoolValue(node.Value), nil
	case *Variable:
		return e.evalVariable(node)
	case *FunctionCall:
		return e.evalFunction(node)
	default:
		return nil, &EvalError{Message: fmt.Sprintf("unknown expression type: %T", expr)}
	}
}

// evalVariable evaluates a variable reference.
func (e *Evaluator) evalVariable(v *Variable) (Value, error) {
	val, err := e.ctx.ResolveVariable(v.Name)
	if err != nil {
		if uve, ok := err.(*UnknownVariableError); ok {
			uve.Pos = v.Pos()
			return nil, uve
		}
		return nil, &EvalError{Pos: v.Pos(), Message: err.Error()}
	}
	return val, nil
}

// evalFunction evaluates a function call.
func (e *Evaluator) evalFunction(f *FunctionCall) (Value, error) {
	name := strings.ToLower(f.Name)

	switch name {
	case "and":
		return e.evalAnd(f)
	case "or":
		return e.evalOr(f)
	case "not":
		return e.evalNot(f)
	case "in":
		return e.evalIn(f)
	case "re":
		return e.evalRe(f)
	case "eq":
		return e.evalComparison(f, "eq")
	case "ne":
		return e.evalComparison(f, "ne")
	case "gt":
		return e.evalComparison(f, "gt")
	case "ge":
		return e.evalComparison(f, "ge")
	case "lt":
		return e.evalComparison(f, "lt")
	case "le":
		return e.evalComparison(f, "le")
	default:
		return nil, &UnknownFunctionError{Pos: f.Pos(), Name: f.Name}
	}
}

// evalAnd evaluates and(a, b, c, ...) - variadic AND with short-circuit.
func (e *Evaluator) evalAnd(f *FunctionCall) (Value, error) {
	// Identity element: and() -> true
	if len(f.Args) == 0 {
		return BoolValue(true), nil
	}

	for _, arg := range f.Args {
		val, err := e.Eval(arg)
		if err != nil {
			return nil, err
		}
		if !val.Bool() {
			return BoolValue(false), nil // short-circuit
		}
	}
	return BoolValue(true), nil
}

// evalOr evaluates or(a, b, c, ...) - variadic OR with short-circuit.
func (e *Evaluator) evalOr(f *FunctionCall) (Value, error) {
	// Identity element: or() -> false
	if len(f.Args) == 0 {
		return BoolValue(false), nil
	}

	for _, arg := range f.Args {
		val, err := e.Eval(arg)
		if err != nil {
			return nil, err
		}
		if val.Bool() {
			return BoolValue(true), nil // short-circuit
		}
	}
	return BoolValue(false), nil
}

// evalNot evaluates not(a) - unary NOT.
func (e *Evaluator) evalNot(f *FunctionCall) (Value, error) {
	if len(f.Args) != 1 {
		return nil, &EvalError{
			Pos:     f.Pos(),
			Message: fmt.Sprintf("not() expects 1 argument, got %d", len(f.Args)),
		}
	}

	val, err := e.Eval(f.Args[0])
	if err != nil {
		return nil, err
	}
	return BoolValue(!val.Bool()), nil
}

// evalIn evaluates in(x, y, z, ...) - true if x equals any of y, z, ...
func (e *Evaluator) evalIn(f *FunctionCall) (Value, error) {
	if len(f.Args) < 2 {
		return nil, &EvalError{
			Pos:     f.Pos(),
			Message: fmt.Sprintf("in() expects at least 2 arguments, got %d", len(f.Args)),
		}
	}

	needle, err := e.Eval(f.Args[0])
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(f.Args); i++ {
		candidate, err := e.Eval(f.Args[i])
		if err != nil {
			return nil, err
		}
		if valuesEqual(needle, candidate) {
			return BoolValue(true), nil
		}
	}
	return BoolValue(false), nil
}

// evalRe evaluates re(pattern, value) - regex match.
func (e *Evaluator) evalRe(f *FunctionCall) (Value, error) {
	if len(f.Args) != 2 {
		return nil, &EvalError{
			Pos:     f.Pos(),
			Message: fmt.Sprintf("re() expects 2 arguments, got %d", len(f.Args)),
		}
	}

	patternVal, err := e.Eval(f.Args[0])
	if err != nil {
		return nil, err
	}
	pattern := patternVal.String()

	// Limit pattern length to prevent DoS
	if len(pattern) > MaxRegexPatternLength {
		return nil, &EvalError{
			Pos:     f.Args[0].Pos(),
			Message: fmt.Sprintf("regex pattern exceeds maximum length of %d characters", MaxRegexPatternLength),
		}
	}

	valueVal, err := e.Eval(f.Args[1])
	if err != nil {
		return nil, err
	}
	value := valueVal.String()

	// Compile regex (case-insensitive by default for convenience)
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, &EvalError{
			Pos:     f.Args[0].Pos(),
			Message: fmt.Sprintf("invalid regex pattern: %s", err),
		}
	}

	// Execute regex with timeout to prevent ReDoS
	matched, err := matchWithTimeout(re, value, RegexTimeout)
	if err != nil {
		return nil, &EvalError{
			Pos:     f.Args[0].Pos(),
			Message: fmt.Sprintf("regex evaluation failed: %s", err),
		}
	}

	return BoolValue(matched), nil
}

// matchWithTimeout executes a regex match with a timeout as a safety measure.
//
// Note on goroutine behavior: Go's regexp package does not support cancellation,
// so on timeout the spawned goroutine will continue until MatchString completes.
// The buffered channel prevents the goroutine from blocking on send. This is
// acceptable because:
//  1. Go's RE2-based regexp guarantees linear time complexity O(n*m), preventing
//     catastrophic backtracking that causes ReDoS in PCRE-based engines.
//  2. Pattern length is already limited to MaxRegexPatternLength (1000 chars).
//  3. Input strings (device attributes) are typically short (<256 chars).
//  4. The timeout is a safety net, not the primary defense against DoS.
//
// Under normal operation, regex matches complete well within the timeout and
// the goroutine exits promptly.
func matchWithTimeout(re *regexp.Regexp, s string, timeout time.Duration) (bool, error) {
	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- re.MatchString(s)
	}()

	select {
	case result := <-resultCh:
		return result, nil
	case <-time.After(timeout):
		return false, fmt.Errorf("regex evaluation timed out after %v", timeout)
	}
}

// evalComparison evaluates comparison functions: eq, ne, gt, ge, lt, le.
func (e *Evaluator) evalComparison(f *FunctionCall, op string) (Value, error) {
	if len(f.Args) != 2 {
		return nil, &EvalError{
			Pos:     f.Pos(),
			Message: fmt.Sprintf("%s() expects 2 arguments, got %d", op, len(f.Args)),
		}
	}

	left, err := e.Eval(f.Args[0])
	if err != nil {
		return nil, err
	}

	right, err := e.Eval(f.Args[1])
	if err != nil {
		return nil, err
	}

	// For equality/inequality, use value comparison
	if op == "eq" {
		return BoolValue(valuesEqual(left, right)), nil
	}
	if op == "ne" {
		return BoolValue(!valuesEqual(left, right)), nil
	}

	// For ordering comparisons, try numeric first
	cmp, err := compareValues(left, right)
	if err != nil {
		return nil, &EvalError{Pos: f.Pos(), Message: err.Error()}
	}

	switch op {
	case "gt":
		return BoolValue(cmp > 0), nil
	case "ge":
		return BoolValue(cmp >= 0), nil
	case "lt":
		return BoolValue(cmp < 0), nil
	case "le":
		return BoolValue(cmp <= 0), nil
	default:
		return nil, &EvalError{Pos: f.Pos(), Message: fmt.Sprintf("unknown comparison operator: %s", op)}
	}
}

// valuesEqual compares two values for equality.
func valuesEqual(a, b Value) bool {
	// If both are numbers, compare numerically
	if a.Type() == ValueTypeNumber && b.Type() == ValueTypeNumber {
		return a.Number() == b.Number()
	}

	// If both are bools, compare as bools
	if a.Type() == ValueTypeBool && b.Type() == ValueTypeBool {
		return a.Bool() == b.Bool()
	}

	// Otherwise, compare as strings (case-insensitive for flexibility)
	return strings.EqualFold(a.String(), b.String())
}

// compareValues compares two values and returns -1, 0, or 1.
func compareValues(a, b Value) (int, error) {
	// If both are numbers, compare numerically
	if a.Type() == ValueTypeNumber && b.Type() == ValueTypeNumber {
		an, bn := a.Number(), b.Number()
		if an < bn {
			return -1, nil
		}
		if an > bn {
			return 1, nil
		}
		return 0, nil
	}

	// If one is a number and the other is a string, try to parse the string
	if a.Type() == ValueTypeNumber || b.Type() == ValueTypeNumber {
		an := a.Number()
		bn := b.Number()

		// If one value is already a number, try to use the other's Number() conversion
		// Note: StringValue.Number() returns 0, so this only works if at least one is a number
		if a.Type() == ValueTypeNumber && b.Type() == ValueTypeString {
			// Try to parse b as a number with unit
			parsed, _, err := ParseNumberWithUnit(b.String())
			if err != nil {
				return 0, fmt.Errorf("cannot compare number with non-numeric string '%s'", b.String())
			}
			bn = parsed
		} else if b.Type() == ValueTypeNumber && a.Type() == ValueTypeString {
			// Try to parse a as a number with unit
			parsed, _, err := ParseNumberWithUnit(a.String())
			if err != nil {
				return 0, fmt.Errorf("cannot compare number with non-numeric string '%s'", a.String())
			}
			an = parsed
		}

		if an < bn {
			return -1, nil
		}
		if an > bn {
			return 1, nil
		}
		return 0, nil
	}

	// Compare as strings
	as, bs := a.String(), b.String()
	if as < bs {
		return -1, nil
	}
	if as > bs {
		return 1, nil
	}
	return 0, nil
}

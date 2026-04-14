package tracekit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
)

// ErrUnsupportedExpression indicates the expression requires server-side evaluation.
var ErrUnsupportedExpression = errors.New("unsupported expression: requires server-side evaluation")

// IsSDKEvaluable returns true if the expression can be evaluated locally by the SDK.
// Returns false for expressions containing function calls, regex operators, assignment,
// array indexing, ternary, range, template literals, or bitwise operators.
func IsSDKEvaluable(expression string) bool {
	// Function calls: word followed by opening paren
	if regexp.MustCompile(`\b[a-zA-Z_]\w*\s*\(`).MatchString(expression) {
		return false
	}

	// Regex match operator
	if regexp.MustCompile(`\bmatches\b`).MatchString(expression) {
		return false
	}

	// Regex operator =~
	if strings.Contains(expression, "=~") {
		return false
	}

	// Bitwise NOT ~  (but not inside =~)
	// Already handled =~ above, so check for standalone ~
	for i, ch := range expression {
		if ch == '~' && (i == 0 || expression[i-1] != '=') {
			return false
		}
	}

	// Bitwise AND: single & not part of &&
	for i := 0; i < len(expression); i++ {
		if expression[i] == '&' {
			if i+1 < len(expression) && expression[i+1] == '&' {
				i++ // skip &&
				continue
			}
			return false
		}
	}

	// Bitwise OR: single | not part of ||
	for i := 0; i < len(expression); i++ {
		if expression[i] == '|' {
			if i+1 < len(expression) && expression[i+1] == '|' {
				i++ // skip ||
				continue
			}
			return false
		}
	}

	// Bit shift
	if strings.Contains(expression, "<<") || strings.Contains(expression, ">>") {
		return false
	}

	// Template literals
	if strings.Contains(expression, "${") {
		return false
	}

	// Range operator
	if strings.Contains(expression, "..") {
		return false
	}

	// Ternary
	if strings.Contains(expression, "?") {
		return false
	}

	// Array indexing [N]
	if regexp.MustCompile(`\[\d`).MatchString(expression) {
		return false
	}

	// Compound assignment
	if regexp.MustCompile(`[+\-*/]=`).MatchString(expression) {
		return false
	}

	return true
}

// nilSafeMap wraps a map to provide safe property access on nil values.
// When a key resolves to nil or does not exist, nested access returns nil
// instead of panicking.
func prepareEnv(env map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(env))
	for k, v := range env {
		switch val := v.(type) {
		case map[string]interface{}:
			result[k] = prepareEnvNested(val)
		default:
			result[k] = v
		}
	}
	return result
}

// nilSafeMapType is a map that returns a further nilSafeMapType for any
// missing key, allowing chained property access on nil/missing values
// to resolve to nil instead of panicking.
type nilSafeMapType map[string]interface{}

func prepareEnvNested(m map[string]interface{}) nilSafeMapType {
	if m == nil {
		return nilSafeMapType{}
	}
	result := make(nilSafeMapType, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			result[k] = prepareEnvNested(val)
		case nil:
			// Store a sentinel nilSafeMapType so that further .property access
			// returns nil instead of erroring
			result[k] = nilSafeMapType(nil)
		default:
			result[k] = v
			_ = val
		}
	}
	return result
}

// isNilAccessError returns true if the error is a "cannot fetch X from nil" error
// from expr-lang, which we treat as nil result (safe nil-chaining behavior).
func isNilAccessError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "from <nil>")
}

// safeEval wraps expr.Eval, passing through all errors including nil-access
// errors for the caller to handle contextually.
func safeEval(expression string, env map[string]interface{}) (interface{}, error) {
	return expr.Eval(expression, env)
}

// EvaluateCondition evaluates an expression string against the given environment
// and returns a boolean result. Empty expressions return true (no condition = always fire).
// Returns ErrUnsupportedExpression for expressions that require server-side evaluation.
func EvaluateCondition(expression string, env map[string]interface{}) (bool, error) {
	if expression == "" {
		return true, nil
	}

	if !IsSDKEvaluable(expression) {
		return false, ErrUnsupportedExpression
	}

	safeEnv := prepareEnv(env)
	result, err := safeEval(expression, safeEnv)
	if err != nil {
		// If we got a nil-access error in a comparison expression,
		// the nil-accessed side is effectively nil.
		if isNilAccessError(err) {
			if strings.Contains(expression, "== nil") {
				return true, nil
			}
			if strings.Contains(expression, "!= nil") {
				return false, nil
			}
			// For other expressions involving nil access (e.g. arithmetic),
			// the condition is false.
			return false, nil
		}
		return false, fmt.Errorf("expression evaluation failed: %w", err)
	}

	switch v := result.(type) {
	case bool:
		return v, nil
	case nil:
		// nil result from nil-safe property access; treat as false for conditions
		return false, nil
	default:
		return false, fmt.Errorf("condition must evaluate to bool, got %T", result)
	}
}

// EvaluateExpression evaluates an expression and returns the raw result.
// Unlike EvaluateCondition, this does not require a boolean result.
// Returns ErrUnsupportedExpression for server-only expressions.
func EvaluateExpression(expression string, env map[string]interface{}) (interface{}, error) {
	if expression == "" {
		return nil, nil
	}

	if !IsSDKEvaluable(expression) {
		return nil, ErrUnsupportedExpression
	}

	safeEnv := prepareEnv(env)
	result, err := safeEval(expression, safeEnv)
	if err != nil {
		// Nil-access errors mean the property path resolved to nil
		if isNilAccessError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("expression evaluation failed: %w", err)
	}

	return result, nil
}

// EvaluateExpressions evaluates multiple expressions against the given environment.
// Results are keyed by expression string. On error, nil is stored for that expression.
func EvaluateExpressions(expressions []string, env map[string]interface{}) map[string]interface{} {
	results := make(map[string]interface{}, len(expressions))
	for _, expression := range expressions {
		result, err := EvaluateExpression(expression, env)
		if err != nil {
			results[expression] = nil
		} else {
			results[expression] = result
		}
	}
	return results
}

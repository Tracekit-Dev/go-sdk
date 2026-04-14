package tracekit

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

type fixtureFile struct {
	SpecVersion      string                 `json:"spec_version"`
	DefaultVariables map[string]interface{} `json:"default_variables"`
	TestCases        []fixtureCase          `json:"test_cases"`
}

type fixtureCase struct {
	ID          string                 `json:"id"`
	Category    string                 `json:"category"`
	Description string                 `json:"description"`
	Expression  string                 `json:"expression"`
	Variables   map[string]interface{} `json:"variables"`
	Expected    interface{}            `json:"expected"`
	Classify    string                 `json:"classify"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	data, err := os.ReadFile("../testdata/expression_fixtures.json")
	if err != nil {
		t.Fatalf("failed to read fixtures: %v", err)
	}
	var f fixtureFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("failed to parse fixtures: %v", err)
	}
	return f
}

func TestExpressionFixtures(t *testing.T) {
	fixtures := loadFixtures(t)

	for _, tc := range fixtures.TestCases {
		tc := tc
		t.Run(tc.ID+"_"+tc.Description, func(t *testing.T) {
			env := fixtures.DefaultVariables
			if tc.Variables != nil {
				env = tc.Variables
			}

			if tc.Classify == "server-only" {
				// Server-only expressions should be classified as not SDK-evaluable
				if IsSDKEvaluable(tc.Expression) {
					t.Errorf("expected IsSDKEvaluable(%q) = false for server-only expression", tc.Expression)
				}
				return
			}

			// Expected can be bool, number, or string
			switch expected := tc.Expected.(type) {
			case bool:
				// Boolean expected: use EvaluateCondition
				result, err := EvaluateCondition(tc.Expression, env)
				if err != nil {
					t.Fatalf("EvaluateCondition(%q) returned error: %v", tc.Expression, err)
				}
				if result != expected {
					t.Errorf("EvaluateCondition(%q) = %v, want %v", tc.Expression, result, expected)
				}
			case float64:
				// Numeric expected: use EvaluateExpression (returns raw value)
				exprResult, exprErr := EvaluateExpression(tc.Expression, env)
				if exprErr != nil {
					t.Fatalf("EvaluateExpression(%q) returned error: %v", tc.Expression, exprErr)
				}
				switch v := exprResult.(type) {
				case int:
					if float64(v) != expected {
						t.Errorf("EvaluateExpression(%q) = %v, want %v", tc.Expression, v, expected)
					}
				case float64:
					if v != expected {
						t.Errorf("EvaluateExpression(%q) = %v, want %v", tc.Expression, v, expected)
					}
				default:
					t.Errorf("EvaluateExpression(%q) returned %T(%v), want numeric %v", tc.Expression, exprResult, exprResult, expected)
				}
			case string:
				// String expected: use EvaluateExpression (returns raw value)
				exprResult, exprErr := EvaluateExpression(tc.Expression, env)
				if exprErr != nil {
					t.Fatalf("EvaluateExpression(%q) returned error: %v", tc.Expression, exprErr)
				}
				if str, ok := exprResult.(string); !ok || str != expected {
					t.Errorf("EvaluateExpression(%q) = %v, want %q", tc.Expression, exprResult, expected)
				}
			default:
				t.Fatalf("unexpected expected type %T for test case %s", tc.Expected, tc.ID)
			}
		})
	}
}

func TestEmptyCondition(t *testing.T) {
	env := map[string]interface{}{"status": 200}
	result, err := EvaluateCondition("", env)
	if err != nil {
		t.Fatalf("EvaluateCondition(\"\") returned error: %v", err)
	}
	if !result {
		t.Error("EvaluateCondition(\"\") = false, want true")
	}
}

func TestMalformedExpression(t *testing.T) {
	env := map[string]interface{}{"status": 200}
	_, err := EvaluateCondition("!!!", env)
	if err == nil {
		t.Error("EvaluateCondition(\"!!!\") expected error, got nil")
	}
}

func TestNilVariableAccess(t *testing.T) {
	env := map[string]interface{}{"user": nil}
	// Should not panic
	_, err := EvaluateCondition("user.settings == nil", env)
	// This may return an error or true, but should not panic
	_ = err
}

func TestUnsupportedExpressionError(t *testing.T) {
	env := map[string]interface{}{"x": "hello"}
	evaluable := IsSDKEvaluable("len(x) > 3")
	if evaluable {
		t.Error("IsSDKEvaluable should return false for function call expressions")
	}

	_, err := EvaluateCondition("len(x) > 3", env)
	if err == nil {
		t.Fatal("expected error for function call expression")
	}
	if !errors.Is(err, ErrUnsupportedExpression) {
		t.Errorf("expected ErrUnsupportedExpression, got: %v", err)
	}
}

func TestEvaluateExpressions(t *testing.T) {
	env := map[string]interface{}{
		"status": 200,
		"method": "GET",
	}
	results := EvaluateExpressions([]string{"status", "method", "nonexistent()"}, env)
	if results["status"] == nil {
		t.Error("expected non-nil result for 'status'")
	}
	if results["method"] == nil {
		t.Error("expected non-nil result for 'method'")
	}
	// function call should return nil (error case)
	if results["nonexistent()"] != nil {
		t.Error("expected nil result for unsupported expression")
	}
}

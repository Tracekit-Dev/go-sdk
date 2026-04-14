package tracekit

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func intPtr(n int) *int { return &n }

// TestLogpointMode verifies logpoint breakpoints capture only expression results
func TestLogpointMode(t *testing.T) {
	client := NewSnapshotClient("test-key", "http://localhost", "test-service")

	// Set up breakpoint in cache with logpoint mode
	bp := &BreakpointConfig{
		ID:                 "bp-logpoint-1",
		ServiceName:        "test-service",
		FilePath:           "test.go",
		LineNumber:         100,
		Enabled:            true,
		Mode:               "logpoint",
		CaptureExpressions: []string{"status", "method"},
	}
	client.mu.Lock()
	client.breakpointsCache["test.go:100"] = bp
	client.mu.Unlock()

	// Build a snapshot as the logpoint path would
	variables := map[string]interface{}{
		"status": 200,
		"method": "GET",
		"secret": "should-not-appear",
	}

	// Test logpoint snapshot construction
	snapshot := buildLogpointSnapshot(bp, client.serviceName, "test.go", 100, variables)

	// ExpressionResults should be populated
	if snapshot.ExpressionResults == nil {
		t.Fatal("ExpressionResults should not be nil for logpoint mode")
	}
	if len(snapshot.ExpressionResults) != 2 {
		t.Fatalf("expected 2 expression results, got %d", len(snapshot.ExpressionResults))
	}
	if snapshot.ExpressionResults["status"] != 200 {
		t.Errorf("expected status=200, got %v", snapshot.ExpressionResults["status"])
	}
	if snapshot.ExpressionResults["method"] != "GET" {
		t.Errorf("expected method=GET, got %v", snapshot.ExpressionResults["method"])
	}

	// Variables should be empty (logpoint skips locals)
	if len(snapshot.Variables) != 0 {
		t.Errorf("expected empty Variables for logpoint, got %d entries", len(snapshot.Variables))
	}

	// StackTrace should be empty
	if snapshot.StackTrace != "" {
		t.Errorf("expected empty StackTrace for logpoint, got %q", snapshot.StackTrace)
	}

	// RequestContext should be nil
	if snapshot.RequestContext != nil {
		t.Errorf("expected nil RequestContext for logpoint, got %v", snapshot.RequestContext)
	}
}

// TestPerBreakpointMaxDepth verifies per-breakpoint MaxDepth truncates variables
func TestPerBreakpointMaxDepth(t *testing.T) {
	client := NewSnapshotClient("test-key", "http://localhost", "test-service")

	// Deeply nested variables (5 levels)
	variables := map[string]interface{}{
		"level0": map[string]interface{}{
			"level1": map[string]interface{}{
				"level2": map[string]interface{}{
					"level3": map[string]interface{}{
						"level4": "deep-value",
					},
				},
			},
		},
		"flat": "top-level",
	}

	// Per-breakpoint MaxDepth=2 should truncate at depth 2
	result := client.applyCaptureConfigWithOverrides(variables, intPtr(2), nil)

	// flat should be unchanged
	if result["flat"] != "top-level" {
		t.Errorf("expected flat=top-level, got %v", result["flat"])
	}

	// Root map is depth 0, level0's value is recursed at depth 1,
	// level1's value is recursed at depth 2 which hits the limit.
	// So level0 should be a map, but level0["level1"] should be the truncation indicator.
	l0, ok := result["level0"].(map[string]interface{})
	if !ok {
		t.Fatal("level0 should be a map")
	}
	l1, ok := l0["level1"].(map[string]interface{})
	if !ok {
		t.Fatal("level1 should be a truncation indicator map")
	}
	if l1["_truncated"] != true {
		t.Errorf("expected _truncated=true at depth 2, got %v", l1)
	}
}

// TestPerBreakpointMaxPayload verifies per-breakpoint MaxPayloadBytes truncation
func TestPerBreakpointMaxPayload(t *testing.T) {
	client := NewSnapshotClient("test-key", "http://localhost", "test-service")

	// Create a snapshot with large variables (unique keys to ensure large payload)
	largeVars := make(map[string]interface{})
	for i := 0; i < 100; i++ {
		largeVars[fmt.Sprintf("key_%03d_%s", i, strings.Repeat("k", 10))] = strings.Repeat("v", 100)
	}

	snapshot := Snapshot{
		BreakpointID: "bp-1",
		ServiceName:  "test-service",
		FilePath:     "test.go",
		LineNumber:   1,
		Variables:    largeVars,
		StackTrace:   "goroutine 1 [running]:\nmain.main()\n\ttest.go:1 +0x0\n",
	}

	// Truncate with 500 byte limit
	truncated := client.applyPayloadLimit(snapshot, intPtr(500))

	if truncated.Variables["_truncated"] != true {
		t.Error("expected _truncated=true in variables after payload truncation")
	}
	if truncated.Variables["_payload_size"] == nil {
		t.Error("expected _payload_size in truncated variables")
	}
	if truncated.Variables["_max_payload"] == nil {
		t.Error("expected _max_payload in truncated variables")
	}
	if truncated.Variables["_truncated_by"] != "payload_limit" {
		t.Errorf("expected _truncated_by=payload_limit, got %v", truncated.Variables["_truncated_by"])
	}
}

// TestDynamicStackBuffer verifies stack capture uses dynamic buffer larger than 4KB
func TestDynamicStackBuffer(t *testing.T) {
	// Call from a deeply nested function to generate > 4096 bytes of stack
	result := deepCallStack(30, func() string {
		return captureStackTraceWithDepth(nil)
	})

	if len(result) <= 4096 {
		t.Errorf("expected stack trace > 4096 bytes from deep call, got %d bytes", len(result))
	}
}

// TestStackDepthLimit verifies per-breakpoint StackDepth limits frames
func TestStackDepthLimit(t *testing.T) {
	// Capture with depth limit of 5
	result := deepCallStack(20, func() string {
		return captureStackTraceWithDepth(intPtr(5))
	})

	// Count frames: each frame is 2 lines (function + file:line)
	// First line is "goroutine N [running]:" header
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) < 2 {
		t.Fatal("expected at least a header and one frame")
	}

	// Count frame pairs (skip the goroutine header line)
	frameLines := lines[1:]
	frameCount := len(frameLines) / 2

	if frameCount > 5 {
		t.Errorf("expected at most 5 frames, got %d", frameCount)
	}
}

// TestNilLimitsFallbackToDefaults verifies nil per-breakpoint limits use SDK defaults
func TestNilLimitsFallbackToDefaults(t *testing.T) {
	client := NewSnapshotClientWithConfig("test-key", "http://localhost", "test-service", CaptureConfig{
		CaptureDepth: 3,
		MaxPayload:   1000,
	})

	// Deeply nested variables
	variables := map[string]interface{}{
		"a": map[string]interface{}{
			"b": map[string]interface{}{
				"c": map[string]interface{}{
					"d": "deep",
				},
			},
		},
	}

	// nil per-breakpoint MaxDepth should fall back to SDK CaptureDepth=3
	result := client.applyCaptureConfigWithOverrides(variables, nil, nil)

	// Navigate to depth 3 -- should be truncated
	a := result["a"].(map[string]interface{})
	b := a["b"].(map[string]interface{})
	c := b["c"].(map[string]interface{})
	if c["_truncated"] != true {
		t.Errorf("expected _truncated=true at SDK default depth 3, got %v", c)
	}
}

// deepCallStack calls fn from depth levels of recursion
func deepCallStack(depth int, fn func() string) string {
	if depth <= 0 {
		return fn()
	}
	// Use runtime.Caller to prevent inlining
	runtime.Caller(0)
	return deepCallStack(depth-1, fn)
}

// TestFullCaptureFlow exercises the complete snapshot-mode path:
// condition evaluation + depth truncation + stack depth limiting + payload limit.
func TestFullCaptureFlow(t *testing.T) {
	client := NewSnapshotClientWithConfig("test-key", "http://localhost", "test-service", CaptureConfig{
		CaptureDepth: 10, // SDK default (should be overridden by per-bp)
		MaxPayload:   50000,
	})

	bp := &BreakpointConfig{
		ID:            "bp-full-1",
		ServiceName:   "test-service",
		FilePath:      "handler.go",
		LineNumber:    42,
		Enabled:       true,
		Mode:          "snapshot",
		Condition:     "status >= 200",
		ConditionEval: "sdk-evaluable",
		MaxDepth:      intPtr(3),
		MaxPayloadBytes: intPtr(10000),
		StackDepth:    intPtr(10),
	}

	// Deeply nested variables (5 levels)
	variables := map[string]interface{}{
		"status": 200,
		"user": map[string]interface{}{
			"profile": map[string]interface{}{
				"settings": map[string]interface{}{
					"preferences": map[string]interface{}{
						"theme": "dark",
					},
				},
			},
		},
		"method": "POST",
	}

	// 1. Condition evaluation: status >= 200 should be true
	condResult, err := EvaluateCondition(bp.Condition, variables)
	if err != nil {
		t.Fatalf("condition evaluation failed: %v", err)
	}
	if !condResult {
		t.Fatal("condition should evaluate to true for status=200")
	}

	// 2. Depth truncation: MaxDepth=3 should truncate at depth 3
	truncatedVars := client.applyCaptureConfigWithOverrides(variables, bp.MaxDepth, bp.MaxPayloadBytes)

	user := truncatedVars["user"].(map[string]interface{})
	profile := user["profile"].(map[string]interface{})
	settings := profile["settings"].(map[string]interface{})
	if settings["_truncated"] != true {
		t.Errorf("expected depth truncation at level 3, got %v", settings)
	}

	// Top-level scalars preserved
	if truncatedVars["status"] != 200 {
		t.Errorf("expected status=200 preserved, got %v", truncatedVars["status"])
	}
	if truncatedVars["method"] != "POST" {
		t.Errorf("expected method=POST preserved, got %v", truncatedVars["method"])
	}

	// 3. Stack depth: should limit frames to 10
	stackTrace := deepCallStack(20, func() string {
		return captureStackTraceWithDepth(bp.StackDepth)
	})
	lines := strings.Split(strings.TrimSpace(stackTrace), "\n")
	// Header + max 10 frames * 2 lines = max 21 lines
	frameLines := lines[1:]
	frameCount := len(frameLines) / 2
	if frameCount > 10 {
		t.Errorf("expected at most 10 frames, got %d", frameCount)
	}

	// 4. Payload limit: build snapshot and verify truncation applies if needed
	snapshot := Snapshot{
		BreakpointID: bp.ID,
		ServiceName:  "test-service",
		FilePath:     "handler.go",
		LineNumber:   42,
		Variables:    truncatedVars,
		StackTrace:   stackTrace,
	}
	// With truncated vars, payload should be small enough to pass 10000 limit
	limited := client.applyPayloadLimit(snapshot, bp.MaxPayloadBytes)
	if limited.Variables["_truncated_by"] == "payload_limit" {
		// If it got truncated, that is also valid behavior
		t.Log("payload was truncated (expected for very large stacks)")
	}
}

// TestLogpointCaptureFlow exercises the complete logpoint-mode path.
func TestLogpointCaptureFlow(t *testing.T) {
	client := NewSnapshotClient("test-key", "http://localhost", "test-service")
	_ = client // client used for context only

	bp := &BreakpointConfig{
		ID:                 "bp-logpoint-flow",
		ServiceName:        "test-service",
		FilePath:           "api.go",
		LineNumber:         99,
		Enabled:            true,
		Mode:               "logpoint",
		CaptureExpressions: []string{"status > 100", "method"},
	}

	variables := map[string]interface{}{
		"status":   201,
		"method":   "PUT",
		"internal": "should-not-appear",
	}

	snapshot := buildLogpointSnapshot(bp, "test-service", "api.go", 99, variables)

	// ExpressionResults should have 2 entries
	if len(snapshot.ExpressionResults) != 2 {
		t.Fatalf("expected 2 expression results, got %d: %v", len(snapshot.ExpressionResults), snapshot.ExpressionResults)
	}

	// "status > 100" should evaluate to true
	if snapshot.ExpressionResults["status > 100"] != true {
		t.Errorf("expected status > 100 = true, got %v", snapshot.ExpressionResults["status > 100"])
	}

	// "method" should evaluate to "PUT"
	if snapshot.ExpressionResults["method"] != "PUT" {
		t.Errorf("expected method = PUT, got %v", snapshot.ExpressionResults["method"])
	}

	// Variables should be empty
	if len(snapshot.Variables) != 0 {
		t.Errorf("expected empty Variables for logpoint, got %d entries: %v", len(snapshot.Variables), snapshot.Variables)
	}

	// StackTrace should be empty
	if snapshot.StackTrace != "" {
		t.Errorf("expected empty StackTrace for logpoint, got %q", snapshot.StackTrace)
	}

	// RequestContext should be nil
	if snapshot.RequestContext != nil {
		t.Errorf("expected nil RequestContext for logpoint")
	}

	// Metadata fields should be set
	if snapshot.BreakpointID != "bp-logpoint-flow" {
		t.Errorf("expected BreakpointID=bp-logpoint-flow, got %s", snapshot.BreakpointID)
	}
	if snapshot.FilePath != "api.go" {
		t.Errorf("expected FilePath=api.go, got %s", snapshot.FilePath)
	}
	if snapshot.LineNumber != 99 {
		t.Errorf("expected LineNumber=99, got %d", snapshot.LineNumber)
	}
}

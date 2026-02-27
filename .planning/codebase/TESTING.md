# Testing Patterns

**Analysis Date:** 2026-02-27

## Test Framework

**Runner:**
- Go standard testing (built-in `testing` package)
- Version: Go 1.23.0 (from `go.mod`)
- Config: `go test ./...` standard command

**Assertion Library:**
- None used - simple `if got != want` manual assertions
- No test framework dependencies (testify, convey, etc.)

**Run Commands:**
```bash
go test ./...              # Run all tests
go test ./... -v           # Verbose output
go test ./... -run TestName # Run specific test
go test -cover ./...       # Show coverage
```

## Test File Organization

**Location:**
- Co-located with source files (same directory)
- Pattern: `<source>_test.go`
- Example: `config.go` has `config_test.go` in same `tracekit/` directory

**Naming:**
- Test functions: `Test<FunctionName>` prefix
- Table-driven tests: test cases in slice of structs named `tests`
- Sub-tests: `t.Run(tt.name, ...)`

**Current Test Coverage:**
- Location: `tracekit/config_test.go`
- Only configuration/endpoint resolution tests present
- No tests for HTTP client, span recording, metrics, or snapshot capture

## Test Structure

**Suite Organization:**

```go
// From config_test.go - typical table-driven test structure
func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		path     string
		useSSL   bool
		want     string
	}{
		{
			name:     "just host with SSL",
			endpoint: "app.tracekit.dev",
			path:     "/v1/traces",
			useSSL:   true,
			want:     "https://app.tracekit.dev/v1/traces",
		},
		// ... more test cases
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEndpoint(tt.endpoint, tt.path, tt.useSSL)
			if got != tt.want {
				t.Errorf("resolveEndpoint(%q, %q, %v) = %q; want %q",
					tt.endpoint, tt.path, tt.useSSL, got, tt.want)
			}
		})
	}
}
```

**Patterns:**

- **Setup**: Tests are self-contained; no shared setup/teardown
- **Teardown**: Not needed; no resource cleanup (no file handles, DB connections, etc.)
- **Assertion**: Direct comparison with `if got != tt.want` followed by `t.Errorf()`
- **Error reporting**: `t.Errorf()` with formatted message showing actual vs expected

## Test Examples

### Endpoint Resolution Tests (from config_test.go)

```go
func TestEndpointResolution(t *testing.T) {
	tests := []struct {
		name            string
		config          *Config
		wantTraces      string
		wantMetrics     string
		wantSnapshots   string
	}{
		{
			name: "default production config",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "app.tracekit.dev",
				UseSSL:      true,
			},
			wantTraces:    "https://app.tracekit.dev/v1/traces",
			wantMetrics:   "https://app.tracekit.dev/v1/metrics",
			wantSnapshots: "https://app.tracekit.dev",
		},
		{
			name: "local development",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "localhost:8080",
				UseSSL:      false,
			},
			wantTraces:    "http://localhost:8080/v1/traces",
			wantMetrics:   "http://localhost:8080/v1/metrics",
			wantSnapshots: "http://localhost:8080",
		},
		// ... more cases
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set defaults like NewSDK does
			if tt.config.TracesPath == "" {
				tt.config.TracesPath = "/v1/traces"
			}
			if tt.config.MetricsPath == "" {
				tt.config.MetricsPath = "/v1/metrics"
			}

			// Resolve endpoints
			tracesEndpoint := resolveEndpoint(tt.config.Endpoint, tt.config.TracesPath, tt.config.UseSSL)
			metricsEndpoint := resolveEndpoint(tt.config.Endpoint, tt.config.MetricsPath, tt.config.UseSSL)
			snapshotEndpoint := resolveEndpoint(tt.config.Endpoint, "", tt.config.UseSSL)

			if tracesEndpoint != tt.wantTraces {
				t.Errorf("traces endpoint = %q; want %q", tracesEndpoint, tt.wantTraces)
			}
			if metricsEndpoint != tt.wantMetrics {
				t.Errorf("metrics endpoint = %q; want %q", metricsEndpoint, tt.wantMetrics)
			}
			if snapshotEndpoint != tt.wantSnapshots {
				t.Errorf("snapshots endpoint = %q; want %q", snapshotEndpoint, tt.wantSnapshots)
			}
		})
	}
}
```

## Mocking

**Framework:**
- No mocking framework used in current tests
- Manual construction of test fixtures preferred
- Potential for future mocking: interfaces like `Counter`, `Gauge`, `Histogram` could use mock implementations

**Patterns:**

Since no mocks currently exist, but interfaces are designed mockable:

```go
// Interface from metrics.go
type Counter interface {
	Inc()
	Add(value float64)
}

// Could be mocked by implementing the interface
type mockCounter struct {
	calls []string
}

func (m *mockCounter) Inc() {
	m.calls = append(m.calls, "Inc")
}

func (m *mockCounter) Add(value float64) {
	m.calls = append(m.calls, fmt.Sprintf("Add(%f)", value))
}
```

**What to Mock (recommendations for future tests):**
- HTTP clients: use `*http.Client` with test server
- OpenTelemetry tracer: use `trace.NoopTracer()` or test implementations
- Database connections: use in-memory SQLite for TracedDB tests
- GORM instance: use test database

**What NOT to Mock:**
- Context objects
- Data structures being tested (Config, SnapshotClient)
- Simple utility functions (extractServiceName, extractClientIP)

## Fixtures and Factories

**Test Data:**

Currently no shared fixtures. Test data constructed inline:

```go
// From config_test.go - inline fixture construction
config: &Config{
	APIKey:      "test-key",
	ServiceName: "test-service",
	Endpoint:    "app.tracekit.dev",
	UseSSL:      true,
}
```

**Recommended Factory Pattern (not yet used):**

```go
// Could create helper functions for common test configs
func newTestConfig(apiKey, serviceName string) *Config {
	return &Config{
		APIKey:         apiKey,
		ServiceName:    serviceName,
		Endpoint:       "localhost:8080",
		UseSSL:         false,
		SamplingRate:   1.0,
		BatchTimeout:   5 * time.Second,
	}
}
```

**Location:**
- Not extracted yet; could be in `config_test.go` helpers section
- Recommend: `_test.go` file helpers or separate `testdata/` directory if complex

## Coverage

**Requirements:**
- No explicit coverage target enforced
- Current coverage: Low - only config endpoint resolution tested

**View Coverage:**
```bash
go test ./... -cover              # Summary
go test ./... -coverprofile=c.out # Detailed report
go tool cover -html=c.out         # HTML visualization
```

## Test Types

**Unit Tests:**
- Scope: Single function/method in isolation
- Approach: Table-driven tests with multiple input combinations
- Example: `TestResolveEndpoint` tests 12+ combinations of endpoint/path/SSL

**Integration Tests:**
- Not present currently
- Recommended for: HTTP handlers, database operations, metrics export
- Would test: SDK initialization, tracer provider setup, GORM plugin callbacks

**E2E Tests:**
- Not used
- Would require: Running local TraceKit server, actual HTTP clients

## Common Patterns

**Async Testing:**
Not currently tested, but recommended pattern for goroutine operations:

```go
// For testing non-blocking operations (snapshots, metrics export)
done := make(chan struct{})
go func() {
	// operation that should complete
	defer close(done)
}()

select {
case <-done:
	// Success - operation completed
case <-time.After(100 * time.Millisecond):
	t.Fatalf("operation timeout")
}
```

**Error Testing:**

Currently no error cases tested. Recommended pattern:

```go
func TestCheckAndCaptureNoBreakpoint(t *testing.T) {
	client := NewSnapshotClient("key", "http://localhost", "test-service")

	// Should not panic or error when no breakpoint exists
	client.CheckAndCapture("file.go", 10, map[string]interface{}{})

	// Verify: no snapshot sent (would require mocking HTTP client)
}
```

**Table-Driven Test Assertions:**

Pattern from existing tests - multiple assertions per case:

```go
for _, tt := range tests {
	t.Run(tt.name, func(t *testing.T) {
		// Test setup
		tracesEndpoint := resolveEndpoint(tt.config.Endpoint, tt.config.TracesPath, tt.config.UseSSL)
		metricsEndpoint := resolveEndpoint(tt.config.Endpoint, tt.config.MetricsPath, tt.config.UseSSL)
		snapshotEndpoint := resolveEndpoint(tt.config.Endpoint, "", tt.config.UseSSL)

		// Multiple independent assertions
		if tracesEndpoint != tt.wantTraces {
			t.Errorf("traces endpoint = %q; want %q", tracesEndpoint, tt.wantTraces)
		}
		if metricsEndpoint != tt.wantMetrics {
			t.Errorf("metrics endpoint = %q; want %q", metricsEndpoint, tt.wantMetrics)
		}
		if snapshotEndpoint != tt.wantSnapshots {
			t.Errorf("snapshots endpoint = %q; want %q", snapshotEndpoint, tt.wantSnapshots)
		}
	})
}
```

## Current Test Coverage by Module

| Module | File | Tests | Coverage |
|--------|------|-------|----------|
| Config | `config_test.go` | 2 test functions (24 test cases) | Endpoint resolution |
| HTTP | `http.go` | None | Not tested |
| Span | `span.go` | None | Not tested |
| Metrics | `metrics.go` | None | Not tested |
| Snapshot | `client.go` | None | Not tested |
| Database | `database.go` | None | Not tested |
| GORM | `gorm.go` | None | Not tested |
| Gin | `gin.go` | None | Not tested |
| Echo | `echo.go` | None | Not tested |

## Recommendations for Additional Tests

**High Priority:**
1. **Snapshot Capture Tests** (`client.go`):
   - Test breakpoint matching logic (label vs line-based)
   - Test security scanning and redaction
   - Test cache updates and expiration

2. **HTTP Instrumentation Tests** (`http.go`):
   - Test client IP extraction from various headers
   - Test service name extraction and mapping
   - Test middleware wrapping

3. **Metrics Tests** (`metrics.go`):
   - Test metric creation and registration
   - Test double-checked locking in registry
   - Test noop implementations for disabled metrics

**Medium Priority:**
4. **Span Recording Tests** (`span.go`):
   - Test error recording with stack traces
   - Test attribute addition patterns
   - Test business attribute type conversions

5. **Database Wrapper Tests** (`database.go`):
   - Test query tracing and attribute recording
   - Test error handling for failed queries
   - Use in-memory SQLite for actual execution

**Nice to Have:**
6. Integration tests with actual OpenTelemetry exporters
7. E2E tests with local TraceKit server
8. Benchmark tests for hot paths (metric recording, snapshot capture)

---

*Testing analysis: 2026-02-27*

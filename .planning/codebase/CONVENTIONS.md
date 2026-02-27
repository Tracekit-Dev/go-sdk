# Coding Conventions

**Analysis Date:** 2026-02-27

## Naming Patterns

**Files:**
- Package files: lowercase with underscores (e.g., `config.go`, `metrics_buffer.go`, `metrics_exporter.go`)
- No capital letters or camelCase in filenames
- Test files: `<name>_test.go` suffix (e.g., `config_test.go`)
- Related functionality: grouped by feature (e.g., all metrics files: `metrics.go`, `metrics_buffer.go`, `metrics_exporter.go`)

**Functions:**
- Public functions: PascalCase (e.g., `NewSnapshotClient`, `StartSpan`, `CheckAndCapture`, `HTTPHandler`)
- Private functions: camelCase (e.g., `extractServiceName`, `scanForSecurityIssues`, `captureStackTrace`)
- Method receivers: single letter (e.g., `(s *SDK)`, `(c *SnapshotClient)`, `(p *gormPlugin)`)
- Constructor functions: `New<TypeName>` prefix (e.g., `NewSDK`, `NewSnapshotClient`, `newMetricsRegistry`)

**Variables:**
- Local variables: camelCase (e.g., `tracesEndpoint`, `serviceName`, `clientIP`)
- Constants: PascalCase or UPPER_SNAKE_CASE for exported, camelCase for private
  - Example exported: `requestContextKey` (exported string constants)
  - Example private: `maxStackSize` (const in function)
- Package-level vars/consts: PascalCase if exported, camelCase if private

**Types:**
- Structs: PascalCase (e.g., `SnapshotClient`, `Config`, `SDK`, `BreakpointConfig`)
- Interfaces: PascalCase (e.g., `Counter`, `Gauge`, `Histogram`)
- Private implementation structs: camelCase (e.g., `counter`, `gauge`, `gormPlugin`, `localUISpanProcessor`)
- Type aliases for context keys: custom type (e.g., `type contextKey string`)

## Code Style

**Formatting:**
- Go standard format (enforced by `gofmt`)
- 4-space indentation (Go default via tabs)
- Line length: no hard limit, but code is generally kept concise
- No semicolons at end of statements

**Linting:**
- Default Go linter rules apply
- No explicit `.golangci.yml` found - follows Go conventions
- Code follows idiomatic Go patterns

## Import Organization

**Order:**
1. Standard library imports (e.g., `"context"`, `"net/http"`, `"time"`)
2. Third-party imports (e.g., `"go.opentelemetry.io/..."`, `"gorm.io/..."`, `"github.com/..."`)
3. No relative imports - uses absolute module paths

**Example from `config.go`:**
```go
import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	// ... more OpenTelemetry imports
	"go.opentelemetry.io/otel/trace"
)
```

**Path Aliases:**
- Not used in this codebase
- Full module paths: `github.com/Tracekit-Dev/go-sdk/tracekit`

## Error Handling

**Patterns:**
- Explicit error returns: functions return `error` as last return value
- Error checking: `if err != nil { return err }` pattern throughout
- Wrapped errors: `fmt.Errorf("message: %w", err)` for context preservation
- Silent failures in goroutines: error logged via `log.Printf()` when async operations fail

**Examples:**
```go
// From config.go - explicit error check with wrapping
if err := sdk.initTracer(tracesEndpoint); err != nil {
	return nil, fmt.Errorf("failed to initialize tracer: %w", err)
}

// From client.go - silent failure in async operation
go func() {
	url := fmt.Sprintf("%s/sdk/snapshots/auto-register", c.baseURL)
	// ... code
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return  // Silent failure
	}
}()

// From span.go - error recording with context
if err != nil {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
```

## Logging

**Framework:** Standard `"log"` package

**Patterns:**
- `log.Printf()` for formatted output with context
- `log.Println()` for simple messages
- Emoji prefixes for visual categorization:
  - `✅` for success/initialization
  - `⚠️` for warnings
  - `📸` for snapshot operations
  - `🔍` for debug/discovery operations

**Examples from codebase:**
```go
// From client.go
log.Printf("📸 TraceKit Snapshot Client started for service: %s", c.serviceName)
log.Printf("⚠️  Failed to fetch breakpoints: %v", err)
log.Printf("📸 Snapshot captured: %s:%d", snapshot.FilePath, snapshot.LineNumber)

// From config.go
log.Printf("✅ TraceKit SDK initialized for service: %s", config.ServiceName)
log.Println("🔍 Local UI detected at http://localhost:9999")
```

## Comments

**When to Comment:**
- Comment exported functions and types (go doc convention)
- Explain WHY, not WHAT - code structure is self-evident
- Use comments for non-obvious logic, business rules, or workarounds
- Mark temporary limitations with inline comments

**Example patterns:**
```go
// CheckAndCapture checks if there's an active breakpoint at this location and captures a snapshot
func (c *SnapshotClient) CheckAndCapture(filePath string, lineNumber int, variables map[string]interface{}) {
	// Check cache for matching breakpoint
	key := fmt.Sprintf("%s:%d", filePath, lineNumber)
	bp, exists := c.breakpointsCache[key]

	if !exists {
		return // No active breakpoint at this location
	}

	// TODO: Extract trace/span ID from context if available
}
```

**JSDoc/Go Doc:**
- Exported functions: comment starting with function name
- Format: `// FunctionName description.`
- Include parameter and return descriptions for complex functions
- No tags; uses comment format followed by signature

**Example:**
```go
// ExtractClientIP extracts the client IP address from an HTTP request.
// It checks X-Forwarded-For, X-Real-IP headers (for proxied requests)
// and falls back to RemoteAddr.
// This function is used by the HTTP middleware to automatically add client IP to traces.
func ExtractClientIP(r *http.Request) string {
	// implementation
}

// CheckAndCaptureWithContext checks and captures with trace context
// It automatically registers the breakpoint location on first call
// label: optional stable identifier for the checkpoint
func (c *SnapshotClient) CheckAndCaptureWithContext(ctx context.Context, label string, variables map[string]interface{}) {
	// implementation
}
```

## Function Design

**Size:** Functions are concise and focused
- Single responsibility principle observed
- Helper functions created for cross-cutting logic
- Example: `captureStackTrace()` extracted as helper called from `RecordError()`

**Parameters:**
- Context parameter first: `ctx context.Context` as first parameter in functions that need it
- No excessive parameter lists; structs used when needed
- Variadic options pattern for optional parameters: `opts ...trace.SpanStartOption`

**Return Values:**
- Error as last return value (Go convention)
- Multiple returns used for (value, error) patterns
- Named returns avoided - explicit return statements preferred

**Example:**
```go
// From config.go - context first, multiple returns
func (s *SDK) StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return s.tracer.Start(ctx, name, opts...)
}

// From client.go - error as last return
func (c *SnapshotClient) fetchActiveBreakpoints() error {
	// implementation
}
```

## Module Design

**Exports:**
- Capitalized for public API: `SDK`, `Config`, `SnapshotClient`, `StartSpan()`, `HTTPHandler()`
- Lowercase for internal: `metricsRegistry`, `counter`, `gormPlugin`, `contextKey`
- All exported types and functions have doc comments

**Barrel Files:**
- Not used; each file is focused on specific functionality
- Each file imports what it needs explicitly

**Package Organization:**
- Single package: `tracekit` - all SDK code lives in one package
- Clear separation by feature files: `config.go`, `span.go`, `metrics.go`, `client.go`, etc.
- Related types and methods grouped in same file

**Example structure from `config.go`:**
```go
package tracekit

type Config struct { /* exported */ }
type SDK struct { /* exported */ }

// Constructor
func NewSDK(config *Config) (*SDK, error) { }

// Helper
func resolveEndpoint(endpoint, path string, useSSL bool) string { }

// SDK methods
func (s *SDK) Tracer() trace.Tracer { }
func (s *SDK) Shutdown(ctx context.Context) error { }
```

## Concurrency Patterns

**Mutex usage:**
- Used with `sync.RWMutex` for cache management (e.g., `SnapshotClient.mu`)
- Pattern: lock before read/write, defer unlock
- Example: `defer c.mu.Unlock()` immediately after `c.mu.Lock()`

**Goroutines:**
- Spawned for non-blocking operations: async HTTP calls, background polling
- No explicit synchronization primitives for goroutine management beyond channels
- Example: `go c.captureSnapshot(snapshot)` for fire-and-forget

**Channels:**
- `stopChan` pattern for graceful shutdown (e.g., `make(chan struct{})`)
- Select/case pattern for multiple channel operations

## Type Assertions

**Pattern:**
- Always check assertion result: `if val, ok := x.(Type); ok { }`
- Never assume type assertion will succeed
- Example from gin.go:
```go
if ctx, exists := c.Get(string(requestContextKey)); exists {
	if requestCtx, ok := ctx.(map[string]interface{}); ok {
		return requestCtx
	}
}
return nil
```

## Interface Usage

**Patterns:**
- Metrics define small interfaces: `Counter`, `Gauge`, `Histogram`
- Implementations provided by package (counter, gauge, histogram structs)
- No-op implementations for disabled features (e.g., `noopCounter`, `noopGauge`)

**Example:**
```go
// From metrics.go
type Counter interface {
	Inc()
	Add(value float64)
}

// Private implementation
type counter struct {
	name   string
	tags   map[string]string
	buffer *metricsBuffer
}

// No-op for disabled metrics
type noopCounter struct{}
func (n *noopCounter) Inc() { }
```

---

*Convention analysis: 2026-02-27*

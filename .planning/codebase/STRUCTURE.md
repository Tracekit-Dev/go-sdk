# Codebase Structure

**Analysis Date:** 2026-02-27

## Directory Layout

```
go-sdk/
├── tracekit/                  # Main SDK package
│   ├── config.go              # SDK initialization and configuration
│   ├── client.go              # Snapshot client for code monitoring
│   ├── span.go                # Span creation and attribute methods
│   ├── http.go                # HTTP handler and client instrumentation
│   ├── database.go            # SQL database wrapper with tracing
│   ├── gorm.go                # GORM ORM plugin with tracing
│   ├── gin.go                 # Gin web framework middleware
│   ├── echo.go                # Echo web framework middleware
│   ├── grpc.go                # gRPC server/client interceptors
│   ├── redis.go               # Redis client instrumentation
│   ├── mongodb.go             # MongoDB client options with tracing
│   ├── metrics.go             # Counter/Gauge/Histogram interfaces and registry
│   ├── metrics_buffer.go      # Metrics buffering and periodic flush
│   ├── metrics_exporter.go    # OTLP metrics exporter
│   └── config_test.go         # Unit tests for configuration
├── examples/
│   └── context_propagation.go # Example demonstrating span propagation
├── go.mod                      # Go module definition
├── go.sum                      # Dependency checksums
├── LICENSE                     # Apache 2.0 license
└── README.md                   # Project documentation
```

## Directory Purposes

**tracekit/:**
- Purpose: Single Go package containing entire SDK
- Contains: Core SDK, instrumentation layers, examples
- Key files: config.go (entry point), span.go (tracing API), integration modules

**examples/:**
- Purpose: Demonstrate SDK usage patterns
- Contains: Runnable example applications
- Key files: context_propagation.go (shows span propagation across Gin, GORM, Redis, HTTP)

## Key File Locations

**Entry Points:**
- `tracekit/config.go`: NewSDK() - initializes SDK, sets up tracer provider
- `tracekit/client.go`: NewSnapshotClient() - initializes code monitoring
- `examples/context_propagation.go`: Full working example with Gin, GORM, Redis, HTTP client

**Configuration:**
- `tracekit/config.go`: Config struct (lines 26-69) - API key, endpoint, sampling rate, etc.
- `go.mod`: Dependency versions and Go toolchain (1.23.0, toolchain 1.24.10)

**Core Logic:**

*Tracing:*
- `tracekit/span.go`: StartSpan, AddAttribute, RecordError, SetSuccess
- `tracekit/span.go`: Helper methods for HTTP, database, user, business attributes

*Instrumentation:*
- `tracekit/http.go`: HTTPHandler, HTTPMiddleware, HTTPClient wrappers and client IP extraction
- `tracekit/database.go`: TracedDB wrapper implementing sql.DB interface with context support
- `tracekit/gorm.go`: gormPlugin implementing GORM plugin interface for callback instrumentation
- `tracekit/gin.go`: GinMiddleware for Gin framework with request context capture
- `tracekit/echo.go`: EchoMiddleware for Echo framework
- `tracekit/grpc.go`: Server/client interceptor options
- `tracekit/redis.go`: Redis hook for command and pipeline operations
- `tracekit/mongodb.go`: MongoDB client options with monitor

*Metrics:*
- `tracekit/metrics.go`: Counter, Gauge, Histogram interfaces; metricsRegistry management
- `tracekit/metrics_buffer.go`: In-memory buffering with periodic flush (10s default)
- `tracekit/metrics_exporter.go`: OTLP export format conversion and HTTP posting

*Code Monitoring:*
- `tracekit/client.go`: SnapshotClient with breakpoint polling (30s), snapshot capture, security scanning

**Testing:**
- `tracekit/config_test.go`: Tests for config and endpoint resolution logic

## Naming Conventions

**Files:**
- Lowercase snake_case: `config.go`, `metrics_buffer.go`
- Suffixes: `_test.go` for tests, no other special suffixes
- One primary responsibility per file (config, client, span, each integration module)

**Directories:**
- Single package: all code in `tracekit/` namespace
- Flat structure: no subdirectories within tracekit/
- Example directory: `examples/` for runnable programs

**Go Exported Names (Public API):**
- Type names: PascalCase (SDK, Config, Snapshot, SnapshotClient, TracedDB, Counter, Gauge, Histogram, BreakpointConfig)
- Function names: PascalCase (NewSDK, NewSnapshotClient, StartSpan, AddAttribute, WrapDB, WrapRedis, HTTPHandler, GinMiddleware)
- Constant names: UPPER_SNAKE_CASE (none in current codebase)
- Interface names: Singular nouns with -er suffix when appropriate (Counter, Gauge, Histogram)

**Go Unexported Names (Private to Package):**
- Lowercase for private types: `gormPlugin`, `redisHook`, `metricsRegistry`, `metricsBuffer`, `metricsExporter`
- Lowercase for private functions: `resolveEndpoint`, `extractBaseURL`, `detectLocalUI`, `initTracer`
- Lowercase for private constants/helpers: `requestContextKey`, `contextKey type`

**Configuration Structs:**
- Config: Required fields at top (APIKey, ServiceName), then optional fields with comments
- Public struct embedding: SDK embeds tracer and config references directly
- JSON tags: Used for marshaling (e.g., BreakpointConfig has json tags for API serialization)

## Where to Add New Code

**New Feature - Tracing Enhancement:**
- Primary code: `tracekit/span.go` - Add helper method on SDK type (e.g., AddCacheAttributes)
- Pattern: Follow existing pattern - create typed attributes, add to span via SetAttributes
- Example: AddHTTPAttributes (line 122), AddDatabaseAttributes (line 131), AddUserAttributes (line 141)

**New Integration - Library Instrumentation:**
- Implementation: New file `tracekit/{library}.go` (e.g., `memcached.go` for Memcached)
- Pattern options:
  - Wrapper pattern (database.go): Create new struct wrapping the library type, implement key methods with tracing
  - Middleware pattern (http.go): Create http.Handler or equivalent wrapper
  - Hook/Callback pattern (gorm.go, redis.go): Implement library's hook interface
  - Options pattern (mongodb.go): Return library's options struct with monitor/interceptor attached
- Dependencies: Import library and OpenTelemetry contrib instrumentation if available
- Export: Public function on SDK (e.g., WrapMemcached, MemcachedMiddleware, etc.)

**New Metrics Type:**
- Implementation: Add to `tracekit/metrics.go`
- Pattern: Create interface (e.g., type Summary interface { Observe(value float64) })
- Add struct implementing interface with buffer reference
- Add registry method and SDK method (e.g., Summary(name, tags) Summary)
- Add exporter logic in `tracekit/metrics_exporter.go` toOTLP() method (switch case for type)
- Example: Add Summary for percentile tracking following Counter/Gauge/Histogram pattern

**New Code Monitoring Feature:**
- Implementation: Extend `tracekit/client.go`
- Pattern: Add method to SnapshotClient struct
- Follow existing patterns: GetBreakpoints(), CaptureSnapshot(), ScanForSecurityIssues()
- Example: AddStackFrameFilter to skip internal frames

**Test Addition:**
- Location: `tracekit/config_test.go` for config tests, or new `tracekit/{module}_test.go`
- Pattern: Use Go's testing.T, no external test framework
- Naming: TestFunctionName pattern (e.g., TestResolveEndpoint)

**New Example:**
- Location: `examples/{scenario}.go` with main() function
- Pattern: Demonstrate specific SDK usage (e.g., grpc_tracing.go for gRPC example)
- Build as standalone programs runnable via `go run examples/{scenario}.go`

## Special Directories

**tracekit/ (package directory):**
- Purpose: Single Go package containing all SDK code
- Generated: No
- Committed: Yes
- Convention: All source files in this directory are part of the tracekit package

**examples/ (example programs):**
- Purpose: Runnable example applications demonstrating SDK usage
- Generated: No
- Committed: Yes
- Convention: Each file is independent main() package, not imported by SDK

**vendor/ (if present):**
- Purpose: Vendored dependencies (managed by go.mod/go.sum)
- Generated: Yes (via go mod vendor)
- Committed: No (modern projects use go.mod directly)

**.planning/ (GSD planning directory):**
- Purpose: Store codebase analysis documents (created by `/gsd:map-codebase`)
- Generated: Yes
- Committed: Yes (documentation)
- Subdirectories: `.planning/codebase/` for architecture/structure documents

## Module Dependencies (from go.mod)

**Direct Dependencies - OpenTelemetry:**
- `go.opentelemetry.io/otel` - Core tracing APIs
- `go.opentelemetry.io/otel/sdk` - SDK implementation (tracer provider, batching)
- `go.opentelemetry.io/otel/trace` - Span interfaces
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` - HTTP exporter for traces
- `go.opentelemetry.io/contrib/instrumentation/{lib}` - Pre-built instrumentations for libraries

**Direct Dependencies - Framework/Library Support:**
- `github.com/gin-gonic/gin` - Web framework
- `github.com/labstack/echo/v4` - Alternative web framework
- `gorm.io/gorm` and `gorm.io/driver/sqlite` - ORM
- `github.com/redis/go-redis/v9` - Redis client
- `go.mongodb.org/mongo-driver` - MongoDB client
- `google.golang.org/grpc` - gRPC framework

## Import Organization

**Pattern within tracekit files:**

1. Standard library (crypto/tls, context, fmt, log, net, etc.)
2. OpenTelemetry core (go.opentelemetry.io/otel)
3. OpenTelemetry SDK (go.opentelemetry.io/otel/sdk, trace, attribute)
4. External libraries (gin, gorm, redis, mongo, grpc, echo)

**Example from config.go:**
```go
import (
  "crypto/tls"
  "context"
  // ... more stdlib
  "go.opentelemetry.io/otel"
  "go.opentelemetry.io/otel/attribute"
  "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
  // ... more OTEL
)
```

## Code Organization Within Files

**Typical file structure:**

1. Package declaration
2. Import block(s)
3. Type definitions (structs, interfaces)
4. Constructor functions (New*)
5. Method receivers on types
6. Helper functions
7. Unexported utility functions

**Example from client.go:**
- Lines 1-17: Package + imports
- Lines 19-70: Type definitions (SnapshotClient, BreakpointConfig, SecurityFlag, Snapshot)
- Lines 72-83: Constructor (NewSnapshotClient)
- Lines 85-227: Public methods (Start, Stop, CheckAndCapture, CheckAndCaptureWithContext)
- Lines 229-420: Private methods (pollBreakpoints, fetchActiveBreakpoints, autoRegisterBreakpoint, captureSnapshot)
- Lines 416-472: Helper methods (scanForSecurityIssues, extractRequestContext)

---

*Structure analysis: 2026-02-27*

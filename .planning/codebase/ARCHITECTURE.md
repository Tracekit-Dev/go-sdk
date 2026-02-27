# Architecture

**Analysis Date:** 2026-02-27

## Pattern Overview

**Overall:** Multi-layered SDK providing distributed tracing and observability instrumentation for Go applications.

**Key Characteristics:**
- Wrapper-based architecture that instruments existing Go libraries (HTTP, databases, message queues)
- OpenTelemetry as the core tracing standard with HTTP/OTLP export
- Dual instrumentation capability: automatic (middleware) and manual (SDK methods)
- Code monitoring (snapshot capture) as secondary concern with automatic breakpoint registration
- Context propagation through Go's context.Context for span correlation across service boundaries

## Layers

**Configuration Layer:**
- Purpose: Initialize SDK with configuration, setup tracer provider, connect exporters
- Location: `tracekit/config.go`
- Contains: Config struct, SDK struct, tracer initialization, local UI detection
- Depends on: OpenTelemetry OTLP, HTTP client
- Used by: All other modules through SDK instance

**Core Tracing Layer:**
- Purpose: Expose low-level span management and attribute setting
- Location: `tracekit/span.go`
- Contains: StartSpan, AddAttribute, RecordError, SetSuccess, helper methods for HTTP/DB/user attributes
- Depends on: SDK tracer provider
- Used by: Business logic code, integration layers

**Integration Layer (Instrumentation Providers):**
- Purpose: Wrap existing libraries with OpenTelemetry instrumentation
- Locations:
  - HTTP: `tracekit/http.go` - Wraps http.Handler, http.Client, RoundTripper
  - Database: `tracekit/database.go` - Wraps sql.DB with traced methods
  - ORM: `tracekit/gorm.go` - GORM plugin with callbacks for Create/Update/Delete/Query
  - Cache: `tracekit/redis.go` - Redis hook for GET/SET/Pipeline operations
  - Document DB: `tracekit/mongodb.go` - MongoDB client options
  - RPC: `tracekit/grpc.go` - gRPC server/client interceptors
  - Web Frameworks: `tracekit/gin.go`, `tracekit/echo.go` - Middleware for request tracing
- Depends on: OpenTelemetry contrib instrumentation libraries
- Used by: Application code integrating with those libraries

**Metrics Layer:**
- Purpose: Collect and export metrics (counters, gauges, histograms)
- Locations: `tracekit/metrics.go`, `tracekit/metrics_buffer.go`, `tracekit/metrics_exporter.go`
- Contains: Counter/Gauge/Histogram interfaces, metricsRegistry, buffering, OTLP export
- Depends on: HTTP client
- Used by: SDK methods Counter/Gauge/Histogram, business logic

**Code Monitoring Layer (Optional):**
- Purpose: Capture variable state at runtime breakpoints
- Location: `tracekit/client.go`
- Contains: SnapshotClient, breakpoint polling, variable capture, security scanning
- Depends on: HTTP client
- Used by: SDK CheckAndCapture methods when enabled

## Data Flow

**Tracing Flow:**

1. HTTP request arrives at application
2. HTTPHandler/Middleware wraps request with OpenTelemetry span
3. Request context propagates through code (stored in context.Context)
4. Instrumented operations (DB queries, HTTP calls, cache ops) extract span from context
5. Child spans created under request span
6. Errors recorded with stack traces via RecordError
7. Span ends when operation completes
8. OTLP exporter batches spans and sends to backend (async, 10s batches)

Example trace tree:
```
HTTP Request Span (GET /orders)
├── Database Query Span (SELECT users WHERE id=?)
├── Redis GET Span (redis.get:user:123)
└── HTTP Client Span (POST https://payment-service/charge)
    └── Child span from payment service (propagated via headers)
```

**Metrics Flow:**

1. Code calls sdk.Counter("request_count").Add(1)
2. Counter adds metric data point to metricsBuffer
3. Buffer accumulates points (max 100 or timeout 10s)
4. Flush exports batch to OTLP metrics endpoint (async)

**Code Monitoring Flow:**

1. Application calls sdk.CheckAndCaptureWithContext(ctx, "label", variables)
2. SnapshotClient checks cache for active breakpoints
3. If match found: capture runtime stack trace, scan variables for sensitive data
4. Create Snapshot payload with trace/span IDs from context
5. Send to backend asynchronously (non-blocking)
6. Backend returns latest breakpoint list, client refreshes cache

**State Management:**
- Span state: Managed by OpenTelemetry SDK (stored in context.Context)
- Breakpoint cache: In-memory map, refreshed every 30s from backend
- Metrics: Buffered in metricsBuffer, flushed to exporter asynchronously
- Registration tracking: In-memory set to avoid duplicate auto-registration requests

## Key Abstractions

**SDK (tracekit.SDK):**
- Purpose: Main entry point, holds tracer provider and delegates to sub-systems
- Examples: `tracekit/config.go` lines 72-79
- Pattern: Single SDK instance per application, initialized once at startup

**Span Context Propagation:**
- Purpose: Link related operations across service boundaries
- Examples: `tracekit/gin.go` - Gin middleware captures request context; `tracekit/http.go` - HTTPClient preserves context
- Pattern: Context attached to requests via OpenTelemetry headers (traceparent, tracestate)

**Instrumentation Wrappers:**
- Purpose: Add tracing to existing libraries without modifying their internals
- Examples: TracedDB wraps sql.DB, gormPlugin implements gorm.Plugin interface, redisHook implements redis.Hook
- Pattern: Either wrap before use (sql.DB) or plugin architecture (GORM, Redis) - depends on library capabilities

**Middleware Pattern:**
- Purpose: Intercept requests before reaching business logic
- Examples: HTTPHandler, GinMiddleware, EchoMiddleware, clientIPMiddleware
- Pattern: Chain middlewares to progressively add instrumentation

**Client IP Extraction:**
- Purpose: Determine actual client IP in proxied environments
- Examples: `tracekit/http.go` lines 175-216, used in HTTPHandler and GinMiddleware
- Pattern: Check X-Forwarded-For, X-Real-IP headers; fall back to RemoteAddr

**Service Name Discovery:**
- Purpose: Identify peer services for inter-service calls
- Examples: `tracekit/http.go` lines 141-173
- Pattern: Parse service names from Kubernetes DNS (.svc.cluster.local), internal domains (.internal), or use configuration mappings

**Breakpoint Label-Based Matching:**
- Purpose: Stable identifier for breakpoints across code redeployments
- Examples: `tracekit/client.go` lines 164-169, 259-265
- Pattern: Primary key = function_name:label; secondary key = file_path:line_number for backwards compatibility

## Entry Points

**SDK Initialization:**
- Location: `tracekit/config.go` - NewSDK()
- Triggers: Application startup
- Responsibilities: Parse config, validate required fields, initialize tracer provider with OTLP exporter, detect local UI, start code monitoring if enabled

**HTTP Request Entry:**
- Location: `tracekit/gin.go` - GinMiddleware() or `tracekit/http.go` - HTTPHandler()
- Triggers: Incoming HTTP request
- Responsibilities: Extract client IP, capture request context, create root span, propagate context to handlers

**Database Query Entry:**
- Location: `tracekit/database.go` - QueryContext(), ExecContext() or `tracekit/gorm.go` - gormPlugin.after()
- Triggers: Application calls db.Query() or gorm.Create()
- Responsibilities: Create span, set DB attributes, record errors, track rows affected

**Code Monitoring Entry:**
- Location: `tracekit/config.go` - CheckAndCaptureWithContext()
- Triggers: Explicit call to sdk.CheckAndCaptureWithContext(ctx, label, vars)
- Responsibilities: Auto-register breakpoint, check cache, capture snapshot with trace context

## Error Handling

**Strategy:** Graceful degradation - errors in tracing/monitoring don't crash application

**Patterns:**
- Span errors recorded via span.RecordError() with full stack trace via captureStackTrace()
- Configuration errors: returned from NewSDK() to fail fast
- Export errors: logged but not fatal (background goroutines)
- Metrics errors: silent fail with TODO comment for optional logging (`tracekit/metrics_buffer.go` line 84)
- Snapshot errors: logged to stderr, non-blocking (async goroutine)
- HTTP client timeouts: 30s for SDK operations, configurable for application clients
- Database errors: recorded on span with error status, returned to caller

## Cross-Cutting Concerns

**Logging:** Direct log.Printf() calls for SDK initialization events and errors. No structured logging framework.

**Validation:**
- Config validation: APIKey and ServiceName required in NewSDK()
- Endpoint parsing: resolveEndpoint() handles various URL formats (full URL, host-only, custom paths)
- IP validation: ExtractClientIP() validates parsed IPs before returning

**Authentication:** X-API-Key header sent on all backend API calls (config, breakpoints, snapshots, metrics)

**Concurrency:**
- Mutex protection on: breakpoint cache (mu sync.RWMutex), metrics registry (mu sync.RWMutex), gauge values (mu sync.Mutex)
- Channel-based signaling for shutdown (stopChan, stop)
- Background goroutines for async work: polling, exporting, capturing

**Security:**
- Variable scanning for sensitive data patterns (password, API key, JWT, credit card)
- Request context redaction: Authorization, Cookie, X-Api-Key headers replaced with [REDACTED]
- Snapshot variable redaction: sensitive variable names and values replaced with [REDACTED]
- Security flags attached to snapshots indicating what was redacted

---

*Architecture analysis: 2026-02-27*

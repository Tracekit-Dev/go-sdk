# External Integrations

**Analysis Date:** 2026-02-27

## APIs & External Services

**TraceKit Backend:**
- Service: app.tracekit.dev (configurable via `Endpoint` in `Config`)
- What it's used for: Trace and metric ingestion, code monitoring breakpoint configuration
  - SDK/Client: Custom HTTP client with OTLP exporter
  - Auth: `TRACEKIT_API_KEY` environment variable or `APIKey` in config struct
  - Endpoints:
    - `/v1/traces` - Trace ingestion (configurable via `TracesPath`)
    - `/v1/metrics` - Metrics ingestion (configurable via `MetricsPath`)
    - `/api/breakpoints` - Code monitoring breakpoint polling
    - `/api/snapshots` - Code monitoring snapshot uploads

**TraceKit Local UI (Development Only):**
- Service: localhost:9999
- What it's used for: Development-time trace visualization
  - Auto-detection: SDK checks `http://localhost:9999/api/health` on startup
  - Enabled only when `ENV=development` and local UI is running
  - Spans processed via custom `localUISpanProcessor` in `tracekit/config.go`

## Data Storage

**Databases (Instrumented):**
- PostgreSQL - Supported via GORM integration
  - Instrumentation: Custom GORM plugin (`tracekit/gorm.go`)
  - ORM: GORM (gorm.io/gorm)
  - Traces all CRUD operations and raw SQL queries

- MySQL - Supported via GORM integration
  - Instrumentation: Custom GORM plugin (`tracekit/gorm.go`)
  - ORM: GORM (gorm.io/gorm)
  - Traces all CRUD operations and raw SQL queries

- SQLite - Supported via GORM and stdlib
  - Driver: gorm.io/driver/sqlite
  - Wrapper: Custom `TracedDB` wrapper in `tracekit/database.go` for stdlib sql.DB
  - Examples use SQLite for testing

**File Storage:**
- Local filesystem only - No cloud storage SDKs included
- Application-level responsibility

**Caching:**
- Redis - Instrumented via custom hooks
  - Client: `github.com/redis/go-redis/v9`
  - Implementation: `redisHook` in `tracekit/redis.go` implements `redis.Hook`
  - Supports standard and cluster clients: `WrapRedis()` and `WrapRedisCluster()`
  - Traces: Redis commands (GET, SET, DEL, etc.) with operation names and error tracking

## Authentication & Identity

**Auth Provider:**
- Custom API key authentication
  - Implementation: Bearer token in HTTP header (`Authorization: Bearer {TRACEKIT_API_KEY}`)
  - Validation: Required at SDK initialization in `NewSDK()` - `tracekit/config.go` line 265

**Request Authentication:**
- All requests to TraceKit backend include API key
- Configured in OTLP exporter headers and snapshot client

## Monitoring & Observability

**Error Tracking:**
- Built into trace spans - Errors recorded via `span.RecordError()` and `span.SetStatus(codes.Error, ...)`
- Applied to: HTTP errors, database errors, Redis errors, gRPC errors
- Examples: `tracekit/database.go` lines 43-45, `tracekit/redis.go` line 77

**Logs:**
- Standard Go `log` package used throughout SDK
- Notable logs:
  - SDK initialization: "✅ TraceKit SDK initialized for service: {serviceName}" (config.go:332)
  - Local UI detection: "🔍 Local UI detected at http://localhost:9999" (config.go:307)
  - Breakpoint registration/updates logged during polling (client.go)
- Application responsible for aggregating logs (no log export to backend)

**Traces & Metrics:**
- Traces exported via OTLP HTTP exporter to TraceKit backend
- Metrics buffered locally and exported to `/v1/metrics` endpoint
- Sampling configurable via `SamplingRate` in Config (default 1.0 = 100%)

## CI/CD & Deployment

**Hosting:**
- Agnostic - SDK is a library; deployment is application-specific
- Supports Docker, Kubernetes, VM, serverless (any Go runtime)

**CI Pipeline:**
- GitHub Actions (inferred from `github.com/Tracekit-Dev/go-sdk` module path)
- Tests present: `tracekit/config_test.go`

## Environment Configuration

**Required env vars:**
- `TRACEKIT_API_KEY` - Authentication token for TraceKit backend

**Optional env vars:**
- `ENV` - Used for development detection (enables local UI and span processor)

**Config struct fields** (alternative to env vars):
- `APIKey` (required) - Authentication
- `ServiceName` (required) - Service identifier for traces and metrics
- `Endpoint` - Backend hostname (default: "app.tracekit.dev")
- `TracesPath` - Traces ingestion path (default: "/v1/traces")
- `MetricsPath` - Metrics ingestion path (default: "/v1/metrics")
- `UseSSL` - Enable HTTPS (default: true)
- `ServiceVersion` - Service version for resource attributes (default: "1.0.0")
- `Environment` - Deployment environment (e.g., "production", "staging")
- `ResourceAttributes` - Custom OTEL resource attributes
- `EnableCodeMonitoring` - Enable code snapshots (default: false)
- `CodeMonitoringPollInterval` - Breakpoint polling interval (default: 30s)
- `SamplingRate` - Trace sampling 0.0-1.0 (default: 1.0)
- `BatchTimeout` - Span batch timeout (default: 5s)
- `ServiceNameMappings` - Map hostnames to service names for peer service attributes

**Secrets location:**
- Environment variables or passed directly in Config struct
- No `.env` file handling - Application responsible

## Webhooks & Callbacks

**Incoming:**
- None explicitly exposed

**Outgoing:**
- None - Unidirectional communication to TraceKit backend
- Code monitoring uses polling (not webhooks) for breakpoint configuration updates

## Backend Communication Protocol

**OTLP HTTP:**
- Protocol: OpenTelemetry Protocol (OTLP) over HTTP
- Format: Protocol Buffers (protobuf)
- Endpoint resolution: `resolveEndpoint()` in `tracekit/config.go` handles:
  - Full URLs with scheme
  - Hostname-only (adds https:// by default)
  - Custom paths
- TLS: Configurable via `UseSSL` (default: true)

**HTTP Client Configuration:**
- Timeout: 30 seconds (for snapshot client in `tracekit/client.go` line 78)
- Timeout: 1 second (for local UI processor)
- Backoff: Uses `cenkalti/backoff/v4` for retry logic on failures

## Service-to-Service Communication

**Traced HTTP Calls:**
- SDK provides `HTTPClient()` method to wrap `http.Client` for instrumentation
- Applied via `otelhttp.NewTransport()` in `tracekit/http.go`
- Captures client IP automatically via `ExtractClientIP()` utility

**gRPC:**
- Server interceptors: `GRPCServerInterceptors()` - use with `grpc.NewServer(options...)`
- Client interceptors: `GRPCClientInterceptors()` - use with `grpc.Dial(target, options...)`
- Both support custom service name mapping via config

## Distributed Tracing

**Context Propagation:**
- W3C Trace Context and Baggage propagators enabled
- Configured in `initTracer()` - `tracekit/config.go` line 357
- Automatic header injection/extraction for HTTP requests
- Supports service-to-service trace context propagation

---

*Integration audit: 2026-02-27*

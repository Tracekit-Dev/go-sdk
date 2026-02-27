# Technology Stack

**Analysis Date:** 2026-02-27

## Languages

**Primary:**
- Go 1.23.0 - SDK implementation with Go 1.24.10 toolchain

**Secondary:**
- None (Go-only SDK)

## Runtime

**Environment:**
- Go 1.23.0+

**Package Manager:**
- Go Modules
- Lockfile: `go.sum` present and committed

## Frameworks

**Core:**
- OpenTelemetry (`go.opentelemetry.io/otel`) v1.38.0 - Distributed tracing foundation
- OpenTelemetry SDK (`go.opentelemetry.io/otel/sdk`) v1.38.0 - Tracer provider and span processor implementation

**HTTP Server Instrumentation:**
- Gin (`github.com/gin-gonic/gin`) v1.10.1 - Framework support
  - Instrumentation: `go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin` v0.63.0
- Echo (`github.com/labstack/echo/v4`) v4.13.4 - Framework support
  - Instrumentation: `go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho` v0.63.0

**HTTP Client Instrumentation:**
- Net/HTTP (stdlib) wrapped with `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` v0.63.0

**Database & ORM:**
- GORM (`gorm.io/gorm`) v1.31.1 - ORM support
- GORM SQLite driver (`gorm.io/driver/sqlite`) v1.6.0 - Example/test driver
- Database/SQL (stdlib) - Custom tracing wrapper implemented

**Cache:**
- Redis (`github.com/redis/go-redis/v9`) v9.7.0 - Redis instrumentation via custom hooks

**NoSQL:**
- MongoDB (`go.mongodb.org/mongo-driver`) v1.17.4 - Document database
  - Instrumentation: `go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo` v0.63.0

**gRPC:**
- gRPC (`google.golang.org/grpc`) v1.75.0 - RPC framework
  - Instrumentation: `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` v0.63.0

**Tracing Export:**
- OTLP HTTP Exporter (`go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`) v1.24.0 - Sends traces to TraceKit backend

**Metrics:**
- Custom metrics implementation (buffer + registry pattern) - No external metrics library used
- OTLP exporter shared infrastructure for trace protocol

## Key Dependencies

**Critical:**
- `go.opentelemetry.io/otel` v1.38.0 - Core observability foundation
- `go.opentelemetry.io/otel/sdk` v1.38.0 - SDK span provider and processing
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` v1.24.0 - Communicates with TraceKit backend

**Infrastructure:**
- `google.golang.org/grpc` v1.75.0 - Protocol for span export and code monitoring
- `google.golang.org/protobuf` v1.36.8 - Protocol buffer support for gRPC
- `golang.org/x/net` v0.43.0 - Low-level networking
- `golang.org/x/sync` v0.16.0 - Synchronization utilities (context, mutex helpers)
- `golang.org/x/crypto` v0.41.0 - TLS/SSL support for secure connections

**Utilities:**
- JSON processing (stdlib `encoding/json` + `go-json` for performance)
- UUID generation (`google/uuid`) - Span ID and trace ID generation
- Backoff/retry (`cenkalti/backoff/v4`) v4.2.1 - Connection retry logic

## Configuration

**Environment:**
- SDK configured via `Config` struct in `tracekit/config.go`
- Required env vars:
  - `TRACEKIT_API_KEY` - Authentication token for backend
- Optional env vars:
  - `ENV` - Environment detection (used for local UI and development mode)
- Supports custom endpoint mapping via config

**Build:**
- No build configuration files (standard `go build` / `go install`)
- Module declaration: `module github.com/Tracekit-Dev/go-sdk`

## Platform Requirements

**Development:**
- Go 1.23.0+ required
- SQLite (via cgo via `mattn/go-sqlite3`) for examples

**Production:**
- No platform-specific requirements (pure Go)
- Network connectivity to TraceKit backend (https://app.tracekit.dev by default)
- Supports both HTTP and HTTPS connections (configurable)

## Version Specifications

| Component | Version | Purpose |
|-----------|---------|---------|
| Go | 1.23.0 | Primary language |
| Gin | v1.10.1 | HTTP server framework |
| Echo | v4.13.4 | HTTP server framework |
| GORM | v1.31.1 | SQL ORM |
| Redis | v9.7.0 | Cache client |
| MongoDB | v1.17.4 | NoSQL database |
| gRPC | v1.75.0 | RPC framework |
| OpenTelemetry | v1.38.0 | Tracing core |
| OTLP Exporter | v1.24.0 | Backend communication |

---

*Stack analysis: 2026-02-27*

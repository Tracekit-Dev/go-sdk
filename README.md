# TraceKit Go SDK

**One SDK, Complete Observability** - Drop-in replacement for manual OpenTelemetry instrumentation with automatic code discovery.

[![Go Reference](https://pkg.go.dev/badge/github.com/Tracekit-Dev/go-sdk.svg)](https://pkg.go.dev/github.com/Tracekit-Dev/go-sdk)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Why TraceKit SDK?

**Before (Manual OpenTelemetry):**
```go
// 50+ lines of boilerplate setup
exporter, _ := otlptracehttp.New(...)
tp := sdktrace.NewTracerProvider(...)
otel.SetTracerProvider(tp)
otel.SetTextMapPropagator(...)

// Then wrap every component separately
r.Use(otelgin.Middleware(...))
db.Use(otelgorm.NewPlugin(...))
client.Transport = otelhttp.NewTransport(...)
// ... repeat for Redis, MongoDB, gRPC, etc.
```

**After (TraceKit SDK):**
```go
// 3 lines - everything instrumented automatically
sdk, _ := tracekit.NewSDK(&tracekit.Config{
    APIKey:      os.Getenv("TRACEKIT_API_KEY"),
    ServiceName: "my-service",
})
defer sdk.Shutdown(context.Background())

// One-line middleware/wrappers
r.Use(sdk.GinMiddleware())
sdk.WrapGorm(db)
sdk.WrapRedis(redisClient)
// Done!
```

---

## Features

### 📊 **Distributed Tracing**
- ✅ HTTP server instrumentation (Gin, Echo, net/http)
- ✅ HTTP client instrumentation
- ✅ Database monitoring (GORM, database/sql, PostgreSQL, MySQL, SQLite)
- ✅ Redis instrumentation
- ✅ MongoDB instrumentation
- ✅ gRPC interceptors (client & server)
- ✅ Custom spans, events, and attributes

### 🔍 **Code Monitoring** (Live Production Debugging)
- ✅ Non-breaking breakpoints
- ✅ **Automatic code discovery from traces**
- ✅ Variable state capture
- ✅ Stack traces without stopping your app
- ✅ < 5ms overhead

---

## Installation

```bash
go get github.com/Tracekit-Dev/go-sdk
```

---

## Quick Start

### 1. Initialize the SDK

```go
package main

import (
    "context"
    "log"
    "os"
    
    "github.com/Tracekit-Dev/go-sdk/tracekit"
)

func main() {
    // Initialize TraceKit SDK
    sdk, err := tracekit.NewSDK(&tracekit.Config{
        APIKey:               os.Getenv("TRACEKIT_API_KEY"),
        ServiceName:          "my-service",
        Environment:          "production", // optional
        EnableCodeMonitoring: true,         // optional
    })
    if err != nil {
        log.Fatal(err)
    }
    defer sdk.Shutdown(context.Background())
    
    // Your application code...
}
```

### 2. Add Framework Middleware (One Line!)

#### Gin

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/Tracekit-Dev/go-sdk/tracekit"
)

r := gin.Default()
r.Use(sdk.GinMiddleware()) // ← That's it! All routes automatically traced
```

#### Echo

```go
import (
    "github.com/labstack/echo/v4"
    "github.com/Tracekit-Dev/go-sdk/tracekit"
)

e := echo.New()
e.Use(sdk.EchoMiddleware()) // ← All routes automatically traced
```

#### net/http (Standard Library)

```go
import "net/http"

mux := http.NewServeMux()
handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("Hello, World!"))
})

// Wrap any handler
mux.Handle("/", sdk.HTTPHandler(handler, "root"))

// Or use as middleware
wrappedMux := sdk.HTTPMiddleware("my-service")(mux)
http.ListenAndServe(":8080", wrappedMux)
```

### 3. Instrument Database (One Line!)

#### GORM

```go
db, _ := gorm.Open(postgres.Open(dsn), &gorm.Config{})
sdk.WrapGorm(db) // ← All queries automatically traced!

// Now every Find(), Create(), Update() is traced
db.Find(&users)
```

#### database/sql with pgx

```go
import "github.com/jackc/pgx/v5/stdlib"

// Register instrumented driver
driverName, _ := sdk.WrapDatabaseDriver("pgx", tracekit.DriverPostgreSQL)

// Use instrumented driver
db, _ := sql.Open(driverName, dsn)

// All queries automatically traced!
db.Query("SELECT * FROM users")
```

### 4. Instrument Redis (One Line!)

```go
import "github.com/redis/go-redis/v9"

client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

sdk.WrapRedis(client) // ← All Redis ops automatically traced!

// All commands now traced
client.Get(ctx, "key")
client.Set(ctx, "key", "value", 0)
```

### 5. Instrument MongoDB (One Line!)

```go
import "go.mongodb.org/mongo-driver/mongo"

// Get instrumented options
opts := sdk.MongoClientOptions().ApplyURI("mongodb://localhost:27017")

// Create client with tracing
client, _ := mongo.Connect(ctx, opts)

// All operations automatically traced!
collection := client.Database("test").Collection("users")
collection.Find(ctx, bson.M{})
```

### 6. Instrument HTTP Clients (One Line!)

```go
// Wrap existing client
client := sdk.HTTPClient(&http.Client{
    Timeout: 10 * time.Second,
})

// All outgoing requests automatically traced!
resp, _ := client.Get("https://api.example.com/data")
```

### 7. Instrument gRPC

#### Server

```go
import "google.golang.org/grpc"

server := grpc.NewServer(
    sdk.GRPCServerInterceptors()..., // ← All RPCs automatically traced
)
```

#### Client

```go
conn, _ := grpc.Dial("localhost:50051",
    sdk.GRPCClientInterceptors()..., // ← All calls automatically traced
)
```

---

## Manual Spans & Custom Instrumentation

### Basic Span Creation

```go
ctx, span := sdk.StartSpan(ctx, "processOrder")
defer span.End()

// Add attributes
sdk.AddAttribute(span, "order.id", orderID)
sdk.AddIntAttribute(span, "order.amount", 9999)

// Add events
sdk.AddEvent(span, "payment.initiated", 
    attribute.String("payment.method", "credit_card"),
)

// Handle errors
if err != nil {
    sdk.RecordError(span, err)
    return err
}

sdk.SetSuccess(span)
```

### Helper Methods for Common Attributes

```go
// HTTP attributes
sdk.AddHTTPAttributes(span, "GET", "/api/users", 200)

// Database attributes
sdk.AddDatabaseAttributes(span, "postgres", "mydb", "SELECT", "users")

// User attributes
sdk.AddUserAttributes(span, "user123", "user@example.com")

// Business attributes
sdk.AddBusinessAttributes(span, map[string]interface{}{
    "order.id":     "12345",
    "customer.id":  "67890",
    "total.amount": 299.99,
})
```

### Trace a Function Automatically

```go
err := sdk.TraceFunction(ctx, "calculateDiscount", func(ctx context.Context, span trace.Span) error {
    // Your business logic
    discount := calculateDiscount(userID)
    
    sdk.AddFloatAttribute(span, "discount.amount", discount)
    return nil
})
```

---

## Code Monitoring (Live Debugging)

### Automatic Discovery (Recommended)

TraceKit **automatically discovers your code** from trace stack traces. No manual instrumentation needed!

**Workflow:**
1. **Send traces** (you're already doing this with the SDK!)
2. **Go to TraceKit UI** → Code Monitoring → "Browse Code" tab
3. **See your discovered code** - files, functions, line numbers from production
4. **Click "Set Breakpoint"** on any code location
5. **View snapshots** when that code runs (variables, stack trace, context)

🎉 **No code changes required!** Stack traces from errors automatically index your code structure.

### Manual Checkpoints (Advanced)

For critical business logic where you want precise control:

```go
// Initialize with code monitoring enabled
sdk, _ := tracekit.NewSDK(&tracekit.Config{
    APIKey:               os.Getenv("TRACEKIT_API_KEY"),
    ServiceName:          "my-service",
    EnableCodeMonitoring: true,
})

// Add checkpoint at critical point
func ProcessPayment(ctx context.Context, amount float64) error {
    // Capture state at this exact moment
    sdk.SnapshotClient().CheckAndCapture("payment.go", 42, map[string]interface{}{
        "amount":      amount,
        "userID":      userID,
        "accountType": accountType,
    })
    
    // Your payment logic...
}
```

Then create a breakpoint in TraceKit UI for `payment.go:42`. When this code runs, you'll see:
- All captured variables
- Complete stack trace
- Request context (trace ID, span ID)
- Timestamp

---

## Complete Example

```go
package main

import (
    "context"
    "log"
    "os"
    
    "github.com/gin-gonic/gin"
    "github.com/redis/go-redis/v9"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
    
    "github.com/Tracekit-Dev/go-sdk/tracekit"
)

func main() {
    // 1. Initialize SDK
    sdk, err := tracekit.NewSDK(&tracekit.Config{
        APIKey:               os.Getenv("TRACEKIT_API_KEY"),
        ServiceName:          "backend-api",
        Environment:          "production",
        EnableCodeMonitoring: true,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer sdk.Shutdown(context.Background())
    
    // 2. Setup database with tracing
    db, _ := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
    sdk.WrapGorm(db)
    
    // 3. Setup Redis with tracing
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    sdk.WrapRedis(redisClient)
    
    // 4. Setup HTTP server with tracing
    r := gin.Default()
    r.Use(sdk.GinMiddleware())
    
    // 5. Define routes - automatically traced!
    r.GET("/api/users", func(c *gin.Context) {
        var users []User
        db.Find(&users) // Traced!
        c.JSON(200, users)
    })
    
    r.POST("/api/orders", func(c *gin.Context) {
        // Custom span for business logic
        ctx, span := sdk.StartSpan(c.Request.Context(), "createOrder")
        defer span.End()
        
        // Process order...
        order := processOrder(ctx, orderData)
        
        sdk.AddBusinessAttributes(span, map[string]interface{}{
            "order.id":     order.ID,
            "order.amount": order.Amount,
        })
        
        sdk.SetSuccess(span)
        c.JSON(201, order)
    })
    
    // 6. Start server
    log.Println("Server starting on :8080")
    r.Run(":8080")
}
```

**That's it!** You now have:
- ✅ All HTTP endpoints traced
- ✅ All database queries traced
- ✅ All Redis operations traced
- ✅ All outgoing HTTP calls traced (if you use `sdk.HTTPClient()`)
- ✅ Custom spans for business logic
- ✅ Code monitoring ready (browse discovered code in UI)

---

## Configuration Options

```go
sdk, err := tracekit.NewSDK(&tracekit.Config{
    // Required
    APIKey:      "your-api-key",
    ServiceName: "my-service",
    
    // Optional - defaults
    Endpoint:                   "app.tracekit.dev",     // TraceKit endpoint
    UseSSL:                     true,                   // Use HTTPS
    Environment:                "production",           // deployment.environment
    ServiceVersion:             "1.0.0",                // service.version
    SamplingRate:               1.0,                    // 100% sampling (0.0-1.0)
    BatchTimeout:               5 * time.Second,        // Batch export interval
    EnableCodeMonitoring:       false,                  // Enable live debugging
    CodeMonitoringPollInterval: 30 * time.Second,       // Breakpoint poll interval
    ResourceAttributes: map[string]string{
        "host.name": "server-01",
        "region":    "us-east-1",
    },
})
```

---

## Best Practices

### ✅ DO:
- Use automatic discovery for code monitoring (primary workflow)
- Add manual checkpoints only for critical business logic
- Use helper methods for common attributes (`AddHTTPAttributes`, etc.)
- Set meaningful span names (operation-focused: "processPayment", "validateUser")
- Add business context to spans (order IDs, user IDs, amounts)
- Use `defer span.End()` immediately after `StartSpan()`

### ❌ DON'T:
- Capture sensitive data (passwords, tokens, PII) in spans or snapshots
- Add checkpoints in tight loops (use conditions if needed)
- Keep breakpoints active indefinitely
- Capture large objects (>100KB) in snapshots
- Create spans for every function (focus on meaningful operations)

---

## Performance

| Operation | Overhead | Notes |
|-----------|----------|-------|
| HTTP middleware | < 1ms | Per request |
| Database query | < 0.5ms | Per query |
| Redis operation | < 0.2ms | Per operation |
| Custom span | < 0.1ms | Span creation |
| Snapshot capture | < 5ms | When breakpoint hit |
| Breakpoint poll | Negligible | Every 30s, non-blocking |

**Production-safe** with minimal impact on application performance.

---

## Troubleshooting

### Traces not appearing?

1. Check API key is correct: `echo $TRACEKIT_API_KEY`
2. Verify endpoint is accessible: `curl https://app.tracekit.dev`
3. Check logs for OpenTelemetry errors
4. Ensure middleware is added before routes

### Code monitoring not working?

1. Enable in config: `EnableCodeMonitoring: true`
2. Send some traces (code discovery needs stack traces)
3. Browse discovered code in UI
4. Create breakpoints for discovered locations

### High cardinality warnings?

Avoid high-cardinality attributes (UUIDs, timestamps) as span names or tag keys. Use them as attribute values instead.

---

## Migration from Manual OpenTelemetry

**Replace this:**
```go
// Old manual setup (~80 lines)
exporter, _ := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(...))
tp := sdktrace.NewTracerProvider(...)
otel.SetTracerProvider(tp)
r.Use(otelgin.Middleware(...))
db.Use(otelgorm.NewPlugin(...))
// ... more setup
```

**With this:**
```go
// New TraceKit SDK (3 lines)
sdk, _ := tracekit.NewSDK(&tracekit.Config{...})
r.Use(sdk.GinMiddleware())
sdk.WrapGorm(db)
```

**Same functionality, 95% less code!**

---

## Examples

See [examples/](../../examples/) for complete working applications:
- Gin web server
- gRPC service
- Background worker
- Microservices

---

## Support

- **Documentation**: [https://app.tracekit.dev/docs](https://app.tracekit.dev/docs)
- **Code Monitoring**: [https://app.tracekit.dev/docs/code-monitoring](https://app.tracekit.dev/docs/code-monitoring)
- **GitHub**: [github.com/Tracekit-Dev/go-sdk](https://github.com/Tracekit-Dev/go-sdk)
- **Issues**: [GitHub Issues](https://github.com/Tracekit-Dev/go-sdk/issues)

---

## License

MIT License - see [LICENSE](LICENSE) file

---

**Built with ❤️ by TraceKit** - Making observability simple and powerful.

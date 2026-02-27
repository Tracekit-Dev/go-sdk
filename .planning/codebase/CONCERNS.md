# Codebase Concerns

**Analysis Date:** 2026-02-27

## Tech Debt

**Silent failure in metrics export:**
- Issue: Metrics buffer flushes silently fail without logging on error. `metrics_buffer.go` line 81-83 catches errors from exporter but only logs "[best-effort]" philosophy.
- Files: `tracekit/metrics_buffer.go:81-83`
- Impact: Metrics export failures go unnoticed in production. Operators have no visibility into why metrics aren't reaching the backend. This complicates troubleshooting.
- Fix approach: Implement configurable error logging. Add `OnMetricsExportError` callback or at minimum emit a warning log that can be disabled via configuration. Update TODO comment on line 83 to add optional logging.

**Incomplete trace/span ID extraction in snapshots:**
- Issue: `CheckAndCapture()` method has TODO comment indicating trace/span IDs aren't extracted from context. Only `CheckAndCaptureWithContext()` implements this feature.
- Files: `tracekit/client.go:223`
- Impact: Code monitoring snapshots captured via `CheckAndCapture()` lack trace context correlation. This reduces debugging value when correlating snapshots to distributed traces.
- Fix approach: Extract trace/span IDs in `CheckAndCapture()` from the current goroutine context if available, or accept an optional context parameter. Add tests for both snapshot methods.

**Hard-coded local UI endpoint and development check:**
- Issue: Local UI detection uses hard-coded hostname `localhost:9999` and checks `ENV == "development"` string comparison. Multiple locations: config.go lines 162, 188, 226, 304.
- Files: `tracekit/config.go:162`, `tracekit/config.go:188`, `tracekit/config.go:226`, `tracekit/config.go:304`
- Impact: Hard to test, hard to configure for non-standard setups. String comparison on ENV is fragile - requires exact case match.
- Fix approach: Add `LocalUIEndpoint` and `LocalUIEnabled` to Config struct. Change ENV check to case-insensitive. Make endpoint configurable with sensible defaults.

**No graceful goroutine shutdown in snapshot polling:**
- Issue: `pollBreakpoints()` in `tracekit/client.go:98-116` runs in an unbounded goroutine that only stops on channel close. If `Stop()` is called before startup completes, race condition possible.
- Files: `tracekit/client.go:86-95`, `tracekit/client.go:98-116`
- Impact: Potential goroutine leaks in tests or short-lived applications. No wait-for-completion mechanism.
- Fix approach: Add sync.WaitGroup to track polling goroutine. Change `Stop()` to wait for poll loop exit with timeout. Add integration test that repeatedly starts/stops client.

**Limited error handling in autoRegisterBreakpoint goroutine:**
- Issue: Auto-registration in `tracekit/client.go:337-368` runs in background goroutine with no error reporting. Silently catches errors on lines 350, 358.
- Files: `tracekit/client.go:337-368`
- Impact: Breakpoint registration failures go silent. Developers won't know if breakpoints failed to register automatically.
- Fix approach: Implement optional error callback or channel-based error reporting. Log registration failures at WARN level by default.

**Query statement logging exposes sensitive data:**
- Issue: Database statements logged via span attributes (database.go:37, gorm.go:79) may contain query parameters with sensitive data (passwords, API keys, PII).
- Files: `tracekit/database.go:37`, `tracekit/gorm.go:79`
- Impact: Security risk - sensitive data in query strings/statements could be leaked to trace backend. OWASP A02:2021 - Cryptographic Failures.
- Fix approach: Implement query sanitization utility. Replace parameter values with placeholders. Add `SanitizeQueries` flag to Config (default: true). Document security implications.

**Context key collision risk in snapshot client:**
- Issue: `contextKey` type defined in gin.go but used in client.go. Both use string "tracekit.request_context" key. If multiple packages define similar keys, collisions possible.
- Files: `tracekit/gin.go:10-13`, `tracekit/client.go:408`
- Impact: Context value collisions if user code uses same key. Should be a private package constant.
- Fix approach: Move `contextKey` type to internal/context package or define once in config.go. Export as `ContextKey` constant. Add documentation on context key usage.

## Performance Bottlenecks

**Fixed 100-item metrics buffer with no backpressure:**
- Issue: Metrics buffer has hard-coded `maxSize: 100` and `flushInterval: 10s` (metrics_buffer.go:33-34). No configuration or backpressure handling.
- Files: `tracekit/metrics_buffer.go:28-35`
- Impact: Under high metric volume, buffer could grow unbounded if flush fails. No backpressure means unlimited memory growth possible. Fixed flush interval may miss bursty metric patterns.
- Fix approach: Make buffer size, flush interval, and flush timeout configurable. Implement buffer overflow handling (drop oldest, sample, or blocking). Add metrics for buffer health (size, flushes, errors).

**Stack trace capture limited to 32 frames:**
- Issue: Stack trace in span.go captures only first 32 frames (`maxStackSize = 32` on line 70). Deeply nested calls truncated.
- Files: `tracekit/span.go:70`
- Impact: Lost context for debugging deep call stacks. Hard to diagnose issues in frameworks with deep call chains.
- Fix approach: Make max stack size configurable. Consider streaming frames for very deep stacks. Add configuration to span.go error handling.

**No connection pooling configuration for snapshot HTTP client:**
- Issue: Snapshot client creates http.Client with 30s timeout but no connection pooling tuning (client.go:78).
- Files: `tracekit/client.go:78`
- Impact: Could create many TCP connections for rapid successive snapshot sends. No keep-alive or connection reuse.
- Fix approach: Use default http.Client with configured keep-alive, or expose DialContext override for connection pooling customization.

**Regex compilation on every security scan:**
- Issue: `scanForSecurityIssues()` in client.go compiles sensitive data regex patterns on every call (lines 419-424).
- Files: `tracekit/client.go:417-424`
- Impact: Repeated regex compilation is expensive. With many snapshots, this creates CPU overhead.
- Fix approach: Compile patterns once at package init or client creation. Cache in client struct.

## Security Considerations

**No authentication for snapshot auto-register endpoint:**
- Issue: Auto-register sends breakpoint data without validating response authenticity. Endpoint is hit every time code calls `CheckAndCaptureWithContext()`.
- Files: `tracekit/client.go:337-368`
- Impact: Man-in-the-middle could inject false breakpoints. No HTTPS enforcement at SDK level (relies on config).
- Fix approach: Add response signing/verification. Enforce HTTPS via flag. Add certificate pinning option. Document HTTPS requirement.

**Sensitive variables redacted but still logged:**
- Issue: Security scanning redacts values but original variables still sent via snapshot goroutine. Variables map is passed to `captureSnapshot()`.
- Files: `tracekit/client.go:209-220`, `tracekit/client.go:301-319`
- Impact: Redaction is cosmetic - logs still contain variable names and potentially structured PII (JSON objects, maps).
- Fix approach: Implement deeper sanitization - redact nested object values, array contents. Add PII detection beyond pattern matching.

**Client IP extraction doesn't validate X-Forwarded-For chain:**
- Issue: `ExtractClientIP()` in http.go:182-191 takes first IP from X-Forwarded-For without validating trust chain.
- Files: `tracekit/http.go:175-216`
- Impact: If untrusted proxies exist, attacker can spoof client IP via X-Forwarded-For header.
- Fix approach: Add `TrustedProxies` list to Config. Only use X-Forwarded-For if request came from trusted proxy. Use `net` package's subnet matching.

**API key sent in headers without encryption:**
- Issue: X-API-Key header sent unencrypted in every HTTP request. Vulnerable if HTTPS not enforced.
- Files: `tracekit/config.go:368`, `tracekit/client.go:128`, `tracekit/client.go:355`, `tracekit/http.go:387`
- Impact: API key compromise if traffic intercepted or logs leaked.
- Fix approach: Enforce HTTPS at SDK level (fail to initialize if UseSSL=false and Endpoint requires it). Add HSTS support. Document that HTTP is dev-only.

## Fragile Areas

**Complex endpoint resolution logic with multiple path handling rules:**
- Issue: URL construction in config.go (lines 82-157) has intricate logic for extracting base URLs, handling service-specific paths, detecting custom paths. Multiple conditional branches.
- Files: `tracekit/config.go:82-157`
- Why fragile: Easy to introduce bugs when adding new endpoint patterns. Hard to maintain. Examples show issues with custom paths vs /v1/traces paths.
- Safe modification: Add comprehensive test cases for all endpoint patterns (custom paths, with/without scheme, with/without trailing slash). Extract to dedicated URLBuilder struct. Add integration tests with real endpoint.
- Test coverage: config_test.go has some tests but doesn't cover all custom path scenarios. Add test cases for: localhost:port/api, bare hostnames, IPv6, unusual schemes.

**Breakpoint cache key management with label and line fallback:**
- Issue: Breakpoints stored with two keys (function:label and file:line) in client.go:156-181. Mismatch between registration and lookup could cause cache miss.
- Files: `tracekit/client.go:155-181`, `tracekit/client.go:253-267`
- Why fragile: Dual-key approach error-prone. If registration uses label but lookup uses line, breakpoint won't trigger.
- Safe modification: Add validation that breakpoint matches on both lookup methods. Add cache consistency tests. Consider single-key approach (label only after registration).
- Test coverage: No tests for cache misses or label/line mismatches. Add tests for registration then immediate capture.

**Concurrent access to breakpoint cache with read-write locks:**
- Issue: Multiple RWMutex-protected operations in client.go. updateBreakpointCache (157-181), CheckAndCaptureWithContext (254-267), autoRegisterBreakpoint (328-334).
- Files: `tracekit/client.go:157`, `tracekit/client.go:254`, `tracekit/client.go:328`
- Why fragile: RWMutex can mask race conditions. Tight lock windows (lines 328-334 unlock at 334) but goroutine registration happens after unlock. Race between unlock and subsequent fetch.
- Safe modification: Use sync.Map or fine-grained locking. Add -race flag to test suite. Test with high concurrency (10K+ goroutines).
- Test coverage: No concurrency/race tests. Add tests that call CheckAndCaptureWithContext from 100+ goroutines simultaneously.

**Local UI span processor with non-blocking send:**
- Issue: Local UI processor (config.go:185-238) sends spans in background goroutine with fire-and-forget semantics. No error handling or retry.
- Files: `tracekit/config.go:185-238`
- Why fragile: Lost spans if local UI server restarts. No timeout per span (uses 1s timeout on client). Developer won't know if local debugging is working.
- Safe modification: Add health check callback. Implement exponential backoff retry. Add metrics for local UI sends. Consider sync send with timeout for development.
- Test coverage: No tests for local UI processor. Add mock server tests for success/failure/timeout scenarios.

## Known Bugs

**Metrics buffer flush triggered by goroutine can race with shutdown:**
- Symptoms: Occasional nil pointer or "send on closed channel" panic during SDK Shutdown
- Files: `tracekit/metrics_buffer.go:45`, `tracekit/metrics_buffer.go:88-91`
- Trigger: Call `sdk.Shutdown()` while metrics are being added. Background flush triggered by line 45 can race with close(b.stop) at line 88.
- Workaround: Add small sleep before Shutdown (e.g., 200ms) to let in-flight flushes complete. Or implement flush completion channel.
- Fix: Add sync.WaitGroup tracking active flushes. Shutdown waits for all flushes to complete before closing channel.

**HTTP client methods without context parameter lose trace context:**
- Symptoms: Database operations via QueryContext() don't propagate context properly when called from non-context paths
- Files: `tracekit/database.go:53-54`, `tracekit/database.go:72-73`, `tracekit/database.go:103-104`
- Trigger: Call `Query()`, `QueryRow()`, or `Exec()` (non-context variants) from within a span - span context is lost
- Workaround: Always use context variants (QueryContext, etc) and pass ctx from current span
- Fix: QueryContext methods should check if parent span exists in context.Background() calls

## Test Coverage Gaps

**No tests for configuration endpoint resolution:**
- What's not tested: Custom base paths, IPv6 addresses, unusual port numbers, edge cases with multiple slashes
- Files: `tracekit/config.go:82-157`
- Risk: Endpoint misconfigurations go unnoticed in production. Users report broken traces only after deployment.
- Priority: High

**Missing snapshot client concurrent access tests:**
- What's not tested: Multiple goroutines calling CheckAndCaptureWithContext simultaneously, cache consistency under concurrent updates
- Files: `tracekit/client.go:155-368`
- Risk: Race conditions and cache corruption in high-concurrency scenarios (web servers with many goroutines)
- Priority: High

**No error injection tests for backend failures:**
- What's not tested: Behavior when trace export fails, metrics export fails, snapshot upload fails. Graceful degradation.
- Files: `tracekit/metrics_exporter.go`, `tracekit/client.go`, entire SDK shutdown
- Risk: Unknown behavior under failure conditions. SDK reliability untested.
- Priority: Medium

**Limited integration tests for framework middleware:**
- What's not tested: Actual HTTP requests through Gin/Echo middleware, distributed trace context propagation across frameworks
- Files: `tracekit/gin.go`, `tracekit/echo.go`, `tracekit/http.go`
- Risk: Middleware integration bugs only discovered in production
- Priority: Medium

**No load/stress tests for metrics and snapshots:**
- What's not tested: Behavior under high volume (10K+ metrics/sec, 100+ snapshots/sec), memory leaks, goroutine leaks
- Files: All buffering and async code paths
- Risk: SDK works in dev but fails under production load
- Priority: High

## Scaling Limits

**Fixed metrics buffer size doesn't scale with workload:**
- Current capacity: Buffer flushes at 100 metrics or every 10 seconds
- Limit: At 10K metrics/sec, buffer overflows in 10ms. No adaptive behavior.
- Scaling path: Make buffer size dynamic based on flush latency and throughput. Implement bounded queue with drop-oldest strategy. Add telemetry for buffer health.

**Single-threaded breakpoint poll doesn't scale with many services:**
- Current capacity: One poll request every 30 seconds per service instance
- Limit: At 1000 services, 33 requests/sec to breakpoint endpoint. No batching.
- Scaling path: Implement batch breakpoint fetch endpoint. Add caching with TTL. Reduce poll frequency with jitter to prevent thundering herd.

**No connection reuse across repeated snapshot sends:**
- Current capacity: Each snapshot creates new HTTP connection
- Limit: High latency (TCP handshake) for rapid snapshots. Resource exhaustion under heavy debugging.
- Scaling path: Maintain persistent connection pool. Implement HTTP/2 multiplexing for snapshot uploads.

## Dependency Concerns

**Version mismatch between OpenTelemetry packages:**
- Risk: go.mod shows `otlptrace v1.24.0` but other otel packages are `v1.38.0`. This versioning mismatch can cause SDK instability.
- Files: `go.mod:18`, `go.mod:17`
- Impact: Potential incompatibilities between OTLP exporter and tracer provider. Subtle bugs around span batching or attribute handling.
- Migration plan: Upgrade otlptrace to v1.38.0 to match other dependencies. Verify compatibility with backend OTLP receiver.

**Go version requirement not strictly enforced:**
- Risk: go.mod specifies `go 1.23.0` but toolchain is `go1.24.10`. SDK might not build on Go 1.23.
- Files: `go.mod:3-5`
- Impact: CI/CD in environments with older Go versions will fail silently or use wrong toolchain.
- Migration plan: Test build on exact `go 1.23.0`. Either update minimum to 1.24 or ensure backward compatibility.

## Missing Critical Features

**No distributed context propagation example:**
- Problem: `examples/context_propagation.go` exists but shows only basic usage. No example of trace context flowing across service boundaries (HTTP headers, gRPC metadata).
- Blocks: Users unsure how to set up proper distributed tracing across multiple services.
- Solution: Add example showing trace context propagation via HTTP W3C headers and gRPC metadata extraction.

**No shutdown timeout handling:**
- Problem: SDK.Shutdown() has no timeout. Can hang indefinitely if backend is unreachable.
- Blocks: Graceful shutdown impossible in containerized environments with kill timeouts.
- Solution: Add `ShutdownTimeout` to Config (default 30s). Implement context-aware shutdown with deadline.

**No debug/verbose logging mode:**
- Problem: SDK runs silently except for emojis in console. Troubleshooting connectivity issues requires code inspection.
- Blocks: Operators can't easily diagnose why traces aren't reaching backend without adding custom logs.
- Solution: Add `DebugLogging` flag to Config. Log all HTTP requests/responses, breakpoint fetches, metric flushes at debug level.

---

*Concerns audit: 2026-02-27*

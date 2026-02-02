package tracekit

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
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds the TraceKit SDK configuration
type Config struct {
	// Required
	APIKey      string
	ServiceName string

	// Optional - defaults to app.tracekit.dev
	// Can be just the host (app.tracekit.dev) or full URL
	Endpoint string

	// Optional - defaults to /v1/traces
	TracesPath string

	// Optional - defaults to /v1/metrics
	MetricsPath string

	// Optional - defaults to true (use TLS)
	UseSSL bool

	// Optional - service version
	ServiceVersion string

	// Optional - deployment environment
	Environment string

	// Optional - additional resource attributes
	ResourceAttributes map[string]string

	// Optional - enable code monitoring
	EnableCodeMonitoring bool

	// Optional - code monitoring poll interval (default: 30s)
	CodeMonitoringPollInterval time.Duration

	// Optional - sampling rate (0.0 to 1.0, default: 1.0 = 100%)
	SamplingRate float64

	// Optional - batch timeout (default: 5s)
	BatchTimeout time.Duration

	// Optional - map hostnames to service names for peer.service attribute
	// Useful for mapping localhost URLs to actual service names
	// Example: map[string]string{"localhost:8084": "node-test-app", "localhost:8082": "go-test-app"}
	ServiceNameMappings map[string]string
}

// SDK is the main TraceKit SDK client
type SDK struct {
	config          *Config
	tracer          trace.Tracer
	tracerProvider  *sdktrace.TracerProvider
	snapshotClient  *SnapshotClient
	metricsRegistry *metricsRegistry
	localUIEnabled  bool
}

// resolveEndpoint builds the full endpoint URL from base endpoint and path
func resolveEndpoint(endpoint, path string, useSSL bool) string {
	// If endpoint already has a scheme
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		// Strip trailing slash if present
		endpoint = strings.TrimSuffix(endpoint, "/")

		// Check if endpoint already has a path component (anything after the host)
		// e.g., "http://localhost:8081/v1/traces" or "http://localhost:8081/custom"
		trimmed := strings.TrimPrefix(endpoint, "https://")
		trimmed = strings.TrimPrefix(trimmed, "http://")

		// If there's a "/" after the host, it has a real path
		if strings.Contains(trimmed, "/") {
			// Always extract base URL and append correct path
			base := extractBaseURL(endpoint)
			if path == "" {
				return base
			}
			return base + path
		}

		// Just host (possibly had trailing /), add the path
		return endpoint + path
	}

	// Build URL with scheme
	scheme := "https://"
	if !useSSL {
		scheme = "http://"
	}

	// Strip trailing slash from endpoint if present
	endpoint = strings.TrimSuffix(endpoint, "/")

	return scheme + endpoint + path
}

// extractBaseURL extracts scheme + host from a full URL, but only if it contains
// known service-specific paths like /v1/traces or /v1/metrics.
// This prevents extracting base from custom base paths like /api or /custom.
// e.g., "http://localhost:8081/v1/traces" -> "http://localhost:8081"
// e.g., "http://localhost:8081/custom" -> "http://localhost:8081/custom" (kept as-is)
func extractBaseURL(fullURL string) string {
	// Check if URL contains known service-specific paths
	hasServicePath := strings.Contains(fullURL, "/v1/traces") ||
		strings.Contains(fullURL, "/v1/metrics") ||
		strings.Contains(fullURL, "/api/v1/traces") ||
		strings.Contains(fullURL, "/api/v1/metrics")

	// If it doesn't have a service-specific path, keep the URL as-is
	// (it's likely a custom base path like /custom or /api)
	if !hasServicePath {
		return fullURL
	}

	// Extract scheme
	var scheme string
	remaining := fullURL
	if strings.HasPrefix(fullURL, "https://") {
		scheme = "https://"
		remaining = strings.TrimPrefix(fullURL, "https://")
	} else if strings.HasPrefix(fullURL, "http://") {
		scheme = "http://"
		remaining = strings.TrimPrefix(fullURL, "http://")
	} else {
		return fullURL // No scheme, return as-is
	}

	// Find first "/" to separate host from path
	if idx := strings.Index(remaining, "/"); idx != -1 {
		return scheme + remaining[:idx]
	}

	// No path, return as-is
	return scheme + remaining
}

// detectLocalUI checks if TraceKit Local UI is running
func detectLocalUI() bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://localhost:9999/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// localUISpanProcessor is a custom span processor that sends traces to local UI
type localUISpanProcessor struct {
	client *http.Client
}

// newLocalUISpanProcessor creates a new local UI span processor
func newLocalUISpanProcessor() *localUISpanProcessor {
	return &localUISpanProcessor{
		client: &http.Client{Timeout: 1 * time.Second},
	}
}

// OnStart is called when a span starts (no-op for our use case)
func (p *localUISpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {}

// OnEnd is called when a span ends - sends to local UI in a goroutine
func (p *localUISpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	// Only send in development environment
	if os.Getenv("ENV") != "development" {
		return
	}

	// Send to local UI in background (non-blocking)
	go func() {
		// Convert span to OTLP format (simplified)
		// The actual OTLP exporter handles the full conversion
		// For local UI, we'll send a simplified payload
		payload := map[string]interface{}{
			"resourceSpans": []map[string]interface{}{
				{
					"scopeSpans": []map[string]interface{}{
						{
							"spans": []map[string]interface{}{
								{
									"traceId":           s.SpanContext().TraceID().String(),
									"spanId":            s.SpanContext().SpanID().String(),
									"parentSpanId":      s.Parent().SpanID().String(),
									"name":              s.Name(),
									"kind":              s.SpanKind(),
									"startTimeUnixNano": s.StartTime().UnixNano(),
									"endTimeUnixNano":   s.EndTime().UnixNano(),
									"attributes":        convertAttributes(s.Attributes()),
									"status":            map[string]interface{}{"code": s.Status().Code},
								},
							},
						},
					},
				},
			},
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return
		}

		req, err := http.NewRequest("POST", "http://localhost:9999/v1/traces", bytes.NewBuffer(body))
		if err != nil {
			return
		}

		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			log.Println("🔍 Sent to Local UI")
		}
	}()
}

// Shutdown is called when the processor is shutting down
func (p *localUISpanProcessor) Shutdown(ctx context.Context) error {
	return nil
}

// ForceFlush is called to force flush pending spans
func (p *localUISpanProcessor) ForceFlush(ctx context.Context) error {
	return nil
}

// convertAttributes converts OTEL attributes to a simple map
func convertAttributes(attrs []attribute.KeyValue) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(attrs))
	for _, attr := range attrs {
		result = append(result, map[string]interface{}{
			"key":   string(attr.Key),
			"value": map[string]interface{}{"stringValue": attr.Value.AsString()},
		})
	}
	return result
}

// NewSDK creates and initializes the TraceKit SDK
func NewSDK(config *Config) (*SDK, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("APIKey is required")
	}
	if config.ServiceName == "" {
		return nil, fmt.Errorf("ServiceName is required")
	}

	// Set defaults
	if config.Endpoint == "" {
		config.Endpoint = "app.tracekit.dev"
	}
	if config.TracesPath == "" {
		config.TracesPath = "/v1/traces"
	}
	if config.MetricsPath == "" {
		config.MetricsPath = "/v1/metrics"
	}
	if config.ServiceVersion == "" {
		config.ServiceVersion = "1.0.0"
	}
	if config.SamplingRate == 0 {
		config.SamplingRate = 1.0
	}
	if config.BatchTimeout == 0 {
		config.BatchTimeout = 5 * time.Second
	}
	if config.CodeMonitoringPollInterval == 0 {
		config.CodeMonitoringPollInterval = 30 * time.Second
	}

	// Resolve full endpoint URLs
	tracesEndpoint := resolveEndpoint(config.Endpoint, config.TracesPath, config.UseSSL)
	metricsEndpoint := resolveEndpoint(config.Endpoint, config.MetricsPath, config.UseSSL)

	sdk := &SDK{
		config: config,
	}

	// Detect local UI in development mode
	if os.Getenv("ENV") == "development" {
		if detectLocalUI() {
			sdk.localUIEnabled = true
			log.Println("🔍 Local UI detected at http://localhost:9999")
		}
	}

	// Initialize tracer
	if err := sdk.initTracer(tracesEndpoint); err != nil {
		return nil, fmt.Errorf("failed to initialize tracer: %w", err)
	}

	// Initialize metrics registry
	sdk.metricsRegistry = newMetricsRegistry(metricsEndpoint, config.APIKey, config.ServiceName)

	// Initialize code monitoring if enabled
	if config.EnableCodeMonitoring {
		// Snapshot client needs base URL (without path)
		snapshotEndpoint := resolveEndpoint(config.Endpoint, "", config.UseSSL)

		sdk.snapshotClient = NewSnapshotClient(
			config.APIKey,
			snapshotEndpoint,
			config.ServiceName,
		)
		sdk.snapshotClient.Start()
	}

	log.Printf("✅ TraceKit SDK initialized for service: %s", config.ServiceName)
	return sdk, nil
}

// initTracer initializes the OpenTelemetry tracer
func (s *SDK) initTracer(tracesEndpoint string) error {
	ctx := context.Background()

	// Parse the traces endpoint to extract host and path
	var endpoint, urlPath string
	var useSSL bool

	if strings.HasPrefix(tracesEndpoint, "https://") {
		useSSL = true
		tracesEndpoint = strings.TrimPrefix(tracesEndpoint, "https://")
	} else if strings.HasPrefix(tracesEndpoint, "http://") {
		useSSL = false
		tracesEndpoint = strings.TrimPrefix(tracesEndpoint, "http://")
	}

	// Split host and path
	parts := strings.SplitN(tracesEndpoint, "/", 2)
	endpoint = parts[0]
	if len(parts) > 1 {
		urlPath = "/" + parts[1]
	} else {
		urlPath = "/v1/traces"
	}

	// Configure OTLP exporter
	var opts []otlptracehttp.Option
	opts = append(opts,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithURLPath(urlPath),
		otlptracehttp.WithHeaders(map[string]string{
			"X-API-Key": s.config.APIKey,
		}),
	)

	// Configure TLS
	if useSSL {
		opts = append(opts, otlptracehttp.WithTLSClientConfig(&tls.Config{}))
	} else {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	// Create exporter
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return err
	}

	// Build resource attributes
	attrs := []attribute.KeyValue{
		semconv.ServiceName(s.config.ServiceName),
		semconv.ServiceVersion(s.config.ServiceVersion),
	}

	if s.config.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironment(s.config.Environment))
	}

	// Add custom attributes
	for k, v := range s.config.ResourceAttributes {
		attrs = append(attrs, attribute.String(k, v))
	}

	// Create resource
	res, err := resource.New(
		ctx,
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return err
	}

	// Create tracer provider with sampling
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(s.config.SamplingRate))

	// Prepare tracer provider options
	tpOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(s.config.BatchTimeout),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}

	// Add local UI span processor if enabled
	if s.localUIEnabled {
		tpOptions = append(tpOptions, sdktrace.WithSpanProcessor(newLocalUISpanProcessor()))
	}

	s.tracerProvider = sdktrace.NewTracerProvider(tpOptions...)

	// Set global providers
	otel.SetTracerProvider(s.tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Get tracer
	s.tracer = s.tracerProvider.Tracer(s.config.ServiceName)

	return nil
}

// Tracer returns the underlying OpenTelemetry tracer
func (s *SDK) Tracer() trace.Tracer {
	return s.tracer
}

// SnapshotClient returns the code monitoring client (nil if not enabled)
func (s *SDK) SnapshotClient() *SnapshotClient {
	return s.snapshotClient
}

// CheckAndCapture checks if there's a breakpoint and captures a snapshot
func (s *SDK) CheckAndCapture(filePath string, lineNumber int, variables map[string]interface{}) {
	if s.snapshotClient != nil {
		s.snapshotClient.CheckAndCapture(filePath, lineNumber, variables)
	}
}

// CheckAndCaptureWithContext is a wrapper for code monitoring snapshot capture with context
// It automatically registers the breakpoint location - no need to manually create breakpoints!
//
// label: Optional stable identifier (e.g. "payment-error", "auth-check")
//
//	If empty, uses auto-generated label based on function name
func (s *SDK) CheckAndCaptureWithContext(ctx context.Context, label string, variables map[string]interface{}) {
	if s.snapshotClient != nil {
		s.snapshotClient.CheckAndCaptureWithContext(ctx, label, variables)
	}
}

// Shutdown gracefully shuts down the SDK
func (s *SDK) Shutdown(ctx context.Context) error {
	if s.snapshotClient != nil {
		s.snapshotClient.Stop()
	}

	if s.metricsRegistry != nil {
		s.metricsRegistry.shutdown()
	}

	if s.tracerProvider != nil {
		return s.tracerProvider.Shutdown(ctx)
	}

	return nil
}

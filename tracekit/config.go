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
	Endpoint string

	// Optional - defaults to /v1/traces
	TracesPath string

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
	config         *Config
	tracer         trace.Tracer
	tracerProvider *sdktrace.TracerProvider
	snapshotClient *SnapshotClient
	localUIEnabled bool
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
	if err := sdk.initTracer(); err != nil {
		return nil, fmt.Errorf("failed to initialize tracer: %w", err)
	}

	// Initialize code monitoring if enabled
	if config.EnableCodeMonitoring {
		endpoint := config.Endpoint
		if config.UseSSL {
			endpoint = "https://" + endpoint
		} else {
			endpoint = "http://" + endpoint
		}

		sdk.snapshotClient = NewSnapshotClient(
			config.APIKey,
			endpoint,
			config.ServiceName,
		)
		sdk.snapshotClient.Start()
	}

	log.Printf("✅ TraceKit SDK initialized for service: %s", config.ServiceName)
	return sdk, nil
}

// initTracer initializes the OpenTelemetry tracer
func (s *SDK) initTracer() error {
	ctx := context.Background()

	// Configure OTLP exporter
	var opts []otlptracehttp.Option
	opts = append(opts,
		otlptracehttp.WithEndpoint(s.config.Endpoint),
		otlptracehttp.WithURLPath(s.config.TracesPath),
		otlptracehttp.WithHeaders(map[string]string{
			"X-API-Key": s.config.APIKey,
		}),
	)

	// Configure TLS
	if s.config.UseSSL {
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

	if s.tracerProvider != nil {
		return s.tracerProvider.Shutdown(ctx)
	}

	return nil
}

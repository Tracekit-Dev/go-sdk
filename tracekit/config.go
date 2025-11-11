package tracekit

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
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
}

// SDK is the main TraceKit SDK client
type SDK struct {
	config         *Config
	tracer         trace.Tracer
	tracerProvider *sdktrace.TracerProvider
	snapshotClient *SnapshotClient
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

	s.tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(s.config.BatchTimeout),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

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

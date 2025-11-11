package tracekit

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// HTTPHandler wraps an http.Handler with OpenTelemetry instrumentation
func (s *SDK) HTTPHandler(handler http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(handler, operation,
		otelhttp.WithTracerProvider(s.tracerProvider),
	)
}

// HTTPMiddleware returns a middleware function for standard http.Handler chains
func (s *SDK) HTTPMiddleware(operation string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.HTTPHandler(next, operation)
	}
}

// HTTPClient wraps an http.Client with OpenTelemetry instrumentation
func (s *SDK) HTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}

	client.Transport = otelhttp.NewTransport(client.Transport,
		otelhttp.WithTracerProvider(s.tracerProvider),
	)

	return client
}

// WrapRoundTripper wraps an http.RoundTripper with OpenTelemetry instrumentation
func (s *SDK) WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(rt,
		otelhttp.WithTracerProvider(s.tracerProvider),
	)
}

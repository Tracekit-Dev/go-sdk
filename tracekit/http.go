package tracekit

import (
	"net"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// clientIPMiddleware adds client IP to the current span
type clientIPMiddleware struct {
	handler http.Handler
}

// ServeHTTP adds the client IP attribute to the span
func (m *clientIPMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract client IP
	clientIP := ExtractClientIP(r)

	// Add to current span if valid
	if clientIP != "" {
		span := trace.SpanFromContext(r.Context())
		if span.SpanContext().IsValid() {
			span.SetAttributes(attribute.String("http.client_ip", clientIP))
		}
	}

	// Continue to the next handler
	m.handler.ServeHTTP(w, r)
}

// HTTPHandler wraps an http.Handler with OpenTelemetry instrumentation
// and automatically captures client IP address
func (s *SDK) HTTPHandler(handler http.Handler, operation string) http.Handler {
	// Wrap with OTEL instrumentation
	otelHandler := otelhttp.NewHandler(handler, operation,
		otelhttp.WithTracerProvider(s.tracerProvider),
	)

	// Wrap with client IP middleware
	return &clientIPMiddleware{handler: otelHandler}
}

// HTTPMiddleware returns a middleware function for standard http.Handler chains
func (s *SDK) HTTPMiddleware(operation string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.HTTPHandler(next, operation)
	}
}

// HTTPClient wraps an http.Client with OpenTelemetry instrumentation
// Automatically creates CLIENT spans for outgoing HTTP calls with peer.service attribute
func (s *SDK) HTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}

	client.Transport = otelhttp.NewTransport(client.Transport,
		otelhttp.WithTracerProvider(s.tracerProvider),
		otelhttp.WithSpanOptions(
			trace.WithSpanKind(trace.SpanKindClient),
		),
	)

	// Wrap with our custom transport to add peer.service
	client.Transport = &peerServiceTransport{
		base:                client.Transport,
		serviceNameMappings: s.config.ServiceNameMappings,
	}

	return client
}

// WrapRoundTripper wraps an http.RoundTripper with OpenTelemetry instrumentation
func (s *SDK) WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	wrapped := otelhttp.NewTransport(rt,
		otelhttp.WithTracerProvider(s.tracerProvider),
		otelhttp.WithSpanOptions(
			trace.WithSpanKind(trace.SpanKindClient),
		),
	)

	// Wrap with our custom transport to add peer.service
	return &peerServiceTransport{
		base: wrapped,
	}
}

// peerServiceTransport adds peer.service attribute to outgoing HTTP requests
type peerServiceTransport struct {
	base                http.RoundTripper
	serviceNameMappings map[string]string
}

// RoundTrip implements http.RoundTripper
func (t *peerServiceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Extract service name from URL and add as span attribute
	serviceName := t.extractServiceName(req.URL.Host)

	// Get current span and add peer.service attribute
	span := trace.SpanFromContext(req.Context())
	if span.SpanContext().IsValid() {
		span.SetAttributes(
			semconv.PeerService(serviceName),
			attribute.String("http.host", req.URL.Host),
			attribute.String("http.scheme", req.URL.Scheme),
		)
	}

	return t.base.RoundTrip(req)
}

// extractServiceName extracts or maps service name from hostname
func (t *peerServiceTransport) extractServiceName(hostname string) string {
	// First, check if there's a configured mapping for this hostname
	// This allows mapping localhost:port to actual service names
	if t.serviceNameMappings != nil {
		if serviceName, ok := t.serviceNameMappings[hostname]; ok {
			return serviceName
		}

		// Also check without port
		hostWithoutPort := hostname
		if idx := strings.Index(hostname, ":"); idx != -1 {
			hostWithoutPort = hostname[:idx]
		}
		if serviceName, ok := t.serviceNameMappings[hostWithoutPort]; ok {
			return serviceName
		}
	}

	// Fall back to default extraction
	return extractServiceName(hostname)
}

// extractServiceName extracts service name from hostname
func extractServiceName(hostname string) string {
	// Handle Kubernetes service names
	// e.g., payment.internal.svc.cluster.local -> payment
	if strings.Contains(hostname, ".svc.cluster.local") {
		parts := strings.Split(hostname, ".")
		if len(parts) > 0 {
			return parts[0]
		}
	}

	// Handle internal domain
	// e.g., payment.internal:3000 -> payment
	if strings.Contains(hostname, ".internal") {
		// Strip port if present
		host := hostname
		if idx := strings.Index(host, ":"); idx != -1 {
			host = host[:idx]
		}
		parts := strings.Split(host, ".")
		if len(parts) > 0 {
			return parts[0]
		}
	}

	// For other hostnames, strip port and return full hostname
	// e.g., api.example.com:443 -> api.example.com
	// e.g., payment-service:3000 -> payment-service
	if idx := strings.Index(hostname, ":"); idx != -1 {
		return hostname[:idx]
	}

	return hostname
}

// ExtractClientIP extracts the client IP address from an HTTP request.
// It checks X-Forwarded-For, X-Real-IP headers (for proxied requests)
// and falls back to RemoteAddr.
// This function is used by the HTTP middleware to automatically add client IP to traces.
func ExtractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (for requests behind proxy/load balancer)
	// Format: "client, proxy1, proxy2"
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (the client)
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			clientIP := strings.TrimSpace(ips[0])
			// Validate it's a valid IP
			if net.ParseIP(clientIP) != nil {
				return clientIP
			}
		}
	}

	// Check X-Real-IP header (alternative proxy header)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		xri = strings.TrimSpace(xri)
		if net.ParseIP(xri) != nil {
			return xri
		}
	}

	// Fallback to RemoteAddr (direct connection)
	// RemoteAddr format: "IP:port" or "[IPv6]:port"
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If SplitHostPort fails, try using RemoteAddr as-is
		ip = r.RemoteAddr
	}

	// Validate and return
	if net.ParseIP(ip) != nil {
		return ip
	}

	return ""
}

package tracekit

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const requestContextKey contextKey = "tracekit.request_context"

// GinMiddleware returns a Gin middleware with OpenTelemetry instrumentation
// It captures request context for code monitoring and adds client IP to spans
func (s *SDK) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract client IP before creating span
		clientIP := ExtractClientIP(c.Request)

		// Capture request context for code monitoring
		requestContext := extractGinRequestContext(c)

		// Store in gin context for later retrieval
		c.Set(string(requestContextKey), requestContext)

		// Create OTEL middleware with client IP as a span attribute
		// We need to create it per-request so we can include the IP
		opts := []otelgin.Option{
			otelgin.WithTracerProvider(s.tracerProvider),
		}

		// Add client IP as initial span attribute if available
		if clientIP != "" {
			opts = append(opts, otelgin.WithSpanStartOptions(
				trace.WithAttributes(attribute.String("http.client_ip", clientIP)),
			))
		}

		otelMiddleware := otelgin.Middleware(s.config.ServiceName, opts...)

		// Call OTEL middleware
		otelMiddleware(c)
	}
}

// extractGinRequestContext extracts HTTP request details from Gin context
func extractGinRequestContext(c *gin.Context) map[string]interface{} {
	ctx := make(map[string]interface{})

	// Basic request info
	ctx["method"] = c.Request.Method
	ctx["path"] = c.Request.URL.Path
	ctx["remote_addr"] = c.ClientIP()
	ctx["user_agent"] = c.Request.UserAgent()

	// Query parameters
	if len(c.Request.URL.RawQuery) > 0 {
		params := make(map[string]string)
		for key, values := range c.Request.URL.Query() {
			if len(values) > 0 {
				params[key] = values[0]
			}
		}
		ctx["query_params"] = params
	}

	// Headers (filtered for security)
	headers := make(map[string]string)
	for key, values := range c.Request.Header {
		// Skip sensitive headers
		if key == "Authorization" || key == "Cookie" || key == "X-Api-Key" {
			headers[key] = "[REDACTED]"
			continue
		}
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	ctx["headers"] = headers

	return ctx
}

// GetRequestContext retrieves the request context from Gin context
func GetRequestContext(c *gin.Context) map[string]interface{} {
	if ctx, exists := c.Get(string(requestContextKey)); exists {
		if requestCtx, ok := ctx.(map[string]interface{}); ok {
			return requestCtx
		}
	}
	return nil
}

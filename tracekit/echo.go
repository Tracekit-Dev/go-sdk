package tracekit

import (
	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
)

// EchoMiddleware returns an Echo middleware with OpenTelemetry instrumentation
func (s *SDK) EchoMiddleware() echo.MiddlewareFunc {
	return otelecho.Middleware(s.config.ServiceName,
		otelecho.WithTracerProvider(s.tracerProvider),
	)
}

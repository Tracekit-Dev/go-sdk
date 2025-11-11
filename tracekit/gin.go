package tracekit

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// GinMiddleware returns a Gin middleware with OpenTelemetry instrumentation
func (s *SDK) GinMiddleware() gin.HandlerFunc {
	return otelgin.Middleware(s.config.ServiceName,
		otelgin.WithTracerProvider(s.tracerProvider),
	)
}

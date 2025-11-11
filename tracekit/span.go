package tracekit

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StartSpan starts a new span with the given name
func (s *SDK) StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return s.tracer.Start(ctx, name, opts...)
}

// AddAttribute adds a string attribute to a span
func (s *SDK) AddAttribute(span trace.Span, key, value string) {
	span.SetAttributes(attribute.String(key, value))
}

// AddAttributes adds multiple attributes to a span
func (s *SDK) AddAttributes(span trace.Span, attrs ...attribute.KeyValue) {
	span.SetAttributes(attrs...)
}

// AddIntAttribute adds an integer attribute to a span
func (s *SDK) AddIntAttribute(span trace.Span, key string, value int64) {
	span.SetAttributes(attribute.Int64(key, value))
}

// AddFloatAttribute adds a float attribute to a span
func (s *SDK) AddFloatAttribute(span trace.Span, key string, value float64) {
	span.SetAttributes(attribute.Float64(key, value))
}

// AddBoolAttribute adds a boolean attribute to a span
func (s *SDK) AddBoolAttribute(span trace.Span, key string, value bool) {
	span.SetAttributes(attribute.Bool(key, value))
}

// AddEvent adds an event to a span
func (s *SDK) AddEvent(span trace.Span, name string, attrs ...attribute.KeyValue) {
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// RecordError records an error on a span and marks it as error
func (s *SDK) RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// RecordErrorWithMessage records an error with a custom message
func (s *SDK) RecordErrorWithMessage(span trace.Span, err error, message string) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, message)
	}
}

// SetSuccess marks a span as successful
func (s *SDK) SetSuccess(span trace.Span) {
	span.SetStatus(codes.Ok, "")
}

// SetSuccessWithMessage marks a span as successful with a message
func (s *SDK) SetSuccessWithMessage(span trace.Span, message string) {
	span.SetStatus(codes.Ok, message)
}

// Helper functions for common attribute patterns

// AddHTTPAttributes adds common HTTP attributes to a span
func (s *SDK) AddHTTPAttributes(span trace.Span, method, url string, statusCode int) {
	s.AddAttributes(span,
		attribute.String("http.method", method),
		attribute.String("http.url", url),
		attribute.Int("http.status_code", statusCode),
	)
}

// AddDatabaseAttributes adds common database attributes to a span
func (s *SDK) AddDatabaseAttributes(span trace.Span, dbSystem, dbName, operation, table string) {
	s.AddAttributes(span,
		attribute.String("db.system", dbSystem),
		attribute.String("db.name", dbName),
		attribute.String("db.operation", operation),
		attribute.String("db.sql.table", table),
	)
}

// AddUserAttributes adds user-related attributes to a span
func (s *SDK) AddUserAttributes(span trace.Span, userID, email string) {
	attrs := []attribute.KeyValue{}
	if userID != "" {
		attrs = append(attrs, attribute.String("user.id", userID))
	}
	if email != "" {
		attrs = append(attrs, attribute.String("user.email", email))
	}
	if len(attrs) > 0 {
		s.AddAttributes(span, attrs...)
	}
}

// AddBusinessAttributes adds business-specific attributes (order ID, transaction ID, etc.)
func (s *SDK) AddBusinessAttributes(span trace.Span, attrs map[string]interface{}) {
	var otelAttrs []attribute.KeyValue

	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			otelAttrs = append(otelAttrs, attribute.String(k, val))
		case int:
			otelAttrs = append(otelAttrs, attribute.Int64(k, int64(val)))
		case int64:
			otelAttrs = append(otelAttrs, attribute.Int64(k, val))
		case float64:
			otelAttrs = append(otelAttrs, attribute.Float64(k, val))
		case bool:
			otelAttrs = append(otelAttrs, attribute.Bool(k, val))
		default:
			otelAttrs = append(otelAttrs, attribute.String(k, fmt.Sprintf("%v", val)))
		}
	}

	s.AddAttributes(span, otelAttrs...)
}

// TraceFunction wraps a function with automatic span creation
func (s *SDK) TraceFunction(ctx context.Context, name string, fn func(context.Context, trace.Span) error) error {
	ctx, span := s.StartSpan(ctx, name)
	defer span.End()

	err := fn(ctx, span)
	if err != nil {
		s.RecordError(span, err)
		return err
	}

	s.SetSuccess(span)
	return nil
}

package tracekit

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// GormPlugin returns a GORM plugin with OpenTelemetry instrumentation
// Use with: db.Use(sdk.GormPlugin())
func (s *SDK) GormPlugin() gorm.Plugin {
	return &gormPlugin{
		tracer: s.tracer,
	}
}

// gormPlugin implements gorm.Plugin interface for OpenTelemetry tracing
type gormPlugin struct {
	tracer trace.Tracer
}

func (p *gormPlugin) Name() string {
	return "otelgorm"
}

func (p *gormPlugin) Initialize(db *gorm.DB) error {
	// Register callbacks for all GORM operations
	db.Callback().Create().Before("gorm:create").Register("otel:before_create", p.before)
	db.Callback().Create().After("gorm:create").Register("otel:after_create", p.after("gorm.Create"))

	db.Callback().Query().Before("gorm:query").Register("otel:before_query", p.before)
	db.Callback().Query().After("gorm:query").Register("otel:after_query", p.after("gorm.Query"))

	db.Callback().Delete().Before("gorm:delete").Register("otel:before_delete", p.before)
	db.Callback().Delete().After("gorm:delete").Register("otel:after_delete", p.after("gorm.Delete"))

	db.Callback().Update().Before("gorm:update").Register("otel:before_update", p.before)
	db.Callback().Update().After("gorm:update").Register("otel:after_update", p.after("gorm.Update"))

	db.Callback().Row().Before("gorm:row").Register("otel:before_row", p.before)
	db.Callback().Row().After("gorm:row").Register("otel:after_row", p.after("gorm.Row"))

	db.Callback().Raw().Before("gorm:raw").Register("otel:before_raw", p.before)
	db.Callback().Raw().After("gorm:raw").Register("otel:after_raw", p.after("gorm.Raw"))

	return nil
}

func (p *gormPlugin) before(db *gorm.DB) {
	ctx, span := p.tracer.Start(db.Statement.Context, "gorm.query")

	// Store the span in the statement context
	db.Statement.Context = ctx
	db.InstanceSet("otel:span", span)
}

func (p *gormPlugin) after(operation string) func(db *gorm.DB) {
	return func(db *gorm.DB) {
		// Retrieve the span
		spanVal, ok := db.InstanceGet("otel:span")
		if !ok {
			return
		}

		span, ok := spanVal.(trace.Span)
		if !ok {
			return
		}
		defer span.End()

		// Update span name with actual operation
		span.SetName(operation)

		// Add attributes
		span.SetAttributes(
			attribute.String("db.system", db.Dialector.Name()),
			attribute.String("db.statement", db.Statement.SQL.String()),
		)

		if db.Statement.Table != "" {
			span.SetAttributes(attribute.String("db.table", db.Statement.Table))
		}

		// Record rows affected
		if db.Statement.RowsAffected >= 0 {
			span.SetAttributes(attribute.Int64("db.rows_affected", db.Statement.RowsAffected))
		}

		// Record error if any
		if db.Error != nil && db.Error != gorm.ErrRecordNotFound {
			span.RecordError(db.Error)
			span.SetAttributes(attribute.String("db.error", db.Error.Error()))
		}
	}
}

// WithGormTracing is a helper to configure a GORM DB with tracing
// Example:
//
//	db, err := gorm.Open(postgres.Open(dsn), sdk.WithGormTracing(&gorm.Config{}))
func (s *SDK) WithGormTracing(config *gorm.Config) *gorm.Config {
	if config == nil {
		config = &gorm.Config{}
	}

	// We'll add the plugin after DB is opened
	return config
}

// TraceGormDB adds tracing to an existing GORM DB instance
func (s *SDK) TraceGormDB(db *gorm.DB) error {
	return db.Use(s.GormPlugin())
}

// Helper to get database system name from error
func getDatabaseSystem(db *gorm.DB) string {
	if db.Dialector != nil {
		return db.Dialector.Name()
	}
	return "unknown"
}

// Helper to format SQL for display (truncate if too long)
func formatSQL(sql string) string {
	const maxLen = 500
	if len(sql) > maxLen {
		return fmt.Sprintf("%s... (truncated)", sql[:maxLen])
	}
	return sql
}

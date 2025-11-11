package tracekit

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// WrapDB wraps a database/sql DB with OpenTelemetry tracing
// This creates traced versions of all database operations
func (s *SDK) WrapDB(db *sql.DB, dbSystem string) *TracedDB {
	return &TracedDB{
		db:       db,
		tracer:   s.tracer,
		dbSystem: dbSystem,
	}
}

// TracedDB is a wrapper around sql.DB that adds tracing
type TracedDB struct {
	db       *sql.DB
	tracer   trace.Tracer
	dbSystem string
}

// QueryContext executes a query with tracing
func (tdb *TracedDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	ctx, span := tdb.tracer.Start(ctx, "sql.query")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", tdb.dbSystem),
		attribute.String("db.statement", query),
		attribute.String("db.operation", "SELECT"),
	)

	rows, err := tdb.db.QueryContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return rows, nil
}

// Query executes a query with tracing (no context)
func (tdb *TracedDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return tdb.QueryContext(context.Background(), query, args...)
}

// QueryRowContext executes a query that returns a single row with tracing
func (tdb *TracedDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	ctx, span := tdb.tracer.Start(ctx, "sql.query_row")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", tdb.dbSystem),
		attribute.String("db.statement", query),
		attribute.String("db.operation", "SELECT"),
	)

	return tdb.db.QueryRowContext(ctx, query, args...)
}

// QueryRow executes a query that returns a single row with tracing (no context)
func (tdb *TracedDB) QueryRow(query string, args ...interface{}) *sql.Row {
	return tdb.QueryRowContext(context.Background(), query, args...)
}

// ExecContext executes a query without returning rows, with tracing
func (tdb *TracedDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	ctx, span := tdb.tracer.Start(ctx, "sql.exec")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", tdb.dbSystem),
		attribute.String("db.statement", query),
	)

	result, err := tdb.db.ExecContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Add rows affected if available
	if affected, err := result.RowsAffected(); err == nil {
		span.SetAttributes(attribute.Int64("db.rows_affected", affected))
	}

	span.SetStatus(codes.Ok, "")
	return result, nil
}

// Exec executes a query without returning rows, with tracing (no context)
func (tdb *TracedDB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return tdb.ExecContext(context.Background(), query, args...)
}

// PrepareContext creates a prepared statement with tracing
func (tdb *TracedDB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	ctx, span := tdb.tracer.Start(ctx, "sql.prepare")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", tdb.dbSystem),
		attribute.String("db.statement", query),
	)

	stmt, err := tdb.db.PrepareContext(ctx, query)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return stmt, nil
}

// Prepare creates a prepared statement with tracing (no context)
func (tdb *TracedDB) Prepare(query string) (*sql.Stmt, error) {
	return tdb.PrepareContext(context.Background(), query)
}

// BeginTx starts a transaction with tracing
func (tdb *TracedDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	ctx, span := tdb.tracer.Start(ctx, "sql.begin_transaction")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", tdb.dbSystem),
		attribute.String("db.operation", "BEGIN"),
	)

	tx, err := tdb.db.BeginTx(ctx, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return tx, nil
}

// Begin starts a transaction with tracing (no context)
func (tdb *TracedDB) Begin() (*sql.Tx, error) {
	return tdb.BeginTx(context.Background(), nil)
}

// PingContext verifies connection with tracing
func (tdb *TracedDB) PingContext(ctx context.Context) error {
	ctx, span := tdb.tracer.Start(ctx, "sql.ping")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", tdb.dbSystem),
		attribute.String("db.operation", "PING"),
	)

	err := tdb.db.PingContext(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Ping verifies connection with tracing (no context)
func (tdb *TracedDB) Ping() error {
	return tdb.PingContext(context.Background())
}

// Close closes the database connection
func (tdb *TracedDB) Close() error {
	return tdb.db.Close()
}

// DB returns the underlying sql.DB
func (tdb *TracedDB) DB() *sql.DB {
	return tdb.db
}

// SetMaxOpenConns sets the maximum number of open connections
func (tdb *TracedDB) SetMaxOpenConns(n int) {
	tdb.db.SetMaxOpenConns(n)
}

// SetMaxIdleConns sets the maximum number of idle connections
func (tdb *TracedDB) SetMaxIdleConns(n int) {
	tdb.db.SetMaxIdleConns(n)
}

// Stats returns database statistics
func (tdb *TracedDB) Stats() sql.DBStats {
	return tdb.db.Stats()
}

// Driver returns the database driver
func (tdb *TracedDB) Driver() driver.Driver {
	return tdb.db.Driver()
}

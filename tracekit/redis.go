package tracekit

import (
	"context"
	"net"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// WrapRedis adds OpenTelemetry instrumentation to a Redis client using hooks
func (s *SDK) WrapRedis(client *redis.Client) error {
	// Add before and after hooks for tracing
	client.AddHook(&redisHook{
		tracer: s.tracer,
	})
	return nil
}

// WrapRedisCluster adds OpenTelemetry instrumentation to a Redis cluster client
func (s *SDK) WrapRedisCluster(client *redis.ClusterClient) error {
	client.AddHook(&redisHook{
		tracer: s.tracer,
	})
	return nil
}

// redisHook implements redis.Hook interface for OpenTelemetry tracing
type redisHook struct {
	tracer trace.Tracer
}

func (h *redisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (h *redisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, span := h.tracer.Start(ctx, "redis."+cmd.Name())
		defer span.End()

		span.SetAttributes(
			attribute.String("db.system", "redis"),
			attribute.String("db.operation", cmd.Name()),
		)

		err := next(ctx, cmd)
		// redis.Nil is not an error - it just means "key not found" or "no data"
		if err != nil && err != redis.Nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return err
	}
}

func (h *redisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, span := h.tracer.Start(ctx, "redis.pipeline")
		defer span.End()

		span.SetAttributes(
			attribute.String("db.system", "redis"),
			attribute.Int("db.redis.pipeline_length", len(cmds)),
		)

		err := next(ctx, cmds)
		// redis.Nil is not an error - it just means "key not found" or "no data"
		if err != nil && err != redis.Nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return err
	}
}

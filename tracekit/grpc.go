package tracekit

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// GRPCServerInterceptors returns gRPC server interceptors with OpenTelemetry
func (s *SDK) GRPCServerInterceptors() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler(
			otelgrpc.WithTracerProvider(s.tracerProvider),
		)),
	}
}

// GRPCClientInterceptors returns gRPC client interceptors with OpenTelemetry
func (s *SDK) GRPCClientInterceptors() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(
			otelgrpc.WithTracerProvider(s.tracerProvider),
		)),
	}
}

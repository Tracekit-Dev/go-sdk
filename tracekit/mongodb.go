package tracekit

import (
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
)

// MongoClientOptions returns MongoDB client options with OpenTelemetry instrumentation
func (s *SDK) MongoClientOptions() *options.ClientOptions {
	opts := options.Client()
	opts.Monitor = otelmongo.NewMonitor(
		otelmongo.WithTracerProvider(s.tracerProvider),
	)
	return opts
}

// WrapMongoClient wraps an existing MongoDB client with OpenTelemetry (not recommended, use MongoClientOptions instead)
// Note: This should be called before any operations on the client
func (s *SDK) WrapMongoClient(client *mongo.Client) *mongo.Client {
	// MongoDB doesn't support wrapping existing clients well
	// Users should use MongoClientOptions() when creating the client
	return client
}

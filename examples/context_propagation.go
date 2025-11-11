package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Tracekit-Dev/go-sdk/tracekit"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Example demonstrating automatic context propagation and span linking

type Order struct {
	ID        uint
	UserID    string
	Amount    float64
	Status    string
	CreatedAt time.Time
}

func main() {
	// Initialize TraceKit SDK
	sdk, err := tracekit.NewSDK(&tracekit.Config{
		APIKey:      os.Getenv("TRACEKIT_API_KEY"),
		ServiceName: "order-service",
		Environment: "development",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sdk.Shutdown(context.Background())

	// Setup database
	db, _ := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	sdk.TraceGormDB(db)
	db.AutoMigrate(&Order{})

	// Setup Redis
	redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	sdk.WrapRedis(redisClient)

	// Setup HTTP client for calling other services
	httpClient := sdk.HTTPClient(&http.Client{Timeout: 10 * time.Second})

	// Setup Gin with tracing middleware
	r := gin.Default()
	r.Use(sdk.GinMiddleware())

	// Example 1: Simple request with database query
	// Shows: HTTP span → Database span (parent-child)
	r.GET("/orders/:id", func(c *gin.Context) {
		// Context from HTTP request contains the HTTP span
		ctx := c.Request.Context()

		// This database query becomes a CHILD span of the HTTP span
		var order Order
		if err := db.WithContext(ctx).First(&order, c.Param("id")).Error; err != nil {
			c.JSON(404, gin.H{"error": "Order not found"})
			return
		}

		c.JSON(200, order)
	})

	// Example 2: Request with multiple operations
	// Shows: HTTP span → Custom span → DB + Redis spans
	r.POST("/orders", func(c *gin.Context) {
		ctx := c.Request.Context()

		// Create a custom span for order processing
		ctx, span := sdk.StartSpan(ctx, "processOrder")
		defer span.End()

		// Add business context
		sdk.AddBusinessAttributes(span, map[string]interface{}{
			"user.id": "user123",
			"amount":  99.99,
		})

		// Check cache (becomes child of "processOrder" span)
		cached, err := redisClient.Get(ctx, "order:last").Result()
		if err == nil {
			sdk.AddAttribute(span, "cache", "hit")
			sdk.AddEvent(span, "cache.found",
				attribute.String("value", cached))
		} else {
			sdk.AddAttribute(span, "cache", "miss")
		}

		// Create order in database (becomes child of "processOrder" span)
		order := Order{
			UserID: "user123",
			Amount: 99.99,
			Status: "pending",
		}

		if err := db.WithContext(ctx).Create(&order).Error; err != nil {
			// Record error with details
			sdk.RecordError(span, err)
			sdk.AddAttribute(span, "error.type", "database_error")
			c.JSON(500, gin.H{"error": "Failed to create order"})
			return
		}

		sdk.AddEvent(span, "order.created")

		// Cache the order
		redisClient.Set(ctx, fmt.Sprintf("order:%d", order.ID), order.ID, time.Hour)

		sdk.SetSuccess(span)
		c.JSON(201, order)
	})

	// Example 3: Distributed tracing across services
	// Shows: HTTP span → Custom span → HTTP client span → External service
	r.POST("/orders/:id/process", func(c *gin.Context) {
		ctx := c.Request.Context()

		ctx, span := sdk.StartSpan(ctx, "processOrderWithPayment")
		defer span.End()

		orderID := c.Param("id")
		sdk.AddAttribute(span, "order.id", orderID)

		// Get order from database (child span)
		var order Order
		if err := db.WithContext(ctx).First(&order, orderID).Error; err != nil {
			sdk.RecordError(span, err)
			c.JSON(404, gin.H{"error": "Order not found"})
			return
		}

		sdk.AddEvent(span, "order.loaded")

		// Call payment service (trace context automatically propagated!)
		// The payment service will see this request as part of the same trace
		paymentReq, _ := http.NewRequestWithContext(ctx, "POST",
			"http://payment-service/charge",
			nil)

		resp, err := httpClient.Do(paymentReq)
		if err != nil {
			sdk.RecordError(span, err)
			sdk.AddAttribute(span, "payment.status", "failed")
			sdk.AddEvent(span, "payment.failed",
				attribute.String("error", err.Error()))
			c.JSON(500, gin.H{"error": "Payment failed"})
			return
		}
		defer resp.Body.Close()

		sdk.AddIntAttribute(span, "payment.status_code", int64(resp.StatusCode))
		sdk.AddEvent(span, "payment.completed")

		// Update order status (child span)
		db.WithContext(ctx).Model(&order).Update("status", "completed")

		sdk.AddEvent(span, "order.updated")
		sdk.SetSuccess(span)

		c.JSON(200, gin.H{
			"order":   order,
			"message": "Order processed successfully",
		})
	})

	// Example 4: Error handling with detailed context
	r.POST("/orders/:id/refund", func(c *gin.Context) {
		ctx := c.Request.Context()

		ctx, span := sdk.StartSpan(ctx, "refundOrder")
		defer span.End()

		orderID := c.Param("id")
		sdk.AddAttribute(span, "order.id", orderID)

		// Load order
		var order Order
		if err := db.WithContext(ctx).First(&order, orderID).Error; err != nil {
			sdk.RecordError(span, err)
			sdk.AddAttribute(span, "error.type", "order_not_found")
			c.JSON(404, gin.H{"error": "Order not found"})
			return
		}

		// Business logic validation
		if order.Status != "completed" {
			sdk.AddAttribute(span, "refund.rejected", "invalid_status")
			sdk.AddEvent(span, "refund.rejected",
				attribute.String("current_status", order.Status))
			sdk.SetError(span, "Cannot refund order with status: "+order.Status)
			c.JSON(400, gin.H{"error": "Can only refund completed orders"})
			return
		}

		// Call payment service for refund
		refundReq, _ := http.NewRequestWithContext(ctx, "POST",
			fmt.Sprintf("http://payment-service/refund/%d", order.ID),
			nil)

		resp, err := httpClient.Do(refundReq)
		if err != nil {
			sdk.RecordError(span, err)
			sdk.AddAttribute(span, "refund.error", "payment_service_error")
			sdk.AddIntAttribute(span, "retry.count", 1)
			c.JSON(500, gin.H{"error": "Refund processing failed"})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			sdk.AddIntAttribute(span, "refund.status_code", int64(resp.StatusCode))
			sdk.SetError(span, fmt.Sprintf("Refund failed with status: %d", resp.StatusCode))
			c.JSON(500, gin.H{"error": "Refund was rejected"})
			return
		}

		// Update order status
		db.WithContext(ctx).Model(&order).Update("status", "refunded")

		sdk.AddEvent(span, "refund.completed")
		sdk.AddFloatAttribute(span, "refund.amount", order.Amount)
		sdk.SetSuccess(span)

		c.JSON(200, gin.H{
			"order":   order,
			"message": "Refund processed successfully",
		})
	})

	log.Println("✅ Order service running on :8080")
	log.Println("📊 All requests automatically traced with context propagation!")
	r.Run(":8080")
}

/*
WHAT YOU'LL SEE IN TRACEKIT:

Example 1 - Simple GET:
HTTP GET /orders/1 (50ms)
  └─ gorm.Query SELECT * FROM orders WHERE id = 1 (10ms)

Example 2 - POST with custom span:
HTTP POST /orders (120ms)
  └─ processOrder (100ms)
      ├─ redis.GET order:last (2ms)
      ├─ gorm.Create INSERT INTO orders (30ms)
      └─ redis.SET order:123 (2ms)

Example 3 - Distributed trace:
HTTP POST /orders/1/process (250ms)
  └─ processOrderWithPayment (230ms)
      ├─ gorm.Query SELECT * FROM orders (10ms)
      ├─ HTTP POST http://payment-service/charge (150ms)
      │   └─ [Payment Service spans would appear here as children]
      └─ gorm.Update UPDATE orders (20ms)

Example 4 - Error handling:
HTTP POST /orders/1/refund (200ms) ❌
  └─ refundOrder (180ms) ❌
      ├─ gorm.Query SELECT * FROM orders (10ms)
      └─ HTTP POST http://payment-service/refund/1 (150ms) ❌
          [Error details: payment_service_error, status: 500]

All spans include:
  - Parent-child relationships (automatic)
  - Trace ID (same across all services)
  - Span ID (unique per span)
  - Attributes (order.id, user.id, amounts, etc.)
  - Events (order.created, payment.completed, etc.)
  - Errors (full error messages and stack traces)
  - Timestamps (start time, end time, duration)
*/

package tracekit

import (
	"sync"
	"time"
)

// metricDataPoint represents a single metric observation
type metricDataPoint struct {
	name      string
	tags      map[string]string
	value     float64
	timestamp time.Time
	typ       string // "counter", "gauge", "histogram"
}

// metricsBuffer collects metrics and flushes them periodically
type metricsBuffer struct {
	data     []metricDataPoint
	mu       sync.Mutex
	exporter *metricsExporter
	stop     chan struct{}

	maxSize      int
	flushInterval time.Duration
}

func newMetricsBuffer(endpoint, apiKey, serviceName string) *metricsBuffer {
	return &metricsBuffer{
		data:          make([]metricDataPoint, 0, 100),
		exporter:      newMetricsExporter(endpoint, apiKey, serviceName),
		stop:          make(chan struct{}),
		maxSize:       100,
		flushInterval: 10 * time.Second,
	}
}

func (b *metricsBuffer) add(dp metricDataPoint) {
	b.mu.Lock()
	b.data = append(b.data, dp)
	shouldFlush := len(b.data) >= b.maxSize
	b.mu.Unlock()

	if shouldFlush {
		go b.flush()
	}
}

func (b *metricsBuffer) start() {
	go b.flushLoop()
}

func (b *metricsBuffer) flushLoop() {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stop:
			b.flush() // Final flush
			return
		case <-ticker.C:
			b.flush()
		}
	}
}

func (b *metricsBuffer) flush() {
	b.mu.Lock()
	if len(b.data) == 0 {
		b.mu.Unlock()
		return
	}

	// Swap buffer
	dataPoints := b.data
	b.data = make([]metricDataPoint, 0, b.maxSize)
	b.mu.Unlock()

	// Export in background
	if err := b.exporter.export(dataPoints); err != nil {
		// Silent fail - metrics are best-effort
		// TODO: Add optional logging
	}
}

func (b *metricsBuffer) shutdown() {
	close(b.stop)
	// Give it a moment to finish the final flush
	time.Sleep(100 * time.Millisecond)
}

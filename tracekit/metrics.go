package tracekit

import (
	"sync"
	"time"
)

// Counter tracks monotonically increasing values
type Counter interface {
	Inc()
	Add(value float64)
}

// Gauge tracks point-in-time values
type Gauge interface {
	Set(value float64)
	Inc()
	Dec()
}

// Histogram tracks value distributions
type Histogram interface {
	Record(value float64)
}

// counter implementation
type counter struct {
	name   string
	tags   map[string]string
	buffer *metricsBuffer
}

func (c *counter) Inc() {
	c.Add(1)
}

func (c *counter) Add(value float64) {
	if value < 0 {
		return // Counters must be monotonic
	}

	c.buffer.add(metricDataPoint{
		name:      c.name,
		tags:      c.tags,
		value:     value,
		timestamp: time.Now(),
		typ:       "counter",
	})
}

// gauge implementation
type gauge struct {
	name   string
	tags   map[string]string
	value  float64
	mu     sync.Mutex
	buffer *metricsBuffer
}

func (g *gauge) Set(value float64) {
	g.mu.Lock()
	g.value = value
	g.mu.Unlock()

	g.buffer.add(metricDataPoint{
		name:      g.name,
		tags:      g.tags,
		value:     value,
		timestamp: time.Now(),
		typ:       "gauge",
	})
}

func (g *gauge) Inc() {
	g.mu.Lock()
	g.value++
	val := g.value
	g.mu.Unlock()

	g.buffer.add(metricDataPoint{
		name:      g.name,
		tags:      g.tags,
		value:     val,
		timestamp: time.Now(),
		typ:       "gauge",
	})
}

func (g *gauge) Dec() {
	g.mu.Lock()
	g.value--
	val := g.value
	g.mu.Unlock()

	g.buffer.add(metricDataPoint{
		name:      g.name,
		tags:      g.tags,
		value:     val,
		timestamp: time.Now(),
		typ:       "gauge",
	})
}

// histogram implementation
type histogram struct {
	name   string
	tags   map[string]string
	buffer *metricsBuffer
}

func (h *histogram) Record(value float64) {
	h.buffer.add(metricDataPoint{
		name:      h.name,
		tags:      h.tags,
		value:     value,
		timestamp: time.Now(),
		typ:       "histogram",
	})
}

// metricsRegistry manages all metrics
type metricsRegistry struct {
	counters   map[string]*counter
	gauges     map[string]*gauge
	histograms map[string]*histogram
	mu         sync.RWMutex
	buffer     *metricsBuffer
}

func newMetricsRegistry(endpoint, apiKey, serviceName string) *metricsRegistry {
	mr := &metricsRegistry{
		counters:   make(map[string]*counter),
		gauges:     make(map[string]*gauge),
		histograms: make(map[string]*histogram),
	}

	mr.buffer = newMetricsBuffer(endpoint, apiKey, serviceName)
	mr.buffer.start()

	return mr
}

func (mr *metricsRegistry) counter(name string, tags map[string]string) Counter {
	key := metricKey(name, tags)

	mr.mu.RLock()
	if c, exists := mr.counters[key]; exists {
		mr.mu.RUnlock()
		return c
	}
	mr.mu.RUnlock()

	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Double-check after lock
	if c, exists := mr.counters[key]; exists {
		return c
	}

	c := &counter{
		name:   name,
		tags:   copyTags(tags),
		buffer: mr.buffer,
	}
	mr.counters[key] = c
	return c
}

func (mr *metricsRegistry) gauge(name string, tags map[string]string) Gauge {
	key := metricKey(name, tags)

	mr.mu.RLock()
	if g, exists := mr.gauges[key]; exists {
		mr.mu.RUnlock()
		return g
	}
	mr.mu.RUnlock()

	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Double-check after lock
	if g, exists := mr.gauges[key]; exists {
		return g
	}

	g := &gauge{
		name:   name,
		tags:   copyTags(tags),
		buffer: mr.buffer,
	}
	mr.gauges[key] = g
	return g
}

func (mr *metricsRegistry) histogram(name string, tags map[string]string) Histogram {
	key := metricKey(name, tags)

	mr.mu.RLock()
	if h, exists := mr.histograms[key]; exists {
		mr.mu.RUnlock()
		return h
	}
	mr.mu.RUnlock()

	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Double-check after lock
	if h, exists := mr.histograms[key]; exists {
		return h
	}

	h := &histogram{
		name:   name,
		tags:   copyTags(tags),
		buffer: mr.buffer,
	}
	mr.histograms[key] = h
	return h
}

func (mr *metricsRegistry) shutdown() {
	mr.buffer.shutdown()
}

// Helper: create unique key for metric
func metricKey(name string, tags map[string]string) string {
	if len(tags) == 0 {
		return name
	}

	// Simple key format: name{k1=v1,k2=v2}
	key := name + "{"
	first := true
	for k, v := range tags {
		if !first {
			key += ","
		}
		key += k + "=" + v
		first = false
	}
	key += "}"
	return key
}

// Helper: copy tags map
func copyTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}
	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}
	return copied
}

// SDK methods for metrics
func (s *SDK) Counter(name string, tags map[string]string) Counter {
	if s.metricsRegistry == nil {
		return &noopCounter{}
	}
	return s.metricsRegistry.counter(name, tags)
}

func (s *SDK) Gauge(name string, tags map[string]string) Gauge {
	if s.metricsRegistry == nil {
		return &noopGauge{}
	}
	return s.metricsRegistry.gauge(name, tags)
}

func (s *SDK) Histogram(name string, tags map[string]string) Histogram {
	if s.metricsRegistry == nil {
		return &noopHistogram{}
	}
	return s.metricsRegistry.histogram(name, tags)
}

// No-op implementations for when metrics are disabled
type noopCounter struct{}

func (n *noopCounter) Inc()             {}
func (n *noopCounter) Add(value float64) {}

type noopGauge struct{}

func (n *noopGauge) Set(value float64) {}
func (n *noopGauge) Inc()              {}
func (n *noopGauge) Dec()              {}

type noopHistogram struct{}

func (n *noopHistogram) Record(value float64) {}

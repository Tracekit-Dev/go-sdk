package tracekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// SnapshotClient handles code monitoring snapshots
type SnapshotClient struct {
	apiKey      string
	baseURL     string
	serviceName string
	client      *http.Client
	stopChan    chan struct{}

	// Cache of active breakpoints
	breakpointsCache  map[string]*BreakpointConfig
	lastFetch         time.Time
	registrationCache map[string]bool // Track registered locations
	mu                sync.RWMutex    // Protects caches
}

// BreakpointConfig represents a breakpoint configuration
type BreakpointConfig struct {
	ID           string                 `json:"id"`
	ServiceName  string                 `json:"service_name"`
	FilePath     string                 `json:"file_path"`
	LineNumber   int                    `json:"line_number"`
	Condition    string                 `json:"condition,omitempty"`
	MaxCaptures  int                    `json:"max_captures"`
	CaptureCount int                    `json:"capture_count"`
	ExpireAt     *time.Time             `json:"expire_at,omitempty"`
	Enabled      bool                   `json:"enabled"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// Snapshot represents a captured code state
type Snapshot struct {
	BreakpointID   string                 `json:"breakpoint_id"`
	ServiceName    string                 `json:"service_name"`
	FilePath       string                 `json:"file_path"`
	LineNumber     int                    `json:"line_number"`
	Variables      map[string]interface{} `json:"variables"`
	StackTrace     string                 `json:"stack_trace"`
	TraceID        string                 `json:"trace_id,omitempty"`
	SpanID         string                 `json:"span_id,omitempty"`
	RequestContext map[string]interface{} `json:"request_context,omitempty"`
	CapturedAt     time.Time              `json:"captured_at"`
}

// NewSnapshotClient creates a new snapshot client
func NewSnapshotClient(apiKey, baseURL, serviceName string) *SnapshotClient {
	return &SnapshotClient{
		apiKey:           apiKey,
		baseURL:          baseURL,
		serviceName:      serviceName,
		client:           &http.Client{Timeout: 10 * time.Second},
		stopChan:         make(chan struct{}),
		breakpointsCache: make(map[string]*BreakpointConfig),
	}
}

// Start begins polling for active breakpoints
func (c *SnapshotClient) Start() {
	go c.pollBreakpoints()
	log.Printf("📸 TraceKit Snapshot Client started for service: %s", c.serviceName)
}

// Stop stops the snapshot client
func (c *SnapshotClient) Stop() {
	close(c.stopChan)
	log.Println("📸 TraceKit Snapshot Client stopped")
}

// pollBreakpoints periodically fetches active breakpoints from the backend
func (c *SnapshotClient) pollBreakpoints() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Fetch immediately on startup
	if err := c.fetchActiveBreakpoints(); err != nil {
		log.Printf("⚠️  Failed to fetch initial breakpoints: %v", err)
	}

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			if err := c.fetchActiveBreakpoints(); err != nil {
				log.Printf("⚠️  Failed to fetch breakpoints: %v", err)
			}
		}
	}
}

// fetchActiveBreakpoints retrieves active breakpoints from the backend
func (c *SnapshotClient) fetchActiveBreakpoints() error {
	url := fmt.Sprintf("%s/sdk/snapshots/active/%s", c.baseURL, c.serviceName)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Breakpoints []BreakpointConfig `json:"breakpoints"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	// Update cache
	c.updateBreakpointCache(result.Breakpoints)
	c.lastFetch = time.Now()

	return nil
}

// updateBreakpointCache updates the in-memory cache of breakpoints
func (c *SnapshotClient) updateBreakpointCache(breakpoints []BreakpointConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	newCache := make(map[string]*BreakpointConfig)

	for i := range breakpoints {
		bp := &breakpoints[i]
		key := fmt.Sprintf("%s:%d", bp.FilePath, bp.LineNumber)
		newCache[key] = bp
	}

	c.breakpointsCache = newCache

	if len(breakpoints) > 0 {
		log.Printf("📸 Updated breakpoint cache: %d active breakpoints", len(breakpoints))
	}
}

// CheckAndCapture checks if there's an active breakpoint at this location and captures a snapshot
func (c *SnapshotClient) CheckAndCapture(filePath string, lineNumber int, variables map[string]interface{}) {
	// Check cache for matching breakpoint
	key := fmt.Sprintf("%s:%d", filePath, lineNumber)
	bp, exists := c.breakpointsCache[key]

	if !exists {
		return // No active breakpoint at this location
	}

	// Check if breakpoint has expired
	if bp.ExpireAt != nil && time.Now().After(*bp.ExpireAt) {
		return
	}

	// Check if max captures reached
	if bp.MaxCaptures > 0 && bp.CaptureCount >= bp.MaxCaptures {
		return
	}

	// Capture stack trace
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	stackTrace := string(buf[:n])

	// Create snapshot
	snapshot := Snapshot{
		BreakpointID: bp.ID,
		ServiceName:  c.serviceName,
		FilePath:     filePath,
		LineNumber:   lineNumber,
		Variables:    variables,
		StackTrace:   stackTrace,
		CapturedAt:   time.Now(),
	}

	// TODO: Extract trace/span ID from context if available

	// Send snapshot to backend (non-blocking)
	go c.captureSnapshot(snapshot)
}

// CheckAndCaptureWithContext checks and captures with trace context
// It automatically registers the breakpoint location on first call
func (c *SnapshotClient) CheckAndCaptureWithContext(ctx context.Context, variables map[string]interface{}) {
	// Get caller information automatically
	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		return
	}

	// Get function name
	funcName := runtime.FuncForPC(pc).Name()

	// Auto-register or update breakpoint
	key := fmt.Sprintf("%s:%d", file, line)
	c.autoRegisterBreakpoint(file, line, funcName)
	
	// Check cache for matching breakpoint
	c.mu.RLock()
	bp, exists := c.breakpointsCache[key]
	c.mu.RUnlock()

	if !exists || !bp.Enabled {
		return // No active breakpoint at this location
	}

	// Check if breakpoint has expired
	if bp.ExpireAt != nil && time.Now().After(*bp.ExpireAt) {
		return
	}

	// Check if max captures reached
	if bp.MaxCaptures > 0 && bp.CaptureCount >= bp.MaxCaptures {
		return
	}

	// Capture stack trace
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	stackTrace := string(buf[:n])

	// Extract trace/span IDs from OpenTelemetry context
	traceID := ""
	spanID := ""
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		traceID = span.SpanContext().TraceID().String()
		spanID = span.SpanContext().SpanID().String()
	}

	// Extract HTTP request context if available
	requestContext := c.extractRequestContext(ctx)

	// Create snapshot
	snapshot := Snapshot{
		BreakpointID:   bp.ID,
		ServiceName:    c.serviceName,
		FilePath:       file,
		LineNumber:     line,
		Variables:      variables,
		StackTrace:     stackTrace,
		TraceID:        traceID,
		SpanID:         spanID,
		RequestContext: requestContext,
		CapturedAt:     time.Now(),
	}

	// Send snapshot to backend (non-blocking)
	go c.captureSnapshot(snapshot)
}

// autoRegisterBreakpoint automatically creates or updates a breakpoint
func (c *SnapshotClient) autoRegisterBreakpoint(file string, line int, funcName string) {
	key := fmt.Sprintf("%s:%d", file, line)

	// Check if we've already registered this location
	c.mu.Lock()
	if _, exists := c.breakpointsCache[key]; exists {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Auto-register with backend (non-blocking)
	go func() {
		url := fmt.Sprintf("%s/sdk/snapshots/auto-register", c.baseURL)

		payload := map[string]interface{}{
			"service_name":  c.serviceName,
			"file_path":     file,
			"line_number":   line,
			"function_name": funcName,
		}

		body, _ := json.Marshal(payload)
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
		if err != nil {
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", c.apiKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		// Refresh breakpoints cache after registration
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			time.Sleep(500 * time.Millisecond) // Small delay for backend processing
			c.fetchActiveBreakpoints()
		}
	}()
}

// captureSnapshot sends the snapshot to the backend
func (c *SnapshotClient) captureSnapshot(snapshot Snapshot) {
	url := fmt.Sprintf("%s/sdk/snapshots/capture", c.baseURL)

	body, err := json.Marshal(snapshot)
	if err != nil {
		log.Printf("⚠️  Failed to marshal snapshot: %v", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("⚠️  Failed to create snapshot request: %v", err)
		return
	}

	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("⚠️  Failed to send snapshot: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("⚠️  Failed to capture snapshot: status %d", resp.StatusCode)
		return
	}

	log.Printf("📸 Snapshot captured: %s:%d", snapshot.FilePath, snapshot.LineNumber)
}

// extractRequestContext extracts HTTP request details from context
func (c *SnapshotClient) extractRequestContext(ctx context.Context) map[string]interface{} {
	// Try to extract request context stored by middleware
	if reqCtx := ctx.Value(contextKey("tracekit.request_context")); reqCtx != nil {
		if rc, ok := reqCtx.(map[string]interface{}); ok {
			return rc
		}
	}
	return nil
}

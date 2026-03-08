package tracekit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// PIIPattern represents a pattern for detecting and redacting sensitive data
type PIIPattern struct {
	Pattern *regexp.Regexp
	Marker  string // e.g., "[REDACTED:email]"
}

// CaptureConfig holds opt-in capture limit settings.
// All limits are disabled by default (zero values = unlimited).
type CaptureConfig struct {
	CaptureDepth   int           // 0 = unlimited depth (default)
	MaxPayload     int           // 0 = unlimited payload bytes (default)
	CaptureTimeout time.Duration // 0 = no timeout (default)
	Debug          bool          // Enable debug logging
	PIIScrubbing   *bool         // nil or true = enabled (default), false = disabled
	PIIPatterns    []PIIPattern  // Additional custom PII patterns appended to built-in set

	// Circuit breaker config -- nil means use defaults (3 failures in 60s, 5min cooldown)
	CircuitBreaker *CircuitBreakerConfig
}

// CircuitBreakerConfig allows users to override circuit breaker thresholds.
// Zero values mean "use defaults".
type CircuitBreakerConfig struct {
	MaxFailures int   // default 3: failures before tripping
	WindowMs    int64 // default 60000: failure counting window (ms)
	CooldownMs  int64 // default 300000: time circuit stays open (ms)
}

// circuitBreaker tracks HTTP failure state for self-healing behavior.
// When the backend is unreachable, it stops sending snapshots after repeated
// failures and automatically recovers after a cooldown period.
type circuitBreaker struct {
	mu                sync.Mutex
	failureTimestamps []time.Time
	state             string // "closed" or "open"
	openedAt          time.Time
	config            CircuitBreakerConfig
}

// newCircuitBreaker creates a circuit breaker with the given config (nil = defaults)
func newCircuitBreaker(cfg *CircuitBreakerConfig) *circuitBreaker {
	defaults := CircuitBreakerConfig{
		MaxFailures: 3,
		WindowMs:    60000,
		CooldownMs:  300000,
	}
	if cfg != nil {
		if cfg.MaxFailures > 0 {
			defaults.MaxFailures = cfg.MaxFailures
		}
		if cfg.WindowMs > 0 {
			defaults.WindowMs = cfg.WindowMs
		}
		if cfg.CooldownMs > 0 {
			defaults.CooldownMs = cfg.CooldownMs
		}
	}
	return &circuitBreaker{
		state:  "closed",
		config: defaults,
	}
}

// ShouldAllow returns true if the circuit is closed (requests allowed).
// If open, checks cooldown -- if elapsed, resets to closed and allows.
func (cb *circuitBreaker) ShouldAllow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "closed" {
		return true
	}

	// State is "open" -- check if cooldown has elapsed
	cooldown := time.Duration(cb.config.CooldownMs) * time.Millisecond
	if time.Since(cb.openedAt) >= cooldown {
		cb.state = "closed"
		cb.failureTimestamps = nil
		log.Println("TraceKit: Code monitoring resumed")
		return true
	}

	return false
}

// RecordFailure records an HTTP failure. If threshold exceeded, trips the circuit.
// Returns true if the circuit just tripped (caller should queue telemetry event).
func (cb *circuitBreaker) RecordFailure() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.failureTimestamps = append(cb.failureTimestamps, now)

	// Prune timestamps older than the window
	window := time.Duration(cb.config.WindowMs) * time.Millisecond
	cutoff := now.Add(-window)
	pruned := cb.failureTimestamps[:0]
	for _, ts := range cb.failureTimestamps {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	cb.failureTimestamps = pruned

	// Check if threshold exceeded
	if len(cb.failureTimestamps) >= cb.config.MaxFailures && cb.state == "closed" {
		cb.state = "open"
		cb.openedAt = now
		log.Printf("TraceKit: Code monitoring paused (%d capture failures in %ds). Auto-resumes in %d min.",
			cb.config.MaxFailures,
			cb.config.WindowMs/1000,
			cb.config.CooldownMs/60000)
		return true
	}

	return false
}

// SnapshotClient handles code monitoring snapshots
type SnapshotClient struct {
	apiKey      string
	baseURL     string
	serviceName string
	client      *http.Client
	stopChan    chan struct{}
	config      CaptureConfig

	// Pre-compiled PII patterns (built-in + custom), initialized once
	piiPatterns       []PIIPattern
	sensitiveNameExpr *regexp.Regexp

	// Cache of active breakpoints
	breakpointsCache  map[string]*BreakpointConfig
	lastFetch         time.Time
	registrationCache map[string]bool // Track registered locations
	mu                sync.RWMutex    // Protects caches

	// Circuit breaker for snapshot HTTP calls
	cb            *circuitBreaker
	pendingEvents []map[string]interface{}
	eventsMu      sync.Mutex

	// Kill switch: server-initiated monitoring disable
	killSwitchActive bool
	normalPollInterval time.Duration

	// SSE (Server-Sent Events) real-time updates
	sseEndpoint string
	sseActive   bool
	sseCancel   context.CancelFunc
}

// BreakpointConfig represents a breakpoint configuration
type BreakpointConfig struct {
	ID           string                 `json:"id"`
	ServiceName  string                 `json:"service_name"`
	FilePath     string                 `json:"file_path"`
	FunctionName string                 `json:"function_name"`
	Label        string                 `json:"label,omitempty"` // Stable identifier
	LineNumber   int                    `json:"line_number"`
	Condition    string                 `json:"condition,omitempty"`
	MaxCaptures  int                    `json:"max_captures"`
	CaptureCount int                    `json:"capture_count"`
	ExpireAt     *time.Time             `json:"expire_at,omitempty"`
	Enabled      bool                   `json:"enabled"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// SecurityFlag represents a security issue found in snapshot variables
type SecurityFlag struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Variable string `json:"variable,omitempty"`
}

// Snapshot represents a captured code state
type Snapshot struct {
	BreakpointID   string                 `json:"breakpoint_id"`
	ServiceName    string                 `json:"service_name"`
	FilePath       string                 `json:"file_path"`
	LineNumber     int                    `json:"line_number"`
	Variables      map[string]interface{} `json:"variables"`
	SecurityFlags  []SecurityFlag         `json:"security_flags,omitempty"`
	StackTrace     string                 `json:"stack_trace"`
	TraceID        string                 `json:"trace_id,omitempty"`
	SpanID         string                 `json:"span_id,omitempty"`
	RequestContext map[string]interface{} `json:"request_context,omitempty"`
	CapturedAt     time.Time              `json:"captured_at"`
}

// defaultPIIPatterns returns the standard 13-pattern set for PII/credential detection.
// Patterns are compiled once and reused for all scans.
func defaultPIIPatterns() []PIIPattern {
	return []PIIPattern{
		{Pattern: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), Marker: "[REDACTED:email]"},
		{Pattern: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), Marker: "[REDACTED:ssn]"},
		{Pattern: regexp.MustCompile(`\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b`), Marker: "[REDACTED:credit_card]"},
		{Pattern: regexp.MustCompile(`\b\d{3}[-.]?\d{3}[-.]?\d{4}\b`), Marker: "[REDACTED:phone]"},
		{Pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), Marker: "[REDACTED:aws_key]"},
		{Pattern: regexp.MustCompile(`(?i)aws.{0,20}secret.{0,20}[A-Za-z0-9/+=]{40}`), Marker: "[REDACTED:aws_secret]"},
		{Pattern: regexp.MustCompile(`(?i)(?:bearer\s+)[A-Za-z0-9._~+/=\-]{20,}`), Marker: "[REDACTED:oauth_token]"},
		{Pattern: regexp.MustCompile(`sk_live_[0-9a-zA-Z]{10,}`), Marker: "[REDACTED:stripe_key]"},
		{Pattern: regexp.MustCompile(`(?i)(?:password|passwd|pwd)\s*[=:]\s*["']?[^\s"']{6,}`), Marker: "[REDACTED:password]"},
		{Pattern: regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`), Marker: "[REDACTED:jwt]"},
		{Pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC )?PRIVATE KEY-----`), Marker: "[REDACTED:private_key]"},
		{Pattern: regexp.MustCompile(`(?i)(?:api[_\-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9_\-]{20,}`), Marker: "[REDACTED:api_key]"},
	}
}

// NewSnapshotClient creates a new snapshot client with PII scrubbing enabled by default
func NewSnapshotClient(apiKey, baseURL, serviceName string) *SnapshotClient {
	c := &SnapshotClient{
		apiKey:             apiKey,
		baseURL:            baseURL,
		serviceName:        serviceName,
		client:             &http.Client{Timeout: 30 * time.Second},
		stopChan:           make(chan struct{}),
		breakpointsCache:   make(map[string]*BreakpointConfig),
		registrationCache:  make(map[string]bool),
		cb:                 newCircuitBreaker(nil),
		normalPollInterval: 30 * time.Second,
	}
	c.initPIIPatterns()
	return c
}

// NewSnapshotClientWithConfig creates a new snapshot client with capture limits config
func NewSnapshotClientWithConfig(apiKey, baseURL, serviceName string, config CaptureConfig) *SnapshotClient {
	c := NewSnapshotClient(apiKey, baseURL, serviceName)
	c.config = config
	c.cb = newCircuitBreaker(config.CircuitBreaker)
	c.initPIIPatterns() // Re-init to pick up custom patterns from config
	return c
}

// initPIIPatterns compiles and caches all PII patterns (built-in + custom)
func (c *SnapshotClient) initPIIPatterns() {
	c.piiPatterns = defaultPIIPatterns()
	// Append any custom patterns from config
	if len(c.config.PIIPatterns) > 0 {
		c.piiPatterns = append(c.piiPatterns, c.config.PIIPatterns...)
	}
	// Variable name pattern: matches sensitive words separated by underscores, hyphens, or string boundaries.
	// Go's RE2 treats _ as a word char, so \b won't match api_key or user_token.
	// Use letter-based boundaries instead to catch both api_key and apiKey styles
	// while still avoiding false positives on unrelated words like "monkey" or "turkey".
	c.sensitiveNameExpr = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(password|passwd|pwd|secret|token|key|credential|api_key|apikey)(?:[^a-zA-Z]|$)`)
}

// SetCaptureConfig updates the capture limit configuration
func (c *SnapshotClient) SetCaptureConfig(config CaptureConfig) {
	c.config = config
}

// Start begins polling for active breakpoints
func (c *SnapshotClient) Start() {
	go c.pollBreakpoints()
	log.Printf("📸 TraceKit Snapshot Client started for service: %s", c.serviceName)
}

// Stop stops the snapshot client
func (c *SnapshotClient) Stop() {
	close(c.stopChan)
	if c.sseCancel != nil {
		c.sseCancel()
	}
	log.Println("📸 TraceKit Snapshot Client stopped")
}

// pollBreakpoints periodically fetches active breakpoints from the backend
func (c *SnapshotClient) pollBreakpoints() {
	ticker := time.NewTicker(c.normalPollInterval)
	defer ticker.Stop()

	// Fetch immediately on startup
	if err := c.fetchActiveBreakpoints(); err != nil {
		log.Printf("⚠️  Failed to fetch initial breakpoints: %v", err)
	}

	wasKilled := false
	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			// Skip polling when SSE is actively connected (SSE handles updates)
			if c.sseActive {
				continue
			}
			if err := c.fetchActiveBreakpoints(); err != nil {
				log.Printf("⚠️  Failed to fetch breakpoints: %v", err)
			}

			// Adjust poll interval when kill switch state changes
			if c.killSwitchActive && !wasKilled {
				ticker.Reset(60 * time.Second)
				wasKilled = true
			} else if !c.killSwitchActive && wasKilled {
				ticker.Reset(c.normalPollInterval)
				wasKilled = false
			}
		}
	}
}

// fetchActiveBreakpoints retrieves active breakpoints from the backend.
// If there are pending telemetry events, they are included in the request body.
func (c *SnapshotClient) fetchActiveBreakpoints() error {
	url := fmt.Sprintf("%s/sdk/snapshots/active/%s", c.baseURL, c.serviceName)

	// Drain any pending telemetry events to piggyback on the poll
	pendingEvents := c.drainPendingEvents()

	var req *http.Request
	var err error

	if len(pendingEvents) > 0 {
		// POST with events in body
		payload := map[string]interface{}{
			"events": pendingEvents,
		}
		body, _ := json.Marshal(payload)
		req, err = http.NewRequest("POST", url, bytes.NewBuffer(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
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
		KillSwitch  *bool              `json:"kill_switch,omitempty"`
		SSEEndpoint string             `json:"sse_endpoint,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	// Handle kill switch state (missing field = false for backward compat)
	newKillState := result.KillSwitch != nil && *result.KillSwitch
	if newKillState && !c.killSwitchActive {
		log.Println("TraceKit: Code monitoring disabled by server kill switch. Polling at reduced frequency.")
	} else if !newKillState && c.killSwitchActive {
		log.Println("TraceKit: Code monitoring re-enabled by server.")
	}
	c.killSwitchActive = newKillState

	// If kill-switched, close any active SSE connection
	if c.killSwitchActive && c.sseActive && c.sseCancel != nil {
		c.sseCancel()
		c.sseActive = false
		log.Println("TraceKit: SSE connection closed due to kill switch")
	}

	// SSE auto-discovery: if sse_endpoint present and not already connected, start SSE
	if result.SSEEndpoint != "" && !c.sseActive && !c.killSwitchActive && len(result.Breakpoints) > 0 {
		c.sseEndpoint = result.SSEEndpoint
		go c.connectSSE(result.SSEEndpoint)
	}

	// Update cache
	c.updateBreakpointCache(result.Breakpoints)
	c.lastFetch = time.Now()

	return nil
}

// connectSSE establishes a Server-Sent Events connection for real-time breakpoint updates.
// Falls back to polling if SSE connection fails or is interrupted.
func (c *SnapshotClient) connectSSE(endpoint string) {
	// Crash isolation: never let SSE bugs crash the host application
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TraceKit: recovered from panic in connectSSE: %v", r)
		}
		c.sseActive = false
	}()

	fullURL := fmt.Sprintf("%s%s", c.baseURL, endpoint)

	ctx, cancel := context.WithCancel(context.Background())
	c.sseCancel = cancel
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		log.Printf("TraceKit: SSE request creation failed: %v", err)
		return
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	// Use a separate client without the default timeout for long-lived SSE connections
	sseClient := &http.Client{}
	resp, err := sseClient.Do(req)
	if err != nil {
		log.Printf("TraceKit: SSE connection failed, falling back to polling: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("TraceKit: SSE endpoint returned %d, falling back to polling", resp.StatusCode)
		return
	}

	c.sseActive = true
	log.Println("TraceKit: SSE connection established for real-time breakpoint updates")

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var dataBuffer strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			if dataBuffer.Len() > 0 {
				dataBuffer.WriteString("\n")
			}
			dataBuffer.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if line == "" {
			// Empty line = event boundary, process the accumulated event
			if eventType != "" && dataBuffer.Len() > 0 {
				c.handleSSEEvent(eventType, dataBuffer.String())
			}
			eventType = ""
			dataBuffer.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("TraceKit: SSE connection lost: %v, falling back to polling", err)
	} else {
		log.Println("TraceKit: SSE connection closed, falling back to polling")
	}
}

// handleSSEEvent processes a single SSE event
func (c *SnapshotClient) handleSSEEvent(eventType string, data string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TraceKit: recovered from panic handling SSE event %s: %v", eventType, r)
		}
	}()

	switch eventType {
	case "init":
		var initData struct {
			Breakpoints []BreakpointConfig `json:"breakpoints"`
			KillSwitch  bool               `json:"kill_switch"`
		}
		if err := json.Unmarshal([]byte(data), &initData); err != nil {
			log.Printf("TraceKit: failed to parse SSE init event: %v", err)
			return
		}
		c.updateBreakpointCache(initData.Breakpoints)
		c.killSwitchActive = initData.KillSwitch
		if c.killSwitchActive && c.sseCancel != nil {
			c.sseCancel()
		}
		log.Printf("TraceKit: SSE init received, %d breakpoints loaded", len(initData.Breakpoints))

	case "breakpoint_created", "breakpoint_updated":
		var bp BreakpointConfig
		if err := json.Unmarshal([]byte(data), &bp); err != nil {
			log.Printf("TraceKit: failed to parse SSE %s event: %v", eventType, err)
			return
		}
		c.mu.Lock()
		// Upsert by label key and line key
		if bp.Label != "" && bp.FunctionName != "" {
			labelKey := fmt.Sprintf("%s:%s", bp.FunctionName, bp.Label)
			c.breakpointsCache[labelKey] = &bp
		}
		lineKey := fmt.Sprintf("%s:%d", bp.FilePath, bp.LineNumber)
		c.breakpointsCache[lineKey] = &bp
		c.mu.Unlock()
		log.Printf("TraceKit: SSE breakpoint %s: %s", eventType, bp.ID)

	case "breakpoint_deleted":
		var deleteData struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(data), &deleteData); err != nil {
			log.Printf("TraceKit: failed to parse SSE breakpoint_deleted event: %v", err)
			return
		}
		c.mu.Lock()
		for key, bp := range c.breakpointsCache {
			if bp.ID == deleteData.ID {
				delete(c.breakpointsCache, key)
			}
		}
		c.mu.Unlock()
		log.Printf("TraceKit: SSE breakpoint deleted: %s", deleteData.ID)

	case "kill_switch":
		var ksData struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(data), &ksData); err != nil {
			log.Printf("TraceKit: failed to parse SSE kill_switch event: %v", err)
			return
		}
		c.killSwitchActive = ksData.Enabled
		if ksData.Enabled {
			log.Println("TraceKit: Kill switch enabled via SSE, closing connection")
			if c.sseCancel != nil {
				c.sseCancel()
			}
		}

	case "heartbeat":
		// No action needed -- keeps connection alive

	default:
		log.Printf("TraceKit: unknown SSE event type: %s", eventType)
	}
}

// updateBreakpointCache updates the in-memory cache of breakpoints
func (c *SnapshotClient) updateBreakpointCache(breakpoints []BreakpointConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newCache := make(map[string]*BreakpointConfig)

	for i := range breakpoints {
		bp := &breakpoints[i]

		// Primary key: function + label (stable)
		if bp.Label != "" && bp.FunctionName != "" {
			labelKey := fmt.Sprintf("%s:%s", bp.FunctionName, bp.Label)
			newCache[labelKey] = bp
		}

		// Secondary key: file + line (for backwards compatibility)
		lineKey := fmt.Sprintf("%s:%d", bp.FilePath, bp.LineNumber)
		newCache[lineKey] = bp
	}

	c.breakpointsCache = newCache

	if len(breakpoints) > 0 {
		log.Printf("📸 Updated breakpoint cache: %d active breakpoints", len(breakpoints))
	}
}

// CheckAndCapture checks if there's an active breakpoint at this location and captures a snapshot
func (c *SnapshotClient) CheckAndCapture(filePath string, lineNumber int, variables map[string]interface{}) {
	// Crash isolation: never let a TraceKit bug crash the host application
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TraceKit: recovered from panic in CheckAndCapture: %v", r)
		}
	}()

	// Kill switch: skip all capture when server has disabled monitoring
	if c.killSwitchActive {
		return
	}

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

	// Apply opt-in capture limits
	variables = c.applyCaptureConfig(variables)

	// Capture stack trace
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	stackTrace := string(buf[:n])

	// Scan variables for security issues
	sanitizedVars, securityFlags := c.scanForSecurityIssues(variables)

	// Create snapshot
	snapshot := Snapshot{
		BreakpointID:  bp.ID,
		ServiceName:   c.serviceName,
		FilePath:      filePath,
		LineNumber:    lineNumber,
		Variables:     sanitizedVars,
		SecurityFlags: securityFlags,
		StackTrace:    stackTrace,
		CapturedAt:    time.Now(),
	}

	// TODO: Extract trace/span ID from context if available

	// Send snapshot to backend (non-blocking)
	go c.captureSnapshot(snapshot)
}

// CheckAndCaptureWithContext checks and captures with trace context
// It automatically registers the breakpoint location on first call
// label: optional stable identifier for the checkpoint
func (c *SnapshotClient) CheckAndCaptureWithContext(ctx context.Context, label string, variables map[string]interface{}) {
	// Crash isolation: never let a TraceKit bug crash the host application
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TraceKit: recovered from panic in CheckAndCaptureWithContext: %v", r)
		}
	}()

	// Kill switch: skip all capture when server has disabled monitoring
	if c.killSwitchActive {
		return
	}

	// Apply capture timeout if configured
	if c.config.CaptureTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.CaptureTimeout)
		defer cancel()
	}

	// Get caller information automatically
	// Skip 2 frames: this function + SDK wrapper (config.go)
	pc, file, line, ok := runtime.Caller(2)
	if !ok {
		return
	}

	// Get function name
	funcName := runtime.FuncForPC(pc).Name()

	// Auto-generate label if not provided
	if label == "" {
		// Extract last part of function name (e.g., "main.processPayment" -> "processPayment")
		parts := strings.Split(funcName, ".")
		label = parts[len(parts)-1]
	}

	// Auto-register or update breakpoint
	c.autoRegisterBreakpoint(file, line, funcName, label)

	// Check cache for matching breakpoint (try label first, then line)
	c.mu.RLock()
	var bp *BreakpointConfig
	var exists bool

	// Primary: Match by function + label
	labelKey := fmt.Sprintf("%s:%s", funcName, label)
	bp, exists = c.breakpointsCache[labelKey]

	// Fallback: Match by file + line
	if !exists {
		lineKey := fmt.Sprintf("%s:%d", file, line)
		bp, exists = c.breakpointsCache[lineKey]
	}
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
	if span.SpanContext().IsValid() && span.SpanContext().IsSampled() {
		traceID = span.SpanContext().TraceID().String()
		spanID = span.SpanContext().SpanID().String()
	}

	// Check if capture timeout exceeded
	if c.config.CaptureTimeout > 0 {
		select {
		case <-ctx.Done():
			log.Printf("TraceKit: capture timeout exceeded, skipping snapshot")
			return
		default:
		}
	}

	// Extract HTTP request context if available
	requestContext := c.extractRequestContext(ctx)

	// Apply opt-in capture limits
	variables = c.applyCaptureConfig(variables)

	// Scan variables for security issues
	sanitizedVars, securityFlags := c.scanForSecurityIssues(variables)

	// Create snapshot
	snapshot := Snapshot{
		BreakpointID:   bp.ID,
		ServiceName:    c.serviceName,
		FilePath:       file,
		LineNumber:     line,
		Variables:      sanitizedVars,
		SecurityFlags:  securityFlags,
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
func (c *SnapshotClient) autoRegisterBreakpoint(file string, line int, funcName string, label string) {
	// Use label as primary key for registration tracking
	regKey := fmt.Sprintf("%s:%s", funcName, label)

	// Check if we've already registered this location
	c.mu.Lock()
	if c.registrationCache[regKey] {
		c.mu.Unlock()
		return
	}
	c.registrationCache[regKey] = true
	c.mu.Unlock()

	// Auto-register with backend (non-blocking)
	go func() {
		url := fmt.Sprintf("%s/sdk/snapshots/auto-register", c.baseURL)

		payload := map[string]interface{}{
			"service_name":  c.serviceName,
			"file_path":     file,
			"line_number":   line,
			"function_name": funcName,
			"label":         label,
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
	// Crash isolation for async capture goroutine
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TraceKit: recovered from panic in captureSnapshot: %v", r)
		}
	}()

	// Circuit breaker check: skip if circuit is open
	if !c.cb.ShouldAllow() {
		return
	}

	url := fmt.Sprintf("%s/sdk/snapshots/capture", c.baseURL)

	body, err := c.safeSerialize(snapshot)
	if err != nil {
		// Serialization error -- do NOT count as HTTP failure
		log.Printf("TraceKit: failed to marshal snapshot: %v", err)
		return
	}

	// Apply max payload limit if configured
	if c.config.MaxPayload > 0 && len(body) > c.config.MaxPayload {
		// Truncate variables and retry
		snapshot.Variables = map[string]interface{}{
			"_truncated":    true,
			"_payload_size": len(body),
			"_max_payload":  c.config.MaxPayload,
		}
		body, err = json.Marshal(snapshot)
		if err != nil {
			log.Printf("TraceKit: failed to marshal truncated snapshot: %v", err)
			return
		}
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
		// Network/connection error -- count as HTTP failure for circuit breaker
		log.Printf("⚠️  Failed to send snapshot: %v", err)
		if tripped := c.cb.RecordFailure(); tripped {
			c.queueCircuitBreakerEvent()
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		// Server error -- count as HTTP failure for circuit breaker
		log.Printf("⚠️  Failed to capture snapshot: status %d", resp.StatusCode)
		if tripped := c.cb.RecordFailure(); tripped {
			c.queueCircuitBreakerEvent()
		}
		return
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Client error (4xx) -- do NOT count as circuit breaker failure
		log.Printf("⚠️  Failed to capture snapshot: status %d", resp.StatusCode)
		return
	}

	log.Printf("📸 Snapshot captured: %s:%d", snapshot.FilePath, snapshot.LineNumber)
}

// queueCircuitBreakerEvent queues a telemetry event to be sent with the next breakpoint poll
func (c *SnapshotClient) queueCircuitBreakerEvent() {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()

	c.pendingEvents = append(c.pendingEvents, map[string]interface{}{
		"type":             "circuit_breaker_tripped",
		"service_name":     c.serviceName,
		"failure_count":    c.cb.config.MaxFailures,
		"window_seconds":   c.cb.config.WindowMs / 1000,
		"cooldown_seconds": c.cb.config.CooldownMs / 1000,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	})
}

// drainPendingEvents returns and clears queued telemetry events (for inclusion in poll requests)
func (c *SnapshotClient) drainPendingEvents() []map[string]interface{} {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()

	if len(c.pendingEvents) == 0 {
		return nil
	}

	events := c.pendingEvents
	c.pendingEvents = nil
	return events
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

// isPIIScrubbingEnabled returns true if PII scrubbing is active (default: true)
func (c *SnapshotClient) isPIIScrubbingEnabled() bool {
	if c.config.PIIScrubbing == nil {
		return true // Default: enabled
	}
	return *c.config.PIIScrubbing
}

// scanForSecurityIssues scans variables for sensitive data and returns sanitized variables with security flags.
// Uses typed [REDACTED:type] markers and scans serialized JSON to catch nested PII.
func (c *SnapshotClient) scanForSecurityIssues(variables map[string]interface{}) (map[string]interface{}, []SecurityFlag) {
	// If PII scrubbing is disabled, return variables as-is
	if !c.isPIIScrubbingEnabled() {
		return variables, nil
	}

	var securityFlags []SecurityFlag
	sanitized := make(map[string]interface{})

	for name, value := range variables {
		// Check variable name for sensitive keywords (word-boundary matching)
		if c.sensitiveNameExpr.MatchString(name) {
			securityFlags = append(securityFlags, SecurityFlag{
				Type:     "sensitive_variable_name",
				Severity: "medium",
				Variable: name,
			})
			sanitized[name] = "[REDACTED:sensitive_name]"
			continue
		}

		// Serialize value to JSON to scan nested structures
		serialized, err := json.Marshal(value)
		if err != nil {
			sanitized[name] = fmt.Sprintf("[%T]", value)
			continue
		}

		flagged := false
		for _, pp := range c.piiPatterns {
			if pp.Pattern.Match(serialized) {
				securityFlags = append(securityFlags, SecurityFlag{
					Type:     fmt.Sprintf("sensitive_data_%s", pp.Marker),
					Severity: "high",
					Variable: name,
				})
				sanitized[name] = pp.Marker
				flagged = true
				break
			}
		}

		if !flagged {
			sanitized[name] = value
		}
	}

	return sanitized, securityFlags
}

// safeSerialize marshals data to JSON, recovering from panics caused by circular references etc.
func (c *SnapshotClient) safeSerialize(v interface{}) (result []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TraceKit: recovered from panic during serialization: %v", r)
			// Fallback: serialize a minimal representation
			result = []byte(fmt.Sprintf(`{"_error":"serialization panic: %v"}`, r))
			err = nil
		}
	}()
	return json.Marshal(v)
}

// applyCaptureConfig applies opt-in capture limits to variables.
// Returns the original variables unchanged if no limits are configured (default).
func (c *SnapshotClient) applyCaptureConfig(variables map[string]interface{}) map[string]interface{} {
	if c.config.CaptureDepth <= 0 {
		return variables // No depth limit (default)
	}

	return c.limitDepth(variables, 0)
}

// limitDepth truncates nested maps/slices beyond the configured depth
func (c *SnapshotClient) limitDepth(data map[string]interface{}, currentDepth int) map[string]interface{} {
	if currentDepth >= c.config.CaptureDepth {
		return map[string]interface{}{
			"_truncated": true,
			"_depth":     currentDepth,
		}
	}

	result := make(map[string]interface{}, len(data))
	for k, v := range data {
		switch val := v.(type) {
		case map[string]interface{}:
			result[k] = c.limitDepth(val, currentDepth+1)
		case []interface{}:
			result[k] = c.limitDepthSlice(val, currentDepth+1)
		default:
			result[k] = v
		}
	}
	return result
}

// limitDepthSlice truncates slices beyond the configured depth
func (c *SnapshotClient) limitDepthSlice(data []interface{}, currentDepth int) interface{} {
	if currentDepth >= c.config.CaptureDepth {
		return map[string]interface{}{
			"_truncated": true,
			"_depth":     currentDepth,
			"_length":    len(data),
		}
	}

	result := make([]interface{}, len(data))
	for i, v := range data {
		switch val := v.(type) {
		case map[string]interface{}:
			result[i] = c.limitDepth(val, currentDepth+1)
		case []interface{}:
			result[i] = c.limitDepthSlice(val, currentDepth+1)
		default:
			result[i] = v
		}
	}
	return result
}

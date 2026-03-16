package tracekit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// LLMConfig holds configuration for LLM auto-instrumentation.
type LLMConfig struct {
	// Enabled is the master toggle for LLM instrumentation (default: true).
	Enabled bool

	// OpenAI enables OpenAI-specific instrumentation (default: true).
	OpenAI bool

	// Anthropic enables Anthropic-specific instrumentation (default: true).
	Anthropic bool

	// CaptureContent enables capturing prompt/completion content (default: false).
	// Can be overridden by TRACEKIT_LLM_CAPTURE_CONTENT env var.
	CaptureContent bool
}

// DefaultLLMConfig returns an LLMConfig with sensible defaults.
func DefaultLLMConfig() LLMConfig {
	return LLMConfig{
		Enabled:        true,
		OpenAI:         true,
		Anthropic:      true,
		CaptureContent: false,
	}
}

// LLMOption is a functional option for configuring NewLLMTransport.
type LLMOption func(*LLMTransport)

// WithLLMConfig sets the LLM configuration for the transport.
func WithLLMConfig(cfg LLMConfig) LLMOption {
	return func(t *LLMTransport) {
		t.config = cfg
	}
}

// WithCaptureContent enables or disables content capture.
func WithCaptureContent(capture bool) LLMOption {
	return func(t *LLMTransport) {
		t.config.CaptureContent = capture
	}
}

// LLMTransport is an http.RoundTripper that automatically instruments
// OpenAI and Anthropic API calls with OpenTelemetry GenAI semantic conventions.
//
// Usage:
//
//	transport := tracekit.NewLLMTransport(nil)
//	httpClient := &http.Client{Transport: transport}
//	// Pass httpClient to OpenAI/Anthropic Go SDK
type LLMTransport struct {
	base   http.RoundTripper
	tracer trace.Tracer
	config LLMConfig
}

// NewLLMTransport creates an LLMTransport that wraps the given base transport.
// If base is nil, http.DefaultTransport is used.
// The tracer is obtained from the global OpenTelemetry tracer provider.
func NewLLMTransport(base http.RoundTripper, opts ...LLMOption) *LLMTransport {
	if base == nil {
		base = http.DefaultTransport
	}

	t := &LLMTransport{
		base:   base,
		tracer: otel.Tracer("tracekit-llm"),
		config: DefaultLLMConfig(),
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// RoundTrip implements http.RoundTripper. It detects OpenAI and Anthropic API
// calls, instruments them with GenAI spans, and passes all other requests through.
func (t *LLMTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.config.Enabled {
		return t.base.RoundTrip(req)
	}

	provider := detectProvider(req.URL.Host)
	if provider == "" {
		// Not an LLM provider, pass through without instrumentation.
		return t.base.RoundTrip(req)
	}

	// Check per-provider toggle.
	if provider == "openai" && !t.config.OpenAI {
		return t.base.RoundTrip(req)
	}
	if provider == "anthropic" && !t.config.Anthropic {
		return t.base.RoundTrip(req)
	}

	// Only instrument POST requests (API calls), not GETs (model listing, etc.).
	if req.Method != http.MethodPost {
		return t.base.RoundTrip(req)
	}

	// Read request body to extract model and params.
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return t.base.RoundTrip(req)
		}
		// Replace body so downstream can read it (Pitfall 3).
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	// Delegate to provider-specific handler.
	switch provider {
	case "openai":
		return handleOpenAIRoundTrip(t, req, body)
	case "anthropic":
		return handleAnthropicRoundTrip(t, req, body)
	default:
		return t.base.RoundTrip(req)
	}
}

// detectProvider identifies the LLM provider from the request host.
func detectProvider(host string) string {
	// Strip port if present.
	h := host
	if idx := strings.Index(h, ":"); idx != -1 {
		h = h[:idx]
	}

	switch h {
	case "api.openai.com":
		return "openai"
	case "api.anthropic.com":
		return "anthropic"
	default:
		return ""
	}
}

// shouldCaptureContent returns true if content capture is enabled,
// checking both config and TRACEKIT_LLM_CAPTURE_CONTENT env var.
func (t *LLMTransport) shouldCaptureContent() bool {
	// Env var overrides config.
	if envVal := os.Getenv("TRACEKIT_LLM_CAPTURE_CONTENT"); envVal != "" {
		return strings.EqualFold(envVal, "true") || envVal == "1"
	}
	return t.config.CaptureContent
}

// --- Shared attribute helpers ---

// setGenAIRequestAttrs sets common gen_ai.request.* attributes on a span.
func setGenAIRequestAttrs(span trace.Span, provider, model string, maxTokens int, temperature, topP float64) {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", provider),
		attribute.String("gen_ai.request.model", model),
	}
	if maxTokens > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.request.max_tokens", maxTokens))
	}
	if temperature > 0 {
		attrs = append(attrs, attribute.Float64("gen_ai.request.temperature", temperature))
	}
	if topP > 0 {
		attrs = append(attrs, attribute.Float64("gen_ai.request.top_p", topP))
	}
	span.SetAttributes(attrs...)
}

// setGenAIResponseAttrs sets common gen_ai.response.* and gen_ai.usage.* attributes.
func setGenAIResponseAttrs(span trace.Span, responseID, responseModel string, finishReasons []string, inputTokens, outputTokens int) {
	attrs := []attribute.KeyValue{}
	if responseID != "" {
		attrs = append(attrs, attribute.String("gen_ai.response.id", responseID))
	}
	if responseModel != "" {
		attrs = append(attrs, attribute.String("gen_ai.response.model", responseModel))
	}
	if len(finishReasons) > 0 {
		attrs = append(attrs, attribute.StringSlice("gen_ai.response.finish_reasons", finishReasons))
	}
	if inputTokens > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.usage.input_tokens", inputTokens))
	}
	if outputTokens > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.usage.output_tokens", outputTokens))
	}
	span.SetAttributes(attrs...)
}

// setGenAIErrorAttrs records an error on the span with error.type attribute.
func setGenAIErrorAttrs(span trace.Span, err error) {
	span.SetStatus(codes.Error, err.Error())
	span.RecordError(err)
	span.SetAttributes(attribute.String("error.type", errorTypeName(err)))
}

// errorTypeName extracts a short type name for the error.
func errorTypeName(err error) string {
	if err == nil {
		return ""
	}
	t := fmt.Sprintf("%T", err)
	t = strings.TrimPrefix(t, "*")
	if t == "" {
		return "Error"
	}
	return t
}

// recordToolCallEvent adds a gen_ai.tool.call span event.
func recordToolCallEvent(span trace.Span, name, callID, arguments string) {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.tool.name", name),
	}
	if callID != "" {
		attrs = append(attrs, attribute.String("gen_ai.tool.call.id", callID))
	}
	if arguments != "" {
		attrs = append(attrs, attribute.String("gen_ai.tool.call.arguments", arguments))
	}
	span.AddEvent("gen_ai.tool.call", trace.WithAttributes(attrs...))
}

// --- Content capture helpers ---

// piiScrubber is a pre-compiled set of patterns for scrubbing PII from content.
var piiScrubber = newPIIScrubber()

type llmPIIScrubber struct {
	patterns          []piiScrubPattern
	sensitiveNameExpr *regexp.Regexp
}

type piiScrubPattern struct {
	pattern *regexp.Regexp
	marker  string
}

func newPIIScrubber() *llmPIIScrubber {
	return &llmPIIScrubber{
		patterns: []piiScrubPattern{
			{regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), "[REDACTED:email]"},
			{regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), "[REDACTED:ssn]"},
			{regexp.MustCompile(`\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b`), "[REDACTED:credit_card]"},
			{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "[REDACTED:aws_key]"},
			{regexp.MustCompile(`(?i)(?:bearer\s+)[A-Za-z0-9._~+/=\-]{20,}`), "[REDACTED:oauth_token]"},
			{regexp.MustCompile(`sk_live_[0-9a-zA-Z]{10,}`), "[REDACTED:stripe_key]"},
			{regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`), "[REDACTED:jwt]"},
			{regexp.MustCompile(`-----BEGIN (?:RSA |EC )?PRIVATE KEY-----`), "[REDACTED:private_key]"},
		},
		sensitiveNameExpr: regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(password|passwd|pwd|secret|token|key|credential|api_key|apikey)(?:[^a-zA-Z]|$)`),
	}
}

// scrub applies PII patterns to the given string content.
func (s *llmPIIScrubber) scrub(content string) string {
	for _, p := range s.patterns {
		content = p.pattern.ReplaceAllString(content, p.marker)
	}
	return content
}

// captureInputMessages sets gen_ai.input.messages on the span if content capture is enabled.
func captureInputMessages(span trace.Span, messages interface{}) {
	data, err := json.Marshal(messages)
	if err != nil {
		return
	}
	scrubbed := piiScrubber.scrub(string(data))
	span.SetAttributes(attribute.String("gen_ai.input.messages", scrubbed))
}

// captureOutputMessages sets gen_ai.output.messages on the span if content capture is enabled.
func captureOutputMessages(span trace.Span, content interface{}) {
	data, err := json.Marshal(content)
	if err != nil {
		return
	}
	scrubbed := piiScrubber.scrub(string(data))
	span.SetAttributes(attribute.String("gen_ai.output.messages", scrubbed))
}

// captureSystemInstructions sets gen_ai.system_instructions on the span.
func captureSystemInstructions(span trace.Span, system interface{}) {
	if system == nil {
		return
	}
	data, err := json.Marshal(system)
	if err != nil {
		return
	}
	scrubbed := piiScrubber.scrub(string(data))
	span.SetAttributes(attribute.String("gen_ai.system_instructions", scrubbed))
}

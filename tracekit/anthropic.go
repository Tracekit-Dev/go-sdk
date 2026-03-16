package tracekit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// --- Internal structs for Anthropic JSON parsing ---

type anthropicRequest struct {
	Model       string        `json:"model"`
	Messages    []interface{} `json:"messages"`
	System      interface{}   `json:"system,omitempty"` // string or []ContentBlock
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	TopP        float64       `json:"top_p,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type anthropicResponse struct {
	ID         string                   `json:"id"`
	Type       string                   `json:"type"`
	Model      string                   `json:"model"`
	Content    []map[string]interface{} `json:"content"`
	StopReason string                   `json:"stop_reason,omitempty"`
	Usage      *anthropicUsage          `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// anthropicStreamEvent represents a generic SSE event from Anthropic's streaming API.
type anthropicStreamEvent struct {
	Type    string           `json:"type"`
	Message *anthropicResponse `json:"message,omitempty"`
	Delta   json.RawMessage  `json:"delta,omitempty"`
	Usage   *anthropicUsage  `json:"usage,omitempty"`
	Index   int              `json:"index,omitempty"`
}

// handleAnthropicRoundTrip handles an Anthropic API request with GenAI span instrumentation.
func handleAnthropicRoundTrip(t *LLMTransport, req *http.Request, body []byte) (*http.Response, error) {
	// Parse request body.
	var antReq anthropicRequest
	if err := json.Unmarshal(body, &antReq); err != nil {
		// Can't parse request -- pass through without instrumentation.
		return t.base.RoundTrip(req)
	}

	model := antReq.Model
	if model == "" {
		model = "unknown"
	}

	// Start span.
	ctx, span := t.tracer.Start(req.Context(), fmt.Sprintf("chat %s", model),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	req = req.WithContext(ctx)

	// Set request attributes.
	setGenAIRequestAttrs(span, "anthropic", model, antReq.MaxTokens, antReq.Temperature, antReq.TopP)

	// Capture content if enabled.
	if t.shouldCaptureContent() {
		if len(antReq.Messages) > 0 {
			captureInputMessages(span, antReq.Messages)
		}
		if antReq.System != nil {
			captureSystemInstructions(span, antReq.System)
		}
	}

	// Execute the actual request.
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		setGenAIErrorAttrs(span, err)
		span.End()
		return resp, err
	}

	// Handle error HTTP status codes.
	if resp.StatusCode >= 400 {
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		span.End()
		return resp, nil
	}

	if antReq.Stream {
		// Wrap the response body to intercept streaming SSE events.
		resp.Body = &anthropicStreamReader{
			reader:    bufio.NewReader(resp.Body),
			original:  resp.Body,
			span:      span,
			transport: t,
		}
	} else {
		// Non-streaming: read response, extract attributes, re-wrap body.
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			setGenAIErrorAttrs(span, readErr)
			span.End()
			resp.Body = io.NopCloser(bytes.NewReader(nil))
			return resp, nil
		}

		var antResp anthropicResponse
		if err := json.Unmarshal(respBody, &antResp); err == nil {
			finishReasons := []string{}
			if antResp.StopReason != "" {
				finishReasons = append(finishReasons, antResp.StopReason)
			}

			inputTokens, outputTokens := 0, 0
			if antResp.Usage != nil {
				inputTokens = antResp.Usage.InputTokens
				outputTokens = antResp.Usage.OutputTokens

				// Anthropic-specific cache token attributes.
				if antResp.Usage.CacheCreationInputTokens > 0 {
					span.SetAttributes(attribute.Int("gen_ai.usage.cache_creation.input_tokens", antResp.Usage.CacheCreationInputTokens))
				}
				if antResp.Usage.CacheReadInputTokens > 0 {
					span.SetAttributes(attribute.Int("gen_ai.usage.cache_read.input_tokens", antResp.Usage.CacheReadInputTokens))
				}
			}

			setGenAIResponseAttrs(span, antResp.ID, antResp.Model, finishReasons, inputTokens, outputTokens)

			// Record tool use blocks as events.
			for _, block := range antResp.Content {
				if blockType, _ := block["type"].(string); blockType == "tool_use" {
					name, _ := block["name"].(string)
					callID, _ := block["id"].(string)
					argsRaw, _ := block["input"]
					args := ""
					if argsRaw != nil {
						if b, err := json.Marshal(argsRaw); err == nil {
							args = string(b)
						}
					}
					if name != "" {
						recordToolCallEvent(span, name, callID, args)
					}
				}
			}

			// Capture output content if enabled.
			if t.shouldCaptureContent() && len(antResp.Content) > 0 {
				captureOutputMessages(span, antResp.Content)
			}
		}

		// Re-wrap response body so caller can read it.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		span.End()
	}

	return resp, nil
}

// --- Anthropic Streaming Reader ---

// anthropicStreamReader wraps a streaming SSE response body from Anthropic,
// transparently passing through all data while accumulating GenAI attributes.
type anthropicStreamReader struct {
	reader    *bufio.Reader
	original  io.ReadCloser
	span      trace.Span
	transport *LLMTransport

	mu            sync.Mutex
	responseID    string
	responseModel string
	stopReason    string
	inputTokens   int
	outputTokens  int
	cacheCreate   int
	cacheRead     int
	closed        bool

	buf bytes.Buffer
}

// Read implements io.Reader.
func (r *anthropicStreamReader) Read(p []byte) (int, error) {
	if r.buf.Len() > 0 {
		return r.buf.Read(p)
	}

	line, err := r.reader.ReadString('\n')
	if len(line) > 0 {
		r.parseLine(line)
		r.buf.WriteString(line)
	}

	if err != nil {
		if err == io.EOF {
			r.finalize()
		}
		if r.buf.Len() > 0 {
			n, _ := r.buf.Read(p)
			return n, err
		}
		return 0, err
	}

	return r.buf.Read(p)
}

// Close implements io.Closer.
func (r *anthropicStreamReader) Close() error {
	r.finalize()
	return r.original.Close()
}

// parseLine extracts GenAI attributes from an Anthropic SSE data line.
func (r *anthropicStreamReader) parseLine(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")

	var event anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch event.Type {
	case "message_start":
		// message_start contains the full message object with input_tokens.
		if event.Message != nil {
			r.responseID = event.Message.ID
			r.responseModel = event.Message.Model
			if event.Message.Usage != nil {
				r.inputTokens = event.Message.Usage.InputTokens
				r.cacheCreate = event.Message.Usage.CacheCreationInputTokens
				r.cacheRead = event.Message.Usage.CacheReadInputTokens
			}
		}

	case "message_delta":
		// message_delta contains output_tokens and stop_reason.
		if event.Usage != nil {
			r.outputTokens = event.Usage.OutputTokens
		}
		// Parse delta for stop_reason.
		if event.Delta != nil {
			var delta struct {
				StopReason string `json:"stop_reason,omitempty"`
			}
			if err := json.Unmarshal(event.Delta, &delta); err == nil && delta.StopReason != "" {
				r.stopReason = delta.StopReason
			}
		}

	case "message_stop":
		// Stream complete -- finalize will be called on Close/EOF.
	}
}

// finalize sets the accumulated attributes on the span and ends it.
func (r *anthropicStreamReader) finalize() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}
	r.closed = true

	finishReasons := []string{}
	if r.stopReason != "" {
		finishReasons = append(finishReasons, r.stopReason)
	}

	setGenAIResponseAttrs(r.span, r.responseID, r.responseModel, finishReasons, r.inputTokens, r.outputTokens)

	if r.cacheCreate > 0 {
		r.span.SetAttributes(attribute.Int("gen_ai.usage.cache_creation.input_tokens", r.cacheCreate))
	}
	if r.cacheRead > 0 {
		r.span.SetAttributes(attribute.Int("gen_ai.usage.cache_read.input_tokens", r.cacheRead))
	}

	r.span.End()
}

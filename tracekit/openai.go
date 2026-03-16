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

// --- Internal structs for OpenAI JSON parsing ---

type openaiRequest struct {
	Model         string                 `json:"model"`
	Messages      []interface{}          `json:"messages"`
	MaxTokens     int                    `json:"max_tokens,omitempty"`
	Temperature   float64                `json:"temperature,omitempty"`
	TopP          float64                `json:"top_p,omitempty"`
	Stream        bool                   `json:"stream,omitempty"`
	StreamOptions map[string]interface{} `json:"stream_options,omitempty"`
}

type openaiResponse struct {
	ID                string          `json:"id"`
	Model             string          `json:"model"`
	Choices           []openaiChoice  `json:"choices"`
	Usage             *openaiUsage    `json:"usage,omitempty"`
	SystemFingerprint string          `json:"system_fingerprint,omitempty"`
}

type openaiChoice struct {
	Index        int                    `json:"index"`
	Message      map[string]interface{} `json:"message,omitempty"`
	Delta        map[string]interface{} `json:"delta,omitempty"`
	FinishReason string                 `json:"finish_reason,omitempty"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiStreamChunk struct {
	ID                string          `json:"id"`
	Model             string          `json:"model"`
	Choices           []openaiChoice  `json:"choices"`
	Usage             *openaiUsage    `json:"usage,omitempty"`
	SystemFingerprint string          `json:"system_fingerprint,omitempty"`
}

// handleOpenAIRoundTrip handles an OpenAI API request with GenAI span instrumentation.
func handleOpenAIRoundTrip(t *LLMTransport, req *http.Request, body []byte) (*http.Response, error) {
	// Parse request body.
	var oaiReq openaiRequest
	if err := json.Unmarshal(body, &oaiReq); err != nil {
		// Can't parse request -- pass through without instrumentation.
		return t.base.RoundTrip(req)
	}

	model := oaiReq.Model
	if model == "" {
		model = "unknown"
	}

	// Start span.
	ctx, span := t.tracer.Start(req.Context(), fmt.Sprintf("chat %s", model),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	req = req.WithContext(ctx)

	// Set request attributes.
	setGenAIRequestAttrs(span, "openai", model, oaiReq.MaxTokens, oaiReq.Temperature, oaiReq.TopP)

	// Capture content if enabled.
	if t.shouldCaptureContent() && len(oaiReq.Messages) > 0 {
		captureInputMessages(span, oaiReq.Messages)
	}

	// For streaming requests, inject stream_options.include_usage if not already set.
	if oaiReq.Stream {
		body = injectOpenAIStreamUsage(body, oaiReq.StreamOptions)
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
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

	if oaiReq.Stream {
		// Wrap the response body to intercept streaming SSE events.
		resp.Body = &openaiStreamReader{
			reader:         bufio.NewReader(resp.Body),
			original:       resp.Body,
			span:           span,
			transport:      t,
		}
	} else {
		// Non-streaming: read response, extract attributes, re-wrap body.
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			setGenAIErrorAttrs(span, readErr)
			span.End()
			// Re-wrap with empty body so caller doesn't get a closed reader.
			resp.Body = io.NopCloser(bytes.NewReader(nil))
			return resp, nil
		}

		var oaiResp openaiResponse
		if err := json.Unmarshal(respBody, &oaiResp); err == nil {
			// Set response attributes.
			finishReasons := make([]string, 0, len(oaiResp.Choices))
			for _, c := range oaiResp.Choices {
				if c.FinishReason != "" {
					finishReasons = append(finishReasons, c.FinishReason)
				}
			}

			inputTokens, outputTokens := 0, 0
			if oaiResp.Usage != nil {
				inputTokens = oaiResp.Usage.PromptTokens
				outputTokens = oaiResp.Usage.CompletionTokens
			}

			setGenAIResponseAttrs(span, oaiResp.ID, oaiResp.Model, finishReasons, inputTokens, outputTokens)

			if oaiResp.SystemFingerprint != "" {
				span.SetAttributes(attribute.String("openai.response.system_fingerprint", oaiResp.SystemFingerprint))
			}

			// Record tool calls as events.
			for _, choice := range oaiResp.Choices {
				if toolCalls, ok := choice.Message["tool_calls"].([]interface{}); ok {
					for _, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							recordToolCallFromMap(span, tcMap)
						}
					}
				}
			}

			// Capture output content if enabled.
			if t.shouldCaptureContent() && len(oaiResp.Choices) > 0 {
				captureOutputMessages(span, oaiResp.Choices)
			}
		}

		// Re-wrap response body so caller can read it.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		span.End()
	}

	return resp, nil
}

// injectOpenAIStreamUsage ensures stream_options.include_usage is true in the request body.
func injectOpenAIStreamUsage(body []byte, existingOpts map[string]interface{}) []byte {
	// Check if include_usage is already set.
	if existingOpts != nil {
		if v, ok := existingOpts["include_usage"]; ok {
			if b, ok := v.(bool); ok && b {
				return body // Already set.
			}
		}
	}

	// Parse as generic map to inject stream_options.
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body
	}

	so, ok := bodyMap["stream_options"].(map[string]interface{})
	if !ok {
		so = make(map[string]interface{})
	}
	so["include_usage"] = true
	bodyMap["stream_options"] = so

	newBody, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return newBody
}

// recordToolCallFromMap records a tool call span event from a parsed JSON map.
func recordToolCallFromMap(span trace.Span, tcMap map[string]interface{}) {
	fn, _ := tcMap["function"].(map[string]interface{})
	if fn == nil {
		return
	}
	name, _ := fn["name"].(string)
	callID, _ := tcMap["id"].(string)
	args, _ := fn["arguments"].(string)
	if name != "" {
		recordToolCallEvent(span, name, callID, args)
	}
}

// --- OpenAI Streaming Reader ---

// openaiStreamReader wraps a streaming SSE response body, transparently passing
// through all data while accumulating GenAI attributes from SSE chunks.
// When the stream ends (EOF or Close), it sets the final attributes and ends the span.
type openaiStreamReader struct {
	reader    *bufio.Reader
	original  io.ReadCloser
	span      trace.Span
	transport *LLMTransport

	mu            sync.Mutex
	responseID    string
	responseModel string
	fingerprint   string
	finishReasons []string
	inputTokens   int
	outputTokens  int
	closed        bool

	// Buffer for Read() calls -- stores raw data to return to caller.
	buf bytes.Buffer
}

// Read implements io.Reader. It reads SSE data, parses it for GenAI attributes,
// and returns the raw bytes to the caller transparently.
func (r *openaiStreamReader) Read(p []byte) (int, error) {
	// If we have buffered data from a previous parse, return it first.
	if r.buf.Len() > 0 {
		return r.buf.Read(p)
	}

	// Read a line from the SSE stream.
	line, err := r.reader.ReadString('\n')
	if len(line) > 0 {
		// Parse SSE data lines for GenAI attributes.
		r.parseLine(line)
		// Buffer the line for the caller.
		r.buf.WriteString(line)
	}

	if err != nil {
		if err == io.EOF {
			r.finalize()
		}
		// Return any buffered data first.
		if r.buf.Len() > 0 {
			n, _ := r.buf.Read(p)
			return n, err
		}
		return 0, err
	}

	return r.buf.Read(p)
}

// Close implements io.Closer. It finalizes the span and closes the original body.
func (r *openaiStreamReader) Close() error {
	r.finalize()
	return r.original.Close()
}

// parseLine extracts GenAI attributes from an SSE data line.
func (r *openaiStreamReader) parseLine(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return
	}

	var chunk openaiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if chunk.ID != "" {
		r.responseID = chunk.ID
	}
	if chunk.Model != "" {
		r.responseModel = chunk.Model
	}
	if chunk.SystemFingerprint != "" {
		r.fingerprint = chunk.SystemFingerprint
	}

	for _, c := range chunk.Choices {
		if c.FinishReason != "" {
			r.finishReasons = append(r.finishReasons, c.FinishReason)
		}
	}

	if chunk.Usage != nil {
		r.inputTokens = chunk.Usage.PromptTokens
		r.outputTokens = chunk.Usage.CompletionTokens
	}
}

// finalize sets the accumulated attributes on the span and ends it.
func (r *openaiStreamReader) finalize() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}
	r.closed = true

	setGenAIResponseAttrs(r.span, r.responseID, r.responseModel, r.finishReasons, r.inputTokens, r.outputTokens)

	if r.fingerprint != "" {
		r.span.SetAttributes(attribute.String("openai.response.system_fingerprint", r.fingerprint))
	}

	r.span.End()
}

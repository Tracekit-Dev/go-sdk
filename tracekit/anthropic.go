package tracekit

import "net/http"

// handleAnthropicRoundTrip is a placeholder; implemented in Task 2.
func handleAnthropicRoundTrip(t *LLMTransport, req *http.Request, body []byte) (*http.Response, error) {
	return t.base.RoundTrip(req)
}

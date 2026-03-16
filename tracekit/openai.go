package tracekit

import "net/http"

// handleOpenAIRoundTrip is a placeholder; implemented in Task 2.
func handleOpenAIRoundTrip(t *LLMTransport, req *http.Request, body []byte) (*http.Response, error) {
	return t.base.RoundTrip(req)
}

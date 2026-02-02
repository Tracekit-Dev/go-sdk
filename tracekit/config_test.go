package tracekit

import (
	"testing"
)

func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		path     string
		useSSL   bool
		want     string
	}{
		// Just host cases
		{
			name:     "just host with SSL",
			endpoint: "app.tracekit.dev",
			path:     "/v1/traces",
			useSSL:   true,
			want:     "https://app.tracekit.dev/v1/traces",
		},
		{
			name:     "just host without SSL",
			endpoint: "localhost:8081",
			path:     "/v1/traces",
			useSSL:   false,
			want:     "http://localhost:8081/v1/traces",
		},
		{
			name:     "just host with trailing slash",
			endpoint: "app.tracekit.dev/",
			path:     "/v1/metrics",
			useSSL:   true,
			want:     "https://app.tracekit.dev/v1/metrics",
		},

		// Host with scheme cases
		{
			name:     "http with host only",
			endpoint: "http://localhost:8081",
			path:     "/v1/traces",
			useSSL:   true, // Should be ignored
			want:     "http://localhost:8081/v1/traces",
		},
		{
			name:     "https with host only",
			endpoint: "https://app.tracekit.dev",
			path:     "/v1/metrics",
			useSSL:   false, // Should be ignored
			want:     "https://app.tracekit.dev/v1/metrics",
		},
		{
			name:     "http with host and trailing slash",
			endpoint: "http://localhost:8081/",
			path:     "/v1/traces",
			useSSL:   true,
			want:     "http://localhost:8081/v1/traces",
		},

		// Full URL cases
		{
			name:     "full URL with standard path",
			endpoint: "http://localhost:8081/v1/traces",
			path:     "/v1/traces",
			useSSL:   true,
			want:     "http://localhost:8081/v1/traces",
		},
		{
			name:     "full URL with custom path",
			endpoint: "http://localhost:8081/custom/path",
			path:     "/v1/traces",
			useSSL:   true,
			want:     "http://localhost:8081/custom/path",
		},
		{
			name:     "full URL with trailing slash",
			endpoint: "https://app.tracekit.dev/api/v2/",
			path:     "/v1/traces",
			useSSL:   false,
			want:     "https://app.tracekit.dev/api/v2",
		},

		// Edge cases
		{
			name:     "empty path for snapshots",
			endpoint: "app.tracekit.dev",
			path:     "",
			useSSL:   true,
			want:     "https://app.tracekit.dev",
		},
		{
			name:     "http with empty path",
			endpoint: "http://localhost:8081",
			path:     "",
			useSSL:   true,
			want:     "http://localhost:8081",
		},
		{
			name:     "http with trailing slash and empty path",
			endpoint: "http://localhost:8081/",
			path:     "",
			useSSL:   true,
			want:     "http://localhost:8081",
		},
		{
			name:     "snapshot with full URL extracts base (http)",
			endpoint: "http://localhost:8081/v1/traces",
			path:     "",
			useSSL:   true, // Should be ignored
			want:     "http://localhost:8081",
		},
		{
			name:     "snapshot with full URL extracts base (https)",
			endpoint: "https://app.tracekit.dev/v1/traces",
			path:     "",
			useSSL:   false, // Should be ignored
			want:     "https://app.tracekit.dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEndpoint(tt.endpoint, tt.path, tt.useSSL)
			if got != tt.want {
				t.Errorf("resolveEndpoint(%q, %q, %v) = %q; want %q",
					tt.endpoint, tt.path, tt.useSSL, got, tt.want)
			}
		})
	}
}

func TestEndpointResolution(t *testing.T) {
	tests := []struct {
		name            string
		config          *Config
		wantTraces      string
		wantMetrics     string
		wantSnapshots   string
	}{
		{
			name: "default production config",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "app.tracekit.dev",
				UseSSL:      true,
			},
			wantTraces:    "https://app.tracekit.dev/v1/traces",
			wantMetrics:   "https://app.tracekit.dev/v1/metrics",
			wantSnapshots: "https://app.tracekit.dev",
		},
		{
			name: "local development",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "localhost:8080",
				UseSSL:      false,
			},
			wantTraces:    "http://localhost:8080/v1/traces",
			wantMetrics:   "http://localhost:8080/v1/metrics",
			wantSnapshots: "http://localhost:8080",
		},
		{
			name: "custom paths",
			config: &Config{
				APIKey:       "test-key",
				ServiceName:  "test-service",
				Endpoint:     "app.tracekit.dev",
				TracesPath:   "/api/v2/traces",
				MetricsPath:  "/api/v2/metrics",
				UseSSL:       true,
			},
			wantTraces:    "https://app.tracekit.dev/api/v2/traces",
			wantMetrics:   "https://app.tracekit.dev/api/v2/metrics",
			wantSnapshots: "https://app.tracekit.dev",
		},
		{
			name: "full URLs provided",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "http://localhost:8081/custom",
				UseSSL:      true, // Should be ignored
			},
			wantTraces:    "http://localhost:8081/custom",
			wantMetrics:   "http://localhost:8081/custom",
			wantSnapshots: "http://localhost:8081/custom",
		},
		{
			name: "trailing slash handling",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "http://localhost:8081/",
				UseSSL:      false,
			},
			wantTraces:    "http://localhost:8081/v1/traces",
			wantMetrics:   "http://localhost:8081/v1/metrics",
			wantSnapshots: "http://localhost:8081",
		},
		{
			name: "full URL with path - snapshots extract base",
			config: &Config{
				APIKey:      "test-key",
				ServiceName: "test-service",
				Endpoint:    "http://localhost:8081/v1/traces",
				UseSSL:      true, // Should be ignored
			},
			wantTraces:    "http://localhost:8081/v1/traces",
			wantMetrics:   "http://localhost:8081/v1/traces",
			wantSnapshots: "http://localhost:8081", // Should extract base URL
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set defaults like NewSDK does
			if tt.config.TracesPath == "" {
				tt.config.TracesPath = "/v1/traces"
			}
			if tt.config.MetricsPath == "" {
				tt.config.MetricsPath = "/v1/metrics"
			}

			// Resolve endpoints
			tracesEndpoint := resolveEndpoint(tt.config.Endpoint, tt.config.TracesPath, tt.config.UseSSL)
			metricsEndpoint := resolveEndpoint(tt.config.Endpoint, tt.config.MetricsPath, tt.config.UseSSL)
			snapshotEndpoint := resolveEndpoint(tt.config.Endpoint, "", tt.config.UseSSL)

			if tracesEndpoint != tt.wantTraces {
				t.Errorf("traces endpoint = %q; want %q", tracesEndpoint, tt.wantTraces)
			}
			if metricsEndpoint != tt.wantMetrics {
				t.Errorf("metrics endpoint = %q; want %q", metricsEndpoint, tt.wantMetrics)
			}
			if snapshotEndpoint != tt.wantSnapshots {
				t.Errorf("snapshots endpoint = %q; want %q", snapshotEndpoint, tt.wantSnapshots)
			}
		})
	}
}

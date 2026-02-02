package tracekit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// metricsExporter sends metrics to the backend
type metricsExporter struct {
	endpoint    string
	apiKey      string
	serviceName string
	client      *http.Client
}

func newMetricsExporter(endpoint, apiKey, serviceName string) *metricsExporter {
	return &metricsExporter{
		endpoint:    endpoint, // Use endpoint as-is (already resolved in config)
		apiKey:      apiKey,
		serviceName: serviceName,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (e *metricsExporter) export(dataPoints []metricDataPoint) error {
	if len(dataPoints) == 0 {
		return nil
	}

	payload := e.toOTLP(dataPoints)

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	req, err := http.NewRequest("POST", e.endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %d", resp.StatusCode)
	}

	return nil
}

// toOTLP converts metrics to OTLP format
func (e *metricsExporter) toOTLP(dataPoints []metricDataPoint) map[string]interface{} {
	// Group by name and type
	grouped := make(map[string][]metricDataPoint)
	for _, dp := range dataPoints {
		key := dp.name + ":" + dp.typ
		grouped[key] = append(grouped[key], dp)
	}

	// Build metrics array
	metrics := make([]map[string]interface{}, 0, len(grouped))

	for key, dps := range grouped {
		parts := strings.Split(key, ":")
		name := parts[0]
		typ := parts[1]

		// Convert data points
		otlpDPs := make([]map[string]interface{}, 0, len(dps))
		for _, dp := range dps {
			// Convert tags to attributes
			attributes := make([]map[string]interface{}, 0, len(dp.tags))
			for k, v := range dp.tags {
				attributes = append(attributes, map[string]interface{}{
					"key": k,
					"value": map[string]interface{}{
						"stringValue": v,
					},
				})
			}

			otlpDPs = append(otlpDPs, map[string]interface{}{
				"attributes":   attributes,
				"timeUnixNano": fmt.Sprintf("%d", dp.timestamp.UnixNano()),
				"asDouble":     dp.value,
			})
		}

		// Create metric based on type
		var metric map[string]interface{}
		switch typ {
		case "counter":
			metric = map[string]interface{}{
				"name": name,
				"sum": map[string]interface{}{
					"dataPoints":             otlpDPs,
					"aggregationTemporality": 2, // DELTA
					"isMonotonic":            true,
				},
			}
		case "gauge", "histogram":
			metric = map[string]interface{}{
				"name": name,
				"gauge": map[string]interface{}{
					"dataPoints": otlpDPs,
				},
			}
		}

		metrics = append(metrics, metric)
	}

	return map[string]interface{}{
		"resourceMetrics": []map[string]interface{}{
			{
				"resource": map[string]interface{}{
					"attributes": []map[string]interface{}{
						{
							"key": "service.name",
							"value": map[string]interface{}{
								"stringValue": e.serviceName,
							},
						},
					},
				},
				"scopeMetrics": []map[string]interface{}{
					{
						"scope": map[string]interface{}{
							"name": "tracekit",
						},
						"metrics": metrics,
					},
				},
			},
		},
	}
}

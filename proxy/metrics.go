package proxy

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Metrics collects and exposes proxy metrics
type Metrics struct {
	mu             sync.RWMutex
	requestCount   map[string]int
	errorCount     map[string]int
	responseSizes  []int64
	latencies      []time.Duration
	startTime      time.Time
}

// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		requestCount: make(map[string]int),
		errorCount:   make(map[string]int),
		startTime:    time.Now(),
	}
}

// RecordRequest records a request metric
func (m *Metrics) RecordRequest(path string, statusCode int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s_%d", path, statusCode)
	m.requestCount[key]++

	if statusCode >= 400 {
		m.errorCount[key]++
	}

	m.responseSizes = append(m.responseSizes, 0) // Simplified
	m.latencies = append(m.latencies, latency)
}

// GetMetrics returns current metrics as a map
func (m *Metrics) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalRequests := 0
	totalErrors := 0
	for _, v := range m.requestCount {
		totalRequests += v
	}
	for _, v := range m.errorCount {
		totalErrors += v
	}

	avgLatency := time.Duration(0)
	if len(m.latencies) > 0 {
		sum := time.Duration(0)
		for _, l := range m.latencies {
			sum += l
		}
		avgLatency = sum / time.Duration(len(m.latencies))
	}

	return map[string]interface{}{
		"total_requests": totalRequests,
		"total_errors":   totalErrors,
		"error_rate":     float64(totalErrors) / float64(totalRequests),
		"avg_latency":    avgLatency.String(),
		"uptime":         time.Since(m.startTime).String(),
		"by_endpoint":    m.requestCount,
		"errors_by_endpoint": m.errorCount,
	}
}

// ServeHTTP exposes metrics via HTTP
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	metrics := m.GetMetrics()

	fmt.Fprintln(w, "# Proxy Metrics")
	fmt.Fprintf(w, "# Total Requests: %v\n", metrics["total_requests"])
	fmt.Fprintf(w, "# Total Errors: %v\n", metrics["total_errors"])
	fmt.Fprintf(w, "# Error Rate: %v\n", metrics["error_rate"])
	fmt.Fprintf(w, "# Average Latency: %v\n", metrics["avg_latency"])
	fmt.Fprintf(w, "# Uptime: %v\n", metrics["uptime"])

	if byEndpoint, ok := metrics["by_endpoint"].(map[string]int); ok {
		fmt.Fprintln(w, "\n# By Endpoint:")
		for k, v := range byEndpoint {
			fmt.Fprintf(w, "%s %d\n", k, v)
		}
	}
}

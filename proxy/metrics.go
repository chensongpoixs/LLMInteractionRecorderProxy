package proxy

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

const maxMetricSamples = 10000 // ring buffer capacity for latencies and response sizes

// Metrics collects and exposes proxy metrics with bounded memory usage.
type Metrics struct {
	mu            sync.RWMutex
	requestCount  map[string]int
	errorCount    map[string]int
	latencies     []time.Duration // ring buffer
	responseSizes []int64         // ring buffer
	ringWrite     int             // next write position in ring
	ringCount     int             // number of valid entries (≤ maxMetricSamples)
	totalBytes    int64
	startTime     time.Time
}

// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		requestCount:  make(map[string]int),
		errorCount:    make(map[string]int),
		latencies:     make([]time.Duration, maxMetricSamples),
		responseSizes: make([]int64, maxMetricSamples),
		startTime:     time.Now(),
	}
}

// RecordRequest records a request metric.
func (m *Metrics) RecordRequest(path string, statusCode int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s_%d", path, statusCode)
	m.requestCount[key]++

	if statusCode >= 400 {
		m.errorCount[key]++
	}

	m.latencies[m.ringWrite] = latency
	m.responseSizes[m.ringWrite] = 0
	m.ringWrite++
	if m.ringCount < maxMetricSamples {
		m.ringCount++
	}
	if m.ringWrite >= maxMetricSamples {
		m.ringWrite = 0
	}
}

// RecordBytesSent records the response body size for the most recent request.
// Call this after RecordRequest when the actual byte count is known.
func (m *Metrics) RecordBytesSent(path string, statusCode int, latency time.Duration, bytesSent int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s_%d", path, statusCode)
	m.requestCount[key]++

	if statusCode >= 400 {
		m.errorCount[key]++
	}

	m.latencies[m.ringWrite] = latency
	m.responseSizes[m.ringWrite] = bytesSent
	m.totalBytes += bytesSent
	m.ringWrite++
	if m.ringCount < maxMetricSamples {
		m.ringCount++
	}
	if m.ringWrite >= maxMetricSamples {
		m.ringWrite = 0
	}
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
	if m.ringCount > 0 {
		sum := time.Duration(0)
		for i := 0; i < m.ringCount; i++ {
			sum += m.latencies[i]
		}
		avgLatency = sum / time.Duration(m.ringCount)
	}

	errRate := 0.0
	if totalRequests > 0 {
		errRate = float64(totalErrors) / float64(totalRequests)
	}

	return map[string]interface{}{
		"total_requests":     totalRequests,
		"total_errors":       totalErrors,
		"error_rate":         errRate,
		"avg_latency":        avgLatency.String(),
		"uptime":             time.Since(m.startTime).String(),
		"total_bytes":        m.totalBytes,
		"by_endpoint":        m.requestCount,
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
	fmt.Fprintf(w, "# Total Bytes Sent: %v\n", metrics["total_bytes"])

	if byEndpoint, ok := metrics["by_endpoint"].(map[string]int); ok {
		fmt.Fprintln(w, "\n# By Endpoint:")
		for k, v := range byEndpoint {
			fmt.Fprintf(w, "%s %d\n", k, v)
		}
	}
}

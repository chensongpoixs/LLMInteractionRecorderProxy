package proxy

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"proxy-llm/config"
	"proxy-llm/logger"
	"proxy-llm/storage"
)

// Handler provides HTTP handlers for the proxy
type Handler struct {
	config    *config.Config
	storageLogger *storage.Logger
	metrics   *Metrics
	appLogger *logger.Logger

	requestCount   atomic.Int64
	errorCount     atomic.Int64
	totalBytesSent atomic.Int64
}

type usageDailyPoint struct {
	Date             string  `json:"date"`
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type usageModelPoint struct {
	Model            string  `json:"model"`
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type usageRecentItem struct {
	Timestamp        string  `json:"timestamp"`
	Model            string  `json:"model"`
	Endpoint         string  `json:"endpoint"`
	StatusCode       int     `json:"status_code"`
	DurationMS       int64   `json:"duration_ms"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type usageSummaryResponse struct {
	Days            int               `json:"days"`
	GeneratedAt     string            `json:"generated_at"`
	TotalRequests   int               `json:"total_requests"`
	SuccessRequests int               `json:"success_requests"`
	SuccessRate     float64           `json:"success_rate"`
	AvgLatencyMS    int64             `json:"avg_latency_ms"`
	PromptTokens    int64             `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	TotalTokens     int64             `json:"total_tokens"`
	EstimatedCostUSD float64          `json:"estimated_cost_usd"`
	Daily           []usageDailyPoint `json:"daily"`
	ByModel         []usageModelPoint `json:"by_model"`
	Recent          []usageRecentItem `json:"recent"`
}

// NewHandler creates a new handler instance
func NewHandler(cfg *config.Config, storageLogger *storage.Logger, metrics *Metrics, appLogger *logger.Logger) *Handler {
	appLogger.Info("Initializing HTTP handler...")
	return &Handler{
		config:    cfg,
		storageLogger: storageLogger,
		metrics:   metrics,
		appLogger: appLogger,
	}
}

// HandleHealthCheck responds with server health status
func (h *Handler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	h.appLogger.Debug("Health check requested from: %s", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"requests":  h.requestCount.Load(),
		"errors":    h.errorCount.Load(),
	})
}

// HandleListLogs lists available log files
func (h *Handler) HandleListLogs(w http.ResponseWriter, r *http.Request) {
	h.appLogger.Debug("Listing log files requested from: %s", r.RemoteAddr)
	sessionID := r.URL.Query().Get("session")
	date := r.URL.Query().Get("date")

	files, err := h.storageLogger.ListLogs(sessionID, date)
	if err != nil {
		h.appLogger.Error("Failed to list log files: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.appLogger.Debug("Found %d log files", len(files))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
		"count": len(files),
	})
}

// HandleGetLog retrieves a specific log file
func (h *Handler) HandleGetLog(w http.ResponseWriter, r *http.Request) {
	h.appLogger.Debug("Retrieving log file from: %s", r.RemoteAddr)
	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		h.appLogger.Warn("Log file retrieval missing 'file' parameter from: %s", r.RemoteAddr)
		http.Error(w, "Missing 'file' parameter", http.StatusBadRequest)
		return
	}

	entries, err := h.storageLogger.ReadLog(filePath)
	if err != nil {
		h.appLogger.Error("Failed to read log file %s: %v", filePath, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.appLogger.Debug("Read %d entries from %s", len(entries), filePath)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"file":    filePath,
		"entries": entries,
	})
}

// IncrementRequestCount increases the request counter
func (h *Handler) IncrementRequestCount() {
	h.requestCount.Add(1)
}

// IncrementErrorCount increases the error counter
func (h *Handler) IncrementErrorCount() {
	h.errorCount.Add(1)
}

// RecordBytesSent records bytes sent in response
func (h *Handler) RecordBytesSent(n int64) {
	h.totalBytesSent.Add(n)
}

// GetStats returns proxy statistics
func (h *Handler) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"total_requests":   h.requestCount.Load(),
		"total_errors":     h.errorCount.Load(),
		"total_bytes_sent": h.totalBytesSent.Load(),
		"uptime":           time.Since(time.Now().Add(-time.Duration(h.requestCount.Load()) * time.Second)).String(),
	}
}

// Middleware for request logging
func (h *Handler) RequestLogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		h.IncrementRequestCount()
		next(wrapped, r)

		duration := time.Since(start)
		fmt.Printf("[%s] %s %s %d %v\n",
			start.Format("15:04:05"),
			r.Method,
			r.URL.Path,
			wrapped.statusCode,
			duration,
		)

		// Update metrics
		if h.metrics != nil {
			h.metrics.RecordRequest(r.URL.Path, wrapped.statusCode, duration)
		}

		if wrapped.statusCode >= 400 {
			h.IncrementErrorCount()
			if h.appLogger != nil {
				h.appLogger.Warn("Client error %d for %s %s", wrapped.statusCode, r.Method, r.URL.Path)
			}
		}
	}
}

// HandleUsageSummary returns aggregated token usage for dashboard UI.
func (h *Handler) HandleUsageSummary(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		h.appLogger.Error("Failed to list logs for usage summary: %v", err)
		http.Error(w, "Failed to read usage logs", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	start := now.AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	type modelAgg struct {
		requests   int
		prompt     int64
		completion int64
		total      int64
		cost       float64
	}
	type dayAgg struct {
		requests   int
		prompt     int64
		completion int64
		total      int64
		cost       float64
	}

	dayMap := make(map[string]*dayAgg)
	modelMap := make(map[string]*modelAgg)
	recent := make([]usageRecentItem, 0, 64)

	var totalRequests, successRequests int
	var latencyTotalMS int64
	var promptTokens, completionTokens, totalTokens int64
	var estimatedCost float64

	for _, file := range files {
		path := filepath.ToSlash(file)
		if strings.Contains(path, "/streams/") || strings.Contains(path, "_stream_") {
			continue
		}

		entries, readErr := h.storageLogger.ReadLog(file)
		if readErr != nil {
			if h.appLogger != nil {
				h.appLogger.Warn("Skip unreadable log file %s: %v", file, readErr)
			}
			continue
		}

		for _, raw := range entries {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}

			ts := rec.Timestamp
			if ts.IsZero() || ts.Before(start) || ts.After(now) {
				continue
			}

			prompt, completion, total := tokensFromRecord(rec)
			if total == 0 {
				total = prompt + completion
			}

			latency := parseDurationMS(rec.Duration)
			cost := estimateCostUSD(rec.Model, prompt, completion)
			dateKey := ts.Format("2006-01-02")

			totalRequests++
			if rec.StatusCode > 0 && rec.StatusCode < 400 {
				successRequests++
			}
			latencyTotalMS += latency
			promptTokens += prompt
			completionTokens += completion
			totalTokens += total
			estimatedCost += cost

			if _, ok := dayMap[dateKey]; !ok {
				dayMap[dateKey] = &dayAgg{}
			}
			dayMap[dateKey].requests++
			dayMap[dateKey].prompt += prompt
			dayMap[dateKey].completion += completion
			dayMap[dateKey].total += total
			dayMap[dateKey].cost += cost

			modelKey := rec.Model
			if strings.TrimSpace(modelKey) == "" {
				modelKey = "unknown"
			}
			if _, ok := modelMap[modelKey]; !ok {
				modelMap[modelKey] = &modelAgg{}
			}
			modelMap[modelKey].requests++
			modelMap[modelKey].prompt += prompt
			modelMap[modelKey].completion += completion
			modelMap[modelKey].total += total
			modelMap[modelKey].cost += cost

			recent = append(recent, usageRecentItem{
				Timestamp:        ts.Format(time.RFC3339),
				Model:            modelKey,
				Endpoint:         rec.Endpoint,
				StatusCode:       rec.StatusCode,
				DurationMS:       latency,
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      total,
				EstimatedCostUSD: round2(cost),
			})
		}
	}

	daily := make([]usageDailyPoint, 0, len(dayMap))
	for d, v := range dayMap {
		daily = append(daily, usageDailyPoint{
			Date:             d,
			Requests:         v.requests,
			PromptTokens:     v.prompt,
			CompletionTokens: v.completion,
			TotalTokens:      v.total,
			EstimatedCostUSD: round2(v.cost),
		})
	}
	sort.Slice(daily, func(i, j int) bool { return daily[i].Date < daily[j].Date })

	byModel := make([]usageModelPoint, 0, len(modelMap))
	for m, v := range modelMap {
		byModel = append(byModel, usageModelPoint{
			Model:            m,
			Requests:         v.requests,
			PromptTokens:     v.prompt,
			CompletionTokens: v.completion,
			TotalTokens:      v.total,
			EstimatedCostUSD: round2(v.cost),
		})
	}
	sort.Slice(byModel, func(i, j int) bool { return byModel[i].TotalTokens > byModel[j].TotalTokens })

	sort.Slice(recent, func(i, j int) bool { return recent[i].Timestamp > recent[j].Timestamp })
	if len(recent) > 20 {
		recent = recent[:20]
	}

	successRate := 0.0
	avgLatency := int64(0)
	if totalRequests > 0 {
		successRate = float64(successRequests) / float64(totalRequests)
		avgLatency = latencyTotalMS / int64(totalRequests)
	}

	resp := usageSummaryResponse{
		Days:             days,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		TotalRequests:    totalRequests,
		SuccessRequests:  successRequests,
		SuccessRate:      round4(successRate),
		AvgLatencyMS:     avgLatency,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		EstimatedCostUSD: round2(estimatedCost),
		Daily:            daily,
		ByModel:          byModel,
		Recent:           recent,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func tokensFromRecord(rec storage.RequestLog) (int64, int64, int64) {
	prompt := int64(rec.TokensUsed["prompt_tokens"])
	completion := int64(rec.TokensUsed["completion_tokens"])
	total := int64(rec.TokensUsed["total_tokens"])
	if total == 0 {
		total = prompt + completion
	}
	return prompt, completion, total
}

func parseDurationMS(raw string) int64 {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return d.Milliseconds()
}

func estimateCostUSD(model string, promptTokens, completionTokens int64) float64 {
	// Industry-like rough pricing per 1M tokens (input/output). Keep this as estimation.
	type pricing struct {
		inPer1M  float64
		outPer1M float64
	}

	prices := []struct {
		match string
		p     pricing
	}{
		{match: "gpt-4o", p: pricing{inPer1M: 5.0, outPer1M: 15.0}},
		{match: "gpt-4.1", p: pricing{inPer1M: 2.0, outPer1M: 8.0}},
		{match: "claude-3.7-sonnet", p: pricing{inPer1M: 3.0, outPer1M: 15.0}},
		{match: "claude-3.5-sonnet", p: pricing{inPer1M: 3.0, outPer1M: 15.0}},
		{match: "claude-haiku", p: pricing{inPer1M: 0.8, outPer1M: 4.0}},
		{match: "gemini-1.5-pro", p: pricing{inPer1M: 3.5, outPer1M: 10.5}},
		{match: "gemini-1.5-flash", p: pricing{inPer1M: 0.35, outPer1M: 1.05}},
		{match: "qwen", p: pricing{inPer1M: 0.6, outPer1M: 1.8}},
		{match: "gemma", p: pricing{inPer1M: 0.0, outPer1M: 0.0}},
		{match: "llama", p: pricing{inPer1M: 0.0, outPer1M: 0.0}},
	}

	selected := pricing{inPer1M: 1.0, outPer1M: 3.0}
	lower := strings.ToLower(model)
	for _, item := range prices {
		if strings.Contains(lower, item.match) {
			selected = item.p
			break
		}
	}
	inCost := float64(promptTokens) / 1_000_000.0 * selected.inPer1M
	outCost := float64(completionTokens) / 1_000_000.0 * selected.outPer1M
	return inCost + outCost
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += n
	return n, err
}

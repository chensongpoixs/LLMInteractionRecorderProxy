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
	"proxy-llm/exporter"
	"proxy-llm/logger"
	"proxy-llm/storage"
)

// Handler provides HTTP handlers for the proxy
type Handler struct {
	config        *config.Config
	storageLogger *storage.Logger
	metrics       *Metrics
	appLogger     *logger.Logger

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
	Days             int               `json:"days"`
	GeneratedAt      string            `json:"generated_at"`
	TotalRequests    int               `json:"total_requests"`
	SuccessRequests  int               `json:"success_requests"`
	SuccessRate      float64           `json:"success_rate"`
	AvgLatencyMS     int64             `json:"avg_latency_ms"`
	PromptTokens     int64             `json:"prompt_tokens"`
	CompletionTokens int64             `json:"completion_tokens"`
	TotalTokens      int64             `json:"total_tokens"`
	EstimatedCostUSD float64           `json:"estimated_cost_usd"`
	Daily            []usageDailyPoint `json:"daily"`
	ByModel          []usageModelPoint `json:"by_model"`
	Recent           []usageRecentItem `json:"recent"`
}

type promptListItem struct {
	ID              string `json:"id"`
	Timestamp       string `json:"timestamp"`
	Model           string `json:"model"`
	Language        string `json:"language"`
	PromptTokens    int    `json:"prompt_tokens"`
	TotalTokens     int    `json:"total_tokens"`
	PromptPreview   string `json:"prompt_preview"`
	PromptFull      string `json:"prompt_full"`
	ResponsePreview string `json:"response_preview"`
	ResponseFull    string `json:"response_full"`
}

type promptListResponse struct {
	Items      []promptListItem `json:"items"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	PerPage    int              `json:"per_page"`
	TotalPages int              `json:"total_pages"`
}

type promptDateDay struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type promptDateMonth struct {
	Month string          `json:"month"`
	Days  []promptDateDay `json:"days"`
}

type promptDatesResponse struct {
	Months []promptDateMonth `json:"months"`
}

// NewHandler creates a new handler instance
func NewHandler(cfg *config.Config, storageLogger *storage.Logger, metrics *Metrics, appLogger *logger.Logger) *Handler {
	appLogger.Info("Initializing HTTP handler...")
	return &Handler{
		config:        cfg,
		storageLogger: storageLogger,
		metrics:       metrics,
		appLogger:     appLogger,
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
	days := parseUsageDays(r)
	resp, err := h.buildUsageSummary(days)
	if err != nil {
		h.appLogger.Error("Failed to build usage summary: %v", err)
		http.Error(w, "Failed to read usage logs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleUsageStream streams usage summary updates via SSE.
func (h *Handler) HandleUsageStream(w http.ResponseWriter, r *http.Request) {
	days := parseUsageDays(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	send := func() error {
		resp, err := h.buildUsageSummary(days)
		if err != nil {
			return err
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: usage\ndata: %s\n\n", string(data))
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := send(); err != nil {
		if h.appLogger != nil {
			h.appLogger.Warn("usage SSE initial send failed: %v", err)
		}
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				if h.appLogger != nil {
					h.appLogger.Warn("usage SSE send failed: %v", err)
				}
				return
			}
		}
	}
}

func parseUsageDays(r *http.Request) int {
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	return days
}

func (h *Handler) buildUsageSummary(days int) (usageSummaryResponse, error) {
	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		return usageSummaryResponse{}, err
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
		// Exclude raw chunk files under streams/ only.
		// Keep *_stream_ request logs because they contain final token usage.
		if strings.Contains(path, "/streams/") {
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

	return resp, nil
}

// HandlePromptDates returns available dates grouped by month with prompt counts.
func (h *Handler) HandlePromptDates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	resp, err := h.buildPromptDates()
	if err != nil {
		if h.appLogger != nil {
			h.appLogger.Error("buildPromptDates: %v", err)
		}
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) buildPromptDates() (promptDatesResponse, error) {
	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		return promptDatesResponse{}, err
	}

	dayCount := make(map[string]int)

	for _, file := range files {
		path := filepath.ToSlash(file)
		if strings.Contains(path, "/streams/") {
			continue
		}

		// Extract YYYYMMDD from path like ".../YYYYMMDD/session_..."
		for _, seg := range strings.Split(path, "/") {
			if len(seg) == 8 && isAllDigits(seg) {
				entries, readErr := h.storageLogger.ReadLog(file)
				if readErr != nil {
					continue
				}
				for _, raw := range entries {
					var rec storage.RequestLog
					if err := json.Unmarshal(raw, &rec); err != nil {
						continue
					}
					if rec.RequestBody == nil {
						continue
					}
					promptText := extractLastUserMessage(rec.RequestBody)
					if promptText == "" {
						continue
					}
					promptClean := exporter.SystemReminderRE.ReplaceAllString(promptText, "")
					if len(strings.TrimSpace(promptClean)) < 3 {
						continue
					}
					dayCount[seg]++
				}
				break
			}
		}
	}

	// Group by month
	monthMap := make(map[string][]promptDateDay)
	for dateStr, count := range dayCount {
		month := dateStr[:6] // YYYYMM
		monthMap[month] = append(monthMap[month], promptDateDay{Date: dateStr, Count: count})
	}

	// Sort months descending
	var months []string
	for m := range monthMap {
		months = append(months, m)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(months)))

	result := make([]promptDateMonth, 0, len(months))
	for _, m := range months {
		days := monthMap[m]
		sort.Slice(days, func(i, j int) bool { return days[i].Date > days[j].Date })
		result = append(result, promptDateMonth{Month: m, Days: days})
	}

	return promptDatesResponse{Months: result}, nil
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// HandlePromptList returns a paginated list of user prompts, optionally filtered by date.
func (h *Handler) HandlePromptList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	dateFilter := strings.TrimSpace(r.URL.Query().Get("date"))

	page := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			page = n
		}
	}

	perPage := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("per_page")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			perPage = n
			if perPage > 100 {
				perPage = 100
			}
		}
	}

	resp, err := h.buildPromptList(dateFilter, page, perPage)
	if err != nil {
		if h.appLogger != nil {
			h.appLogger.Error("buildPromptList: %v", err)
		}
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) buildPromptList(dateFilter string, page, perPage int) (promptListResponse, error) {
	files, err := h.storageLogger.ListLogs("", dateFilter)
	if err != nil {
		return promptListResponse{}, err
	}

	allItems := make([]promptListItem, 0, 256)

	for _, file := range files {
		path := filepath.ToSlash(file)
		if strings.Contains(path, "/streams/") {
			continue
		}

		entries, readErr := h.storageLogger.ReadLog(file)
		if readErr != nil {
			continue
		}

		for _, raw := range entries {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}

			if dateFilter != "" {
				ts := rec.Timestamp
				if ts.Format("20060102") != dateFilter {
					continue
				}
			}

			if rec.RequestBody == nil {
				continue
			}

			promptText := extractLastUserMessage(rec.RequestBody)
			if promptText == "" {
				continue
			}

			promptClean := exporter.SystemReminderRE.ReplaceAllString(promptText, "")
			promptClean = strings.TrimSpace(promptClean)
			if len(promptClean) < 3 {
				continue
			}

			lang := exporter.DetectLanguage(promptClean)
			promptTokens, _, totalTokens := tokensFromRecord(rec)

			// Extract response (thinking + solution)
			thinking, solution := extractResponse(&rec)
			var respPreview, respFull string
			if thinking != "" && solution != "" {
				respFull = "【思考过程】\n" + thinking + "\n\n【回复】\n" + solution
			} else if thinking != "" {
				respFull = "【思考过程】\n" + thinking
			} else if solution != "" {
				respFull = solution
			}
			runes := []rune(respFull)
			if len(runes) > 200 {
				respPreview = string(runes[:200]) + "..."
			} else {
				respPreview = respFull
			}

			promptPreview := promptClean
			prunes := []rune(promptPreview)
			if len(prunes) > 200 {
				promptPreview = string(prunes[:200]) + "..."
			}

			allItems = append(allItems, promptListItem{
				ID:              rec.ID,
				Timestamp:       rec.Timestamp.Format(time.RFC3339),
				Model:           rec.Model,
				Language:        lang,
				PromptTokens:    int(promptTokens),
				TotalTokens:     int(totalTokens),
				PromptPreview:   promptPreview,
				PromptFull:      promptClean,
				ResponsePreview: respPreview,
				ResponseFull:    respFull,
			})
		}
	}

	// Sort by timestamp descending (most recent first)
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].Timestamp > allItems[j].Timestamp
	})

	// Deduplicate: keep only the latest entry per unique prompt.
	// Multi-turn conversations produce multiple API calls with the same
	// last user message; we only need one entry per distinct question.
	seen := make(map[string]bool, len(allItems))
	deduped := make([]promptListItem, 0, len(allItems))
	for _, it := range allItems {
		if !seen[it.PromptFull] {
			seen[it.PromptFull] = true
			deduped = append(deduped, it)
		}
	}
	allItems = deduped

	total := len(allItems)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	startIdx := (page - 1) * perPage
	if startIdx >= total {
		return promptListResponse{
			Items:      []promptListItem{},
			Total:      total,
			Page:       page,
			PerPage:    perPage,
			TotalPages: totalPages,
		}, nil
	}

	endIdx := startIdx + perPage
	if endIdx > total {
		endIdx = total
	}

	return promptListResponse{
		Items:      allItems[startIdx:endIdx],
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}, nil
}

// extractResponse extracts thinking and solution from a RequestLog.
func extractResponse(rec *storage.RequestLog) (thinking, solution string) {
	// Priority 1: AggregatedResponse
	if rec.AggregatedResponse != nil {
		if choices, ok := rec.AggregatedResponse["choices"].([]interface{}); ok && len(choices) > 0 {
			if c0, ok := choices[0].(map[string]interface{}); ok {
				if msg, ok := c0["message"].(map[string]interface{}); ok {
					content, _ := msg["content"].(string)
					reasoning, _ := msg["reasoning_content"].(string)
					if reasoning == "" {
						if rc, ok := msg["reasoning"].(string); ok {
							reasoning = rc
						}
					}
					return strings.TrimSpace(reasoning), strings.TrimSpace(content)
				}
			}
		}
	}

	// Priority 2: Messages chain
	for i := len(rec.Messages) - 1; i >= 0; i-- {
		msg := rec.Messages[i]
		if msg.Role == "assistant" {
			if msg.Reasoning != "" || msg.Content != "" {
				return msg.Reasoning, msg.Content
			}
		}
	}

	// Priority 3: ResponseBody
	if rec.ResponseBody != nil {
		// OpenAI style
		if ch, ok := rec.ResponseBody["choices"].([]interface{}); ok && len(ch) > 0 {
			if c0, ok := ch[0].(map[string]interface{}); ok {
				if msg, ok := c0["message"].(map[string]interface{}); ok {
					th, _ := msg["reasoning_content"].(string)
					if th == "" {
						if rc, ok := msg["reasoning"].(string); ok {
							th = rc
						}
					}
					so, _ := msg["content"].(string)
					return strings.TrimSpace(th), strings.TrimSpace(so)
				}
			}
		}
		// Anthropic style
		if c, ok := rec.ResponseBody["content"].([]interface{}); ok {
			var think, reg strings.Builder
			for _, b := range c {
				bm, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				typ, _ := bm["type"].(string)
				if txt, ok := bm["text"].(string); ok {
					if typ == "thinking" {
						think.WriteString(txt)
					} else {
						reg.WriteString(txt)
					}
				}
			}
			return strings.TrimSpace(think.String()), strings.TrimSpace(reg.String())
		}
	}

	return "", ""
}

// extractLastUserMessage extracts the last user message from the request body,
// which represents the actual question being asked. This avoids showing the
// conversation compaction summary that appears as the first user message in
// multi-turn conversations.
func extractLastUserMessage(req map[string]interface{}) string {
	msgs, _ := req["messages"].([]interface{})
	if len(msgs) == 0 {
		return ""
	}
	// Walk backwards to find the last user message with real content.
	// Skip tool results, system instructions, and compaction summaries
	// that Claude Code appends at the end of multi-turn conversations.
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		if role != "user" {
			continue
		}
		content := mm["content"]
		// Handle string content (OpenAI style)
		if s, ok := content.(string); ok {
			s = strings.TrimSpace(s)
			if s == "" || isSystemMessage(s) {
				continue
			}
			return s
		}
		// Handle array content (Anthropic style)
		if arr, ok := content.([]interface{}); ok {
			// Check if this message contains only tool_result blocks.
			hasToolResult := false
			hasOtherBlock := false
			var texts []string
			for _, b := range arr {
				bm, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				typ, _ := bm["type"].(string)
				if typ == "tool_result" {
					hasToolResult = true
					continue
				}
				if typ != "" && typ != "text" {
					continue
				}
				hasOtherBlock = true
				if t, ok := bm["text"].(string); ok {
					texts = append(texts, t)
				}
			}
			// Skip messages that are purely tool results
			if hasToolResult && !hasOtherBlock {
				continue
			}
			result := strings.TrimSpace(strings.Join(texts, "\n"))
			if result == "" || isSystemMessage(result) {
				continue
			}
			return result
		}
	}
	return ""
}

// isSystemMessage checks if text looks like a system-generated instruction.
func isSystemMessage(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || text == "..." {
		return true
	}
	if strings.HasPrefix(text, "This session is being continued from") {
		return true
	}
	if strings.HasPrefix(text, "CRITICAL:") {
		return true
	}
	// XML-style system/task notifications from Claude Code
	if strings.HasPrefix(text, "<task-notification>") || strings.HasPrefix(text, "<system-reminder>") {
		return true
	}
	return false
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

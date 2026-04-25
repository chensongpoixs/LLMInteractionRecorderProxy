package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
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

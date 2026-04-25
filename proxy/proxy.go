package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"proxy-llm/config"
	"proxy-llm/logger"
	"proxy-llm/storage"
)

// Proxy handles forwarding requests to LLM APIs
type Proxy struct {
	server   *http.Server
	clients  map[string]*http.Client
	config   *config.Config
	logger   *storage.Logger
	handler  *Handler
	metrics  *Metrics
	authMw   func(http.HandlerFunc) http.HandlerFunc
	appLogger *logger.Logger
}

// New creates a new proxy instance
func New(cfg *config.Config, storageLogger *storage.Logger, metrics *Metrics, appLogger *logger.Logger) *Proxy {
	appLogger.Info("Initializing proxy server...")

	proxy := &Proxy{
		config:    cfg,
		logger:    storageLogger,
		metrics:   metrics,
		clients:   make(map[string]*http.Client),
		appLogger: appLogger,
	}

	// Create HTTP clients for each model
	appLogger.Info("Creating HTTP clients for %d models...", len(cfg.Models))
	for _, model := range cfg.Models {
		appLogger.Debug("Creating HTTP client for model: %s", model.Name)
		transport := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		}

		proxy.clients[model.Name] = &http.Client{
			Transport: transport,
			Timeout:   model.Timeout,
		}
	}

	// Setup HTTP handlers
	mux := http.NewServeMux()
	proxy.handler = NewHandler(cfg, storageLogger, metrics, appLogger)

	// Register proxy routes
	appLogger.Info("Registering proxy routes...")
	mux.HandleFunc("/v1/chat/completions", proxy.handleRequest("chat/completions"))
	mux.HandleFunc("/v1/completions", proxy.handleRequest("completions"))
	mux.HandleFunc("/v1/embeddings", proxy.handleRequest("embeddings"))
	// llama.cpp llama-server compatible endpoint
	mux.HandleFunc("/v1/api/chat", proxy.handleRequest("chat/completions"))
	// Models endpoint - returns available models for Claude Code validation
	mux.HandleFunc("/v1/models", proxy.handleModels)

	// Health check
	if cfg.Monitoring.EnableHealth {
		appLogger.Info("Enabling health check endpoint: /health")
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "OK")
		})
	}

	// Metrics endpoint
	if cfg.Monitoring.EnableMetrics {
		appLogger.Info("Enabling metrics endpoint: %s", cfg.Monitoring.MetricsPath)
		mux.HandleFunc(cfg.Monitoring.MetricsPath, proxy.metrics.ServeHTTP)
	}

	// Auth middleware
	if cfg.Proxy.EnableAuth {
		appLogger.Info("Enabling authentication middleware")
		proxy.authMw = proxy.createAuthMiddleware()
	}

	// Apply CORS if enabled
	var handler http.Handler = mux
	if cfg.Proxy.EnableCORS {
		appLogger.Info("Enabling CORS middleware")
		handler = proxy.createCORSMiddleware(mux)
	}

	// Wrap with 404 logger - log unregistered endpoints
	handler = proxy.create404Logger(handler)

	// Apply auth if enabled
	if proxy.authMw != nil {
		handler = proxy.authMw(http.HandlerFunc(handler.ServeHTTP))
	}

	proxy.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: handler,
	}

	appLogger.Info("Proxy initialization complete")
	return proxy
}

// generateRequestID creates a unique request ID for tracking
func (p *Proxy) generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}

// handleRequest creates an HTTP handler for a specific endpoint
func (p *Proxy) handleRequest(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := p.generateRequestID()
		requestLogger := p.appLogger.WithRequestID(reqID)

		start := time.Now()
		requestLogger.Info("=== Request received ===")
		requestLogger.Info("Method: %s, Path: %s, Remote: %s", r.Method, r.URL.Path, r.RemoteAddr)

		// Log request headers (excluding sensitive ones)
		requestLogger.Debug("Headers: %v", r.Header)

		// Read request body
		requestLogger.Debug("Reading request body...")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			requestLogger.Error("Failed to read request body: %v", err)
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		r.Body.Close()
		requestLogger.Info("Request body read: %d bytes", len(body))

		// Parse request body
		requestLogger.Debug("Parsing request body...")
		var reqBody map[string]interface{}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			requestLogger.Error("Failed to parse request body: %v", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		requestLogger.Debug("Request body parsed successfully")

		// Log request body if enabled
		if p.appLogger.ShouldLogRequestBody() {
			bodyStr, _ := json.Marshal(reqBody)
			requestLogger.Debug("Request body: %s", string(bodyStr))
		}

		// Determine target model and endpoint
		modelName := "default"
		if model, exists := reqBody["model"]; exists {
			if m, ok := model.(string); ok {
				modelName = m
			}
		}
		requestLogger.Info("Target model: %s", modelName)

		targetURL := fmt.Sprintf("%s/%s", p.getModelBaseURL(modelName), endpoint)
		requestLogger.Info("Target URL: %s", targetURL)

		// Get API key
		apiKey := p.getAPIKey(modelName)
		requestLogger.Debug("API key retrieved for model: %s", modelName)

		// Translate model name from proxy name to actual llama.cpp model name
		requestLogger.Debug("Translating model name from %s to %s", modelName, p.config.Models[0].ModelName)
		p.translateModelNameInBody(reqBody)
		// Re-marshal body with translated model name
		body, _ = json.Marshal(reqBody)
		requestLogger.Debug("Translated model name in request body")

		// Create proxy request
		requestLogger.Debug("Creating proxy request...")
		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
		if err != nil {
			requestLogger.Error("Failed to create proxy request: %v", err)
			http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
			return
		}

		// Copy headers
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
		proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		requestLogger.Debug("Proxy request headers set")

		// Determine if streaming
		isStream := false
		if stream, exists := reqBody["stream"]; exists {
			if s, ok := stream.(bool); ok {
				isStream = s
			}
		}
		requestLogger.Info("Stream mode: %v", isStream)

		// Execute request
		requestLogger.Debug("Looking up HTTP client for model: %s", modelName)
		var client *http.Client
		if c, exists := p.clients[modelName]; exists {
			client = c
			requestLogger.Debug("Using client for model: %s", modelName)
		} else {
			client = p.clients["default"]
			requestLogger.Debug("Falling back to default client")
		}

		// Handle streaming vs non-streaming
		if isStream && p.config.Proxy.EnableStream {
			requestLogger.Info("=== Starting streaming mode ===")
			p.handleStreaming(w, r, proxyReq, client, reqBody, modelName, start, endpoint, requestLogger)
			return
		}

		// Non-streaming response
		requestLogger.Info("=== Sending request to LLM API ===")
		resp, err := client.Do(proxyReq)
		if err != nil {
			requestLogger.Error("Proxy request failed: %v", err)
			p.logRequest(reqBody, modelName, endpoint, start, 500, nil, true, err.Error(), nil, requestLogger)
			http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
			return
		}
		requestLogger.Info("LLM API responded: status=%d", resp.StatusCode)
		defer resp.Body.Close()

		// Read response body
		requestLogger.Debug("Reading LLM API response body...")
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			requestLogger.Error("Failed to read LLM API response: %v", err)
			p.logRequest(reqBody, modelName, endpoint, start, resp.StatusCode, nil, false, err.Error(), nil, requestLogger)
			http.Error(w, "Failed to read response", http.StatusBadGateway)
			return
		}
		requestLogger.Info("LLM API response body size: %d bytes", len(respBody))

		// Parse response for logging
		requestLogger.Debug("Parsing LLM API response...")
		var respParsed map[string]interface{}
		json.Unmarshal(respBody, &respParsed)

		// Log the request/response
		tokensUsed := p.extractTokens(respParsed)
		if p.appLogger.ShouldLogResponseBody() && len(respParsed) > 0 {
			respStr, _ := json.Marshal(respParsed)
			requestLogger.Debug("LLM API response: %s", string(respStr))
		}
		p.logRequest(reqBody, modelName, endpoint, start, resp.StatusCode, respBody, false, "", tokensUsed, requestLogger)

		// Log response details
		duration := time.Since(start)
		p.appLogger.LogResponse(reqID, r.Method, r.URL.Path, resp.StatusCode, duration, len(respBody))

		// Normalize token field names (llama.cpp -> OpenAI standard)
		forwardBody := p.normalizeTokens(respBody)

		// Forward response
		requestLogger.Debug("Forwarding response to client...")
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(forwardBody)
		requestLogger.Info("=== Request completed ===")
	}
}

// handleStreaming manages streaming responses
func (p *Proxy) handleStreaming(w http.ResponseWriter, r *http.Request, proxyReq *http.Request, client *http.Client, reqBody map[string]interface{}, modelName string, start time.Time, endpoint string, requestLogger *logger.Logger) {
	requestLogger.Info("=== Streaming mode initiated ===")

	resp, err := client.Do(proxyReq)
	if err != nil {
		requestLogger.Error("Streaming request failed: %v", err)
		p.logRequest(reqBody, modelName, endpoint, start, 500, nil, true, err.Error(), nil, requestLogger)
		http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Set headers for streaming
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	requestLogger.Info("Streaming headers set, status: %d", resp.StatusCode)

	// Create request log entry
	sessionID := r.Header.Get("X-Session-ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}
	requestLogger.Info("Session ID: %s", sessionID)

	reqLog := &storage.RequestLog{
		ID:          fmt.Sprintf("req_%d", time.Now().UnixNano()),
		Timestamp:   time.Now(),
		SessionID:   sessionID,
		Endpoint:    endpoint,
		Method:      r.Method,
		Model:       modelName,
		Provider:    modelName,
		RequestBody: reqBody,
		Stream:      true,
	}

	// Read and forward stream
	chunkCount := 0
	totalBytes := 0

	scanner := make([]byte, 8192)
	for {
		n, err := resp.Body.Read(scanner)
		if n > 0 {
			chunkData := scanner[:n]
			// Try to parse and normalize chunk if it looks like JSON
			// First, split the raw data into SSE lines
			lines := bytes.Split(chunkData, []byte("\n"))
			for _, line := range lines {
				line = bytes.TrimSpace(line)
				if len(line) == 0 {
					continue
				}
				if bytes.Equal(line, []byte("[DONE]")) {
					w.Write(line)
					w.Write([]byte("\n"))
					w.(http.Flusher).Flush()
					continue
				}
				chunkToWrite := line
				if bytes.HasPrefix(line, []byte("data:")) {
					// Extract JSON data from SSE "data:" line
					sseData := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
					// Find the first { and last } to extract JSON
					if idxStart := bytes.Index(sseData, []byte("{")); idxStart >= 0 {
						if idxEnd := bytes.LastIndex(sseData, []byte("}")); idxEnd > idxStart {
							jsonPart := sseData[idxStart : idxEnd+1]
							normalized := p.normalizeStreamChunk(jsonPart)
							if len(normalized) != len(jsonPart) {
								chunkToWrite = append([]byte("data: "), normalized...)
							}
						}
					}
				} else if idxStart := bytes.Index(line, []byte("{")); idxStart >= 0 {
					if idxEnd := bytes.LastIndex(line, []byte("}")); idxEnd > idxStart {
						jsonPart := line[idxStart : idxEnd+1]
						normalized := p.normalizeStreamChunk(jsonPart)
						if len(normalized) != len(jsonPart) {
							chunkToWrite = normalized
						}
					}
				}
				w.Write(chunkToWrite)
				w.Write([]byte("\n"))
				w.(http.Flusher).Flush()
				chunkCount++
				totalBytes += len(chunkToWrite)
				requestLogger.LogStreaming(reqLog.ID, chunkCount, len(chunkToWrite))
			}

			// Save individual chunk
			streamChunk := &storage.ResponseStream{
				ID:        reqLog.ID,
				Chunk:     string(chunkData),
				Timestamp: time.Now(),
				SessionID: sessionID,
				Index:     chunkCount,
			}
			p.logger.SaveStreamChunk(streamChunk)
		}
		if err != nil {
			if err != io.EOF {
				requestLogger.Error("Streaming error: %v", err)
				reqLog.Error = err.Error()
			} else {
				requestLogger.Info("Streaming completed: %d chunks, %d bytes total", chunkCount, totalBytes)
			}
			break
		}
	}

	// Finalize request log
	reqLog.StatusCode = resp.StatusCode
	reqLog.Duration = time.Since(start).String()
	p.logger.SaveRequest(reqLog)
	requestLogger.Info("Streaming request log saved")
}

// getModelBaseURL returns the base URL for a model
func (p *Proxy) getModelBaseURL(modelName string) string {
	for _, model := range p.config.Models {
		if model.Name == modelName {
			return model.BaseURL
		}
	}
	return p.config.Models[0].BaseURL
}

// getAPIKey returns the API key for a model
func (p *Proxy) getAPIKey(modelName string) string {
	for _, model := range p.config.Models {
		if model.Name == modelName {
			return model.APIKey
		}
	}
	return p.config.Models[0].APIKey
}

// getModelByProxyName returns the full ModelConfig for a proxy model name
func (p *Proxy) getModelByProxyName(modelName string) *config.ModelConfig {
	for _, model := range p.config.Models {
		if model.Name == modelName {
			return &model
		}
	}
	return nil
}

// translateModelNameInBody updates the model name in request body from proxy name to target model name
func (p *Proxy) translateModelNameInBody(reqBody map[string]interface{}) {
	modelConfig := p.getModelByProxyName(reqBody["model"].(string))
	if modelConfig != nil {
		reqBody["model"] = modelConfig.ModelName
	}
}

// logRequest logs a request/response pair
func (p *Proxy) logRequest(reqBody map[string]interface{}, modelName, endpoint string, start time.Time, statusCode int, respBody []byte, isStream bool, errorMsg string, tokensUsed map[string]int, requestLogger *logger.Logger) {
	sessionID := fmt.Sprintf("session_%s", modelName)
	if isStream {
		sessionID += "_stream"
	}

	reqLog := &storage.RequestLog{
		ID:          fmt.Sprintf("req_%d", time.Now().UnixNano()),
		Timestamp:   time.Now(),
		SessionID:   sessionID,
		Endpoint:    endpoint,
		Method:      "POST",
		Model:       modelName,
		Provider:    modelName,
		RequestBody: reqBody,
		StatusCode:  statusCode,
		Stream:      isStream,
		Duration:    time.Since(start).String(),
		Error:       errorMsg,
		TokensUsed:  tokensUsed,
	}

	if !isStream && respBody != nil {
		var respParsed map[string]interface{}
		json.Unmarshal(respBody, &respParsed)
		reqLog.ResponseBody = respParsed
	}

	if err := p.logger.SaveRequest(reqLog); err != nil {
		if requestLogger != nil {
			requestLogger.Error("Failed to save request log: %v", err)
		}
	}
}

// extractTokens extracts token usage from response
// Supports both OpenAI standard (prompt_tokens) and llama.cpp (input_tokens) field names
func (p *Proxy) extractTokens(resp map[string]interface{}) map[string]int {
	tokens := make(map[string]int)

	if usage, exists := resp["usage"]; exists {
		if u, ok := usage.(map[string]interface{}); ok {
			// Extract prompt_tokens (OpenAI/llama.cpp) or input_tokens (llama.cpp legacy)
			if val, exists := u["prompt_tokens"]; exists {
				if v, ok := val.(float64); ok {
					tokens["prompt_tokens"] = int(v)
				}
			}
			if val, exists := u["input_tokens"]; exists {
				if v, ok := val.(float64); ok {
					tokens["prompt_tokens"] = int(v)
				}
			}
			// Extract completion_tokens (OpenAI/llama.cpp) or output_tokens (llama.cpp legacy)
			if val, exists := u["completion_tokens"]; exists {
				if v, ok := val.(float64); ok {
					tokens["completion_tokens"] = int(v)
				}
			}
			if val, exists := u["output_tokens"]; exists {
				if v, ok := val.(float64); ok {
					tokens["completion_tokens"] = int(v)
				}
			}
			if val, exists := u["total_tokens"]; exists {
				if v, ok := val.(float64); ok {
					tokens["total_tokens"] = int(v)
				}
			}
		}
	}

	return tokens
}

// normalizeTokens converts response from llama.cpp format to Claude Code expected format
// 1. Translates model name from actual filename to proxy model name
// 2. Converts OpenAI token field names to llama.cpp field names (input_tokens/output_tokens)
// 3. Removes llama.cpp timings object (replaced by usage)
// Claude Code expects input_tokens/output_tokens when connecting to llama.cpp
func (p *Proxy) normalizeTokens(respBody []byte) []byte {
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return respBody
	}

	converted := false

	// Translate model name back to proxy model name
	if model, exists := resp["model"]; exists {
		if modelName, ok := model.(string); ok {
			proxyName := p.getProxyModelName(modelName)
			if proxyName != modelName {
				resp["model"] = proxyName
				converted = true
			}
		}
	}

	// Check if there's a usage object with OpenAI field names
	if usage, exists := resp["usage"]; exists {
		if u, ok := usage.(map[string]interface{}); ok {
			// Convert prompt_tokens -> input_tokens (Claude Code expects this)
			if _, hasPrompt := u["prompt_tokens"]; hasPrompt {
				u["input_tokens"] = u["prompt_tokens"]
				delete(u, "prompt_tokens")
				converted = true
			}
			// Convert completion_tokens -> output_tokens (Claude Code expects this)
			if _, hasCompletion := u["completion_tokens"]; hasCompletion {
				u["output_tokens"] = u["completion_tokens"]
				delete(u, "completion_tokens")
				converted = true
			}
		}
	}

	// Remove llama.cpp timings object (replaced by usage with converted field names)
	if _, exists := resp["timings"]; exists {
		delete(resp, "timings")
		converted = true
	}

	if converted {
		normalized, _ := json.Marshal(resp)
		return normalized
	}

	return respBody
}

// normalizeStreamChunk converts token field names in a streaming chunk
// This handles both chat.completion and chat.completion.chunk object types
// Also converts llama.cpp timings to usage format for Claude Code
func (p *Proxy) normalizeStreamChunk(chunkData []byte) []byte {
	var chunk map[string]interface{}
	if err := json.Unmarshal(chunkData, &chunk); err != nil {
		return chunkData
	}

	converted := false

	// Check if timings exist (will be converted to usage)
	if _, exists := chunk["timings"]; exists {
		converted = true
	}

	// Translate model name
	if model, exists := chunk["model"]; exists {
		if modelName, ok := model.(string); ok {
			proxyName := p.getProxyModelName(modelName)
			if proxyName != modelName {
				chunk["model"] = proxyName
				converted = true
			}
		}
	}

	// Check choices for usage data (last chunk in streaming)
	if choices, exists := chunk["choices"]; exists {
		if choicesArr, ok := choices.([]interface{}); ok {
			for _, choice := range choicesArr {
				if choiceMap, ok := choice.(map[string]interface{}); ok {
					if usage, exists := choiceMap["usage"]; exists {
						if u, ok := usage.(map[string]interface{}); ok {
							// Convert prompt_tokens -> input_tokens
							if _, hasPrompt := u["prompt_tokens"]; hasPrompt {
								u["input_tokens"] = u["prompt_tokens"]
								delete(u, "prompt_tokens")
								converted = true
							}
							// Convert completion_tokens -> output_tokens
							if _, hasCompletion := u["completion_tokens"]; hasCompletion {
								u["output_tokens"] = u["completion_tokens"]
								delete(u, "completion_tokens")
								converted = true
							}
						}
					}
				}
			}
		}
	}

	// Convert llama.cpp timings to usage format
	// llama.cpp uses timings.prompt_n and timings.predicted_n
	// Claude Code expects usage.input_tokens and usage.output_tokens
	if timings, exists := chunk["timings"]; exists {
		if t, ok := timings.(map[string]interface{}); ok {
			if promptN, exists := t["prompt_n"]; exists {
				if predictedN, exists := t["predicted_n"]; exists {
					// Create usage object if it doesn't exist
					if _, exists := chunk["usage"]; !exists {
						chunk["usage"] = make(map[string]interface{})
					}
					usage := chunk["usage"].(map[string]interface{})

					if pn, ok := promptN.(float64); ok {
						usage["input_tokens"] = int(pn)
						converted = true
					}
					if pn, ok := predictedN.(float64); ok {
						usage["output_tokens"] = int(pn)
						converted = true
					}
					// Remove timings after conversion
					delete(chunk, "timings")
				}
			}
		}
	}

	if converted {
		normalized, _ := json.Marshal(chunk)
		return normalized
	}

	return chunkData
}

// getProxyModelName returns the proxy model name for a llama.cpp model filename
// Uses case-insensitive comparison and strips .gguf extension
func (p *Proxy) getProxyModelName(modelName string) string {
	// Strip .gguf extension and convert to lowercase for comparison
	normalized := strings.ToLower(modelName)
	if strings.HasSuffix(normalized, ".gguf") {
		normalized = normalized[:len(normalized)-5]
	}

	for _, model := range p.config.Models {
		// Compare normalized model names
		configModel := strings.ToLower(model.ModelName)
		if strings.HasSuffix(configModel, ".gguf") {
			configModel = configModel[:len(configModel)-5]
		}
		if configModel == normalized {
			return model.Name
		}
	}
	return modelName
}

// handleModels returns the list of available models for Claude Code validation
func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appLogger := p.appLogger
	if appLogger != nil {
		appLogger.Debug("Models endpoint requested from: %s", r.RemoteAddr)
	}

	// Build model list from configuration
	models := make([]map[string]interface{}, 0, len(p.config.Models))
	for _, model := range p.config.Models {
		models = append(models, map[string]interface{}{
			"id":      model.Name,
			"object":  "model",
			"created": 1677649999,
			"owned_by": model.Name,
		})
	}

	response := map[string]interface{}{
		"object": "list",
		"data":   models,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// createAuthMiddleware creates authentication middleware
func (p *Proxy) createAuthMiddleware() func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get(p.config.Proxy.AuthHeader)
			if authHeader == "" || authHeader != p.config.Proxy.AuthToken {
				if p.appLogger != nil {
					p.appLogger.Warn("Authentication failed for: %s", r.RemoteAddr)
				}
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			if p.appLogger != nil {
				p.appLogger.Debug("Authentication successful for: %s", r.RemoteAddr)
			}
			next(w, r)
		}
	}
}

// createCORSMiddleware creates CORS middleware
func (p *Proxy) createCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// create404Logger wraps handler to log unregistered endpoints
func (p *Proxy) create404Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wrap response writer to capture status code without writing to original
		wrapped := &noWriteStatusCapturingWriter{ResponseWriter: w, statusCode: http.StatusOK, wrote: false}

		next.ServeHTTP(wrapped, r)

		// If 404, log the unregistered endpoint and send custom JSON response
		if wrapped.statusCode == http.StatusNotFound && !wrapped.wrote {
			requestID := fmt.Sprintf("unknown_%d", time.Now().UnixNano())
			if p.appLogger != nil {
				logger := p.appLogger.WithRequestID(requestID)
				logger.Warn("UNREGISTERED ENDPOINT: %s %s from %s",
					r.Method, r.URL.Path, r.RemoteAddr)
				logger.Debug("Request headers: %v", r.Header)
			}

			// Send JSON 404 response with helpful info
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": fmt.Sprintf("Endpoint not found: %s", r.URL.Path),
					"type":    "invalid_request_error",
					"code":    404,
				},
				"registered_endpoints": []string{
					"/v1/chat/completions",
					"/v1/completions",
					"/v1/embeddings",
					"/v1/models",
					"/v1/api/chat",
				},
			})
		}
	})
}

// noWriteStatusCapturingWriter captures status code without writing to original
type noWriteStatusCapturingWriter struct {
	http.ResponseWriter
	statusCode int
	wrote      bool
}

func (w *noWriteStatusCapturingWriter) WriteHeader(code int) {
	if code == http.StatusNotFound {
		w.statusCode = code
		w.wrote = true
		return // Don't write 404 to original - we'll handle it
	}
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *noWriteStatusCapturingWriter) Write(b []byte) (int, error) {
	if w.wrote {
		return len(b), nil // Suppress writing - we'll provide our own response
	}
	return w.ResponseWriter.Write(b)
}

func (w *noWriteStatusCapturingWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Start begins serving HTTP requests
func (p *Proxy) Start() error {
	p.appLogger.Info("Starting proxy server on %s", p.server.Addr)
	if p.config.Server.TLSCert != "" && p.config.Server.TLSKey != "" {
		p.appLogger.Info("TLS enabled: cert=%s, key=%s", p.config.Server.TLSCert, p.config.Server.TLSKey)
		return p.server.ListenAndServeTLS(p.config.Server.TLSCert, p.config.Server.TLSKey)
	}
	return p.server.ListenAndServe()
}

// Shutdown gracefully stops the server
func (p *Proxy) Shutdown(ctx context.Context) error {
	p.appLogger.Info("Shutting down proxy server...")
	return p.server.Shutdown(ctx)
}

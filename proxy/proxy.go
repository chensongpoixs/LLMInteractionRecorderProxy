package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"proxy-llm/config"
	"proxy-llm/logger"
	"proxy-llm/storage"
)

// idSeq is combined with time.Now().UnixNano() so req/up IDs stay unique when the clock
// returns the same value twice (e.g. two concurrent POSTs on Windows) — that looked like
// "one [REQ:...] but two upstream calls" in logs.
var idSeq atomic.Uint64

func nextUniqueID() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), idSeq.Add(1))
}

// Proxy handles forwarding requests to LLM APIs
type Proxy struct {
	server    *http.Server
	clients   map[string]*http.Client
	config    *config.Config
	logger    *storage.Logger
	handler   *Handler
	metrics   *Metrics
	authMw    func(http.HandlerFunc) http.HandlerFunc
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
	mux.HandleFunc("/v1/messages", proxy.handleAnthropicMessages())
	mux.HandleFunc("/v1/completions", proxy.handleRequest("completions"))
	mux.HandleFunc("/v1/embeddings", proxy.handleRequest("embeddings"))
	// llama.cpp llama-server compatible endpoint
	mux.HandleFunc("/v1/api/chat", proxy.handleRequest("chat/completions"))
	// Models endpoint - returns available models for Claude Code validation
	mux.HandleFunc("/v1/models", proxy.handleModels)
	// Usage dashboard endpoints
	mux.HandleFunc("/usage", proxy.handleUsageDashboard)
	mux.HandleFunc("/api/usage/summary", proxy.handler.HandleUsageSummary)
	mux.HandleFunc("/api/usage/stream", proxy.handler.HandleUsageStream)

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
	return "req_" + nextUniqueID()
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
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPClientToProxy(requestLogger, "("+endpoint+")", r, body)
		}

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

		// Translate model name from proxy name to actual model name
		if p.translateModelNameInBody(reqBody) {
			// Re-marshal body with translated model name
			translatedBody, marshalErr := json.Marshal(reqBody)
			if marshalErr != nil {
				requestLogger.Error("Failed to re-marshal body after model translation: %v", marshalErr)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			body = translatedBody
			requestLogger.Debug("Translated model name in request body")
		}

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
		if err := r.Context().Err(); err != nil {
			requestLogger.Warn("Client disconnected before upstream call: %v", err)
			return
		}
		requestLogger.Debug("Looking up HTTP client for model: %s", modelName)
		client := p.getHTTPClient(modelName)
		if _, exists := p.clients[modelName]; exists {
			requestLogger.Debug("Using client for model: %s", modelName)
		} else {
			requestLogger.Debug("Falling back to first configured model client")
		}

		// Handle streaming vs non-streaming
		if isStream && p.config.Proxy.EnableStream {
			uCall := "up_" + nextUniqueID()
			uStart := time.Now()
			retryCount := r.Header.Get("X-Stainless-Retry-Count")
			requestLogger.Info("=== Starting streaming mode ===")
			requestLogger.Info(
				"Upstream call start: call_id=%s endpoint=%s target=%s stream=%v remote=%s retry_count=%s",
				uCall, endpoint, targetURL, isStream, r.RemoteAddr, retryCount,
			)
			if p.shouldLogUpstreamHTTP() {
				p.logHTTPOutgoingUpstream(requestLogger, uCall, proxyReq, body)
			}
			p.handleStreaming(w, r, proxyReq, client, reqBody, modelName, start, endpoint, requestLogger, uCall, uStart, body)
			return
		}

		// Non-streaming response
		upstreamCallID := "up_" + nextUniqueID()
		upstreamStart := time.Now()
		retryCount := r.Header.Get("X-Stainless-Retry-Count")
		requestLogger.Info(
			"Upstream call start: call_id=%s endpoint=%s target=%s stream=%v retry_count=%s",
			upstreamCallID, endpoint, targetURL, isStream, retryCount,
		)
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPOutgoingUpstream(requestLogger, upstreamCallID, proxyReq, body)
		}
		requestLogger.Info("=== Sending request to LLM API ===")
		resp, err := client.Do(proxyReq)
		if err != nil {
			if r.Context().Err() != nil {
				requestLogger.Warn(
					"Upstream call canceled: call_id=%s reason=client_disconnected elapsed=%v err=%v",
					upstreamCallID, time.Since(upstreamStart), r.Context().Err(),
				)
				return
			}
			requestLogger.Error(
				"Upstream call failed: call_id=%s elapsed=%v err=%v",
				upstreamCallID, time.Since(upstreamStart), err,
			)
			requestLogger.Error("Proxy request failed: %v", err)
			p.logRequest(reqBody, modelName, endpoint, start, 500, nil, true, err.Error(), nil, requestLogger)
			http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
			return
		}
		requestLogger.Info(
			"Upstream call success: call_id=%s status=%d elapsed=%v",
			upstreamCallID, resp.StatusCode, time.Since(upstreamStart),
		)
		requestLogger.Info("LLM API responded: status=%d", resp.StatusCode)
		defer resp.Body.Close()

		// Read response body
		if err := r.Context().Err(); err != nil {
			requestLogger.Warn("Client disconnected before reading upstream body: %v", err)
			return
		}
		requestLogger.Debug("Reading LLM API response body...")
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			if r.Context().Err() != nil {
				requestLogger.Warn("Client disconnected during upstream body read: %v", r.Context().Err())
				return
			}
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
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPUpstreamResponseFull(requestLogger, upstreamCallID, resp, respBody)
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

// handleAnthropicMessages adapts Anthropic Messages API requests to OpenAI chat/completions
func (p *Proxy) handleAnthropicMessages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := p.generateRequestID()
		requestLogger := p.appLogger.WithRequestID(reqID)
		start := time.Now()

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		r.Body.Close()
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPClientToProxy(requestLogger, "(/v1/messages)", r, body)
		}

		var anthropicReq map[string]interface{}
		if err := json.Unmarshal(body, &anthropicReq); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		modelName := "default"
		if model, ok := anthropicReq["model"].(string); ok && model != "" {
			modelName = model
		}

		// If the model has a dedicated Anthropic base URL, forward the raw request
		// directly without converting to OpenAI format.
		if anthropicBase := p.getModelBaseURLAnthropic(modelName); anthropicBase != "" {
			isStream := false
			if stream, ok := anthropicReq["stream"].(bool); ok {
				isStream = stream
			}
			if isStream && p.config.Proxy.EnableStream {
				p.handleAnthropicPassthroughStream(w, r, body, modelName, anthropicBase, start, requestLogger)
				return
			}
			p.handleAnthropicPassthrough(w, r, body, modelName, anthropicBase, start, requestLogger)
			return
		}

		// Fallback: convert Anthropic messages payload into OpenAI chat/completions payload.
		chatReq := map[string]interface{}{
			"model":  modelName,
			"stream": false,
		}

		if maxTokens, exists := anthropicReq["max_tokens"]; exists {
			chatReq["max_tokens"] = maxTokens
		}
		if temperature, exists := anthropicReq["temperature"]; exists {
			chatReq["temperature"] = temperature
		}
		if topP, exists := anthropicReq["top_p"]; exists {
			chatReq["top_p"] = topP
		}
		if stream, exists := anthropicReq["stream"]; exists {
			if s, ok := stream.(bool); ok {
				chatReq["stream"] = s
			}
		}

		chatReq["messages"] = convertAnthropicMessagesToOpenAI(anthropicReq)
		applyAnthropicToolsToOpenAI(anthropicReq, chatReq)
		applyAnthropicReasoningToOpenAI(anthropicReq, chatReq)

		translatedModelName := modelName
		if cfg := p.getModelByProxyName(modelName); cfg != nil {
			translatedModelName = cfg.ModelName
		}
		if translatedModelName != modelName {
			chatReq["model"] = translatedModelName
		}

		if stream, _ := chatReq["stream"].(bool); stream && p.config.Proxy.EnableStream {
			p.handleAnthropicMessagesStream(w, r, chatReq, modelName, start, requestLogger)
			return
		}

		reqBytes, _ := json.Marshal(chatReq)
		targetURL := fmt.Sprintf("%s/%s", p.getModelBaseURL(modelName), "chat/completions")
		proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(reqBytes))
		if err != nil {
			http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
			return
		}
		proxyReq.Header.Set("Authorization", "Bearer "+p.getAPIKey(modelName))
		proxyReq.Header.Set("Content-Type", "application/json")

		client := p.getHTTPClient(modelName)

		upstreamCallID := "up_" + nextUniqueID()
		upstreamStart := time.Now()
		retryCount := r.Header.Get("X-Stainless-Retry-Count")
		requestLogger.Info(
			"Upstream call start: call_id=%s endpoint=messages target=%s stream=false remote=%s retry_count=%s",
			upstreamCallID, targetURL, r.RemoteAddr, retryCount,
		)
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPOutgoingUpstream(requestLogger, upstreamCallID, proxyReq, reqBytes)
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			if r.Context().Err() != nil {
				requestLogger.Warn(
					"Upstream call canceled: call_id=%s endpoint=messages reason=client_disconnected elapsed=%v err=%v",
					upstreamCallID, time.Since(upstreamStart), r.Context().Err(),
				)
				return
			}
			requestLogger.Error(
				"Upstream call failed: call_id=%s endpoint=messages elapsed=%v err=%v",
				upstreamCallID, time.Since(upstreamStart), err,
			)
			p.logRequest(chatReq, modelName, "messages", start, 500, nil, false, err.Error(), nil, requestLogger)
			http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
			return
		}
		requestLogger.Info(
			"Upstream call success: call_id=%s endpoint=messages status=%d elapsed=%v",
			upstreamCallID, resp.StatusCode, time.Since(upstreamStart),
		)
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			if r.Context().Err() != nil {
				requestLogger.Warn("Client disconnected during /v1/messages upstream body read: %v", r.Context().Err())
				return
			}
			p.logRequest(chatReq, modelName, "messages", start, resp.StatusCode, nil, false, err.Error(), nil, requestLogger)
			http.Error(w, "Failed to read response", http.StatusBadGateway)
			return
		}
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPUpstreamResponseFull(requestLogger, upstreamCallID, resp, respBody)
		}

		normalizedBody := p.normalizeTokens(respBody)
		var openAIResp map[string]interface{}
		if err := json.Unmarshal(normalizedBody, &openAIResp); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(normalizedBody)
			return
		}

		tokensUsed := p.extractTokens(openAIResp)
		p.logRequest(chatReq, modelName, "messages", start, resp.StatusCode, normalizedBody, false, "", tokensUsed, requestLogger)

		textContent := ""
		thinkingContent := ""
		stopReason := "end_turn"
		toolUseBlocks := make([]map[string]interface{}, 0, 4)
		if choicesRaw, exists := openAIResp["choices"]; exists {
			if choices, ok := choicesRaw.([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if finishReason, ok := choice["finish_reason"].(string); ok {
						switch finishReason {
						case "length":
							stopReason = "max_tokens"
						case "stop":
							stopReason = "end_turn"
						default:
							stopReason = "end_turn"
						}
					}
					if message, ok := choice["message"].(map[string]interface{}); ok {
						if content, ok := message["content"].(string); ok {
							textContent = content
						}
						if rc, ok := message["reasoning_content"].(string); ok && strings.TrimSpace(rc) != "" {
							thinkingContent = rc
						} else if rc, ok := message["reasoning"].(string); ok && strings.TrimSpace(rc) != "" {
							thinkingContent = rc
						}
						toolUseBlocks = extractToolUseBlocksFromOpenAIMessage(message)
					}
				}
			}
		}
		if len(toolUseBlocks) > 0 && stopReason != "max_tokens" {
			stopReason = "tool_use"
		}

		usage := map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": 0,
		}
		if usageRaw, exists := openAIResp["usage"]; exists {
			if usageMap, ok := usageRaw.(map[string]interface{}); ok {
				if inVal, exists := usageMap["input_tokens"]; exists {
					usage["input_tokens"] = inVal
				}
				if outVal, exists := usageMap["output_tokens"]; exists {
					usage["output_tokens"] = outVal
				}
			}
		}

		contentBlocks := make([]map[string]interface{}, 0, 2+len(toolUseBlocks))
		if strings.TrimSpace(thinkingContent) != "" {
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type":     "thinking",
				"thinking": thinkingContent,
			})
		}
		if strings.TrimSpace(textContent) != "" {
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type": "text",
				"text": textContent,
			})
		}
		contentBlocks = append(contentBlocks, toolUseBlocks...)
		if len(contentBlocks) == 0 {
			contentBlocks = append(contentBlocks, map[string]interface{}{"type": "text", "text": ""})
		}

		anthropicResp := map[string]interface{}{
			"id":            "msg_" + nextUniqueID(),
			"type":          "message",
			"role":          "assistant",
			"model":         modelName,
			"content":       contentBlocks,
			"stop_reason":   stopReason,
			"stop_sequence": nil,
			"usage":         usage,
		}

		out, _ := json.Marshal(anthropicResp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(out)
	}
}

// handleAnthropicMessagesStream adapts OpenAI-style SSE to Anthropic Messages SSE events.
func (p *Proxy) handleAnthropicMessagesStream(w http.ResponseWriter, r *http.Request, chatReq map[string]interface{}, modelName string, start time.Time, requestLogger *logger.Logger) {
	reqBytes, _ := json.Marshal(chatReq)
	targetURL := fmt.Sprintf("%s/%s", p.getModelBaseURL(modelName), "chat/completions")
	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(reqBytes))
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Authorization", "Bearer "+p.getAPIKey(modelName))
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Accept", "text/event-stream")

	client := p.getHTTPClient(modelName)

	upstreamCallID := "up_" + nextUniqueID()
	upstreamStart := time.Now()
	retryCount := r.Header.Get("X-Stainless-Retry-Count")
	requestLogger.Info(
		"Upstream call start: call_id=%s endpoint=messages target=%s stream=true remote=%s retry_count=%s",
		upstreamCallID, targetURL, r.RemoteAddr, retryCount,
	)
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPOutgoingUpstream(requestLogger, upstreamCallID, proxyReq, reqBytes)
	}
	resp, err := client.Do(proxyReq)
	if err != nil {
		if r.Context().Err() != nil {
			requestLogger.Warn(
				"Upstream call canceled: call_id=%s endpoint=messages reason=client_disconnected elapsed=%v err=%v",
				upstreamCallID, time.Since(upstreamStart), r.Context().Err(),
			)
			return
		}
		requestLogger.Error(
			"Upstream call failed: call_id=%s endpoint=messages elapsed=%v err=%v",
			upstreamCallID, time.Since(upstreamStart), err,
		)
		p.logRequest(chatReq, modelName, "messages", start, 500, nil, true, err.Error(), nil, requestLogger)
		http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	requestLogger.Info(
		"Upstream call success: call_id=%s endpoint=messages status=%d elapsed=%v",
		upstreamCallID, resp.StatusCode, time.Since(upstreamStart),
	)
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPUpstreamResponseStreamMeta(requestLogger, upstreamCallID, resp)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPUpstreamResponseFull(requestLogger, upstreamCallID, resp, respBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	messageID := "msg_" + nextUniqueID()
	stopReason := "end_turn"
	inputTokens := 0
	outputTokens := 0
	aggregatedText := strings.Builder{}
	streamLines := 0
	streamChunks := 0
	streamParseErrors := 0
	textDeltaCount := 0
	thinkingDeltaCount := 0
	aggregatedThinking := strings.Builder{}
	toolCallDeltaCount := 0
	inputJSONDeltaCount := 0
	streamEndReason := "completed"
	streamErr := ""
	textBlockStarted := false
	type streamedToolCall struct {
		ID        string
		Name      string
		Arguments strings.Builder
	}
	toolCalls := map[int]*streamedToolCall{}
	toolCallOrder := make([]int, 0, 4)
	defer func() {
		requestLogger.Info(
			"Messages stream summary: call_id=%s status=%d end_reason=%s stop_reason=%s elapsed=%v lines=%d chunks=%d parse_errors=%d text_deltas=%d thinking_deltas=%d tool_call_deltas=%d input_json_deltas=%d tool_uses=%d text_len=%d prompt_tokens=%d completion_tokens=%d total_tokens=%d err=%s",
			upstreamCallID,
			resp.StatusCode,
			streamEndReason,
			stopReason,
			time.Since(start),
			streamLines,
			streamChunks,
			streamParseErrors,
			textDeltaCount,
			thinkingDeltaCount,
			toolCallDeltaCount,
			inputJSONDeltaCount,
			len(toolCallOrder),
			aggregatedText.Len(),
			inputTokens,
			outputTokens,
			inputTokens+outputTokens,
			streamErr,
		)
	}()

	writeEvent := func(eventName string, payload map[string]interface{}) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\n", eventName)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()
	}

	writeEvent("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         modelName,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  inputTokens,
				"output_tokens": outputTokens,
			},
		},
	})

	scanner := bufio.NewScanner(resp.Body)
	// Increase scanner buffer for larger SSE lines.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	for scanner.Scan() {
		streamLines++
		select {
		case <-r.Context().Done():
			requestLogger.Warn("Client disconnected during /v1/messages SSE; stop streaming: %v", r.Context().Err())
			streamEndReason = "client_disconnected"
			streamErr = r.Context().Err().Error()
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		dataPart := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataPart == "[DONE]" {
			streamEndReason = "upstream_done"
			break
		}

		chunkBytes := p.normalizeStreamChunk([]byte(dataPart))
		var chunk map[string]interface{}
		if err := json.Unmarshal(chunkBytes, &chunk); err != nil {
			streamParseErrors++
			continue
		}
		streamChunks++

		if id, ok := chunk["id"].(string); ok && id != "" {
			messageID = id
		}

		if usageRaw, exists := chunk["usage"]; exists {
			if usageMap, ok := usageRaw.(map[string]interface{}); ok {
				if v, ok := asInt(usageMap["input_tokens"]); ok {
					inputTokens = v
				}
				if v, ok := asInt(usageMap["output_tokens"]); ok {
					outputTokens = v
				}
			}
		}

		if choicesRaw, exists := chunk["choices"]; exists {
			if choices, ok := choicesRaw.([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
						switch finish {
						case "length":
							stopReason = "max_tokens"
						case "stop":
							stopReason = "end_turn"
						default:
							stopReason = "end_turn"
						}
					}

					if deltaRaw, exists := choice["delta"]; exists {
						if delta, ok := deltaRaw.(map[string]interface{}); ok {
							if text, ok := delta["content"].(string); ok && text != "" {
								if !textBlockStarted {
									writeEvent("content_block_start", map[string]interface{}{
										"type":  "content_block_start",
										"index": 0,
										"content_block": map[string]interface{}{
											"type": "text",
											"text": "",
										},
									})
									textBlockStarted = true
								}
								aggregatedText.WriteString(text)
								textDeltaCount++
								writeEvent("content_block_delta", map[string]interface{}{
									"type":  "content_block_delta",
									"index": 0,
									"delta": map[string]interface{}{
										"type": "text_delta",
										"text": text,
									},
								})
							}
							if rc, ok := delta["reasoning_content"].(string); ok && strings.TrimSpace(rc) != "" {
								aggregatedThinking.WriteString(rc)
								thinkingDeltaCount++
							} else if rc, ok := delta["reasoning"].(string); ok && strings.TrimSpace(rc) != "" {
								aggregatedThinking.WriteString(rc)
								thinkingDeltaCount++
							}
							if tcRaw, hasTC := delta["tool_calls"]; hasTC {
								toolCallDeltaCount++
								if tcArr, ok := tcRaw.([]interface{}); ok {
									for i, t := range tcArr {
										tcMap, ok := t.(map[string]interface{})
										if !ok {
											continue
										}
										idx := i
										if idxRaw, ok := tcMap["index"]; ok {
											if iv, ok := asInt(idxRaw); ok {
												idx = iv
											}
										}
										entry, exists := toolCalls[idx]
										if !exists {
											entry = &streamedToolCall{}
											toolCalls[idx] = entry
											toolCallOrder = append(toolCallOrder, idx)
										}
										if id, ok := tcMap["id"].(string); ok && id != "" {
											entry.ID = id
										}
										if fnRaw, ok := tcMap["function"]; ok {
											if fn, ok := fnRaw.(map[string]interface{}); ok {
												if name, ok := fn["name"].(string); ok && name != "" {
													entry.Name = name
												}
												if args, ok := fn["arguments"].(string); ok && args != "" {
													entry.Arguments.WriteString(args)
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		streamEndReason = "scanner_error"
		streamErr = err.Error()
		p.logRequest(chatReq, modelName, "messages", start, 502, nil, true, err.Error(), nil, requestLogger)
		return
	}

	if textBlockStarted {
		writeEvent("content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": 0,
		})
	}
	thinkingContent := strings.TrimSpace(aggregatedThinking.String())
	if thinkingContent != "" {
		thinkingIndex := 0
		if textBlockStarted {
			thinkingIndex = 1
		}
		writeEvent("content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": thinkingIndex,
			"content_block": map[string]interface{}{
				"type":     "thinking",
				"thinking": "",
			},
		})
		writeEvent("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": thinkingIndex,
			"delta": map[string]interface{}{
				"type":     "thinking_delta",
				"thinking": thinkingContent,
			},
		})
		writeEvent("content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": thinkingIndex,
		})
	}
	if len(toolCallOrder) > 0 && stopReason != "max_tokens" {
		stopReason = "tool_use"
		sort.Ints(toolCallOrder)
		baseIndex := 0
		if textBlockStarted {
			baseIndex++
		}
		if thinkingContent != "" {
			baseIndex++
		}
		for i, idx := range toolCallOrder {
			tc := toolCalls[idx]
			if tc == nil {
				continue
			}
			blockIndex := baseIndex + i
			toolID := tc.ID
			if toolID == "" {
				toolID = "toolu_" + nextUniqueID()
			}
			toolName := tc.Name
			if toolName == "" {
				toolName = "unknown_tool"
			}
			writeEvent("content_block_start", map[string]interface{}{
				"type":  "content_block_start",
				"index": blockIndex,
				"content_block": map[string]interface{}{
					"type":  "tool_use",
					"id":    toolID,
					"name":  toolName,
					"input": map[string]interface{}{},
				},
			})
			argStr := strings.TrimSpace(tc.Arguments.String())
			if argStr != "" {
				inputJSONDeltaCount++
				writeEvent("content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": blockIndex,
					"delta": map[string]interface{}{
						"type":         "input_json_delta",
						"partial_json": argStr,
					},
				})
			}
			writeEvent("content_block_stop", map[string]interface{}{
				"type":  "content_block_stop",
				"index": blockIndex,
			})
		}
	}

	writeEvent("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": outputTokens,
		},
	})

	writeEvent("message_stop", map[string]interface{}{
		"type": "message_stop",
	})

	usage := map[string]int{
		"prompt_tokens":     inputTokens,
		"completion_tokens": outputTokens,
		"total_tokens":      inputTokens + outputTokens,
	}

	respForLog := map[string]interface{}{
		"id":          messageID,
		"type":        "message",
		"role":        "assistant",
		"model":       modelName,
		"text":        aggregatedText.String(),
		"stop_reason": stopReason,
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
	respBytes, _ := json.Marshal(respForLog)
	p.logRequest(chatReq, modelName, "messages", start, resp.StatusCode, respBytes, true, "", usage, requestLogger)
}

// handleAnthropicPassthrough forwards a raw Anthropic request directly to an
// Anthropic-compatible upstream endpoint without any protocol conversion.
func (p *Proxy) handleAnthropicPassthrough(w http.ResponseWriter, r *http.Request, rawBody []byte, modelName, anthropicBase string, start time.Time, requestLogger *logger.Logger) {
	targetURL := anthropicBase + "/messages"
	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(rawBody))
	if err != nil {
		requestLogger.Error("Failed to create Anthropic passthrough request: %v", err)
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Authorization", "Bearer "+p.getAPIKey(modelName))
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("x-api-key", p.getAPIKey(modelName))

	upstreamCallID := "up_" + nextUniqueID()
	upstreamStart := time.Now()
	retryCount := r.Header.Get("X-Stainless-Retry-Count")
	requestLogger.Info(
		"Upstream call start (anthropic passthrough): call_id=%s target=%s remote=%s retry_count=%s",
		upstreamCallID, targetURL, r.RemoteAddr, retryCount,
	)
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPOutgoingUpstream(requestLogger, upstreamCallID, proxyReq, rawBody)
	}

	client := p.getHTTPClient(modelName)
	resp, err := client.Do(proxyReq)
	if err != nil {
		if r.Context().Err() != nil {
			requestLogger.Warn(
				"Upstream call canceled (anthropic passthrough): call_id=%s reason=client_disconnected elapsed=%v err=%v",
				upstreamCallID, time.Since(upstreamStart), r.Context().Err(),
			)
			return
		}
		requestLogger.Error(
			"Upstream call failed (anthropic passthrough): call_id=%s elapsed=%v err=%v",
			upstreamCallID, time.Since(upstreamStart), err,
		)
		p.logRequest(nil, modelName, "messages", start, 500, nil, false, err.Error(), nil, requestLogger)
		http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	requestLogger.Info(
		"Upstream call success (anthropic passthrough): call_id=%s status=%d elapsed=%v",
		upstreamCallID, resp.StatusCode, time.Since(upstreamStart),
	)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if r.Context().Err() != nil {
			requestLogger.Warn("Client disconnected during anthropic passthrough body read: %v", r.Context().Err())
			return
		}
		requestLogger.Error("Failed to read anthropic passthrough response: %v", err)
		p.logRequest(nil, modelName, "messages", start, resp.StatusCode, nil, false, err.Error(), nil, requestLogger)
		http.Error(w, "Failed to read response", http.StatusBadGateway)
		return
	}
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPUpstreamResponseFull(requestLogger, upstreamCallID, resp, respBody)
	}

	var respParsed map[string]interface{}
	json.Unmarshal(respBody, &respParsed)
	tokensUsed := p.extractTokens(respParsed)
	p.logRequest(nil, modelName, "messages", start, resp.StatusCode, respBody, false, "", tokensUsed, requestLogger)

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// handleAnthropicPassthroughStream forwards a raw streaming Anthropic request
// directly to an Anthropic-compatible upstream endpoint. SSE events are relayed
// as-is without any format conversion.
func (p *Proxy) handleAnthropicPassthroughStream(w http.ResponseWriter, r *http.Request, rawBody []byte, modelName, anthropicBase string, start time.Time, requestLogger *logger.Logger) {
	targetURL := anthropicBase + "/messages"
	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(rawBody))
	if err != nil {
		requestLogger.Error("Failed to create Anthropic passthrough stream request: %v", err)
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Authorization", "Bearer "+p.getAPIKey(modelName))
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Accept", "text/event-stream")
	proxyReq.Header.Set("x-api-key", p.getAPIKey(modelName))

	upstreamCallID := "up_" + nextUniqueID()
	upstreamStart := time.Now()
	retryCount := r.Header.Get("X-Stainless-Retry-Count")
	requestLogger.Info(
		"Upstream call start (anthropic passthrough stream): call_id=%s target=%s remote=%s retry_count=%s",
		upstreamCallID, targetURL, r.RemoteAddr, retryCount,
	)
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPOutgoingUpstream(requestLogger, upstreamCallID, proxyReq, rawBody)
	}

	client := p.getHTTPClient(modelName)
	resp, err := client.Do(proxyReq)
	if err != nil {
		if r.Context().Err() != nil {
			requestLogger.Warn(
				"Upstream call canceled (anthropic passthrough stream): call_id=%s reason=client_disconnected elapsed=%v err=%v",
				upstreamCallID, time.Since(upstreamStart), r.Context().Err(),
			)
			return
		}
		requestLogger.Error(
			"Upstream call failed (anthropic passthrough stream): call_id=%s elapsed=%v err=%v",
			upstreamCallID, time.Since(upstreamStart), err,
		)
		p.logRequest(nil, modelName, "messages", start, 500, nil, true, err.Error(), nil, requestLogger)
		http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	requestLogger.Info(
		"Upstream call success (anthropic passthrough stream): call_id=%s status=%d elapsed=%v",
		upstreamCallID, resp.StatusCode, time.Since(upstreamStart),
	)
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPUpstreamResponseStreamMeta(requestLogger, upstreamCallID, resp)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		if p.shouldLogUpstreamHTTP() {
			p.logHTTPUpstreamResponseFull(requestLogger, upstreamCallID, resp, respBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	// Relay SSE stream as-is.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	streamBytes := 0
	chunkCount := 0
	buf := make([]byte, 4096)
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
			chunkCount++
			streamBytes += n
		}
		if readErr != nil {
			if readErr == io.EOF {
				requestLogger.Info("Anthropic passthrough stream completed: call_id=%s chunks=%d bytes=%d",
					upstreamCallID, chunkCount, streamBytes)
			} else {
				requestLogger.Error("Anthropic passthrough stream error: call_id=%s err=%v chunks=%d bytes=%d",
					upstreamCallID, readErr, chunkCount, streamBytes)
			}
			break
		}
	}

	var anthropicReq map[string]interface{}
	json.Unmarshal(rawBody, &anthropicReq)
	p.logRequest(anthropicReq, modelName, "messages", start, resp.StatusCode, nil, true, "", nil, requestLogger)
}

// handleStreaming manages streaming responses
func (p *Proxy) handleStreaming(w http.ResponseWriter, r *http.Request, proxyReq *http.Request, client *http.Client, reqBody map[string]interface{}, modelName string, start time.Time, endpoint string, requestLogger *logger.Logger, upstreamCallID string, upstreamStart time.Time, _ []byte) {
	requestLogger.Info("=== Streaming mode initiated ===")

	resp, err := client.Do(proxyReq)
	if err != nil {
		if r.Context().Err() != nil {
			requestLogger.Warn(
				"Upstream call canceled: call_id=%s endpoint=%s reason=client_disconnected elapsed=%v err=%v",
				upstreamCallID, endpoint, time.Since(upstreamStart), r.Context().Err(),
			)
		} else {
			requestLogger.Error("Streaming request failed: %v", err)
		}
		p.logRequest(reqBody, modelName, endpoint, start, 500, nil, true, err.Error(), nil, requestLogger)
		http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	requestLogger.Info(
		"Upstream call success: call_id=%s endpoint=%s status=%d elapsed=%v",
		upstreamCallID, endpoint, resp.StatusCode, time.Since(upstreamStart),
	)
	if p.shouldLogUpstreamHTTP() {
		p.logHTTPUpstreamResponseStreamMeta(requestLogger, upstreamCallID, resp)
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
		sessionID = "session_" + nextUniqueID()
	}
	requestLogger.Info("Session ID: %s", sessionID)

	reqLog := &storage.RequestLog{
		ID:          "req_" + nextUniqueID(),
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
		select {
		case <-r.Context().Done():
			requestLogger.Warn("Client disconnected during streaming; stop reading upstream: %v", r.Context().Err())
			reqLog.Error = "client disconnected"
			reqLog.StatusCode = 499
			reqLog.Duration = time.Since(start).String()
			p.logger.SaveRequest(reqLog)
			return
		default:
		}

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

// getModelBaseURLAnthropic returns the Anthropic-compatible base URL for a model.
// Returns empty string if not configured (caller should fall back to OpenAI conversion path).
func (p *Proxy) getModelBaseURLAnthropic(modelName string) string {
	for _, model := range p.config.Models {
		if model.Name == modelName {
			return model.BaseURLAnthropic
		}
	}
	return p.config.Models[0].BaseURLAnthropic
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

// translateModelNameInBody updates the model name in request body from proxy name to target model name.
// Returns false if the model field is missing or not a string.
func (p *Proxy) translateModelNameInBody(reqBody map[string]interface{}) bool {
	modelRaw, exists := reqBody["model"]
	if !exists {
		return false
	}
	modelName, ok := modelRaw.(string)
	if !ok {
		return false
	}
	modelConfig := p.getModelByProxyName(modelName)
	if modelConfig == nil {
		return false
	}
	reqBody["model"] = modelConfig.ModelName
	return true
}

// logRequest logs a request/response pair
func (p *Proxy) logRequest(reqBody map[string]interface{}, modelName, endpoint string, start time.Time, statusCode int, respBody []byte, isStream bool, errorMsg string, tokensUsed map[string]int, requestLogger *logger.Logger) {
	sessionID := fmt.Sprintf("session_%s", modelName)
	if isStream {
		sessionID += "_stream"
	}

	reqLog := &storage.RequestLog{
		ID:          "req_" + nextUniqueID(),
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
			"id":       model.Name,
			"object":   "model",
			"created":  1677649999,
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

// createCORSMiddleware creates CORS middleware using configured AllowedOrigins.
func (p *Proxy) createCORSMiddleware(next http.Handler) http.Handler {
	allowedOrigins := p.config.Proxy.AllowedOrigins
	// Fallback: if no origins configured, allow all.
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"*"}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowOrigin := ""
		for _, ao := range allowedOrigins {
			if ao == "*" {
				allowOrigin = "*"
				break
			}
			if ao == origin {
				allowOrigin = origin
				break
			}
		}
		if allowOrigin == "" && len(allowedOrigins) > 0 {
			// No matching origin; still set the first one to avoid breaking simple clients
			// but this effectively denies cross-origin requests from unmatched origins.
			allowOrigin = allowedOrigins[0]
		}

		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Session-ID, X-Stainless-Retry-Count")

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
		wrapped := &noWriteStatusCapturingWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		// If 404, log the unregistered endpoint and send custom JSON response
		if wrapped.statusCode == http.StatusNotFound {
			requestID := "unknown_" + nextUniqueID()
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
					"/v1/messages",
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
}

func (w *noWriteStatusCapturingWriter) WriteHeader(code int) {
	if code == http.StatusNotFound {
		w.statusCode = code
		return // Don't write 404 to original - we'll handle it
	}
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *noWriteStatusCapturingWriter) Write(b []byte) (int, error) {
	if w.statusCode == http.StatusNotFound {
		return len(b), nil // Suppress writing - we'll provide our own response
	}
	return w.ResponseWriter.Write(b)
}

func convertAnthropicMessagesToOpenAI(req map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, 16)
	if system, exists := req["system"]; exists {
		systemText := extractAnthropicTextContent(system)
		if strings.TrimSpace(systemText) != "" {
			out = append(out, map[string]interface{}{
				"role":    "system",
				"content": systemText,
			})
		}
	}

	messagesRaw, exists := req["messages"]
	if !exists {
		return out
	}
	messages, ok := messagesRaw.([]interface{})
	if !ok {
		return out
	}
	for _, m := range messages {
		msgMap, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := msgMap["role"].(string)
		if strings.TrimSpace(role) == "" {
			role = "user"
		}
		content := msgMap["content"]

		switch role {
		case "assistant":
			textContent := extractAnthropicTextContent(content)
			thinkingContent := extractAnthropicThinkingContent(content)
			toolCalls := extractOpenAIToolCallsFromAnthropicContent(content)
			msg := map[string]interface{}{
				"role":    "assistant",
				"content": textContent,
			}
			if strings.TrimSpace(thinkingContent) != "" {
				msg["reasoning_content"] = thinkingContent
			}
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
			out = append(out, msg)
		case "user":
			textContent, toolResults := extractUserTextAndToolResults(content)
			if strings.TrimSpace(textContent) != "" {
				out = append(out, map[string]interface{}{
					"role":    "user",
					"content": textContent,
				})
			}
			out = append(out, toolResults...)
		default:
			out = append(out, map[string]interface{}{
				"role":    role,
				"content": extractAnthropicTextContent(content),
			})
		}
	}
	return out
}

func applyAnthropicToolsToOpenAI(req map[string]interface{}, chatReq map[string]interface{}) {
	toolsRaw, exists := req["tools"]
	if exists {
		if arr, ok := toolsRaw.([]interface{}); ok {
			tools := make([]map[string]interface{}, 0, len(arr))
			for _, item := range arr {
				toolMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := toolMap["name"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				fn := map[string]interface{}{
					"name": name,
				}
				if desc, ok := toolMap["description"].(string); ok && strings.TrimSpace(desc) != "" {
					fn["description"] = desc
				}
				if schema, exists := toolMap["input_schema"]; exists {
					fn["parameters"] = schema
				}
				tools = append(tools, map[string]interface{}{
					"type":     "function",
					"function": fn,
				})
			}
			if len(tools) > 0 {
				chatReq["tools"] = tools
			}
		}
	}

	if tcRaw, exists := req["tool_choice"]; exists {
		switch tc := tcRaw.(type) {
		case string:
			chatReq["tool_choice"] = tc
		case map[string]interface{}:
			t, _ := tc["type"].(string)
			switch t {
			case "auto":
				chatReq["tool_choice"] = "auto"
			case "any":
				chatReq["tool_choice"] = "required"
			case "tool":
				name, _ := tc["name"].(string)
				if strings.TrimSpace(name) != "" {
					chatReq["tool_choice"] = map[string]interface{}{
						"type": "function",
						"function": map[string]interface{}{
							"name": name,
						},
					}
				}
			}
		}
	}
}

func applyAnthropicReasoningToOpenAI(req map[string]interface{}, chatReq map[string]interface{}) {
	// Keep reasoning-related controls for reasoning-capable OpenAI-compatible providers.
	if v, ok := req["thinking"]; ok {
		chatReq["thinking"] = v
	}
	if v, ok := req["reasoning_effort"]; ok {
		chatReq["reasoning_effort"] = v
	}
}

func extractOpenAIToolCallsFromAnthropicContent(content interface{}) []map[string]interface{} {
	blocks, ok := content.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, 4)
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		if blockType != "tool_use" {
			continue
		}
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		args := "{}"
		if input, exists := block["input"]; exists {
			if bs, err := json.Marshal(input); err == nil {
				args = string(bs)
			}
		}
		out = append(out, map[string]interface{}{
			"id":   id,
			"type": "function",
			"function": map[string]interface{}{
				"name":      name,
				"arguments": args,
			},
		})
	}
	return out
}

func extractUserTextAndToolResults(content interface{}) (string, []map[string]interface{}) {
	blocks, ok := content.([]interface{})
	if !ok {
		return extractAnthropicTextContent(content), nil
	}
	textParts := make([]string, 0, len(blocks))
	toolResults := make([]map[string]interface{}, 0, 4)
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text", "":
			if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
				textParts = append(textParts, text)
			}
		case "tool_result":
			toolID, _ := block["tool_use_id"].(string)
			if strings.TrimSpace(toolID) == "" {
				continue
			}
			toolResults = append(toolResults, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": toolID,
				"content":      extractAnthropicTextContent(block["content"]),
			})
		}
	}
	return strings.Join(textParts, "\n"), toolResults
}

func extractAnthropicThinkingContent(content interface{}) string {
	blocks, ok := content.([]interface{})
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		if blockType != "thinking" {
			continue
		}
		if text, ok := block["thinking"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
			continue
		}
		if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
			continue
		}
		if text, ok := block["reasoning_content"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractToolUseBlocksFromOpenAIMessage(message map[string]interface{}) []map[string]interface{} {
	raw, exists := message["tool_calls"]
	if !exists {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		tc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		fnRaw, ok := tc["function"]
		if !ok {
			continue
		}
		fn, ok := fnRaw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		id, _ := tc["id"].(string)
		if strings.TrimSpace(id) == "" {
			id = "toolu_" + nextUniqueID()
		}
		input := map[string]interface{}{}
		if argsRaw, ok := fn["arguments"].(string); ok && strings.TrimSpace(argsRaw) != "" {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(argsRaw), &parsed); err == nil {
				input = parsed
			} else {
				input = map[string]interface{}{"raw": argsRaw}
			}
		}
		out = append(out, map[string]interface{}{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": input,
		})
	}
	return out
}

// extractAnthropicTextContent reads Anthropic content blocks and returns merged text.
func extractAnthropicTextContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		parts := make([]string, 0, len(c))
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType != "text" && blockType != "" {
				continue
			}
			if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func asInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// getHTTPClient returns a non-nil client for the given model name.
// Falls back to the first configured model client, and finally to a new default http.Client.
func (p *Proxy) getHTTPClient(modelName string) *http.Client {
	if c, exists := p.clients[modelName]; exists && c != nil {
		return c
	}
	if len(p.config.Models) > 0 {
		firstName := p.config.Models[0].Name
		if c, exists := p.clients[firstName]; exists && c != nil {
			return c
		}
	}
	return &http.Client{Timeout: 300 * time.Second}
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

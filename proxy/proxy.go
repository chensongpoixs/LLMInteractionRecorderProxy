package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"proxy-llm/config"
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
}

// New creates a new proxy instance
func New(cfg *config.Config, logger *storage.Logger, metrics *Metrics) *Proxy {
	proxy := &Proxy{
		config:  cfg,
		logger:  logger,
		metrics: metrics,
		clients: make(map[string]*http.Client),
	}

	// Create HTTP clients for each model
	for _, model := range cfg.Models {
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
	proxy.handler = NewHandler(cfg, logger, metrics)

	// Register proxy routes
	mux.HandleFunc("/v1/chat/completions", proxy.handleRequest("chat/completions"))
	mux.HandleFunc("/v1/completions", proxy.handleRequest("completions"))
	mux.HandleFunc("/v1/embeddings", proxy.handleRequest("embeddings"))

	// Health check
	if cfg.Monitoring.EnableHealth {
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "OK")
		})
	}

	// Metrics endpoint
	if cfg.Monitoring.EnableMetrics {
		mux.HandleFunc(cfg.Monitoring.MetricsPath, proxy.metrics.ServeHTTP)
	}

	// Auth middleware
	if cfg.Proxy.EnableAuth {
		proxy.authMw = proxy.createAuthMiddleware()
	}

	// Apply CORS if enabled
	var handler http.Handler = mux
	if cfg.Proxy.EnableCORS {
		handler = proxy.createCORSMiddleware(mux)
	}

	// Apply auth if enabled
	if proxy.authMw != nil {
		handler = proxy.authMw(handler.(http.HandlerFunc))
	}

	proxy.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: handler,
	}

	return proxy
}

// handleRequest creates an HTTP handler for a specific endpoint
func (p *Proxy) handleRequest(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Read request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		r.Body.Close()

		// Parse request body
		var reqBody map[string]interface{}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Determine target model and endpoint
		modelName := "default"
		if model, exists := reqBody["model"]; exists {
			if m, ok := model.(string); ok {
				modelName = m
			}
		}

		targetURL := fmt.Sprintf("%s/%s", p.getModelBaseURL(modelName), endpoint)

		// Get API key
		apiKey := p.getAPIKey(modelName)

		// Create proxy request
		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
			return
		}

		// Copy headers
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
		proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))

		// Determine if streaming
		isStream := false
		if stream, exists := reqBody["stream"]; exists {
			if s, ok := stream.(bool); ok {
				isStream = s
			}
		}

		// Execute request
		var client *http.Client
		if c, exists := p.clients[modelName]; exists {
			client = c
		} else {
			client = p.clients["default"]
		}

		// Handle streaming vs non-streaming
		if isStream && p.config.Proxy.EnableStream {
			p.handleStreaming(w, r, proxyReq, client, reqBody, modelName, start, endpoint)
			return
		}

		// Non-streaming response
		resp, err := client.Do(proxyReq)
		if err != nil {
			p.logRequest(reqBody, modelName, endpoint, start, 500, nil, true, err.Error(), nil)
			http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Read response body
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			p.logRequest(reqBody, modelName, endpoint, start, resp.StatusCode, nil, false, err.Error(), nil)
			http.Error(w, "Failed to read response", http.StatusBadGateway)
			return
		}

		// Parse response for logging
		var respParsed map[string]interface{}
		json.Unmarshal(respBody, &respParsed)

		// Log the request/response
		tokensUsed := p.extractTokens(respParsed)
		p.logRequest(reqBody, modelName, endpoint, start, resp.StatusCode, respBody, false, "", tokensUsed)

		// Forward response
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
}

// handleStreaming manages streaming responses
func (p *Proxy) handleStreaming(w http.ResponseWriter, r *http.Request, proxyReq *http.Request, client *http.Client, reqBody map[string]interface{}, modelName string, start time.Time, endpoint string) {
	resp, err := client.Do(proxyReq)
	if err != nil {
		p.logRequest(reqBody, modelName, endpoint, start, 500, nil, true, err.Error(), nil)
		http.Error(w, "Proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Set headers for streaming
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	// Create request log entry
	sessionID := r.Header.Get("X-Session-ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}

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

	scanner := make([]byte, 8192)
	for {
		n, err := resp.Body.Read(scanner)
		if n > 0 {
			chunkData := scanner[:n]
			w.Write(chunkData)
			w.(http.Flusher).Flush()
			chunkCount++

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
				reqLog.Error = err.Error()
			}
			break
		}
	}

	// Finalize request log
	reqLog.StatusCode = resp.StatusCode
	reqLog.Duration = time.Since(start).String()
	p.logger.SaveRequest(reqLog)
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

// logRequest logs a request/response pair
func (p *Proxy) logRequest(reqBody map[string]interface{}, modelName, endpoint string, start time.Time, statusCode int, respBody []byte, isStream bool, errorMsg string, tokensUsed map[string]int) {
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

	p.logger.SaveRequest(reqLog)
}

// extractTokens extracts token usage from response
func (p *Proxy) extractTokens(resp map[string]interface{}) map[string]int {
	tokens := make(map[string]int)

	if usage, exists := resp["usage"]; exists {
		if u, ok := usage.(map[string]interface{}); ok {
			if val, exists := u["prompt_tokens"]; exists {
				if v, ok := val.(float64); ok {
					tokens["prompt_tokens"] = int(v)
				}
			}
			if val, exists := u["completion_tokens"]; exists {
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

// createAuthMiddleware creates authentication middleware
func (p *Proxy) createAuthMiddleware() func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get(p.config.Proxy.AuthHeader)
			if authHeader == "" || authHeader != p.config.Proxy.AuthToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
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

// Start begins serving HTTP requests
func (p *Proxy) Start() error {
	fmt.Printf("Starting proxy server on %s\n", p.server.Addr)
	if p.config.Server.TLSCert != "" && p.config.Server.TLSKey != "" {
		return p.server.ListenAndServeTLS(p.config.Server.TLSCert, p.config.Server.TLSKey)
	}
	return p.server.ListenAndServe()
}

// Shutdown gracefully stops the server
func (p *Proxy) Shutdown(ctx context.Context) error {
	return p.server.Shutdown(ctx)
}

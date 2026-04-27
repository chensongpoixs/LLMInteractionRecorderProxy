package storage

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MessageLog represents a single message in a conversation chain.
type MessageLog struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	Reasoning string        `json:"reasoning,omitempty"`
	ToolCalls []ToolCallLog `json:"tool_calls,omitempty"`
}

// ToolCallLog represents a tool call within a message.
type ToolCallLog struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function FunctionCallLog  `json:"function"`
}

// FunctionCallLog represents a function call details.
type FunctionCallLog struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// RequestLog represents a single request/response pair with full conversation context.
type RequestLog struct {
	ID           string                 `json:"id"`
	Timestamp    time.Time              `json:"timestamp"`
	SessionID    string                 `json:"session_id"`
	ConversationID string               `json:"conversation_id,omitempty"`
	TurnIndex    int                    `json:"turn_index,omitempty"`
	Endpoint     string                 `json:"endpoint"`
	Method       string                 `json:"method"`
	Model        string                 `json:"model"`
	Provider     string                 `json:"provider"`
	SystemPrompt string                 `json:"system_prompt,omitempty"`
	Messages     []MessageLog           `json:"messages,omitempty"`
	RequestBody  map[string]interface{} `json:"request_body"`
	StatusCode   int                    `json:"status_code,omitempty"`
	ResponseBody map[string]interface{} `json:"response_body,omitempty"`
	AggregatedResponse map[string]interface{} `json:"aggregated_response,omitempty"`
	Stream       bool                   `json:"stream"`
	Duration     string                 `json:"duration"`
	Error        string                 `json:"error,omitempty"`
	TokensUsed   map[string]int         `json:"tokens_used,omitempty"`
}

// ResponseStream represents streaming response chunks
type ResponseStream struct {
	ID        string    `json:"id"`
	Chunk     string    `json:"chunk"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Index     int       `json:"index"`
}

// Logger handles writing request/response data to files
type Logger struct {
	mu      sync.Mutex
	dir     string
	format  string
	rotate  string
	maxSize string
	gzip    bool
}

// NewLogger creates a new file logger
func NewLogger(dir, format, rotate, maxSize string, compress bool) (*Logger, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	// Create streams subdirectory
	if err := os.MkdirAll(filepath.Join(dir, "streams"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create streams directory: %w", err)
	}

	return &Logger{
		dir:     dir,
		format:  format,
		rotate:  rotate,
		maxSize: maxSize,
		gzip:    compress,
	}, nil
}

// SaveRequest saves a request with its response to a log file
func (l *Logger) SaveRequest(req *RequestLog) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	fileName := l.buildFileName(req)
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request log: %w", err)
	}

	if l.gzip {
		var buf bytes.Buffer
		writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		writer.Write(append(data, '\n'))
		writer.Close()
		_, err = file.Write(buf.Bytes())
		return err
	}

	_, err = file.Write(append(data, '\n'))
	return err
}

// SaveStreamChunk saves a single streaming response chunk
func (l *Logger) SaveStreamChunk(chunk *ResponseStream) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	dateStr := chunk.Timestamp.Format("20060102")
	if dateStr == "00010101" {
		dateStr = time.Now().Format("20060102")
	}
	streamDir := filepath.Join(l.dir, dateStr, "streams")
	if err := os.MkdirAll(streamDir, 0755); err != nil {
		return err
	}

	sessionID := chunk.SessionID
	if sessionID == "" {
		sessionID = "default"
	}
	fileName := filepath.Join(streamDir, fmt.Sprintf("%s_%s", sessionID, dateStr))

	file, err := os.OpenFile(fileName+".jsonl", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = file.Write(append(data, '\n'))
	return err
}

// ListLogs returns all log files matching a pattern
func (l *Logger) ListLogs(sessionID, date string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(l.dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		if ext != ".jsonl" && ext != ".gz" {
			return nil
		}
		if ext == ".gz" && !strings.HasSuffix(path, ".jsonl.gz") {
			return nil
		}

		base := filepath.Base(path)
		full := filepath.ToSlash(path)
		if sessionID != "" && !strings.Contains(base, sessionID) {
			return nil
		}
		if date != "" && !strings.Contains(full, date) {
			return nil
		}

		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// ReadLog reads and returns all entries from a log file
func (l *Logger) ReadLog(filePath string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Handle gzipped files
	if filepath.Ext(filePath) == ".gz" {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		data, err = io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
	}

	var entries []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) > 0 {
			var entry json.RawMessage
			if err := json.Unmarshal(line, &entry); err == nil {
				entries = append(entries, entry)
			}
		}
	}

	return entries, nil
}

// ExtractMessagesFromRequest extracts MessageLog entries, system prompt, and the remaining
// request body from an OpenAI-style or Anthropic-style request map.
// Returns messages, systemPrompt, and a cleaned requestBody (with messages removed).
func ExtractMessagesFromRequest(reqBody map[string]interface{}) (messages []MessageLog, systemPrompt string, cleanedBody map[string]interface{}) {
	if reqBody == nil {
		return nil, "", nil
	}

	// Copy to avoid mutating the original
	cleanedBody = make(map[string]interface{}, len(reqBody))
	for k, v := range reqBody {
		cleanedBody[k] = v
	}

	// Try OpenAI-style: request.messages[]
	if msgsRaw, exists := reqBody["messages"]; exists {
		if msgsArr, ok := msgsRaw.([]interface{}); ok {
			messages = make([]MessageLog, 0, len(msgsArr))
			for _, m := range msgsArr {
				msg, ok := m.(map[string]interface{})
				if !ok {
					continue
				}
				role, _ := msg["role"].(string)
				content := extractTextContent(msg["content"])

				ml := MessageLog{Role: role, Content: content}

				// Extract reasoning/thinking from assistant messages
				if role == "assistant" {
					if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
						ml.Reasoning = rc
					} else if rc, ok := msg["reasoning"].(string); ok && rc != "" {
						ml.Reasoning = rc
					}
					// Extract tool calls
					if tcRaw, exists := msg["tool_calls"]; exists {
						if tcArr, ok := tcRaw.([]interface{}); ok {
							toolCalls := make([]ToolCallLog, 0, len(tcArr))
							for _, tc := range tcArr {
								tcMap, ok := tc.(map[string]interface{})
								if !ok {
									continue
								}
								tcl := ToolCallLog{
									ID:   getStringField(tcMap, "id"),
									Type: getStringField(tcMap, "type"),
								}
								if fnRaw, exists := tcMap["function"]; exists {
									if fnMap, ok := fnRaw.(map[string]interface{}); ok {
										tcl.Function = FunctionCallLog{
											Name:      getStringField(fnMap, "name"),
											Arguments: getStringField(fnMap, "arguments"),
										}
									}
								}
								toolCalls = append(toolCalls, tcl)
							}
							ml.ToolCalls = toolCalls
						}
					}
				}

				if role == "system" && systemPrompt == "" {
					systemPrompt = content
					continue // don't add system to messages chain
				}
				messages = append(messages, ml)
			}
			delete(cleanedBody, "messages")
		}
	}

	// Try Anthropic-style: system field (could be string or content block)
	if systemPrompt == "" {
		if sys, ok := reqBody["system"].(string); ok && sys != "" {
			systemPrompt = sys
		}
	}

	// Try Anthropic-style: request.messages[] with content blocks
	if len(messages) == 0 {
		if msgsRaw, exists := reqBody["messages"]; exists {
			if msgsArr, ok := msgsRaw.([]interface{}); ok {
				messages = make([]MessageLog, 0, len(msgsArr))
				for _, m := range msgsArr {
					msg, ok := m.(map[string]interface{})
					if !ok {
						continue
					}
					role, _ := msg["role"].(string)
					content := extractAnthropicTextContent(msg["content"])
					ml := MessageLog{Role: role, Content: content}

					if role == "assistant" {
						thinking := extractAnthropicThinkingContent(msg["content"])
						if thinking != "" {
							ml.Reasoning = thinking
						}
					}
					messages = append(messages, ml)
				}
			}
		}
	}

	return messages, systemPrompt, cleanedBody
}

// extractTextContent extracts text from various content field formats.
func extractTextContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType != "text" && blockType != "" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// extractAnthropicTextContent reads Anthropic content blocks and returns merged text.
func extractAnthropicTextContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
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

// extractAnthropicThinkingContent extracts thinking blocks from Anthropic content.
func extractAnthropicThinkingContent(content interface{}) string {
	blocks, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var parts []string
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
		} else if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// getStringField safely extracts a string field from a map.
func getStringField(m map[string]interface{}, key string) string {
	if v, exists := m[key]; exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (l *Logger) buildFileName(req *RequestLog) string {
	dateStr := req.Timestamp.Format("20060102")
	if dateStr == "00010101" {
		dateStr = time.Now().Format("20060102")
	}

	// Store logs by date directory for easier investigation and retention.
	dir := filepath.Join(l.dir, dateStr)
	if err := os.MkdirAll(dir, 0755); err != nil {
		dir = filepath.Join(l.dir, "default")
		os.MkdirAll(dir, 0755)
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "default"
	}

	extension := ".jsonl"
	if l.gzip {
		extension = ".jsonl.gz"
	}

	return filepath.Join(dir, fmt.Sprintf("%s_%s%s", sessionID, dateStr, extension))
}

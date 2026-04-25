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
	"sync"
	"time"
)

// RequestLog represents a single request/response pair
type RequestLog struct {
	ID           string                 `json:"id"`
	Timestamp    time.Time              `json:"timestamp"`
	SessionID    string                 `json:"session_id"`
	Endpoint     string                 `json:"endpoint"`
	Method       string                 `json:"method"`
	Model        string                 `json:"model"`
	Provider     string                 `json:"provider"`
	RequestBody  map[string]interface{} `json:"request_body"`
	StatusCode   int                    `json:"status_code,omitempty"`
	ResponseBody map[string]interface{} `json:"response_body,omitempty"`
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
		dir:    dir,
		format: format,
		rotate: rotate,
		maxSize: maxSize,
		gzip:   compress,
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

	fileName := filepath.Join(l.dir, "streams", fmt.Sprintf("%s_%s", chunk.SessionID, time.Now().Format("20060102")))

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
	pattern := filepath.Join(l.dir, "*")
	if sessionID != "" {
		pattern = filepath.Join(l.dir, sessionID+"*")
	}
	if date != "" {
		pattern = filepath.Join(l.dir, "*"+date+"*")
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, m := range matches {
		ext := filepath.Ext(m)
		if ext == ".jsonl" || ext == ".jsonl.gz" {
			files = append(files, m)
		}
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

func (l *Logger) buildFileName(req *RequestLog) string {
	dateStr := req.Timestamp.Format("20060102")
	sessionDir := req.SessionID
	if sessionDir == "" {
		sessionDir = "default"
	}

	dir := filepath.Join(l.dir, sessionDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		dir = filepath.Join(l.dir, "default")
		os.MkdirAll(dir, 0755)
	}

	timestamp := req.Timestamp.Format("150405")
	extension := ".jsonl"
	if l.gzip {
		extension = ".jsonl.gz"
	}

	return filepath.Join(dir, fmt.Sprintf("%s_%s_%s%s", req.SessionID, dateStr, timestamp, extension))
}

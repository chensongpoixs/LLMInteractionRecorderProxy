package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level represents log severity levels
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Config holds logger configuration
type Config struct {
	Level        Level  `yaml:"level"`
	File         string `yaml:"file"`
	MaxSizeMB    int    `yaml:"max_size_mb"`
	MaxBackups   int    `yaml:"max_backups"`
	MaxAgeDays   int    `yaml:"max_age_days"`
	Compress     bool   `yaml:"compress"`
	Console      bool   `yaml:"console"`
	RequestLog   bool   `yaml:"request_log"`
	RequestBody  bool   `yaml:"request_body"`
	ResponseBody bool   `yaml:"response_body"`
}

// DefaultConfig returns default logger configuration
func DefaultConfig() *Config {
	return &Config{
		Level:      INFO,
		File:       "./logs/app.log",
		MaxSizeMB:  100,
		MaxBackups: 10,
		MaxAgeDays: 30,
		Compress:   true,
		Console:    true,
		RequestLog: true,
	}
}

// Logger provides structured logging capabilities
type Logger struct {
	mu         sync.Mutex
	level      Level
	file       *os.File
	console    *log.Logger
	fileLogger *log.Logger
	requestID  string
	config     *Config
	startTime  time.Time
}

// Global logger instance
var globalLogger *Logger
var globalOnce sync.Once

// New creates a new logger instance
func New(cfg *Config) (*Logger, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	l := &Logger{
		level:     cfg.Level,
		config:    cfg,
		startTime: time.Now(),
	}

	// Setup console logger
	if cfg.Console {
		l.console = log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	}

	// Setup file logger
	if cfg.File != "" {
		dir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		l.file = f
		l.fileLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	}

	return l, nil
}

// InitGlobal initializes the global logger instance
func InitGlobal(cfg *Config) *Logger {
	globalOnce.Do(func() {
		globalLogger, _ = New(cfg)
	})
	return globalLogger
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetRequestID sets the request correlation ID for this logger
func (l *Logger) SetRequestID(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requestID = id
}

// log writes a log message to the appropriate destination
func (l *Logger) log(level Level, msg string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Keep logs at or above configured threshold.
	// DEBUG(0) should include everything; ERROR(3) should include ERROR/FATAL only.
	if level < l.level {
		return
	}

	// Format message with request ID if available
	var formattedMsg string
	if l.requestID != "" {
		formattedMsg = fmt.Sprintf("[REQ:%s] ", l.requestID) + msg
	} else {
		formattedMsg = msg
	}

	// Add additional context
	additionalArgs := []interface{}{}
	if l.requestID != "" {
		additionalArgs = append(additionalArgs, l.requestID)
	}

	// Format with level prefix
	levelPrefix := fmt.Sprintf("[%s] ", level.String())
	finalMsg := levelPrefix + formattedMsg

	// Write to console
	if l.config.Console && l.console != nil {
		l.console.Output(3, fmt.Sprintf(finalMsg, args...))
	}

	// Write to file
	if l.fileLogger != nil {
		fileMsg := fmt.Sprintf("[%s] %s", level.String(), formattedMsg)
		l.fileLogger.Output(3, fmt.Sprintf(fileMsg, args...))
	}
}

// Debug logs debug level message
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.log(DEBUG, msg, args...)
}

// Info logs info level message
func (l *Logger) Info(msg string, args ...interface{}) {
	l.log(INFO, msg, args...)
}

// Warn logs warning level message
func (l *Logger) Warn(msg string, args ...interface{}) {
	l.log(WARN, msg, args...)
}

// Error logs error level message
func (l *Logger) Error(msg string, args ...interface{}) {
	l.log(ERROR, msg, args...)
}

// Fatal logs fatal level message and exits
func (l *Logger) Fatal(msg string, args ...interface{}) {
	l.log(FATAL, msg, args...)
	os.Exit(1)
}

// WithRequestID creates a new logger instance with the given request ID
func (l *Logger) WithRequestID(id string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := &Logger{
		level:      l.level,
		file:       l.file,
		console:    l.console,
		fileLogger: l.fileLogger,
		requestID:  id,
		config:     l.config,
		startTime:  l.startTime,
	}
	return newLogger
}

// LogRequest logs incoming request details
func (l *Logger) LogRequest(reqID, method, path string, headers map[string][]string, bodySize int) {
	l.Info("Incoming request: %s %s (size: %d bytes)", method, path, bodySize)
	if l.config.RequestBody && bodySize > 0 {
		l.Debug("Request body size: %d bytes", bodySize)
	}
}

// LogResponse logs outgoing response details
func (l *Logger) LogResponse(reqID, method, path string, statusCode int, duration time.Duration, responseSize int) {
	statusStr := fmt.Sprintf("%d", statusCode)
	if statusCode >= 200 && statusCode < 300 {
		statusStr = fmt.Sprintf("%d [OK]", statusCode)
	} else if statusCode >= 400 {
		statusStr = fmt.Sprintf("%d [ERROR]", statusCode)
	}
	l.Info("Response: %s %s -> %s (duration: %v, response: %d bytes)",
		method, path, statusStr, duration, responseSize)
}

// LogProxyForward logs proxy forwarding details
func (l *Logger) LogProxyForward(reqID, model, endpoint, targetURL string, isStream bool) {
	streamStr := "no"
	if isStream {
		streamStr = "yes"
	}
	l.Info("Proxy forward: model=%s, endpoint=%s, target=%s, stream=%s",
		model, endpoint, targetURL, streamStr)
}

// LogProxyResponse logs proxy response details
func (l *Logger) LogProxyResponse(reqID, model, endpoint string, statusCode int, bodySize int, duration time.Duration, err error) {
	if err != nil {
		l.Error("Proxy error for %s/%s: %v", model, endpoint, err)
		return
	}
	l.Info("Proxy response: model=%s, endpoint=%s, status=%d, size=%d bytes, duration=%v",
		model, endpoint, statusCode, bodySize, duration)
}

// LogStorage logs storage operation details
func (l *Logger) LogStorage(operation, path string, size int, err error) {
	if err != nil {
		l.Error("Storage %s failed for %s: %v", operation, path, err)
		return
	}
	l.Info("Storage %s: %s (size: %d bytes)", operation, path, size)
}

// LogStreaming logs streaming chunk details
func (l *Logger) LogStreaming(reqID string, chunkIndex int, chunkSize int) {
	l.Debug("Streaming chunk %d: %d bytes", chunkIndex, chunkSize)
}

// LogServer logs server lifecycle events
func (l *Logger) LogServer(event, detail string) {
	l.Info("Server %s: %s", event, detail)
}

// GetDefault returns the global logger instance
func GetDefault() *Logger {
	return globalLogger
}

// ShouldLogRequestBody returns whether request body should be logged
func (l *Logger) ShouldLogRequestBody() bool {
	return l.config.RequestBody
}

// ShouldLogResponseBody returns whether response body should be logged
func (l *Logger) ShouldLogResponseBody() bool {
	return l.config.ResponseBody
}

// Close closes all log file handles
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// Write implements io.Writer interface for compatibility
func (l *Logger) Write(p []byte) (n int, err error) {
	l.Info("%s", string(p))
	return len(p), nil
}

// Helper function to truncate strings for logging
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// Helper function to mask sensitive strings (like API keys)
func MaskString(s string, visibleChars int) string {
	if len(s) <= visibleChars*2 {
		return s
	}
	return s[:visibleChars] + "****" + s[len(s)-visibleChars:]
}

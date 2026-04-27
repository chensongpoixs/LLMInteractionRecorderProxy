package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// envVarPattern matches ${VAR_NAME} placeholders in config values.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ModelConfig defines a single LLM provider configuration
type ModelConfig struct {
	Name             string        `yaml:"name"`
	BaseURL          string        `yaml:"base_url"`           // OpenAI-compatible base URL (e.g. https://api.deepseek.com)
	BaseURLAnthropic string        `yaml:"base_url_anthropic"` // Anthropic-compatible base URL (e.g. https://api.deepseek.com/anthropic)
	APIKey           string        `yaml:"api_key"`
	ModelName        string        `yaml:"model"`
	Timeout          time.Duration `yaml:"timeout"`
	MaxRetries       int           `yaml:"max_retries"`
	// Llama.cpp specific configuration
	LlamaAPI    string `yaml:"llama_api"`     // llama.cpp API endpoint (e.g., "/v1/api/chat")
	LlamaAPIKey string `yaml:"llama_api_key"` // llama.cpp API key if required
	LlamaModel  string `yaml:"llama_model"`   // model name to send to llama.cpp
}

// StorageConfig defines how to store request/response data
type StorageConfig struct {
	Directory string `yaml:"directory"`
	Format    string `yaml:"format"` // "jsonl" or "json"
	Compress  bool   `yaml:"compress"`
	Rotate    string `yaml:"rotate"` // "daily", "size", "weekly"
	MaxSize   string `yaml:"max_size"`
}

// ServerConfig defines the HTTP server settings
type ServerConfig struct {
	Port    int    `yaml:"port"`
	Host    string `yaml:"host"`
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
}

// LoggingConfig defines logging behavior
type LoggingConfig struct {
	Level           string `yaml:"level"` // "debug", "info", "warn", "error"
	File            string `yaml:"file"`
	MaxSizeMB       int    `yaml:"max_size_mb"`
	MaxBackups      int    `yaml:"max_backups"`
	MaxAgeDays      int    `yaml:"max_age_days"`
	Compress        bool   `yaml:"compress"`
	Console         bool   `yaml:"console"`
	RequestLog      bool   `yaml:"request_log"`
	RequestBodyLog  bool   `yaml:"request_body_log"`
	ResponseBodyLog bool   `yaml:"response_body_log"`
	// UpstreamHTTPlog: when true, at INFO: log client->proxy, proxy->LLM, and LLM->proxy (body truncated by upstream_log_max_bytes)
	UpstreamHTTPlog     bool `yaml:"upstream_http_log"`
	UpstreamLogMaxBytes int  `yaml:"upstream_log_max_bytes"` // 0 = 256 KiB
}

// DailyExportConfig runs once per day to merge JSONL logs for the previous calendar day
// into a single file matching the reference dataset line shape.
type DailyExportConfig struct {
	Enable       bool   `yaml:"enable"`
	OutputDir    string `yaml:"output_dir"`
	FilePrefix   string `yaml:"file_prefix"`
	ExportFormat string `yaml:"export_format"` // "reasoning" (default) or "messages" (OpenAI fine-tuning format)
	RunHour      int    `yaml:"run_hour"`      // 0–23, in Timezone
	RunMinute    int    `yaml:"run_minute"`    // 0–59, in Timezone
	Timezone     string `yaml:"timezone"`      // e.g. "Local", "Asia/Shanghai" (empty = Local)
}

// Config is the root configuration structure
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Models      []ModelConfig     `yaml:"models"`
	Storage     StorageConfig     `yaml:"storage"`
	Proxy       ProxyConfig       `yaml:"proxy"`
	Monitoring  MonitoringConfig  `yaml:"monitoring"`
	Logging     LoggingConfig     `yaml:"logging"`
	DailyExport DailyExportConfig `yaml:"daily_export"`
}

// ProxyConfig defines proxy behavior
type ProxyConfig struct {
	EnableStream   bool     `yaml:"enable_stream"`
	MaxBodySize    string   `yaml:"max_body_size"`
	RateLimit      int      `yaml:"rate_limit"`
	EnableCORS     bool     `yaml:"enable_cors"`
	AllowedOrigins []string `yaml:"allowed_origins"`
	EnableAuth     bool     `yaml:"enable_auth"`
	AuthHeader     string   `yaml:"auth_header"`
	AuthToken      string   `yaml:"auth_token"`
}

// MonitoringConfig defines monitoring endpoints
type MonitoringConfig struct {
	EnableHealth  bool   `yaml:"enable_health"`
	EnableMetrics bool   `yaml:"enable_metrics"`
	MetricsPath   string `yaml:"metrics_path"`
}

// expandEnvVars recursively walks a value and replaces ${VAR} placeholders with
// environment variable values.
func expandEnvVars(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return envVarPattern.ReplaceAllStringFunc(val, func(match string) string {
			// match looks like ${VAR_NAME}, extract the name inside braces.
			varName := match[2 : len(match)-1]
			return os.Getenv(varName)
		})
	case map[string]interface{}:
		for k, vv := range val {
			val[k] = expandEnvVars(vv)
		}
		return val
	case []interface{}:
		for i, item := range val {
			val[i] = expandEnvVars(item)
		}
		return val
	default:
		return v
	}
}

// Load reads configuration from file and applies environment variable overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal into a generic map first so we can expand env vars in all string values.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	expanded := expandEnvVars(raw).(map[string]interface{})

	// Re-marshal the expanded map back to YAML, then unmarshal into Config struct.
	expandedBytes, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal expanded config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(expandedBytes, cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Allow environment variable overrides
	if v := os.Getenv("PROXY_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("PROXY_STORAGE_DIR"); v != "" {
		cfg.Storage.Directory = v
	}

	return cfg, nil
}

// DefaultConfig returns a sensible default configuration
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "0.0.0.0",
		},
		Models: []ModelConfig{
			{
				Name:      "default",
				BaseURL:   "https://api.openai.com/v1",
				ModelName: "gpt-4",
				Timeout:   120 * time.Second,
			},
		},
		Storage: StorageConfig{
			Directory: "./data",
			Format:    "jsonl",
			Rotate:    "daily",
		},
		Proxy: ProxyConfig{
			EnableStream: true,
			MaxBodySize:  "10mb",
			EnableCORS:   true,
			EnableAuth:   false,
			AuthHeader:   "Authorization",
		},
		Monitoring: MonitoringConfig{
			EnableHealth:  true,
			EnableMetrics: true,
			MetricsPath:   "/metrics",
		},
		Logging: LoggingConfig{
			Level:               "info",
			File:                "./logs/app.log",
			MaxSizeMB:           100,
			MaxBackups:          10,
			MaxAgeDays:          30,
			Compress:            true,
			Console:             true,
			RequestLog:          true,
			UpstreamHTTPlog:     false,
			UpstreamLogMaxBytes: 0,
		},
		DailyExport: DailyExportConfig{
			Enable:       false,
			OutputDir:    "./exports",
			FilePrefix:   "Opus-4.6-Reasoning-3000x-filtered-",
			ExportFormat: "reasoning",
			RunHour:      0,
			RunMinute:    5,
			Timezone:     "Local",
		},
	}
}

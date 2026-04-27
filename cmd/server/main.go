package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"proxy-llm/config"
	"proxy-llm/exporter"
	"proxy-llm/logger"
	"proxy-llm/proxy"
	"proxy-llm/storage"
)

// logLevelFromString converts string to logger.Level
func logLevelFromString(s string) logger.Level {
	switch s {
	case "debug":
		return logger.DEBUG
	case "info":
		return logger.INFO
	case "warn":
		return logger.WARN
	case "error":
		return logger.ERROR
	default:
		return logger.INFO
	}
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Println("proxy-llm v1.0.0")
		fmt.Println("LLM API Proxy with Data Logging")
		return
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize logging system
	logConfig := &logger.Config{
		Level:        logLevelFromString(cfg.Logging.Level),
		File:         cfg.Logging.File,
		MaxSizeMB:    cfg.Logging.MaxSizeMB,
		MaxBackups:   cfg.Logging.MaxBackups,
		MaxAgeDays:   cfg.Logging.MaxAgeDays,
		Compress:     cfg.Logging.Compress,
		Console:      cfg.Logging.Console,
		RequestLog:   cfg.Logging.RequestLog,
		RequestBody:  cfg.Logging.RequestBodyLog,
		ResponseBody: cfg.Logging.ResponseBodyLog,
	}

	appLogger, err := logger.New(logConfig)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer appLogger.Close()

	appLogger.Info("========================================")
	appLogger.Info("proxy-llm starting up...")
	appLogger.Info("Configuration loaded from: %s", *configPath)
	appLogger.Info("Server: %s:%d", cfg.Server.Host, cfg.Server.Port)
	appLogger.Info("Storage: %s (format: %s)", cfg.Storage.Directory, cfg.Storage.Format)
	appLogger.Info("Logging: level=%s, file=%s, console=%v", cfg.Logging.Level, cfg.Logging.File, cfg.Logging.Console)
	effMax := cfg.Logging.UpstreamLogMaxBytes
	if effMax == 0 {
		effMax = 256 * 1024
	}
	appLogger.Info(
		"Logging options: request_log=%v request_body_log=%v response_body_log=%v upstream_http_log=%v upstream_effective_log_max_bytes=%d",
		cfg.Logging.RequestLog, cfg.Logging.RequestBodyLog, cfg.Logging.ResponseBodyLog, cfg.Logging.UpstreamHTTPlog, effMax,
	)
	appLogger.Info("Models configured: %d", len(cfg.Models))

	for _, model := range cfg.Models {
		appLogger.Info("  - Model: %s, URL: %s, API: %s", model.Name, model.BaseURL, model.ModelName)
	}

	// Create storage logger
	storageLogger, err := storage.NewLogger(
		cfg.Storage.Directory,
		cfg.Storage.Format,
		cfg.Storage.Rotate,
		cfg.Storage.MaxSize,
		cfg.Storage.Compress,
	)
	if err != nil {
		appLogger.Fatal("Failed to create storage logger: %v", err)
	}

	// Create metrics collector
	metrics := proxy.NewMetrics()

	// Create proxy
	proxyServer := proxy.New(cfg, storageLogger, metrics, appLogger)

	// Start proxy server in background so we can react to OS signals.
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- proxyServer.Start()
	}()

	// Optional: daily export of the previous day into one JSONL (see daily_export in config).
	exportCtx, stopExport := context.WithCancel(context.Background())
	defer stopExport()
	if cfg.DailyExport.Enable {
		appLogger.Info("Daily export enabled: dir=%s prefix=%s at %02d:%02d (%s)",
			cfg.DailyExport.OutputDir, cfg.DailyExport.FilePrefix,
			cfg.DailyExport.RunHour, cfg.DailyExport.RunMinute, cfg.DailyExport.Timezone)
		go runDailyExport(exportCtx, cfg, appLogger)
	}

	// Listen for termination signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	select {
	case sig := <-sigChan:
		appLogger.Info("Shutdown signal received (%s), stopping gracefully...", sig.String())
		stopExport()
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			appLogger.Fatal("Server error: %v", err)
		}
		appLogger.Info("Server exited")
		return
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	appLogger.Info("Initiating server shutdown...")
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Fatal("Shutdown error: %v", err)
	}

	// Wait for server goroutine to return.
	if err := <-serverErrCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		appLogger.Fatal("Server exit error: %v", err)
	}

	appLogger.Info("Server stopped, all connections closed")
}

// runDailyExport waits until each scheduled local time, then exports the previous calendar
// day's logs under storage.directory into a single JSONL in daily_export.output_dir.
func runDailyExport(ctx context.Context, cfg *config.Config, appLogger *logger.Logger) {
	c := &cfg.DailyExport
	loc := time.Local
	tz := strings.TrimSpace(c.Timezone)
	if tz != "" && tz != "Local" {
		if l, e := time.LoadLocation(tz); e == nil {
			loc = l
		} else {
			appLogger.Warn("daily_export: invalid timezone %q, using Local: %v", c.Timezone, e)
		}
	}
	outDir := c.OutputDir
	if outDir == "" {
		outDir = "./exports"
	}
	prefix := c.FilePrefix
	if prefix == "" {
		prefix = "dataset-"
	}
	h, m := c.RunHour, c.RunMinute
	if h < 0 || h > 23 {
		h = 0
	}
	if m < 0 || m > 59 {
		m = 0
	}
	for {
		now := time.Now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		d := time.Until(next)
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		y := time.Now().In(loc).AddDate(0, 0, -1)
		day := time.Date(y.Year(), y.Month(), y.Day(), 12, 0, 0, 0, loc)
		outPath := filepath.Join(outDir, prefix+y.Format("20060102")+".jsonl")
		n, err := exporter.ExportDay(cfg.Storage.Directory, day, outPath)
		if err != nil {
			appLogger.Warn("daily export: %v", err)
			continue
		}
		appLogger.Info("daily export: %d rows -> %s", n, outPath)
	}
}

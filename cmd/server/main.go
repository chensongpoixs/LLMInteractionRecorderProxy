package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"proxy-llm/config"
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
		Level:       logLevelFromString(cfg.Logging.Level),
		File:        cfg.Logging.File,
		MaxSizeMB:   cfg.Logging.MaxSizeMB,
		MaxBackups:  cfg.Logging.MaxBackups,
		MaxAgeDays:  cfg.Logging.MaxAgeDays,
		Compress:    cfg.Logging.Compress,
		Console:     cfg.Logging.Console,
		RequestLog:  cfg.Logging.RequestLog,
		RequestBody: cfg.Logging.RequestBodyLog,
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

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		appLogger.Info("Shutdown signal received, stopping gracefully...")
		cancel()
	}()

	// Start proxy server
	if err := proxyServer.Start(); err != nil {
		appLogger.Fatal("Server error: %v", err)
	}

	// Wait for shutdown signal
	<-ctx.Done()

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	appLogger.Info("Initiating server shutdown...")
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Fatal("Shutdown error: %v", err)
	}

	appLogger.Info("Server stopped, all connections closed")
}

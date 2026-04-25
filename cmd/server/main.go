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
	"proxy-llm/proxy"
	"proxy-llm/storage"
)

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

	// Create storage logger
	logger, err := storage.NewLogger(
		cfg.Storage.Directory,
		cfg.Storage.Format,
		cfg.Storage.Rotate,
		cfg.Storage.MaxSize,
		cfg.Storage.Compress,
	)
	if err != nil {
		log.Fatalf("Failed to create storage logger: %v", err)
	}

	// Create metrics collector
	metrics := proxy.NewMetrics()

	// Create proxy
	proxyServer := proxy.New(cfg, logger, metrics)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		fmt.Println("\nShutting down gracefully...")
		cancel()
	}()

	// Start proxy server
	if err := proxyServer.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	// Wait for shutdown signal
	<-ctx.Done()

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}

	fmt.Println("Server stopped")
}

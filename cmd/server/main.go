package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fluxo/export-middleware/pkg/config"
	grpcserver "github.com/fluxo/export-middleware/pkg/grpc"
	"github.com/fluxo/export-middleware/pkg/logger"
	"github.com/fluxo/export-middleware/pkg/oss"
	"github.com/fluxo/export-middleware/pkg/storage"
	"github.com/fluxo/export-middleware/pkg/taskmanager"
)

var (
	configPath = flag.String("config", "config.yaml", "Path to configuration file")
	version    = "1.0.0"
)

func main() {
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log, err := logger.New(
		cfg.Logging.Level,
		cfg.Logging.Format,
		cfg.Logging.Output,
		cfg.Logging.EnableTracing,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	log.Info(fmt.Sprintf("Starting Export Middleware v%s", version))
	log.Info("Configuration loaded successfully", logger.Fields{
		"grpc_port":      cfg.Server.Port,
		"status_port":    cfg.Server.StatusPort,
		"metrics_port":   cfg.Monitoring.MetricsPort,
		"max_concurrent": cfg.Concurrency.MaxConcurrentTasks,
		"oss_endpoint":   cfg.OSS.Endpoint,
		"oss_bucket":     cfg.OSS.Bucket,
	})

	// Initialize storage manager
	storageMgr, err := storage.NewManager(
		cfg.Storage.TempDirectory,
		cfg.Storage.CleanupEnabled,
		cfg.Storage.TempRetention,
		log,
	)
	if err != nil {
		log.Fatal("Failed to initialize storage manager", logger.Fields{"error": err.Error()})
	}
	log.Info("Storage manager initialized", logger.Fields{"temp_dir": cfg.Storage.TempDirectory})

	// Initialize OSS uploader
	ossUploader, err := oss.NewUploader(&cfg.OSS, log)
	if err != nil {
		log.Fatal("Failed to initialize OSS uploader", logger.Fields{"error": err.Error()})
	}
	log.Info("OSS uploader initialized", logger.Fields{
		"endpoint": cfg.OSS.Endpoint,
		"bucket":   cfg.OSS.Bucket,
	})

	// Initialize task manager
	taskMgr := taskmanager.NewManager(cfg, log, storageMgr, ossUploader)
	log.Info("Task manager initialized", logger.Fields{
		"max_concurrent": cfg.Concurrency.MaxConcurrentTasks,
		"queue_size":     cfg.Concurrency.TaskQueueSize,
	})

	// Initialize gRPC server
	grpcServer := grpcserver.NewServer(cfg, log, taskMgr)
	if err := grpcServer.Start(); err != nil {
		log.Fatal("Failed to start gRPC server", logger.Fields{"error": err.Error()})
	}
	log.Info("gRPC server started", logger.Fields{"port": cfg.Server.Port})

	// TODO: Initialize status API server
	// TODO: Initialize metrics server

	log.Info("Export middleware started successfully")
	log.Info("Ready to accept export requests", logger.Fields{
		"grpc_port":    cfg.Server.Port,
		"status_port":  cfg.Server.StatusPort,
		"metrics_port": cfg.Monitoring.MetricsPort,
	})

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Info("Shutdown signal received, initiating graceful shutdown...")

	// Stop gRPC server
	grpcServer.Stop()

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown task manager
	if err := taskMgr.Shutdown(shutdownCtx); err != nil {
		log.Error("Error during task manager shutdown", logger.Fields{"error": err.Error()})
	}

	// Close storage manager
	if err := storageMgr.Close(); err != nil {
		log.Error("Error closing storage manager", logger.Fields{"error": err.Error()})
	}

	// Close OSS uploader
	if err := ossUploader.Close(); err != nil {
		log.Error("Error closing OSS uploader", logger.Fields{"error": err.Error()})
	}

	log.Info("Shutdown complete")
}

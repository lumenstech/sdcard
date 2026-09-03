package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/alerts"
	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/handlers"
	"github.com/lumenstech/secure4k-sidecar/internal/inference"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// ---- Initialize stores ----
	frameStore, err := storage.NewFrameStore(storage.FrameStoreConfig{
		S3Endpoint:  cfg.S3Endpoint,
		S3Bucket:    cfg.S3Bucket,
		S3AccessKey: cfg.S3AccessKey,
		S3SecretKey: cfg.S3SecretKey,
		S3Region:    cfg.S3Region,
		LocalPath:   cfg.LocalFramePath,
	})
	if err != nil {
		slog.Error("failed to init frame store", "error", err)
		os.Exit(1)
	}

	deviceStore, err := device.NewStore(device.StoreConfig{
		DatabaseURL: cfg.DatabaseURL,
	})
	if err != nil {
		slog.Error("failed to init device store", "error", err)
		os.Exit(1)
	}
	defer deviceStore.Close()

	// ---- Initialize services ----
	inferenceService := inference.NewService(inference.Config{
		OllamaURL:  cfg.OllamaURL,
		Model:      cfg.InferenceModel,
		Confidence: cfg.InferenceThreshold,
	})

	alertService := alerts.NewService(alerts.Config{
		SequenceNowURL: cfg.SequenceNowURL,
		SequenceNowKey: cfg.SequenceNowAPIKey,
		DefaultChannel: "whatsapp",
	})

	// ---- Frame processing pipeline ----
	pipeline := handlers.NewPipeline(handlers.PipelineConfig{
		FrameStore:  frameStore,
		DeviceStore: deviceStore,
		Inference:   inferenceService,
		Alerts:      alertService,
		Workers:     cfg.PipelineWorkers,
		QueueSize:   cfg.PipelineQueueSize,
	})
	pipeline.Start()
	defer pipeline.Stop()

	// ---- HTTP routes ----
	mux := http.NewServeMux()

	// Frame ingest (from sidecar upload daemon)
	mux.HandleFunc("POST /v1/frames/ingest", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.IngestFrame(pipeline),
	))

	// Device management
	mux.HandleFunc("POST /v1/devices/register", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.RegisterDevice(deviceStore),
	))
	mux.HandleFunc("POST /v1/devices/heartbeat", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.DeviceHeartbeat(deviceStore),
	))

	// OTA updates
	mux.HandleFunc("GET /v1/ota/check", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.OTACheck(cfg.OTABundlePath),
	))

	// Dashboard API
	mux.HandleFunc("GET /v1/devices", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.ListDevices(deviceStore),
	))
	mux.HandleFunc("GET /v1/events", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.ListEvents(deviceStore),
	))

	// Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","version":"0.1.0"}`)
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
		// Allow large frame uploads (5MB max)
		MaxHeaderBytes: 1 << 20,
	}

	// ---- Graceful shutdown ----
	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}

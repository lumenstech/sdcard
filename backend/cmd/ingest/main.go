package main

import (
	"context"
	"encoding/json"
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
	"github.com/lumenstech/secure4k-sidecar/internal/mqttingest"
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

	frameStore, err := storage.NewFrameStore(storage.FrameStoreConfig{
		S3Endpoint: cfg.S3Endpoint, S3Bucket: cfg.S3Bucket, S3AccessKey: cfg.S3AccessKey,
		S3SecretKey: cfg.S3SecretKey, S3Region: cfg.S3Region, LocalPath: cfg.LocalFramePath,
	})
	if err != nil {
		slog.Error("failed to init frame store", "error", err)
		os.Exit(1)
	}
	defer frameStore.Close()

	deviceStore, err := device.NewStore(device.StoreConfig{DatabaseURL: cfg.DatabaseURL})
	if err != nil {
		slog.Error("failed to init device store", "error", err)
		os.Exit(1)
	}
	defer deviceStore.Close()

	inferenceService := inference.NewService(inference.Config{OllamaURL: cfg.OllamaURL, Model: cfg.InferenceModel, Confidence: cfg.InferenceThreshold})
	alertService := alerts.NewService(alerts.Config{SequenceNowURL: cfg.SequenceNowURL, SequenceNowKey: cfg.SequenceNowAPIKey, DefaultChannel: "whatsapp"})

	pipeline := handlers.NewPipeline(handlers.PipelineConfig{
		FrameStore: frameStore, DeviceStore: deviceStore, Inference: inferenceService, Alerts: alertService,
		Workers: cfg.PipelineWorkers, QueueSize: cfg.PipelineQueueSize, RateLimitPerMinute: cfg.RateLimitPerMinute,
	})
	pipeline.Start()
	defer pipeline.Stop()

	mqttSubscriber := mqttingest.New(mqttingest.Config{BrokerURL: cfg.MQTTBrokerURL, Topic: cfg.MQTTTopic}, pipeline)
	mqttCtx, mqttCancel := context.WithTimeout(context.Background(), 12*time.Second)
	if err := mqttSubscriber.Start(mqttCtx); err != nil {
		slog.Warn("mqtt ingest unavailable; http ingest remains active", "error", err)
	}
	mqttCancel()
	defer mqttSubscriber.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/frames/ingest", handlers.AuthMiddleware(cfg.ValidAPIKeys, handlers.IngestFrame(pipeline)))
	mux.HandleFunc("POST /v1/devices/register", handlers.AuthMiddleware(cfg.ValidAPIKeys, handlers.RegisterDevice(deviceStore)))
	mux.HandleFunc("POST /v1/devices/heartbeat", handlers.AuthMiddleware(cfg.ValidAPIKeys, handlers.DeviceHeartbeat(deviceStore)))
	mux.HandleFunc("GET /v1/ota/check", handlers.AuthMiddleware(cfg.ValidAPIKeys, handlers.OTACheck(cfg.OTABundlePath)))
	mux.HandleFunc("GET /v1/devices", handlers.AuthMiddleware(cfg.ValidAPIKeys, handlers.ListDevices(deviceStore)))
	mux.HandleFunc("GET /v1/events", handlers.AuthMiddleware(cfg.ValidAPIKeys, handlers.ListEvents(deviceStore)))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		deps := map[string]string{"postgres": "ok", "s3": "ok", "ollama": "ok"}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if err := deviceStore.Ping(ctx); err != nil {
			status = "unhealthy"
			deps["postgres"] = err.Error()
		}
		if err := frameStore.S3Health(ctx); err != nil {
			if status == "ok" {
				status = "degraded"
			}
			deps["s3"] = err.Error()
		}
		if err := inferenceService.Health(ctx); err != nil {
			if status == "ok" {
				status = "degraded"
			}
			deps["ollama"] = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		if status == "unhealthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "version": "0.1.0", "dependencies": deps})
	})

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Port), Handler: mux,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20,
	}

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
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}
	mqttSubscriber.Stop()
	pipeline.Stop()
	slog.Info("server stopped")
}

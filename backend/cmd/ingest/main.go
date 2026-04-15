// Command ingest is the Secure4K Sidecar Platform's HTTP+MQTT ingest server.
//
// It is intentionally small and uses only the Go standard library plus pgx
// (Postgres), minio-go (S3), and paho.mqtt.golang (MQTT). No HTTP framework.
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
	"github.com/lumenstech/secure4k-sidecar/internal/mqtt"
	"github.com/lumenstech/secure4k-sidecar/internal/ratelimit"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

const version = "0.1.0"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	frameStore, err := storage.NewFrameStore(storage.FrameStoreConfig{
		S3Endpoint:  cfg.S3Endpoint,
		S3Bucket:    cfg.S3Bucket,
		S3AccessKey: cfg.S3AccessKey,
		S3SecretKey: cfg.S3SecretKey,
		S3Region:    cfg.S3Region,
		S3UseSSL:    cfg.S3UseSSL,
		LocalPath:   cfg.LocalFramePath,
	})
	if err != nil {
		slog.Error("frame store init failed", "error", err)
		os.Exit(1)
	}
	defer frameStore.Close()

	deviceStore, err := device.NewStore(device.StoreConfig{DatabaseURL: cfg.DatabaseURL})
	if err != nil {
		slog.Error("device store init failed", "error", err)
		os.Exit(1)
	}
	defer deviceStore.Close()

	inferenceService := inference.NewService(inference.Config{
		OllamaURL:  cfg.OllamaURL,
		Model:      cfg.InferenceModel,
		Confidence: cfg.InferenceThreshold,
		Timeout:    cfg.InferenceTimeout,
	})

	alertService := alerts.NewService(alerts.Config{
		SequenceNowURL: cfg.SequenceNowURL,
		SequenceNowKey: cfg.SequenceNowAPIKey,
		DefaultChannel: "whatsapp",
	})

	pipeline := handlers.NewPipeline(handlers.PipelineConfig{
		FrameStore:   frameStore,
		DeviceStore:  deviceStore,
		Inference:    inferenceService,
		Alerts:       alertService,
		Workers:      cfg.PipelineWorkers,
		QueueSize:    cfg.PipelineQueueSize,
		FrameTimeout: cfg.FrameTimeout,
	})
	pipeline.Start()
	defer pipeline.Stop()

	limiter := ratelimit.New(cfg.RateLimitPerSec, cfg.RateLimitBurst, 30*time.Minute)

	// ---- HTTP routes ----
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/frames/ingest", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.IngestFrame(pipeline, limiter)))

	mux.HandleFunc("POST /v1/devices/register", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.RegisterDevice(deviceStore)))
	mux.HandleFunc("POST /v1/devices/heartbeat", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.DeviceHeartbeat(deviceStore)))

	mux.HandleFunc("GET /v1/ota/check", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.OTACheck(cfg.OTABundlePath)))
	mux.HandleFunc("GET /v1/ota/bundle/{name}", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.OTADownload(cfg.OTABundlePath)))

	mux.HandleFunc("GET /v1/devices", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.ListDevices(deviceStore)))
	mux.HandleFunc("GET /v1/events", handlers.AuthMiddleware(cfg.ValidAPIKeys,
		handlers.ListEvents(deviceStore)))

	// /health: cheap process liveness.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","version":%q}`, version)
	})

	// /healthz: deep health (DB ping + queue depth) for orchestrators.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		dbErr := deviceStore.Ping(ctx)
		status := http.StatusOK
		dbStatus := "ok"
		if dbErr != nil {
			status = http.StatusServiceUnavailable
			dbStatus = dbErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      map[string]string{"db": dbStatus},
			"version":     version,
			"queue_depth": pipeline.Depth(),
			"queue_cap":   cfg.PipelineQueueSize,
		})
	})

	server := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Port),
		Handler:        mux,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// MQTT subscriber (optional)
	var mqttSub *mqtt.Subscriber
	if cfg.MQTTBrokerURL != "" {
		mqttSub, err = mqtt.New(mqtt.Config{
			BrokerURL: cfg.MQTTBrokerURL,
			Topic:     cfg.MQTTTopic,
			Username:  cfg.MQTTUsername,
			Password:  cfg.MQTTPassword,
		}, pipeline)
		if err != nil {
			// Non-fatal: HTTP ingest still works without MQTT.
			slog.Warn("mqtt subscriber init failed; continuing HTTP-only", "error", err)
		} else {
			defer mqttSub.Close()
		}
	}

	go func() {
		slog.Info("server starting", "port", cfg.Port, "version", version)
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

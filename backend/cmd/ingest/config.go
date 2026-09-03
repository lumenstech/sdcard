package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port int
	DatabaseURL string
	S3Endpoint string
	S3Bucket string
	S3AccessKey string
	S3SecretKey string
	S3Region string
	LocalFramePath string
	OllamaURL string
	InferenceModel string
	InferenceThreshold float64
	SequenceNowURL string
	SequenceNowAPIKey string
	PipelineWorkers int
	PipelineQueueSize int
	RateLimitPerMinute int
	ValidAPIKeys []string
	OTABundlePath string
	MQTTBrokerURL string
	MQTTTopic string
}

func LoadConfig() (*Config, error) {
	port, _ := strconv.Atoi(envOr("PORT", "8090"))
	workers, _ := strconv.Atoi(envOr("PIPELINE_WORKERS", "4"))
	queueSize, _ := strconv.Atoi(envOr("PIPELINE_QUEUE_SIZE", "1000"))
	rateLimit, _ := strconv.Atoi(envOr("RATE_LIMIT_PER_MINUTE", "120"))
	threshold, _ := strconv.ParseFloat(envOr("INFERENCE_THRESHOLD", "0.6"), 64)
	cfg := &Config{
		Port: port,
		DatabaseURL: envOr("DATABASE_URL", "postgres://secure4k:secure4k@localhost:5432/sidecar?sslmode=disable"),
		S3Endpoint: envOr("S3_ENDPOINT", "http://localhost:9000"),
		S3Bucket: envOr("S3_BUCKET", "sidecar-frames"),
		S3AccessKey: envOr("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey: envOr("S3_SECRET_KEY", "minioadmin"),
		S3Region: envOr("S3_REGION", "us-east-1"),
		LocalFramePath: envOr("LOCAL_FRAME_PATH", "/tmp/sidecar-frames"),
		OllamaURL: envOr("OLLAMA_URL", "http://localhost:11434"),
		InferenceModel: envOr("INFERENCE_MODEL", "llava"),
		InferenceThreshold: threshold,
		SequenceNowURL: envOr("SEQUENCENOW_URL", "https://api.sequencenow.com"),
		SequenceNowAPIKey: envOr("SEQUENCENOW_API_KEY", ""),
		PipelineWorkers: workers,
		PipelineQueueSize: queueSize,
		RateLimitPerMinute: rateLimit,
		ValidAPIKeys: splitNonEmpty(envOr("VALID_API_KEYS", "")),
		OTABundlePath: envOr("OTA_BUNDLE_PATH", "/opt/sidecar/ota"),
		MQTTBrokerURL: envOr("MQTT_BROKER_URL", "tcp://localhost:1883"),
		MQTTTopic: envOr("MQTT_TOPIC", "secure4k/frames/+"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.ValidAPIKeys) == 0 {
		return nil, fmt.Errorf("VALID_API_KEYS must contain at least one non-empty key")
	}
	return cfg, nil
}

func splitNonEmpty(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

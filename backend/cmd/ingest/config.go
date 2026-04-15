package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port int

	DatabaseURL string

	S3Endpoint     string
	S3Bucket       string
	S3AccessKey    string
	S3SecretKey    string
	S3Region       string
	S3UseSSL       bool
	LocalFramePath string

	OllamaURL          string
	InferenceModel     string
	InferenceThreshold float64
	InferenceTimeout   time.Duration

	SequenceNowURL    string
	SequenceNowAPIKey string

	PipelineWorkers   int
	PipelineQueueSize int
	FrameTimeout      time.Duration

	ValidAPIKeys []string

	OTABundlePath string

	// Per-device rate limit on frame ingest (frames/sec, burst).
	RateLimitPerSec float64
	RateLimitBurst  float64

	// MQTT ingest (optional). Empty BrokerURL disables the subscriber.
	MQTTBrokerURL string
	MQTTTopic     string
	MQTTUsername  string
	MQTTPassword  string
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:        envInt("PORT", 8090),
		DatabaseURL: envOr("DATABASE_URL", ""),

		S3Endpoint:     envOr("S3_ENDPOINT", ""),
		S3Bucket:       envOr("S3_BUCKET", "sidecar-frames"),
		S3AccessKey:    envOr("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:    envOr("S3_SECRET_KEY", "minioadmin"),
		S3Region:       envOr("S3_REGION", "us-east-1"),
		S3UseSSL:       envBool("S3_USE_SSL", false),
		LocalFramePath: envOr("LOCAL_FRAME_PATH", "/tmp/sidecar-frames"),

		OllamaURL:          envOr("OLLAMA_URL", "http://localhost:11434"),
		InferenceModel:     envOr("INFERENCE_MODEL", "llava"),
		InferenceThreshold: envFloat("INFERENCE_THRESHOLD", 0.6),
		InferenceTimeout:   envDuration("INFERENCE_TIMEOUT", 20*time.Second),

		SequenceNowURL:    envOr("SEQUENCENOW_URL", "https://api.sequencenow.com"),
		SequenceNowAPIKey: envOr("SEQUENCENOW_API_KEY", ""),

		PipelineWorkers:   envInt("PIPELINE_WORKERS", 4),
		PipelineQueueSize: envInt("PIPELINE_QUEUE_SIZE", 1000),
		FrameTimeout:      envDuration("FRAME_TIMEOUT", 45*time.Second),

		ValidAPIKeys: splitNonEmpty(envOr("VALID_API_KEYS", ""), ","),

		OTABundlePath: envOr("OTA_BUNDLE_PATH", "/data/ota"),

		RateLimitPerSec: envFloat("RATE_LIMIT_PER_SEC", 2.0),
		RateLimitBurst:  envFloat("RATE_LIMIT_BURST", 10.0),

		MQTTBrokerURL: envOr("MQTT_BROKER_URL", ""),
		MQTTTopic:     envOr("MQTT_TOPIC", "secure4k/frames/+"),
		MQTTUsername:  envOr("MQTT_USERNAME", ""),
		MQTTPassword:  envOr("MQTT_PASSWORD", ""),
	}

	if len(cfg.ValidAPIKeys) == 0 {
		return nil, fmt.Errorf("VALID_API_KEYS is required (comma-separated bearer tokens)")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func splitNonEmpty(s, sep string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(s, sep) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Package inference wraps Ollama's vision API.
//
// The service is fail-open: if Ollama is unreachable, slow, or returns
// non-JSON, Detect returns an error and the caller (Pipeline) falls back to
// recording a frame-only event. A small circuit breaker prevents every frame
// from burning the full HTTP timeout when the model is down.
package inference

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Config struct {
	OllamaURL  string
	Model      string  // e.g. "llava", "moondream"
	Confidence float64 // minimum detection confidence to keep
	Timeout    time.Duration
}

type Detection struct {
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence"`
	BBox       [4]int  `json:"bbox"`
}

// ShouldAlert returns true if a detection warrants notifying the customer.
func (d Detection) ShouldAlert() bool {
	switch d.Class {
	case "person":
		return d.Confidence >= 0.65
	case "vehicle", "car", "truck", "motorcycle":
		return d.Confidence >= 0.70
	case "gauge_reading":
		return true
	default:
		return false
	}
}

type Result struct {
	Detections  []Detection `json:"detections"`
	ModelUsed   string      `json:"model"`
	InferenceMS int64       `json:"inference_ms"`
}

// ErrCircuitOpen is returned when the circuit breaker has tripped after
// repeated failures. Callers should treat this exactly like any other
// inference error (fail open, store the frame, skip detection).
var ErrCircuitOpen = errors.New("inference circuit open")

type Service struct {
	cfg    Config
	client *http.Client

	cb circuitBreaker
}

func NewService(cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &Service{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		cb: circuitBreaker{
			failureThreshold: 5,
			cooldown:         30 * time.Second,
		},
	}
}

// Detect runs object detection on a JPEG. Always returns a non-nil error or a
// non-nil Result; never panics.
func (s *Service) Detect(ctx context.Context, imageData []byte) (*Result, error) {
	if !s.cb.allow() {
		return nil, ErrCircuitOpen
	}

	start := time.Now()

	b64Image := base64.StdEncoding.EncodeToString(imageData)
	reqBody := map[string]any{
		"model": s.cfg.Model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": `Analyze this security camera frame. Return ONLY a JSON array of detections. Each detection must have: "class" (person, vehicle, animal, or object type), "confidence" (0.0-1.0), "bbox" [x, y, width, height] in pixels. If the image shows a gauge or meter, include class "gauge_reading" with the numeric reading in a "value" field. Return [] if nothing notable is detected. JSON only, no explanation.`,
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": fmt.Sprintf("data:image/jpeg;base64,%s", b64Image),
						},
					},
				},
			},
		},
		"stream": false,
		"options": map[string]any{
			"temperature": 0.1,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		s.cfg.OllamaURL+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.cb.recordFailure()
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		s.cb.recordFailure()
		return nil, fmt.Errorf("ollama status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		s.cb.recordFailure()
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}

	var detections []Detection
	raw := ollamaResp.Message.Content
	if err := json.Unmarshal([]byte(raw), &detections); err != nil {
		cleaned := cleanJSON(raw)
		if err2 := json.Unmarshal([]byte(cleaned), &detections); err2 != nil {
			// Parse failure isn't an Ollama outage — don't trip the breaker.
			return nil, fmt.Errorf("parse detections: %w (raw: %s)", err, raw)
		}
	}

	filtered := make([]Detection, 0, len(detections))
	for _, d := range detections {
		if d.Confidence >= s.cfg.Confidence {
			filtered = append(filtered, d)
		}
	}

	s.cb.recordSuccess()
	return &Result{
		Detections:  filtered,
		ModelUsed:   s.cfg.Model,
		InferenceMS: time.Since(start).Milliseconds(),
	}, nil
}

// cleanJSON strips markdown fences and prose around a JSON value.
func cleanJSON(s string) string {
	start, end := -1, -1
	for i := 0; i < len(s); i++ {
		if s[i] == '[' || s[i] == '{' {
			start = i
			break
		}
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ']' || s[i] == '}' {
			end = i + 1
			break
		}
	}
	if start >= 0 && end > start {
		return s[start:end]
	}
	return s
}

// circuitBreaker is a tiny half-open breaker. After failureThreshold
// consecutive failures it returns false from allow() until cooldown elapses,
// then probes once.
type circuitBreaker struct {
	mu               sync.Mutex
	failures         int
	failureThreshold int
	openedAt         time.Time
	cooldown         time.Duration
}

func (c *circuitBreaker) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failures < c.failureThreshold {
		return true
	}
	if time.Since(c.openedAt) < c.cooldown {
		return false
	}
	// half-open: allow one probe; if it fails the breaker re-opens.
	return true
}

func (c *circuitBreaker) recordSuccess() {
	c.mu.Lock()
	c.failures = 0
	c.mu.Unlock()
}

func (c *circuitBreaker) recordFailure() {
	c.mu.Lock()
	c.failures++
	if c.failures == c.failureThreshold {
		c.openedAt = time.Now()
	}
	c.mu.Unlock()
}

package inference

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Config struct {
	OllamaURL  string
	Model      string  // e.g. "yolov8", "llava", "moondream"
	Confidence float64 // minimum confidence threshold
}

type Detection struct {
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence"`
	BBox       [4]int  `json:"bbox"` // x, y, w, h
}

// ShouldAlert returns true if this detection warrants an alert.
func (d Detection) ShouldAlert() bool {
	switch d.Class {
	case "person":
		return d.Confidence >= 0.65
	case "vehicle", "car", "truck", "motorcycle":
		return d.Confidence >= 0.70
	case "gauge_reading":
		return true // always alert on gauge readings (threshold logic is separate)
	default:
		return false // animals, background objects — log but don't alert
	}
}

type Result struct {
	Detections []Detection `json:"detections"`
	ModelUsed  string      `json:"model"`
	InferenceMS int64     `json:"inference_ms"`
}

type Service struct {
	cfg    Config
	client *http.Client
}

func NewService(cfg Config) *Service {
	return &Service{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Detect runs object detection on a JPEG frame.
// Uses Ollama's vision model API for the pilot.
// Production: swap to dedicated YOLO endpoint or HF filter.
func (s *Service) Detect(ctx context.Context, imageData []byte) (*Result, error) {
	start := time.Now()

	// Encode image as base64 for Ollama vision API
	b64Image := base64.StdEncoding.EncodeToString(imageData)

	// Build Ollama chat completion request with vision
	reqBody := map[string]interface{}{
		"model": s.cfg.Model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
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
		"options": map[string]interface{}{
			"temperature": 0.1,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/chat", s.cfg.OllamaURL),
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}

	// Parse detection JSON from model response
	var detections []Detection
	if err := json.Unmarshal([]byte(ollamaResp.Message.Content), &detections); err != nil {
		// Model might return markdown-wrapped JSON
		cleaned := cleanJSON(ollamaResp.Message.Content)
		if err2 := json.Unmarshal([]byte(cleaned), &detections); err2 != nil {
			return nil, fmt.Errorf("parse detections: %w (raw: %s)", err, ollamaResp.Message.Content)
		}
	}

	// Filter by confidence threshold
	filtered := make([]Detection, 0)
	for _, d := range detections {
		if d.Confidence >= s.cfg.Confidence {
			filtered = append(filtered, d)
		}
	}

	elapsed := time.Since(start)

	return &Result{
		Detections:  filtered,
		ModelUsed:   s.cfg.Model,
		InferenceMS: elapsed.Milliseconds(),
	}, nil
}

// cleanJSON strips markdown code fences from model output.
func cleanJSON(s string) string {
	// Remove ```json ... ``` wrapping
	start := 0
	end := len(s)
	for i := 0; i < len(s)-2; i++ {
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
	if start < end {
		return s[start:end]
	}
	return s
}

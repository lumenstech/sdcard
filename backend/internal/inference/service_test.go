package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCleanJSONStripsMarkdownFences(t *testing.T) {
	cases := map[string]string{
		"```json\n[{\"class\":\"x\"}]\n```":         `[{"class":"x"}]`,
		"Sure! Here is your data: [1, 2, 3] enjoy.": "[1, 2, 3]",
		`{"x":1}`: `{"x":1}`,
	}
	for in, want := range cases {
		if got := cleanJSON(in); got != want {
			t.Errorf("cleanJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldAlertMatrix(t *testing.T) {
	tests := []struct {
		class      string
		confidence float64
		want       bool
	}{
		{"person", 0.9, true},
		{"person", 0.5, false},
		{"vehicle", 0.8, true},
		{"vehicle", 0.6, false},
		{"car", 0.71, true},
		{"animal", 0.95, false},
		{"gauge_reading", 0.0, true},
		{"background", 1.0, false},
	}
	for _, tc := range tests {
		d := Detection{Class: tc.class, Confidence: tc.confidence}
		if d.ShouldAlert() != tc.want {
			t.Errorf("ShouldAlert(%s,%.2f)=%v want %v", tc.class, tc.confidence, !tc.want, tc.want)
		}
	}
}

func TestDetectFailOpenWhenOllamaDown(t *testing.T) {
	// Use a closed listener URL so the request fails fast.
	s := NewService(Config{
		OllamaURL:  "http://127.0.0.1:1", // unlikely to be open
		Model:      "llava",
		Confidence: 0.5,
		Timeout:    500 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.Detect(ctx, []byte("not-really-a-jpeg"))
	if err == nil {
		t.Fatal("expected error when Ollama unreachable, got nil")
	}
}

func TestDetectCircuitOpensAfterRepeatedFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewService(Config{
		OllamaURL:  srv.URL,
		Model:      "llava",
		Confidence: 0.5,
		Timeout:    2 * time.Second,
	})
	for i := 0; i < 5; i++ {
		_, _ = s.Detect(context.Background(), []byte("frame"))
	}
	_, err := s.Detect(context.Background(), []byte("frame"))
	if err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen after 5 failures, got %v", err)
	}
}

func TestDetectParsesValidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":{"content":"[{\"class\":\"person\",\"confidence\":0.9,\"bbox\":[0,0,10,10]}]"}}`))
	}))
	defer srv.Close()

	s := NewService(Config{OllamaURL: srv.URL, Model: "llava", Confidence: 0.5, Timeout: 2 * time.Second})
	res, err := s.Detect(context.Background(), []byte("frame"))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(res.Detections) != 1 || res.Detections[0].Class != "person" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

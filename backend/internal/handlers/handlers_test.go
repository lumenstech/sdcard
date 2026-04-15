package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/alerts"
	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/inference"
	"github.com/lumenstech/secure4k-sidecar/internal/ratelimit"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

func newTestPipeline(t *testing.T) (*Pipeline, device.Store, *storage.FrameStore) {
	t.Helper()
	store := device.NewMemoryStore()
	fs, err := storage.NewFrameStore(storage.FrameStoreConfig{
		LocalPath: t.TempDir(),
		DisableS3: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	infer := inference.NewService(inference.Config{
		OllamaURL: "http://127.0.0.1:1",
		Model:     "x",
		Timeout:   100 * time.Millisecond,
	})
	al := alerts.NewService(alerts.Config{}) // no key -> alerts skipped
	p := NewPipeline(PipelineConfig{
		FrameStore:   fs,
		DeviceStore:  store,
		Inference:    infer,
		Alerts:       al,
		Workers:      2,
		QueueSize:    10,
		FrameTimeout: 2 * time.Second,
	})
	p.Start()
	t.Cleanup(func() { p.Stop(); fs.Close() })
	return p, store, fs
}

// ---- Auth middleware ----

func TestAuthMissingHeader(t *testing.T) {
	h := AuthMiddleware([]string{"good"}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestAuthBadToken(t *testing.T) {
	h := AuthMiddleware([]string{"good"}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestAuthEmptyAllowlistRejects(t *testing.T) {
	h := AuthMiddleware([]string{""}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d (empty token must be rejected)", w.Code)
	}
}

func TestAuthGoodToken(t *testing.T) {
	called := false
	h := AuthMiddleware([]string{"good"}, func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer good")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK || !called {
		t.Fatalf("expected pass-through, got %d called=%v", w.Code, called)
	}
}

// ---- Frame ingest ----

func TestIngestRejectsMissingDeviceID(t *testing.T) {
	p, _, _ := newTestPipeline(t)
	r := httptest.NewRequest("POST", "/v1/frames/ingest", bytes.NewReader([]byte("xx")))
	w := httptest.NewRecorder()
	IngestFrame(p, nil)(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}

func TestIngestRejectsEmptyBody(t *testing.T) {
	p, _, _ := newTestPipeline(t)
	r := httptest.NewRequest("POST", "/v1/frames/ingest", nil)
	r.Header.Set("X-Device-ID", "dev_test")
	w := httptest.NewRecorder()
	IngestFrame(p, nil)(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}

func TestIngestAcceptsFrame(t *testing.T) {
	p, _, _ := newTestPipeline(t)
	r := httptest.NewRequest("POST", "/v1/frames/ingest", bytes.NewReader([]byte("jpeg-bytes")))
	r.Header.Set("X-Device-ID", "dev_test")
	r.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	w := httptest.NewRecorder()
	IngestFrame(p, nil)(w, r)
	if w.Code != http.StatusAccepted {
		body, _ := io.ReadAll(w.Body)
		t.Fatalf("got %d: %s", w.Code, body)
	}
}

func TestIngestRespectsRateLimit(t *testing.T) {
	p, _, _ := newTestPipeline(t)
	rl := ratelimit.New(1, 1, time.Minute)
	mk := func() *http.Request {
		r := httptest.NewRequest("POST", "/v1/frames/ingest", bytes.NewReader([]byte("x")))
		r.Header.Set("X-Device-ID", "dev_test")
		return r
	}
	w1 := httptest.NewRecorder()
	IngestFrame(p, rl)(w1, mk())
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call should pass: %d", w1.Code)
	}
	w2 := httptest.NewRecorder()
	IngestFrame(p, rl)(w2, mk())
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second call should rate-limit: %d", w2.Code)
	}
}

// ---- Device registration idempotency ----

func TestRegisterIsIdempotentByMAC(t *testing.T) {
	store := device.NewMemoryStore()
	body := func() io.Reader {
		b, _ := json.Marshal(RegisterRequest{MAC: "ab:cd"})
		return bytes.NewReader(b)
	}
	r1 := httptest.NewRequest("POST", "/v1/devices/register", body())
	w1 := httptest.NewRecorder()
	RegisterDevice(store)(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first register: %d", w1.Code)
	}

	r2 := httptest.NewRequest("POST", "/v1/devices/register", body())
	w2 := httptest.NewRecorder()
	RegisterDevice(store)(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second register expected 200, got %d", w2.Code)
	}

	devs, _ := store.List(context.Background())
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
}

// ---- Pipeline dedup integration ----

func TestPipelineDeduplicatesEvents(t *testing.T) {
	p, store, _ := newTestPipeline(t)
	dev, _ := store.Create(context.Background(), &device.Device{MAC: "aa:99"})

	ts := time.Unix(1700000999, 0).UTC()
	for i := 0; i < 3; i++ {
		err := p.Submit(&FrameJob{
			DeviceID:   dev.ID,
			Timestamp:  ts,
			ImageData:  []byte("frame"),
			ReceivedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	// Wait for the workers to drain.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p.Depth() == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	events, _ := store.ListEvents(context.Background(), dev.ID, 10)
	if len(events) != 1 {
		t.Fatalf("expected exactly one event after dedup, got %d", len(events))
	}
}

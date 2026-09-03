package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/alerts"
	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/inference"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

func TestPipelinePersistsOnceWhenInferenceUnavailable(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	store, err := device.NewStore(device.StoreConfig{DatabaseURL: dbURL})
	if err != nil { t.Fatal(err) }
	defer store.Close()

	mac := fmt.Sprintf("02:30:00:%02x:%02x:%02x", time.Now().UnixNano()&0xff, (time.Now().UnixNano()>>8)&0xff, (time.Now().UnixNano()>>16)&0xff)
	dev, err := store.Create(ctx, &device.Device{MAC: mac, RegisteredAt: time.Now().UTC()})
	if err != nil { t.Fatal(err) }

	frames, err := storage.NewFrameStore(storage.FrameStoreConfig{LocalPath: t.TempDir()})
	if err != nil { t.Fatal(err) }
	defer frames.Close()
	infer := inference.NewService(inference.Config{OllamaURL: "http://127.0.0.1:1", Model: "llava", Confidence: 0.5})
	alert := alerts.NewService(alerts.Config{})
	p := NewPipeline(PipelineConfig{FrameStore: frames, DeviceStore: store, Inference: infer, Alerts: alert, Workers: 1, QueueSize: 4, RateLimitPerMinute: 10})
	p.Start()

	ts := time.Now().UTC().Truncate(time.Second)
	job := &FrameJob{DeviceID: dev.ID, Timestamp: ts, ImageData: []byte("pilot-frame"), ReceivedAt: time.Now().UTC()}
	if err := p.Submit(ctx, job); err != nil { t.Fatalf("first submit: %v", err) }
	if err := p.Submit(ctx, job); !errors.Is(err, ErrDuplicate) { t.Fatalf("duplicate submit=%v want ErrDuplicate", err) }
	p.Stop()

	events, err := store.ListEvents(ctx, dev.ID, 10)
	if err != nil { t.Fatal(err) }
	if len(events) != 1 { t.Fatalf("events=%d want 1", len(events)) }
	if events[0].Type != "frame" { t.Fatalf("event type=%s want frame", events[0].Type) }
	if events[0].FrameKey == "" { t.Fatal("frame key was not persisted") }
	got, err := frames.Retrieve(ctx, events[0].FrameKey)
	if err != nil { t.Fatal(err) }
	if string(got) != "pilot-frame" { t.Fatalf("stored frame=%q", string(got)) }
}

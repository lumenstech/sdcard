package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/alerts"
	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/inference"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

func testStoreAndDevice(t *testing.T, prefix string) (*device.Store, *device.Device) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := device.NewStore(device.StoreConfig{DatabaseURL: dbURL})
	if err != nil { t.Fatal(err) }
	t.Cleanup(store.Close)
	n := time.Now().UnixNano()
	mac := fmt.Sprintf("02:%s:00:%02x:%02x:%02x", prefix, n&0xff, (n>>8)&0xff, (n>>16)&0xff)
	dev, err := store.Create(context.Background(), &device.Device{MAC: mac, RegisteredAt: time.Now().UTC()})
	if err != nil { t.Fatal(err) }
	return store, dev
}

func TestPipelinePersistsOnceWhenInferenceUnavailable(t *testing.T) {
	ctx := context.Background()
	store, dev := testStoreAndDevice(t, "30")
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

func TestPipelineReleasesClaimWhenStorageFails(t *testing.T) {
	ctx := context.Background()
	store, dev := testStoreAndDevice(t, "31")
	root := filepath.Join(t.TempDir(), "frames")
	frames, err := storage.NewFrameStore(storage.FrameStoreConfig{LocalPath: root})
	if err != nil { t.Fatal(err) }
	defer frames.Close()
	if err := os.RemoveAll(root); err != nil { t.Fatal(err) }
	if err := os.WriteFile(root, []byte("block-directory-creation"), 0600); err != nil { t.Fatal(err) }

	p := NewPipeline(PipelineConfig{
		FrameStore: frames, DeviceStore: store,
		Inference: inference.NewService(inference.Config{OllamaURL: "http://127.0.0.1:1", Model: "llava"}),
		Alerts: alerts.NewService(alerts.Config{}), Workers: 1, QueueSize: 2, RateLimitPerMinute: 10,
	})
	p.Start()
	ts := time.Now().UTC().Truncate(time.Second)
	if err := p.Submit(ctx, &FrameJob{DeviceID: dev.ID, Timestamp: ts, ImageData: []byte("retry-me"), ReceivedAt: time.Now().UTC()}); err != nil { t.Fatal(err) }
	p.Stop()

	claimed, err := store.ClaimFrame(ctx, dev.ID, ts)
	if err != nil { t.Fatal(err) }
	if !claimed { t.Fatal("storage failure left frame receipt claimed; retry would be lost") }
}

package device

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	return NewMemoryStore()
}

func TestCreateAndFindRoundtrip(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	dev, err := s.Create(ctx, &Device{MAC: "aa:01"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if dev.ID == "" {
		t.Fatal("expected ID assigned")
	}
	got, err := s.FindByID(ctx, dev.ID)
	if err != nil || got.MAC != "aa:01" {
		t.Fatalf("FindByID: %+v err=%v", got, err)
	}
	got2, err := s.FindByMAC(ctx, "aa:01")
	if err != nil || got2.ID != dev.ID {
		t.Fatalf("FindByMAC: %+v err=%v", got2, err)
	}
}

func TestDuplicateMACReturnsExisting(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	a, _ := s.Create(ctx, &Device{MAC: "aa:02"})
	b, err := s.Create(ctx, &Device{MAC: "aa:02"})
	if err != nil {
		t.Fatalf("second create err: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("expected same id, got %s vs %s", a.ID, b.ID)
	}
}

func TestListEventsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	d, _ := s.Create(ctx, &Device{MAC: "aa:03"})
	for i := 0; i < 5; i++ {
		ts := time.Unix(int64(1700000000+i), 0)
		if err := s.CreateEvent(ctx, &Event{
			DeviceID:  d.ID,
			FrameKey:  "k",
			Type:      "frame",
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}
	events, err := s.ListEvents(ctx, d.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i-1].Timestamp.Before(events[i].Timestamp) {
			t.Fatalf("not newest-first at index %d", i)
		}
	}
}

func TestEventDeduplication(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()
	d, _ := s.Create(ctx, &Device{MAC: "aa:04"})
	ts := time.Unix(1700000123, 0)

	first := &Event{DeviceID: d.ID, FrameKey: "k", Type: "detection", Class: "person", Timestamp: ts}
	if err := s.CreateEvent(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	dupErr := s.CreateEvent(ctx, &Event{DeviceID: d.ID, FrameKey: "k", Type: "detection", Class: "person", Timestamp: ts})
	if dupErr != ErrDuplicateEvent {
		t.Fatalf("expected ErrDuplicateEvent, got %v", dupErr)
	}
	events, _ := s.ListEvents(ctx, d.ID, 10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

// TestConcurrentAccess proves the store is safe for the pipeline workers.
// Run with `go test -race`.
func TestConcurrentAccess(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()
	d, _ := s.Create(ctx, &Device{MAC: "aa:05"})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				ts := time.Unix(int64(1700000000+i*100+j), 0)
				_ = s.CreateEvent(ctx, &Event{
					DeviceID: d.ID, FrameKey: "k", Type: "frame", Timestamp: ts,
				})
				_, _ = s.FindByID(ctx, d.ID)
				_, _ = s.List(ctx)
			}
		}(i)
	}
	wg.Wait()
}

func TestHeartbeatUpdatesLastSeen(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()
	d, _ := s.Create(ctx, &Device{MAC: "aa:06"})

	hbTime := time.Unix(1700000999, 0).UTC()
	if err := s.UpdateHeartbeat(ctx, d.ID, &Heartbeat{Timestamp: hbTime, Version: "0.2.0"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, _ := s.FindByID(ctx, d.ID)
	if !got.LastHeartbeat.Equal(hbTime) {
		t.Fatalf("last_heartbeat mismatch: got %v want %v", got.LastHeartbeat, hbTime)
	}
	if got.SidecarVersion != "0.2.0" {
		t.Fatalf("expected version updated")
	}
	if got.Status != "online" {
		t.Fatalf("expected status online")
	}
}

func TestHeartbeatUnknownDevice(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	if err := s.UpdateHeartbeat(context.Background(), "nope", &Heartbeat{}); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

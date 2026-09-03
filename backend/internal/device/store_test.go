package device

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func integrationStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := NewStore(StoreConfig{DatabaseURL: url})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

func TestCreateIsIdempotentByMAC(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	mac := fmt.Sprintf("02:00:00:%02x:%02x:%02x", time.Now().UnixNano()&0xff, (time.Now().UnixNano()>>8)&0xff, (time.Now().UnixNano()>>16)&0xff)
	first, err := store.Create(ctx, &Device{MAC: mac, ChipInfo: "t31", SidecarVersion: "0.1.0", RegisteredAt: time.Now().UTC()})
	if err != nil { t.Fatal(err) }
	second, err := store.Create(ctx, &Device{MAC: mac, ChipInfo: "t31", SidecarVersion: "0.1.1", RegisteredAt: time.Now().UTC()})
	if err != nil { t.Fatal(err) }
	if first.ID != second.ID { t.Fatalf("duplicate registration created new ID: %s != %s", first.ID, second.ID) }
	found, err := store.FindByMAC(ctx, mac)
	if err != nil { t.Fatal(err) }
	if found.ID != first.ID { t.Fatalf("FindByMAC got %s want %s", found.ID, first.ID) }
}

func TestFrameClaimIsIdempotent(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	mac := fmt.Sprintf("02:10:00:%02x:%02x:%02x", time.Now().UnixNano()&0xff, (time.Now().UnixNano()>>8)&0xff, (time.Now().UnixNano()>>16)&0xff)
	dev, err := store.Create(ctx, &Device{MAC: mac, RegisteredAt: time.Now().UTC()})
	if err != nil { t.Fatal(err) }
	ts := time.Now().UTC().Truncate(time.Second)
	claimed, err := store.ClaimFrame(ctx, dev.ID, ts)
	if err != nil || !claimed { t.Fatalf("first claim = %v, %v", claimed, err) }
	claimed, err = store.ClaimFrame(ctx, dev.ID, ts)
	if err != nil { t.Fatal(err) }
	if claimed { t.Fatal("duplicate frame was claimed twice") }
	if err := store.ReleaseFrame(ctx, dev.ID, ts); err != nil { t.Fatal(err) }
	claimed, err = store.ClaimFrame(ctx, dev.ID, ts)
	if err != nil || !claimed { t.Fatalf("claim after release = %v, %v", claimed, err) }
}

func TestEventsNewestFirstAndDeduplicated(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	mac := fmt.Sprintf("02:20:00:%02x:%02x:%02x", time.Now().UnixNano()&0xff, (time.Now().UnixNano()>>8)&0xff, (time.Now().UnixNano()>>16)&0xff)
	dev, err := store.Create(ctx, &Device{MAC: mac, RegisteredAt: time.Now().UTC()})
	if err != nil { t.Fatal(err) }
	base := time.Now().UTC().Add(-time.Minute)
	old := &Event{DeviceID: dev.ID, FrameKey: "old", Type: "frame", Timestamp: base, ReceivedAt: base}
	newer := &Event{DeviceID: dev.ID, FrameKey: "new", Type: "frame", Timestamp: base.Add(time.Second), ReceivedAt: base.Add(time.Second)}
	if err := store.CreateEvent(ctx, old); err != nil { t.Fatal(err) }
	if err := store.CreateEvent(ctx, old); err != nil { t.Fatal(err) }
	if err := store.CreateEvent(ctx, newer); err != nil { t.Fatal(err) }
	events, err := store.ListEvents(ctx, dev.ID, 10)
	if err != nil { t.Fatal(err) }
	if len(events) != 2 { t.Fatalf("got %d events want 2", len(events)) }
	if events[0].FrameKey != "new" { t.Fatalf("newest event first = %s", events[0].FrameKey) }
}

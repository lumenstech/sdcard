package device

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// memoryStore is an in-process Store implementation used for tests and as a
// dev fallback. All access is guarded by a single RWMutex; this is safe for
// concurrent pipeline workers but is NOT durable — restart loses state.
type memoryStore struct {
	mu      sync.RWMutex
	devices map[string]*Device // keyed by ID
	byMAC   map[string]*Device // keyed by MAC
	events  []*Event
	dedup   map[string]struct{} // (device_id|ts|type|class) -> exists
}

// NewMemoryStore returns an in-memory Store. Safe for concurrent use.
func NewMemoryStore() Store {
	return &memoryStore{
		devices: make(map[string]*Device),
		byMAC:   make(map[string]*Device),
		events:  make([]*Event, 0, 1024),
		dedup:   make(map[string]struct{}),
	}
}

func (s *memoryStore) Close() {}

func (s *memoryStore) Ping(ctx context.Context) error { return nil }

func (s *memoryStore) Create(ctx context.Context, dev *Device) (*Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Idempotent on MAC: if a device with this MAC already exists, return it.
	if existing, ok := s.byMAC[dev.MAC]; ok {
		return existing, nil
	}

	if dev.ID == "" {
		dev.ID = fmt.Sprintf("dev_%s", uuid.New().String()[:8])
	}
	if dev.Status == "" {
		dev.Status = "online"
	}
	if dev.AlertChannel == "" {
		dev.AlertChannel = "whatsapp"
	}
	if dev.RegisteredAt.IsZero() {
		dev.RegisteredAt = time.Now().UTC()
	}
	if dev.LastHeartbeat.IsZero() {
		dev.LastHeartbeat = dev.RegisteredAt
	}

	s.devices[dev.ID] = dev
	s.byMAC[dev.MAC] = dev
	return dev, nil
}

func (s *memoryStore) FindByMAC(ctx context.Context, mac string) (*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dev, ok := s.byMAC[mac]
	if !ok {
		return nil, ErrNotFound
	}
	return dev, nil
}

func (s *memoryStore) FindByID(ctx context.Context, id string) (*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dev, ok := s.devices[id]
	if !ok {
		return nil, ErrNotFound
	}
	return dev, nil
}

func (s *memoryStore) List(ctx context.Context) ([]*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *memoryStore) UpdateHeartbeat(ctx context.Context, deviceID string, hb *Heartbeat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, ok := s.devices[deviceID]
	if !ok {
		return ErrNotFound
	}
	if !hb.Timestamp.IsZero() {
		dev.LastHeartbeat = hb.Timestamp
	} else {
		dev.LastHeartbeat = time.Now().UTC()
	}
	if hb.Version != "" {
		dev.SidecarVersion = hb.Version
	}
	dev.Status = "online"
	return nil
}

func eventDedupKey(deviceID string, ts time.Time, typ, class string) string {
	return fmt.Sprintf("%s|%d|%s|%s", deviceID, ts.UTC().Unix(), typ, class)
}

func (s *memoryStore) CreateEvent(ctx context.Context, event *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := eventDedupKey(event.DeviceID, event.Timestamp, event.Type, event.Class)
	if _, exists := s.dedup[key]; exists {
		return ErrDuplicateEvent
	}
	s.dedup[key] = struct{}{}

	if event.ID == "" {
		event.ID = fmt.Sprintf("evt_%s", uuid.New().String()[:8])
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	}
	s.events = append(s.events, event)

	// Bound memory: keep last 10k events for the pilot.
	if len(s.events) > 10000 {
		drop := s.events[:len(s.events)-10000]
		for _, ev := range drop {
			delete(s.dedup, eventDedupKey(ev.DeviceID, ev.Timestamp, ev.Type, ev.Class))
		}
		s.events = s.events[len(s.events)-10000:]
	}
	return nil
}

func (s *memoryStore) ListEvents(ctx context.Context, deviceID string, limit int) ([]*Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Event, 0, limit)
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		if deviceID == "" || s.events[i].DeviceID == deviceID {
			out = append(out, s.events[i])
		}
	}
	return out, nil
}

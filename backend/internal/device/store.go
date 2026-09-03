package device

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Device represents a registered sidecar camera.
type Device struct {
	ID             string    `json:"id"`
	MAC            string    `json:"mac"`
	Label          string    `json:"label"`
	ChipInfo       string    `json:"chip_info"`
	Kernel         string    `json:"kernel"`
	MemKB          int       `json:"mem_kb"`
	SidecarVersion string    `json:"sidecar_version"`
	Status         string    `json:"status"` // online, offline, degraded
	RegisteredAt   time.Time `json:"registered_at"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`

	// Owner info (set via dashboard)
	OwnerID    string `json:"owner_id,omitempty"`
	OwnerPhone string `json:"owner_phone,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`

	// Alert preferences
	AlertChannel string `json:"alert_channel"` // whatsapp, sms, email, slack
	AlertPhone   string `json:"alert_phone,omitempty"`
	AlertSlack   string `json:"alert_slack_webhook,omitempty"`
}

// Heartbeat represents periodic health data from a device.
type Heartbeat struct {
	Timestamp      time.Time `json:"timestamp"`
	MemFreeKB      int       `json:"mem_free_kb"`
	DiskPct        int       `json:"disk_pct"`
	UptimeS        int       `json:"uptime_s"`
	BufferedFrames int       `json:"buffered_frames"`
	TempMC         int       `json:"temp_mc"`
	Version        string    `json:"version"`
}

// Event represents a detection or frame event.
type Event struct {
	ID          string    `json:"id"`
	DeviceID    string    `json:"device_id"`
	FrameKey    string    `json:"frame_key"`
	Type        string    `json:"type"`       // frame, detection, alert
	Class       string    `json:"class"`      // person, vehicle, animal, gauge_reading
	Confidence  float64   `json:"confidence"`
	BBox        [4]int    `json:"bbox"`       // x, y, w, h
	Metadata    string    `json:"metadata"`   // JSON blob for gauge readings etc.
	Timestamp   time.Time `json:"timestamp"`
	ReceivedAt  time.Time `json:"received_at"`
	ProcessedAt time.Time `json:"processed_at"`
	AlertSent   bool      `json:"alert_sent"`
}

// StoreConfig holds database connection info.
type StoreConfig struct {
	DatabaseURL string
}

// Store manages devices and events.
// Pilot implementation uses in-memory maps.
// Production: swap to pgx/pgxpool.
type Store struct {
	cfg     StoreConfig
	devices map[string]*Device   // keyed by ID
	byMAC   map[string]*Device   // keyed by MAC
	events  []*Event
	// TODO: Replace with *pgxpool.Pool for production
}

func NewStore(cfg StoreConfig) (*Store, error) {
	// TODO: Connect to PostgreSQL
	// pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	return &Store{
		cfg:     cfg,
		devices: make(map[string]*Device),
		byMAC:   make(map[string]*Device),
		events:  make([]*Event, 0),
	}, nil
}

func (s *Store) Close() {
	// TODO: pool.Close()
}

func (s *Store) Create(ctx context.Context, dev *Device) (*Device, error) {
	dev.ID = fmt.Sprintf("dev_%s", uuid.New().String()[:8])
	s.devices[dev.ID] = dev
	s.byMAC[dev.MAC] = dev
	return dev, nil
}

func (s *Store) FindByMAC(ctx context.Context, mac string) (*Device, error) {
	dev, ok := s.byMAC[mac]
	if !ok {
		return nil, fmt.Errorf("device not found: mac=%s", mac)
	}
	return dev, nil
}

func (s *Store) FindByID(ctx context.Context, id string) (*Device, error) {
	dev, ok := s.devices[id]
	if !ok {
		return nil, fmt.Errorf("device not found: id=%s", id)
	}
	return dev, nil
}

func (s *Store) List(ctx context.Context) ([]*Device, error) {
	result := make([]*Device, 0, len(s.devices))
	for _, dev := range s.devices {
		result = append(result, dev)
	}
	return result, nil
}

func (s *Store) UpdateHeartbeat(ctx context.Context, deviceID string, hb *Heartbeat) error {
	dev, ok := s.devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	dev.LastHeartbeat = hb.Timestamp
	dev.SidecarVersion = hb.Version
	dev.Status = "online"
	return nil
}

func (s *Store) CreateEvent(ctx context.Context, event *Event) error {
	event.ID = fmt.Sprintf("evt_%s", uuid.New().String()[:8])
	s.events = append(s.events, event)
	// Keep last 10000 events in memory for pilot
	if len(s.events) > 10000 {
		s.events = s.events[len(s.events)-10000:]
	}
	return nil
}

func (s *Store) ListEvents(ctx context.Context, deviceID string, limit int) ([]*Event, error) {
	result := make([]*Event, 0)
	// Walk backwards for most recent first
	for i := len(s.events) - 1; i >= 0 && len(result) < limit; i-- {
		if deviceID == "" || s.events[i].DeviceID == deviceID {
			result = append(result, s.events[i])
		}
	}
	return result, nil
}

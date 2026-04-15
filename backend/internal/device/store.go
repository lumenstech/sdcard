// Package device defines the persistence model for sidecar cameras and the
// events they produce. The Store interface has two implementations:
//   - pgxStore: PostgreSQL via pgx/v5, used in production and Docker Compose.
//   - memoryStore: in-process map, used in tests and as a dev fallback when
//     no DATABASE_URL is provided.
package device

import (
	"context"
	"errors"
	"time"
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

	OwnerID    string `json:"owner_id,omitempty"`
	OwnerPhone string `json:"owner_phone,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`

	AlertChannel string `json:"alert_channel"`
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
	Type        string    `json:"type"`
	Class       string    `json:"class"`
	Confidence  float64   `json:"confidence"`
	BBox        [4]int    `json:"bbox"`
	Metadata    string    `json:"metadata"`
	Timestamp   time.Time `json:"timestamp"`
	ReceivedAt  time.Time `json:"received_at"`
	ProcessedAt time.Time `json:"processed_at"`
	AlertSent   bool      `json:"alert_sent"`
}

// ErrNotFound is returned when a device or event lookup misses.
var ErrNotFound = errors.New("device or event not found")

// ErrDuplicateEvent is returned by CreateEvent when the (device_id, timestamp,
// type, class) tuple already exists. Callers should treat this as success
// (idempotent retry from a sidecar) and NOT trigger a duplicate alert.
var ErrDuplicateEvent = errors.New("duplicate event")

// StoreConfig holds connection info for the persistent store.
type StoreConfig struct {
	DatabaseURL string
}

// Store is the persistence interface every backend (pgx, memory) satisfies.
type Store interface {
	Create(ctx context.Context, dev *Device) (*Device, error)
	FindByMAC(ctx context.Context, mac string) (*Device, error)
	FindByID(ctx context.Context, id string) (*Device, error)
	List(ctx context.Context) ([]*Device, error)
	UpdateHeartbeat(ctx context.Context, deviceID string, hb *Heartbeat) error
	CreateEvent(ctx context.Context, event *Event) error
	ListEvents(ctx context.Context, deviceID string, limit int) ([]*Event, error)
	Ping(ctx context.Context) error
	Close()
}

// NewStore returns a pgx-backed Store. If DatabaseURL is empty the function
// returns an in-memory Store and logs a warning. The fallback exists so the
// pilot can boot in dev mode without a database — production deployments MUST
// provide DATABASE_URL.
func NewStore(cfg StoreConfig) (Store, error) {
	if cfg.DatabaseURL == "" {
		return NewMemoryStore(), nil
	}
	return NewPgStore(cfg.DatabaseURL)
}

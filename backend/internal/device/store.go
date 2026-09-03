package device

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	Status         string    `json:"status"`
	RegisteredAt   time.Time `json:"registered_at"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	OwnerID        string    `json:"owner_id,omitempty"`
	OwnerPhone     string    `json:"owner_phone,omitempty"`
	OwnerEmail     string    `json:"owner_email,omitempty"`
	AlertChannel   string    `json:"alert_channel"`
	AlertPhone     string    `json:"alert_phone,omitempty"`
	AlertSlack     string    `json:"alert_slack_webhook,omitempty"`
}

type Heartbeat struct {
	Timestamp      time.Time `json:"timestamp"`
	MemFreeKB      int       `json:"mem_free_kb"`
	DiskPct        int       `json:"disk_pct"`
	UptimeS        int       `json:"uptime_s"`
	BufferedFrames int       `json:"buffered_frames"`
	TempMC         int       `json:"temp_mc"`
	Version        string    `json:"version"`
}

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

type StoreConfig struct {
	DatabaseURL string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(cfg StoreConfig) (*Store, error) {
	if cfg.DatabaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

const deviceColumns = `id, mac, label, chip_info, kernel, mem_kb, sidecar_version,
status, registered_at, last_heartbeat, owner_id, owner_phone, owner_email,
alert_channel, alert_phone, alert_slack`

func scanDevice(row pgx.Row) (*Device, error) {
	var d Device
	if err := row.Scan(
		&d.ID, &d.MAC, &d.Label, &d.ChipInfo, &d.Kernel, &d.MemKB,
		&d.SidecarVersion, &d.Status, &d.RegisteredAt, &d.LastHeartbeat,
		&d.OwnerID, &d.OwnerPhone, &d.OwnerEmail, &d.AlertChannel,
		&d.AlertPhone, &d.AlertSlack,
	); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) Create(ctx context.Context, dev *Device) (*Device, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO devices (mac, chip_info, kernel, mem_kb, sidecar_version, status, registered_at, last_heartbeat)
		VALUES ($1,$2,$3,$4,$5,'online',COALESCE(NULLIF($6, '0001-01-01'::timestamptz), now()),now())
		ON CONFLICT (mac) DO UPDATE SET
			chip_info = EXCLUDED.chip_info,
			kernel = EXCLUDED.kernel,
			mem_kb = EXCLUDED.mem_kb,
			sidecar_version = EXCLUDED.sidecar_version,
			status = 'online',
			last_heartbeat = now(),
			updated_at = now()
		RETURNING `+deviceColumns,
		dev.MAC, dev.ChipInfo, dev.Kernel, dev.MemKB, dev.SidecarVersion, dev.RegisteredAt,
	)
	created, err := scanDevice(row)
	if err != nil {
		return nil, fmt.Errorf("upsert device: %w", err)
	}
	return created, nil
}

func (s *Store) FindByMAC(ctx context.Context, mac string) (*Device, error) {
	d, err := scanDevice(s.pool.QueryRow(ctx, `SELECT `+deviceColumns+` FROM devices WHERE mac=$1`, mac))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("device not found: mac=%s", mac)
	}
	if err != nil {
		return nil, fmt.Errorf("find device by mac: %w", err)
	}
	return d, nil
}

func (s *Store) FindByID(ctx context.Context, id string) (*Device, error) {
	d, err := scanDevice(s.pool.QueryRow(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("device not found: id=%s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("find device by id: %w", err)
	}
	return d, nil
}

func (s *Store) List(ctx context.Context) ([]*Device, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deviceColumns+` FROM devices ORDER BY registered_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	out := make([]*Device, 0)
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateHeartbeat(ctx context.Context, deviceID string, hb *Heartbeat) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin heartbeat tx: %w", err)
	}
	defer tx.Rollback(ctx)
	stamp := hb.Timestamp
	if stamp.IsZero() {
		stamp = time.Now().UTC()
	}
	cmd, err := tx.Exec(ctx, `UPDATE devices SET last_heartbeat=$2, sidecar_version=$3, status='online', updated_at=now() WHERE id=$1`, deviceID, stamp, hb.Version)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}
	if cmd.RowsAffected() != 1 {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	_, err = tx.Exec(ctx, `INSERT INTO heartbeats (device_id,timestamp,mem_free_kb,disk_pct,uptime_s,buffered_frames,temp_mc,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		deviceID, stamp, hb.MemFreeKB, hb.DiskPct, hb.UptimeS, hb.BufferedFrames, hb.TempMC, hb.Version)
	if err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}
	return tx.Commit(ctx)
}

// ClaimFrame durably records an ingest receipt. false means the same device/timestamp
// was already accepted and the caller should treat it as a successful duplicate.
func (s *Store) ClaimFrame(ctx context.Context, deviceID string, ts time.Time) (bool, error) {
	cmd, err := s.pool.Exec(ctx, `INSERT INTO frame_receipts (device_id,timestamp) VALUES ($1,$2) ON CONFLICT DO NOTHING`, deviceID, ts)
	if err != nil {
		return false, fmt.Errorf("claim frame: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (s *Store) CreateEvent(ctx context.Context, event *Event) error {
	metadata := event.Metadata
	if metadata == "" {
		metadata = "{}"
	}
	bbox := []int32{int32(event.BBox[0]), int32(event.BBox[1]), int32(event.BBox[2]), int32(event.BBox[3])}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO events (device_id,frame_key,type,class,confidence,bbox,metadata,timestamp,received_at,processed_at,alert_sent)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,NULLIF($10,'0001-01-01'::timestamptz),$11)
		ON CONFLICT (device_id, frame_key, type, class) DO NOTHING`,
		event.DeviceID, event.FrameKey, event.Type, event.Class, event.Confidence,
		bbox, metadata, event.Timestamp, event.ReceivedAt, event.ProcessedAt, event.AlertSent,
	)
	if err != nil {
		return fmt.Errorf("create event: %w", err)
	}
	return nil
}

func (s *Store) ListEvents(ctx context.Context, deviceID string, limit int) ([]*Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id,device_id,frame_key,type,class,confidence,bbox,metadata,timestamp,received_at,processed_at,alert_sent FROM events`
	args := []any{}
	if deviceID != "" {
		query += ` WHERE device_id=$1 ORDER BY timestamp DESC, received_at DESC LIMIT $2`
		args = append(args, deviceID, limit)
	} else {
		query += ` ORDER BY timestamp DESC, received_at DESC LIMIT $1`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := make([]*Event, 0)
	for rows.Next() {
		var e Event
		var bbox []int32
		var metadata []byte
		var processedAt *time.Time
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.FrameKey, &e.Type, &e.Class, &e.Confidence, &bbox, &metadata, &e.Timestamp, &e.ReceivedAt, &processedAt, &e.AlertSent); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		for i := 0; i < len(bbox) && i < 4; i++ {
			e.BBox[i] = int(bbox[i])
		}
		e.Metadata = string(metadata)
		if processedAt != nil {
			e.ProcessedAt = *processedAt
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

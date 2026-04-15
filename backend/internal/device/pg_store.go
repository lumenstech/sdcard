package device

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxStore is the production Postgres-backed Store.
//
// Concurrency: pgxpool is safe for concurrent use; we never mutate shared state
// beyond the pool itself.
//
// Idempotency:
//   - devices.mac is UNIQUE; Create translates a unique-violation into "return
//     existing device" so registration retries are idempotent.
//   - events have a deterministic id derived from (device_id, ts, type, class)
//     and an INSERT ... ON CONFLICT DO NOTHING; duplicate frame uploads cannot
//     produce duplicate events or duplicate alerts.
type pgxStore struct {
	pool *pgxpool.Pool
}

const pgConnectTimeout = 10 * time.Second
const pgQueryTimeout = 5 * time.Second

// NewPgStore opens a pgxpool against the given URL and verifies connectivity.
// Returns an error if the database is unreachable; the caller should fail
// startup loud rather than silently accept data loss.
func NewPgStore(databaseURL string) (Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), pgConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &pgxStore{pool: pool}, nil
}

func (s *pgxStore) Close() {
	s.pool.Close()
}

func (s *pgxStore) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()
	return s.pool.Ping(ctx)
}

func (s *pgxStore) Create(ctx context.Context, dev *Device) (*Device, error) {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	if dev.ID == "" {
		dev.ID = "dev_" + uuid.New().String()[:8]
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

	const q = `
INSERT INTO devices
  (id, mac, label, chip_info, kernel, mem_kb, sidecar_version, status,
   registered_at, last_heartbeat, owner_id, owner_phone, owner_email,
   alert_channel, alert_phone, alert_slack)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (mac) DO UPDATE
  SET last_heartbeat = EXCLUDED.last_heartbeat,
      sidecar_version = EXCLUDED.sidecar_version,
      status = 'online'
RETURNING id, mac, label, chip_info, kernel, mem_kb, sidecar_version, status,
          registered_at, last_heartbeat, owner_id, owner_phone, owner_email,
          alert_channel, alert_phone, alert_slack`

	row := s.pool.QueryRow(ctx, q,
		dev.ID, dev.MAC, dev.Label, dev.ChipInfo, dev.Kernel, dev.MemKB,
		dev.SidecarVersion, dev.Status, dev.RegisteredAt, dev.LastHeartbeat,
		dev.OwnerID, dev.OwnerPhone, dev.OwnerEmail,
		dev.AlertChannel, dev.AlertPhone, dev.AlertSlack,
	)
	out := &Device{}
	if err := row.Scan(
		&out.ID, &out.MAC, &out.Label, &out.ChipInfo, &out.Kernel, &out.MemKB,
		&out.SidecarVersion, &out.Status, &out.RegisteredAt, &out.LastHeartbeat,
		&out.OwnerID, &out.OwnerPhone, &out.OwnerEmail,
		&out.AlertChannel, &out.AlertPhone, &out.AlertSlack,
	); err != nil {
		return nil, fmt.Errorf("insert device: %w", err)
	}
	return out, nil
}

func (s *pgxStore) FindByMAC(ctx context.Context, mac string) (*Device, error) {
	return s.findOne(ctx, "mac", mac)
}

func (s *pgxStore) FindByID(ctx context.Context, id string) (*Device, error) {
	return s.findOne(ctx, "id", id)
}

func (s *pgxStore) findOne(ctx context.Context, field, value string) (*Device, error) {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	q := `SELECT id, mac, label, chip_info, kernel, mem_kb, sidecar_version,
                 status, registered_at, last_heartbeat, owner_id, owner_phone,
                 owner_email, alert_channel, alert_phone, alert_slack
            FROM devices WHERE ` + field + ` = $1 LIMIT 1`
	out := &Device{}
	err := s.pool.QueryRow(ctx, q, value).Scan(
		&out.ID, &out.MAC, &out.Label, &out.ChipInfo, &out.Kernel, &out.MemKB,
		&out.SidecarVersion, &out.Status, &out.RegisteredAt, &out.LastHeartbeat,
		&out.OwnerID, &out.OwnerPhone, &out.OwnerEmail,
		&out.AlertChannel, &out.AlertPhone, &out.AlertSlack,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find device by %s: %w", field, err)
	}
	return out, nil
}

func (s *pgxStore) List(ctx context.Context) ([]*Device, error) {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	rows, err := s.pool.Query(ctx, `SELECT id, mac, label, chip_info, kernel,
                mem_kb, sidecar_version, status, registered_at, last_heartbeat,
                owner_id, owner_phone, owner_email, alert_channel, alert_phone,
                alert_slack FROM devices ORDER BY registered_at DESC LIMIT 1000`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	out := make([]*Device, 0)
	for rows.Next() {
		d := &Device{}
		if err := rows.Scan(
			&d.ID, &d.MAC, &d.Label, &d.ChipInfo, &d.Kernel, &d.MemKB,
			&d.SidecarVersion, &d.Status, &d.RegisteredAt, &d.LastHeartbeat,
			&d.OwnerID, &d.OwnerPhone, &d.OwnerEmail,
			&d.AlertChannel, &d.AlertPhone, &d.AlertSlack,
		); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *pgxStore) UpdateHeartbeat(ctx context.Context, deviceID string, hb *Heartbeat) error {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	ts := hb.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	tag, err := tx.Exec(ctx, `UPDATE devices
            SET last_heartbeat = $2,
                sidecar_version = COALESCE(NULLIF($3,''), sidecar_version),
                status = 'online',
                updated_at = now()
            WHERE id = $1`, deviceID, ts, hb.Version)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx, `INSERT INTO heartbeats
            (device_id, timestamp, mem_free_kb, disk_pct, uptime_s,
             buffered_frames, temp_mc, version)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		deviceID, ts, hb.MemFreeKB, hb.DiskPct, hb.UptimeS,
		hb.BufferedFrames, hb.TempMC, hb.Version); err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}

	return tx.Commit(ctx)
}

// CreateEvent inserts an event idempotently. The ID is derived from
// (device_id|ts|type|class) so a sidecar that retries the same frame upload
// does not produce duplicate events or duplicate alerts.
func (s *pgxStore) CreateEvent(ctx context.Context, event *Event) error {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	if event.ID == "" {
		event.ID = deterministicEventID(event.DeviceID, event.Timestamp, event.Type, event.Class)
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	}

	bbox := []int32{int32(event.BBox[0]), int32(event.BBox[1]), int32(event.BBox[2]), int32(event.BBox[3])}
	metadata := event.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	tag, err := s.pool.Exec(ctx, `INSERT INTO events
            (id, device_id, frame_key, type, class, confidence, bbox, metadata,
             timestamp, received_at, processed_at, alert_sent)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
            ON CONFLICT (id) DO NOTHING`,
		event.ID, event.DeviceID, event.FrameKey, event.Type, event.Class,
		event.Confidence, bbox, metadata, event.Timestamp, event.ReceivedAt,
		nullTime(event.ProcessedAt), event.AlertSent)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateEvent
		}
		return fmt.Errorf("insert event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDuplicateEvent
	}
	return nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (s *pgxStore) ListEvents(ctx context.Context, deviceID string, limit int) ([]*Event, error) {
	ctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	if limit <= 0 || limit > 500 {
		limit = 50
	}

	var (
		rows pgx.Rows
		err  error
	)
	if deviceID == "" {
		rows, err = s.pool.Query(ctx, `SELECT id, device_id, frame_key, type, class,
                confidence, COALESCE(bbox, '{}'::int[]), COALESCE(metadata::text, '{}'),
                timestamp, received_at, COALESCE(processed_at, '0001-01-01'::timestamptz),
                alert_sent
                FROM events ORDER BY timestamp DESC LIMIT $1`, limit)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT id, device_id, frame_key, type, class,
                confidence, COALESCE(bbox, '{}'::int[]), COALESCE(metadata::text, '{}'),
                timestamp, received_at, COALESCE(processed_at, '0001-01-01'::timestamptz),
                alert_sent
                FROM events WHERE device_id = $1 ORDER BY timestamp DESC LIMIT $2`,
			deviceID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	out := make([]*Event, 0, limit)
	for rows.Next() {
		ev := &Event{}
		var bbox []int32
		if err := rows.Scan(&ev.ID, &ev.DeviceID, &ev.FrameKey, &ev.Type, &ev.Class,
			&ev.Confidence, &bbox, &ev.Metadata, &ev.Timestamp, &ev.ReceivedAt,
			&ev.ProcessedAt, &ev.AlertSent); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		for i := 0; i < 4 && i < len(bbox); i++ {
			ev.BBox[i] = int(bbox[i])
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// deterministicEventID produces a stable id for the (device, ts, type, class)
// tuple. Used as a primary-key dedup mechanism so the pipeline is safe to
// retry frames.
func deterministicEventID(deviceID string, ts time.Time, typ, class string) string {
	// uuid v5-ish hash, fast and collision-resistant enough for pilot scale.
	ns := uuid.NameSpaceURL
	name := fmt.Sprintf("evt|%s|%d|%s|%s", deviceID, ts.UTC().Unix(), typ, class)
	return "evt_" + uuid.NewSHA1(ns, []byte(name)).String()[:8]
}

-- ============================================================
-- Secure4K Sidecar Platform — Database Schema
-- ============================================================
-- Run: psql -d sidecar -f 001_initial.sql
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Devices table
CREATE TABLE IF NOT EXISTS devices (
    id              TEXT PRIMARY KEY DEFAULT 'dev_' || substr(gen_random_uuid()::text, 1, 8),
    mac             TEXT UNIQUE NOT NULL,
    label           TEXT DEFAULT '',
    chip_info       TEXT DEFAULT '',
    kernel          TEXT DEFAULT '',
    mem_kb          INTEGER DEFAULT 0,
    sidecar_version TEXT DEFAULT '',
    status          TEXT DEFAULT 'online' CHECK (status IN ('online', 'offline', 'degraded')),
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_heartbeat  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Owner
    owner_id        TEXT DEFAULT '',
    owner_phone     TEXT DEFAULT '',
    owner_email     TEXT DEFAULT '',

    -- Alert config
    alert_channel   TEXT DEFAULT 'whatsapp' CHECK (alert_channel IN ('whatsapp', 'sms', 'email', 'slack')),
    alert_phone     TEXT DEFAULT '',
    alert_slack     TEXT DEFAULT '',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_devices_mac ON devices(mac);
CREATE INDEX idx_devices_status ON devices(status);
CREATE INDEX idx_devices_owner ON devices(owner_id);

-- Heartbeats (time-series — partition by month in production)
CREATE TABLE IF NOT EXISTS heartbeats (
    id              BIGSERIAL PRIMARY KEY,
    device_id       TEXT NOT NULL REFERENCES devices(id),
    timestamp       TIMESTAMPTZ NOT NULL,
    mem_free_kb     INTEGER DEFAULT 0,
    disk_pct        INTEGER DEFAULT 0,
    uptime_s        INTEGER DEFAULT 0,
    buffered_frames INTEGER DEFAULT 0,
    temp_mc         INTEGER DEFAULT 0,
    version         TEXT DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_heartbeats_device_ts ON heartbeats(device_id, timestamp DESC);

-- Events (detections, alerts, frame records)
-- The id is a deterministic hash of (device_id, timestamp, type, class)
-- assigned by the application layer. Combined with the unique index below
-- this guarantees idempotent event creation: a sidecar that re-uploads a
-- frame because of a network retry will NOT produce duplicate events or
-- duplicate alerts.
CREATE TABLE IF NOT EXISTS events (
    id              TEXT PRIMARY KEY,
    device_id       TEXT NOT NULL REFERENCES devices(id),
    frame_key       TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('frame', 'detection', 'alert', 'gauge_reading')),
    class           TEXT DEFAULT '',
    confidence      DOUBLE PRECISION DEFAULT 0,
    bbox            INTEGER[] DEFAULT '{}',
    metadata        JSONB DEFAULT '{}',
    timestamp       TIMESTAMPTZ NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ,
    alert_sent      BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_events_device_ts ON events(device_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_events_type    ON events(type);
CREATE INDEX IF NOT EXISTS idx_events_class   ON events(class);
CREATE INDEX IF NOT EXISTS idx_events_alert   ON events(alert_sent) WHERE alert_sent = true;
CREATE UNIQUE INDEX IF NOT EXISTS uq_events_dedup
    ON events(device_id, timestamp, type, class);

-- OTA releases
CREATE TABLE IF NOT EXISTS ota_releases (
    id              SERIAL PRIMARY KEY,
    version         TEXT NOT NULL UNIQUE,
    sha256          TEXT NOT NULL,
    url             TEXT NOT NULL,
    release_notes   TEXT DEFAULT '',
    target_chips    TEXT[] DEFAULT '{}',
    is_active       BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Device offline detection function
-- Run via pg_cron every 5 minutes:
--   SELECT mark_offline_devices(interval '10 minutes');
CREATE OR REPLACE FUNCTION mark_offline_devices(threshold INTERVAL)
RETURNS INTEGER AS $$
DECLARE
    affected INTEGER;
BEGIN
    UPDATE devices
    SET status = 'offline', updated_at = now()
    WHERE status = 'online'
      AND last_heartbeat < now() - threshold;
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected;
END;
$$ LANGUAGE plpgsql;

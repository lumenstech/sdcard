# Secure4K Sidecar Platform

Turn any cheap camera into a managed AI security camera by inserting an SD
card. The sidecar captures, buffers, and uploads frames; a small Go backend
ingests them, runs vision inference, and emits alerts.

This repository ships:

| Component        | Purpose                                                |
| ---------------- | ------------------------------------------------------ |
| `sdcard/`        | POSIX-sh scripts that auto-run on the camera           |
| `backend/`       | Go ingest server (HTTP + MQTT, Postgres, MinIO/S3)     |
| `docker-compose.yml` | Pilot stack: ingest, postgres, mosquitto, minio, ollama |
| `prep-sdcard.sh` | Workstation tool to flash a customer SD card           |
| `scripts/smoke.sh` | End-to-end pilot smoke test                          |

## How it works

1. Customer inserts a pre-flashed SD card into their existing camera.
2. The camera firmware auto-executes `initrun.sh`.
3. Sidecar daemons unlock RTSP (best-effort), capture frames, register the
   device once with the backend, and upload frames over HTTP or MQTT.
4. The Go backend stores each frame to local disk + MinIO/S3, runs inference
   via Ollama (failing open if the model is down), persists events to
   Postgres, and dispatches alerts via SequenceNow.

## Pilot quick-start

```bash
cp .env.example .env
# Edit .env — at minimum set VALID_API_KEYS=<a strong token>

make up         # docker compose: postgres, ingest, mosquitto, minio, ollama
make smoke      # runs the pilot smoke test against http://localhost:8090

make logs       # tail ingest logs
make down       # stop everything
```

## Backend

- `cmd/ingest/` — main entrypoint, env-based config.
- `internal/handlers/` — HTTP routes, frame pipeline, OTA, dedup.
- `internal/device/` — `Store` interface with `pgxStore` (Postgres) and
  `memoryStore` (tests / dev fallback).
- `internal/storage/` — local-first frame store with async S3/MinIO upload
  and bounded exponential backoff.
- `internal/inference/` — Ollama vision client with circuit breaker.
- `internal/alerts/` — SequenceNow WhatsApp / SMS / Slack dispatcher.
- `internal/mqtt/` — Paho subscriber that forwards to the same pipeline.
- `internal/ratelimit/` — per-device token-bucket limiter.
- `migrations/001_initial.sql` — Postgres schema (auto-applied by Docker).

```bash
cd backend
go test ./... -race        # all unit tests
go build ./cmd/ingest      # local binary
```

## SD card

Scripts are strict POSIX `#!/bin/sh` (no bashisms):

| Script              | Job                                                      |
| ------------------- | -------------------------------------------------------- |
| `initrun.sh`        | Boot entry: PID guard, network wait, watchdog            |
| `scripts/capture_loop.sh` | RTSP → HTTP → V4L2 → file-scrape capture cascade  |
| `scripts/upload_daemon.sh` | Buffered HTTP/MQTT upload + heartbeat              |
| `scripts/register.sh` | Idempotent registration                                |
| `scripts/rtsp_unlock.sh` | SoC-specific RTSP enable                            |
| `scripts/ota_check.sh` | sha256-verified OTA with rollback                     |

Flash with `prep-sdcard.sh /path/to/sdcard`.

## Pilot readiness assessment

See `docs/PILOT_READINESS.md` for the full reliability audit, launch
checklist, and operator runbook.

## License

Proprietary — Lumens Technology LLC

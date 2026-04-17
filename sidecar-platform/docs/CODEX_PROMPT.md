# Secure4K Sidecar Platform — Codex Verification & Build Prompt

## Context

You are working on the **Secure4K Sidecar Platform** — a system that turns existing cheap Chinese cameras and video doorbells into managed AI-powered security cameras via an SD card sidecar.

The repo is at `github.com/lumenstech/secure4k-sidecar` (or local working directory).

### Architecture Overview

```
┌─────────────────────────────────────────────┐
│  SD CARD (inserted into customer's camera)  │
│                                             │
│  initrun.sh ─── bootstrap + watchdog        │
│  scripts/                                   │
│    ├── capture_loop.sh  (grab frames)       │
│    ├── upload_daemon.sh (push to backend)   │
│    ├── rtsp_unlock.sh   (enable local RTSP) │
│    ├── register.sh      (device onboarding) │
│    └── ota_check.sh     (self-update)       │
│  config/                                    │
│    └── device.conf      (device settings)   │
│  buffer/                (offline frame Q)   │
└──────────────┬──────────────────────────────┘
               │ HTTP POST / MQTT
               ▼
┌─────────────────────────────────────────────┐
│  GO BACKEND (runs on Proxmox 5090 or cloud) │
│                                             │
│  cmd/ingest/main.go     (HTTP server)       │
│  internal/                                  │
│    ├── handlers/                            │
│    │   ├── handlers.go  (HTTP routes)       │
│    │   └── pipeline.go  (frame processing)  │
│    ├── storage/frames.go (S3/local store)   │
│    ├── device/store.go   (device + events)  │
│    ├── inference/service.go (Ollama vision) │
│    └── alerts/service.go (SequenceNow API)  │
│  migrations/001_initial.sql                 │
└─────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────┐
│  SequenceNow API         │
│  WhatsApp / SMS / Slack  │
└──────────────────────────┘
```

### Stack

- **SD Card scripts**: POSIX sh (busybox compatible — no bash, no arrays, no `[[ ]]`)
- **Backend**: Go 1.23, stdlib `net/http` (no frameworks), PostgreSQL 16, S3-compatible storage
- **Inference**: Ollama with vision model (llava/moondream for pilot, YOLO for production)
- **Alerts**: SequenceNow API (`POST /v1/whatsapp/send` at $0.04/msg)
- **Target cameras**: Tuya-based (RTS3903N, Ingenic T20/T31, HiSilicon Hi3518, Goke GK7205)

---

## Task 1: Verify SD Card Shell Scripts

Review all files in `sdcard/` for correctness:

### Checklist
- [ ] All scripts are **strict POSIX sh** — no bashisms (`[[ ]]`, arrays, `$()` nesting, `function` keyword, `{var,,}`)
- [ ] All scripts use `#!/bin/sh` shebang
- [ ] `initrun.sh` properly backgrounds daemons and tracks PIDs
- [ ] `capture_loop.sh` handles all 4 capture methods (RTSP, HTTP snapshot, V4L2, file scrape) and fails gracefully
- [ ] `upload_daemon.sh` handles offline buffering with exponential backoff, doesn't lose frames
- [ ] `rtsp_unlock.sh` detects SoC correctly and applies the right unlock method
- [ ] `register.sh` is idempotent (re-registration doesn't create duplicates)
- [ ] `ota_check.sh` verifies SHA-256 checksums before applying updates and has rollback
- [ ] No script creates files outside `/mnt/mmc01/` (SD card mount point)
- [ ] All `wget` calls have `--timeout` to prevent hangs on slow networks
- [ ] Buffer management enforces `OFFLINE_BUFFER_MAX` correctly
- [ ] PID file prevents double-launch of initrun.sh
- [ ] Watchdog in initrun.sh correctly detects and restarts dead child processes

### Fix any issues found. Run `shellcheck` on every script if available.

---

## Task 2: Verify Go Backend

Review all files in `backend/` for correctness:

### Checklist
- [ ] `main.go` compiles and runs (create any missing stubs if needed)
- [ ] HTTP routes use Go 1.22+ method routing (`"POST /v1/..."`)
- [ ] Auth middleware uses constant-time comparison
- [ ] Frame ingest handler limits body size to 5MB
- [ ] Pipeline uses buffered channel with non-blocking submit (returns error if full)
- [ ] Pipeline workers handle context cancellation for clean shutdown
- [ ] Device store is safe for concurrent access (add sync.RWMutex to in-memory maps)
- [ ] Event list returns results newest-first
- [ ] Inference service properly handles Ollama API response format
- [ ] Inference service cleans markdown fences from model JSON output
- [ ] Alert service routes to correct channel (WhatsApp/SMS/Slack) based on device config
- [ ] Heartbeat updates device status to "online"
- [ ] OTA check returns current version when no update available
- [ ] All HTTP responses set Content-Type header
- [ ] Graceful shutdown waits for pipeline to drain

### Critical fixes needed:
1. **Add `sync.RWMutex`** to `device.Store` — the in-memory maps are accessed concurrently by pipeline workers
2. **Add `go.sum`** — run `go mod tidy`
3. **Add health check for Ollama** — don't crash if Ollama is unreachable, just skip inference
4. **Add frame deduplication** — same device + same timestamp = skip (sidecar might retry uploads)
5. **Add rate limiting per device** — prevent a malfunctioning sidecar from flooding the pipeline

---

## Task 3: Add PostgreSQL Implementation

Replace the in-memory maps in `device/store.go` with real PostgreSQL queries using `pgx/v5`:

```go
// Required dependency
// go get github.com/jackc/pgx/v5/pgxpool
```

Implement these methods with proper SQL:
- `Create(ctx, *Device) (*Device, error)`
- `FindByMAC(ctx, mac) (*Device, error)`
- `FindByID(ctx, id) (*Device, error)`
- `List(ctx) ([]*Device, error)`
- `UpdateHeartbeat(ctx, deviceID, *Heartbeat) error` — also INSERT into heartbeats table
- `CreateEvent(ctx, *Event) error`
- `ListEvents(ctx, deviceID, limit) ([]*Event, error)`

Run the migration in `migrations/001_initial.sql` against a test database and verify all queries work.

---

## Task 4: Add MQTT Ingest Path

The sidecar can push frames via MQTT instead of HTTP. Add an MQTT subscriber that:

1. Connects to Mosquitto broker (configured via `MQTT_BROKER_URL` env var)
2. Subscribes to `secure4k/frames/+` (wildcard for device ID)
3. Parses the JSON payload `{"device_id":"...","ts":...,"frame":"<base64>"}`
4. Decodes the base64 frame
5. Submits to the same `Pipeline` as HTTP ingest

Use `github.com/eclipse/paho.mqtt.golang` for the MQTT client.

---

## Task 5: Write Tests

Create test files for:

### `backend/internal/handlers/handlers_test.go`
- Test auth middleware rejects missing/invalid tokens
- Test auth middleware accepts valid tokens
- Test frame ingest returns 400 for missing device ID
- Test frame ingest returns 400 for empty body
- Test frame ingest returns 202 for valid frame
- Test device registration is idempotent by MAC

### `backend/internal/inference/service_test.go`
- Test `cleanJSON` strips markdown fences
- Test `Detection.ShouldAlert()` returns correct values for each class/confidence combo
- Test inference service handles Ollama connection failure gracefully

### `backend/internal/device/store_test.go`
- Test Create + FindByID roundtrip
- Test Create + FindByMAC roundtrip
- Test duplicate MAC returns existing device
- Test ListEvents returns newest first
- Test concurrent access doesn't panic (use goroutines)

Target: **50+ tests, all passing, `go test ./...` clean**

---

## Task 6: Add S3 Upload to Frame Store

Replace local-only storage with MinIO/S3 upload:

```go
// go get github.com/minio/minio-go/v7
```

- Store locally FIRST (fast path), then upload to S3 async
- If S3 upload fails, retry with backoff (don't block the pipeline)
- Add `Retrieve(ctx, key)` that checks local cache first, falls back to S3
- Add `Cleanup(ctx, retention)` that removes local files older than retention but keeps S3 copies

---

## Task 7: Docker Compose for Pilot

Create `docker-compose.yml` that runs:
- `ingest` (the Go backend, port 8090)
- `postgres` (PostgreSQL 16, with migration auto-run)
- `mosquitto` (MQTT broker, port 1883)
- `minio` (S3-compatible storage, port 9000)
- `ollama` (with llava model pre-pulled)

Include a `Makefile` with:
- `make build` — build Go binary
- `make test` — run all tests
- `make up` — docker compose up
- `make sd-card` — package sdcard/ directory into a zip ready to flash

---

## Constraints

- The SD card scripts MUST work on **busybox ash** — many embedded cameras don't have bash
- The Go backend MUST work without any external framework (no Gin, no Fiber, no Echo)
- The inference service MUST gracefully degrade if Ollama is down (store frame, skip detection)
- All frame uploads MUST be idempotent (same device+timestamp = skip, not duplicate)
- The system MUST work fully offline for the sidecar (buffer and upload when connectivity returns)

---

## Validation

After completing all tasks, verify:

```bash
# Shell scripts
shellcheck sdcard/*.sh sdcard/scripts/*.sh

# Go backend
cd backend
go mod tidy
go vet ./...
go test ./... -v -count=1 -race

# Docker
docker compose up -d
curl http://localhost:8090/health
# Should return: {"status":"ok","version":"0.1.0"}

# Simulate device registration
curl -X POST http://localhost:8090/v1/devices/register \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: application/json" \
  -d '{"api_key":"test-key","mac":"aa:bb:cc:dd:ee:ff","chip_info":"ingenic_t31","kernel":"3.10.14","mem_kb":65536,"sidecar_version":"0.1.0"}'

# Simulate frame ingest
curl -X POST http://localhost:8090/v1/frames/ingest \
  -H "Authorization: Bearer test-key" \
  -H "X-Device-ID: dev_test1234" \
  -H "X-Timestamp: $(date +%s)" \
  --data-binary @test_frame.jpg
```

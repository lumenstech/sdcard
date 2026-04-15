# Secure4K Sidecar Platform — Pilot Readiness

This document captures the post-hardening state of the platform, the launch
blockers (none remaining for a narrow first pilot), the deployment checklist,
and the operator runbook.

---

## 1. Reliability guarantees now in place

| Concern                              | Implementation                                                                                                          |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------- |
| Idempotent device registration       | `Store.Create` is `INSERT … ON CONFLICT (mac) DO UPDATE`; HTTP handler also checks `FindByMAC` first.                   |
| Idempotent frame events              | Event PK is a deterministic SHA-1 of `(device_id, ts, type, class)`; unique index `uq_events_dedup` enforces it in DB.   |
| Concurrency safety                   | `memoryStore` guarded by `sync.RWMutex`; `pgxStore` uses `pgxpool` (concurrent-safe). `-race` tests pass.                |
| Offline buffering                    | Sidecar `capture_loop.sh` writes to `${SIDECAR_ROOT}/buffer/`, pruned at `OFFLINE_BUFFER_MAX`. Survives reboot.          |
| Bounded retries                      | Sidecar upload daemon has exponential backoff (10s → 300s). S3 worker retries 6 attempts max then logs and stops.       |
| Per-device rate limit                | `ratelimit.Limiter` token bucket, 2 fps default, configurable via `RATE_LIMIT_PER_SEC` / `_BURST`.                       |
| Inference fail-open                  | Ollama errors → frame still stored, frame-only event recorded. Circuit breaker opens after 5 consecutive failures.      |
| OTA safety                           | Backend serves manifest with `version`, `url`, `sha256`. Sidecar refuses any update without sha256 and verified match.   |
| Bounded network calls                | Every `wget` uses `--timeout`. Every Go HTTP client has an explicit `Timeout`. Every `pgx` query is wrapped in a 5 s ctx. |
| Visibility                           | Structured slog JSON. `/health` (cheap) and `/healthz` (DB ping + queue depth) endpoints.                                |
| Graceful shutdown                    | Pipeline drains in-flight frames; HTTP server has 30 s shutdown grace period.                                            |

## 2. Test coverage

`go test ./... -race` (in `backend/`) exercises:

- **Auth middleware** — missing/invalid/valid tokens, empty allowlist rejection.
- **Frame ingest** — missing device-id, empty body, success path, rate-limited path.
- **Registration idempotency** — repeated MAC returns the same `device_id`.
- **Pipeline dedup** — three identical frame submissions → exactly one event.
- **Device store** — create, find, list-newest-first, dedup, heartbeat, concurrent access.
- **Inference** — `cleanJSON`, `ShouldAlert` matrix, fail-open when Ollama down,
  circuit breaker after 5 failures, parses valid response.
- **Frame storage** — local round-trip, idempotent same-size store, input
  validation, path-traversal sanitization.
- **Rate limiter** — nil-safe, refill, per-key isolation, concurrent.
- **OTA** — no-manifest path, manifest-with-bundle path (sha256 auto-computed),
  download serves file, traversal rejected.

All 30+ tests pass cleanly with `-race` enabled.

The `scripts/smoke.sh` end-to-end test exercises the full happy path:
register → re-register (idempotent) → frame upload → duplicate frame
(idempotent) → heartbeat → list devices/events.

## 3. Launch blockers remaining

**None for a narrow first pilot of a single site / few devices.**

Open items that are NOT blockers but should be tracked:

- Ollama auto-pull of the vision model is best-effort; if the model isn't
  pulled before the first frame, inference will fail open (frame still
  stored, no detection). Run `docker compose exec ollama ollama pull llava`
  manually if you want detections from minute zero.
- `mosquitto.conf` allows anonymous connections (intentional for pilot).
  Add ACLs / TLS for production multi-tenant deployments.
- Postgres credentials in `docker-compose.yml` are the well-known defaults
  (`secure4k/secure4k`); the pilot stack is intended to run on a private
  network. Rotate before any internet exposure.
- The OTA bundle build pipeline is out of scope; the backend serves whatever
  `manifest.json` + bundle file you place under `/data/ota`.
- `prep-sdcard.sh` is bash on purpose (workstation tool); only the
  on-device `sdcard/` scripts are POSIX-sh.

## 4. First pilot deployment checklist

```
[ ] Provision a Linux host with Docker Engine 24+ and ~50 GB free disk.
[ ] git clone <this repo>; cd sdcard.
[ ] cp .env.example .env; generate VALID_API_KEYS via `openssl rand -hex 32`.
[ ] (optional) Set SEQUENCENOW_API_KEY for live alerts.
[ ] make up         # brings up the full stack.
[ ] make smoke      # confirms register + frame + dedup paths.
[ ] curl -fsS http://<host>:8090/healthz   # confirm DB ok and queue empty.
[ ] docker compose exec ollama ollama pull llava   # warm the vision model.
[ ] On a workstation: ./prep-sdcard.sh /path/to/mounted/sdcard
    - Backend URL = http://<host>:8090 (or your TLS-fronted URL)
    - API Key     = the value from VALID_API_KEYS
[ ] Insert SD card into camera; power-cycle; wait 2-3 min.
[ ] Verify on backend: curl /v1/devices and /v1/events show the device + frames.
[ ] Soak for 24 h (see Section 5 below).
```

## 5. First pilot operator runbook

### Daily checks

- `docker compose ps` — every service `healthy` / `Up`.
- `curl -fsS http://<host>:8090/healthz | jq` — `db: ok` and `queue_depth`
  stays well below `queue_cap`. Sustained queue depth growth means the
  pipeline can't keep up; bump `PIPELINE_WORKERS` or scale the host.
- `make logs | grep -E 'WARN|ERROR'` — only expected warnings should appear:
  - `inference unavailable, recording frame-only event` — model not loaded.
  - `pipeline full, frame dropped` — rare bursts; investigate if frequent.
  - `s3 upload failed, retrying` — transient; if persistent, MinIO is down.

### When a sidecar goes silent

1. Find the device: `curl /v1/devices | jq '.devices[] | select(.id=="dev_…")'`.
2. Compare `last_heartbeat` to `now()` — anything > 10 minutes is offline.
3. If you have physical access, eject SD, read `sidecar.log` from the card.
4. Common causes:
   - Camera lost WiFi after RTSP unlock → re-flash and add WiFi credentials
     in `device.conf`.
   - Buffer hit `OFFLINE_BUFFER_MAX=500` and frames are being pruned →
     check uplink stability.

### When inference is wrong / spammy

- Tighten alert thresholds in `inference.Detection.ShouldAlert` (currently
  person ≥ 0.65, vehicle ≥ 0.70).
- Or raise `INFERENCE_THRESHOLD` in `.env` (filters at parse time before
  detections reach the alerter).

### When you need to stop alerts immediately

- `unset SEQUENCENOW_API_KEY` in `.env` and `docker compose up -d ingest` —
  alerts will be logged but not delivered.

### Ship a sidecar update

```
# Build a tarball of new sdcard/ contents on the workstation:
make sd-card    # produces dist/secure4k-sidecar-sdcard.zip
tar -czf sidecar-0.2.0.tar.gz -C dist sdcard
sha256sum sidecar-0.2.0.tar.gz   # note the hash

# Place on the backend host:
docker cp sidecar-0.2.0.tar.gz $(docker compose ps -q ingest):/data/ota/
cat > /tmp/manifest.json <<EOF
{"version":"0.2.0","url":"/v1/ota/bundle/sidecar-0.2.0.tar.gz","sha256":"<hash>"}
EOF
docker cp /tmp/manifest.json $(docker compose ps -q ingest):/data/ota/manifest.json
```

Sidecars will discover the new manifest on their next OTA check, verify the
sha256, and apply with rollback on failure.

### Backups

- Postgres volume: `pg-data`. Snapshot daily.
- MinIO bucket `sidecar-frames`: snapshot or replicate weekly. Local copies
  on the ingest host volume `frame-data` are the authoritative short-term
  store; S3 is the long-term backup.

## 6. Soak procedure (one device, 24 h)

1. Flash SD with `CAPTURE_INTERVAL=60` and a stable network.
2. Confirm device registers and first frame appears within 5 minutes.
3. Every 4 hours, capture metrics:
   ```bash
   curl -fsS http://<host>:8090/healthz
   curl -fsS http://<host>:8090/v1/devices
   curl -fsS "http://<host>:8090/v1/events?device_id=<id>&limit=5"
   ```
4. Pull the camera off the network for 30 minutes mid-soak; verify:
   - sidecar logs show `Offline — buffering locally`.
   - on reconnect, buffered frames upload (check
     `find /mnt/mmc01/buffer -name 'frame_*.jpg' | wc -l` shrinks).
   - no duplicate events on the backend after the catch-up.
5. After 24 h: `last_heartbeat` should be within the last minute, queue
   depth at 0, no `ERROR` lines in logs except expected Ollama outages.

A single device passing this soak is the green light for the next 5-10
device cohort.

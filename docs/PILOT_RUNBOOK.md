# Secure4K Sidecar Pilot Runbook

## Scope

This runbook is for one narrow real-world pilot device. Do not expand feature scope until registration, evidence persistence, offline buffering, duplicate handling, and recovery are proven on the target camera.

## Backend launch

1. Copy `.env.example` to `.env` and replace `VALID_API_KEYS`, `POSTGRES_PASSWORD`, and `MINIO_ROOT_PASSWORD` with unique pilot secrets.
2. Set `SEQUENCENOW_API_KEY` only if alerts are being exercised in this pilot.
3. Run `make up`.
4. Run `curl -fsS http://localhost:8090/health`. `ok` is preferred. `degraded` is acceptable when Ollama is unavailable; `unhealthy` is not.
5. Run `API_KEY=<pilot-key> make smoke` before inserting an SD card into a camera.

## Prepare one SD card

1. Format the target microSD as FAT32 using the normal OS tooling.
2. Run `./prep-sdcard.sh /path/to/mounted/card` on macOS/Linux.
3. Use the backend URL reachable from the camera network and the same pilot API key configured on the backend.
4. For the first pilot, leave MQTT blank unless MQTT is specifically being tested. HTTP is the recovery path.
5. Safely eject, insert the card, and power-cycle the camera.

## First boot acceptance

Within the first several minutes verify all of the following:

- `sidecar.log` shows one watchdog parent and PIDs for capture, upload, and OTA.
- registration assigns a stable `DEVICE_ID`; a reboot does not create a second device row.
- capture reports the selected method or a visible fallback failure.
- pending frames appear in `/mnt/mmc01/buffer` when the backend is deliberately unreachable.
- pending frames drain after connectivity returns.
- `/v1/events?device_id=<id>` shows one event per accepted frame timestamp.
- MinIO bucket `sidecar-frames` contains uploaded evidence after the backend receives frames.

## Offline recovery test

1. Confirm at least one successful frame first.
2. Make the backend unreachable for the camera without powering the camera off.
3. Leave the camera running long enough to collect several frames.
4. Confirm frame count grows in `/mnt/mmc01/buffer` and no pending files are deleted. When the configured buffer limit is reached, capture must pause rather than discard evidence.
5. Restore connectivity.
6. Confirm upload retries resume with bounded backoff and the buffer drains into `buffer/sent`.
7. Confirm duplicate retries do not create duplicate events.

## Ollama failure test

1. Stop only the Ollama service.
2. Upload a new frame.
3. The HTTP/MQTT ingest path must still accept and persist the frame.
4. Backend logs must contain an inference-unavailable warning.
5. The frame event and evidence must remain queryable.
6. Restart Ollama and verify `/health` returns `ok` after the model is available.

## One-device soak

Use a real registered device ID:

```sh
API_KEY=<pilot-key> DEVICE_ID=<device-id> DURATION_MINUTES=60 INTERVAL_SECONDS=60 make soak
```

For the field pilot, extend this to at least 24 hours after the one-hour run is clean. During the run, inspect backend logs, sidecar log growth, SD free space, MinIO object growth, PostgreSQL event count, and heartbeat freshness. Any unexplained process restart, data loss, duplicate event, unbounded disk growth, or repeated timeout is a stop condition.

## Operator recovery

- **Backend unhealthy / PostgreSQL failed:** stop pilot ingest, preserve the SD buffer, restore PostgreSQL, then restart ingest. Do not delete `frame_receipts` to force retries.
- **MinIO unavailable:** local backend evidence remains under the configured frame volume. Restore MinIO and verify new uploads; do not delete local evidence during recovery.
- **Ollama unavailable:** leave ingest running. This is a degraded state, not a reason to drop frames.
- **Camera cannot capture:** inspect the selected method in `sidecar.log`; let the capture loop re-detect. Do not alter firmware during the pilot to force a method.
- **SD buffer full:** restore backend/network reachability. Capture intentionally pauses at the limit to preserve pending evidence.
- **OTA failure:** the updater refuses artifacts without SHA-256 verification and retains `updates/scripts.previous`. If activation breaks a daemon, restore that directory to `/mnt/mmc01/scripts` and reboot.

## Pilot exit criteria

The device must complete registration, offline buffering/recovery, duplicate retry, Ollama outage, backend restart, and a minimum 24-hour field soak without evidence loss. Only after those conditions pass should broader camera models or additional features be introduced.

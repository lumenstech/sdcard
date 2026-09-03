# Secure4K Sidecar Platform

Turn any cheap camera into a managed AI security camera with an SD card.

## How It Works

1. Customer inserts our pre-flashed SD card into their existing camera
2. Camera boots, sidecar scripts auto-execute
3. Scripts unlock RTSP stream, capture frames, upload to backend
4. Backend runs AI detection (person/vehicle/animal/gauge)
5. Customer gets WhatsApp alerts via SequenceNow

## Repository Structure

```
├── sdcard/                     # Files that go on the SD card
│   ├── initrun.sh              # Boot entrypoint (+ symlinks for different camera brands)
│   ├── scripts/
│   │   ├── capture_loop.sh     # Frame capture daemon
│   │   ├── upload_daemon.sh    # Frame upload + offline buffer
│   │   ├── rtsp_unlock.sh      # Camera RTSP stream unlock
│   │   ├── register.sh         # Device registration
│   │   └── ota_check.sh        # Over-the-air script updates
│   └── config/
│       └── device.conf         # Per-device configuration
│
├── backend/                    # Go ingest server
│   ├── cmd/ingest/             # Main entry point
│   ├── internal/
│   │   ├── handlers/           # HTTP handlers + processing pipeline
│   │   ├── storage/            # S3/local frame storage
│   │   ├── device/             # Device + event data store
│   │   ├── inference/          # Ollama vision integration
│   │   └── alerts/             # SequenceNow WhatsApp/SMS/Slack
│   └── migrations/             # PostgreSQL schema
│
└── docs/
    └── CODEX_PROMPT.md         # Verification prompt for Claude Code
```

## Quick Start (Pilot)

```bash
# 1. Start backend
cd backend
go run ./cmd/ingest/

# 2. Flash SD card
# Copy sdcard/* to a FAT32 microSD card
# Edit sdcard/config/device.conf with your backend URL + API key
# Insert into camera, power cycle

# 3. Watch logs
tail -f /path/to/mounted/sdcard/sidecar.log
```

## Supported Cameras

| Chipset | Common Brands | RTSP Unlock | Status |
|---------|---------------|-------------|--------|
| RTS3903N | Tuya/Merkury/Geeni | SD card patch | Testing |
| Ingenic T20/T31 | Wyze v2/v3, generic | SD flag file | Testing |
| HiSilicon Hi3518 | Generic doorbells | Binary patch | Planned |
| Goke GK7205V200 | Newer generics | OpenIPC | Planned |

## License

Proprietary — Lumens Technology LLC

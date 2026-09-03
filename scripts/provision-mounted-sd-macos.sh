#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-${SD_MOUNT:-}}"
BACKEND_URL="${BACKEND_URL:-}"
API_KEY="${API_KEY:-}"
CAPTURE_INTERVAL="${CAPTURE_INTERVAL:-60}"
RTSP_ENABLE="${RTSP_ENABLE:-1}"
MQTT_BROKER="${MQTT_BROKER:-}"
WIFI_SSID="${WIFI_SSID:-}"
WIFI_PASS="${WIFI_PASS:-}"
BACKUP_ROOT="${BACKUP_ROOT:-$HOME/Secure4K-SD-Backups}"
VERSION="$(cat "$ROOT_DIR/sdcard/config/version")"

fail() { echo "ERROR: $*" >&2; exit 1; }
info() { echo "[secure4k-sd] $*"; }

[ "$(uname -s)" = "Darwin" ] || fail "This provisioner is for macOS."
[ -n "$TARGET" ] || fail "Usage: BACKEND_URL=... API_KEY=... $0 /Volumes/<SDCARD>"
[ -d "$TARGET" ] || fail "Target mount does not exist: $TARGET"
[ -w "$TARGET" ] || fail "Target mount is not writable: $TARGET"
[ -n "$BACKEND_URL" ] || fail "BACKEND_URL is required."
[ -n "$API_KEY" ] || fail "API_KEY is required."

case "$TARGET" in
  /Volumes/*) ;;
  *) fail "Refusing target outside /Volumes: $TARGET" ;;
esac

DISK_INFO="$(diskutil info "$TARGET" 2>/dev/null)" || fail "diskutil could not inspect $TARGET"
DEVICE_NODE="$(printf '%s\n' "$DISK_INFO" | awk -F: '/Device Node/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
FILESYSTEM="$(printf '%s\n' "$DISK_INFO" | awk -F: '/File System Personality/ {sub(/^[^:]*:[ \t]*/, ""); print; exit}')"
REMOVABLE="$(printf '%s\n' "$DISK_INFO" | awk -F: '/Removable Media/ {sub(/^[^:]*:[ \t]*/, ""); print; exit}')"
LOCATION="$(printf '%s\n' "$DISK_INFO" | awk -F: '/Device Location/ {sub(/^[^:]*:[ \t]*/, ""); print; exit}')"

[ -n "$DEVICE_NODE" ] || fail "Could not resolve device node for $TARGET"
if [ "$REMOVABLE" != "Removable" ] && [ "$LOCATION" != "External" ]; then
  fail "Refusing non-removable/non-external target: $DEVICE_NODE ($LOCATION, $REMOVABLE)"
fi

case "$FILESYSTEM" in
  *FAT32*|*MS-DOS*) ;;
  *) fail "Expected FAT32/MS-DOS filesystem, found '$FILESYSTEM'. Back up and format the confirmed SD card as MS-DOS (FAT32), then rerun." ;;
esac

STAMP="$(date '+%Y%m%d-%H%M%S')"
BACKUP_DIR="$BACKUP_ROOT/$STAMP-$(basename "$TARGET")"
mkdir -p "$BACKUP_DIR"
info "Backing up current card contents to $BACKUP_DIR"
rsync -a --exclude '.Spotlight-V100' --exclude '.Trashes' --exclude '.fseventsd' "$TARGET/" "$BACKUP_DIR/"

info "Provisioning Secure4K Sidecar doorbell pilot v$VERSION onto $TARGET"
rm -rf "$TARGET/scripts" "$TARGET/config" "$TARGET/buffer" "$TARGET/updates"
rm -f "$TARGET/initrun.sh" "$TARGET/run.sh" "$TARGET/custom.sh" "$TARGET/RELEASE-MANIFEST.txt"
rm -f "$TARGET/sidecar.log" "$TARGET/sidecar.pid" "$TARGET/.sidecar.lock"

mkdir -p "$TARGET/scripts" "$TARGET/config" "$TARGET/buffer" "$TARGET/updates"
cp "$ROOT_DIR/sdcard/initrun.sh" "$TARGET/initrun.sh"
cp "$ROOT_DIR/sdcard/initrun.sh" "$TARGET/run.sh"
cp "$ROOT_DIR/sdcard/initrun.sh" "$TARGET/custom.sh"
cp "$ROOT_DIR/sdcard/scripts/"*.sh "$TARGET/scripts/"
cp "$ROOT_DIR/sdcard/config/version" "$TARGET/config/version"
chmod +x "$TARGET/initrun.sh" "$TARGET/run.sh" "$TARGET/custom.sh" "$TARGET/scripts/"*.sh

cat > "$TARGET/config/device.conf" <<EOF
# Secure4K Sidecar - provisioned on macOS
# Version: $VERSION
DEVICE_ID="UNREGISTERED"
BACKEND_URL="$BACKEND_URL"
API_KEY="$API_KEY"
CAPTURE_INTERVAL=$CAPTURE_INTERVAL
RTSP_ENABLE=$RTSP_ENABLE
FRAME_QUALITY=75
OFFLINE_BUFFER_MAX=500
OTA_CHECK_INTERVAL=3600
MQTT_BROKER="$MQTT_BROKER"
MQTT_TOPIC="secure4k/frames"
WIFI_SSID="$WIFI_SSID"
WIFI_PASS="$WIFI_PASS"
EOF
chmod 600 "$TARGET/config/device.conf" || true

cat > "$TARGET/RELEASE-MANIFEST.txt" <<EOF
Secure4K Sidecar Doorbell Pilot
Version: $VERSION
Provisioned: $(date -u '+%Y-%m-%dT%H:%M:%SZ')
Target: Chinese doorbell/IP camera pilot
Mode: SD-card sidecar; stock camera firmware remains in place
Backend: $BACKEND_URL
Transport: $([ -n "$MQTT_BROKER" ] && echo "MQTT preferred with HTTP fallback" || echo "HTTP")
EOF

sync

required=(
  initrun.sh run.sh custom.sh
  config/version config/device.conf
  scripts/capture_loop.sh scripts/upload_daemon.sh scripts/register.sh
  scripts/ota_check.sh scripts/rtsp_unlock.sh
  RELEASE-MANIFEST.txt
)
for path in "${required[@]}"; do
  [ -f "$TARGET/$path" ] || fail "Post-write validation missing: $path"
done

EXPECTED_VERSION="$(cat "$ROOT_DIR/sdcard/config/version")"
ACTUAL_VERSION="$(cat "$TARGET/config/version")"
[ "$EXPECTED_VERSION" = "$ACTUAL_VERSION" ] || fail "Version mismatch: expected $EXPECTED_VERSION got $ACTUAL_VERSION"

echo
info "Provisioning complete."
info "Device: $DEVICE_NODE"
info "Filesystem: $FILESYSTEM"
info "Mount: $TARGET"
info "Release: v$VERSION"
info "Backup: $BACKUP_DIR"
info "Backend: $BACKEND_URL"
info "Next: diskutil eject '$TARGET', insert card in the doorbell camera, power-cycle, then inspect sidecar.log."

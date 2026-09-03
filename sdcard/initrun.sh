#!/bin/sh
# ============================================================
# Secure4K Sidecar — SD Card Bootstrap
# ============================================================
# This script is auto-executed by Tuya/HiSilicon/Ingenic
# camera firmware when the SD card is mounted at boot.
#
# Supported boot hooks (camera tries these in order):
#   /mnt/mmc01/initrun.sh    (Tuya RTS3903N, Merkury)
#   /mnt/sdcard/run.sh       (Ingenic T20/T31)
#   /mnt/mmc/custom.sh       (HiSilicon Hi3518)
#
# We ship all three filenames on the SD card pointing
# to this same script via symlinks.
# ============================================================

SIDECAR_ROOT="/mnt/mmc01"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
SCRIPTS_DIR="${SIDECAR_ROOT}/scripts"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
PID_FILE="${SIDECAR_ROOT}/sidecar.pid"
VERSION="0.1.0"

# ---- Logging ------------------------------------------------
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [sidecar] $1" >> "${LOG_FILE}"
}

# ---- Guard: don't double-launch -----------------------------
if [ -f "${PID_FILE}" ]; then
    OLD_PID=$(cat "${PID_FILE}")
    if kill -0 "${OLD_PID}" 2>/dev/null; then
        log "Already running as PID ${OLD_PID}, exiting"
        exit 0
    fi
fi
echo $$ > "${PID_FILE}"

log "=========================================="
log "Secure4K Sidecar v${VERSION} starting"
log "=========================================="

# ---- Wait for network ---------------------------------------
# Camera needs time to connect to WiFi after boot.
# We poll for a default route for up to 120 seconds.
TIMEOUT=120
ELAPSED=0
while [ ${ELAPSED} -lt ${TIMEOUT} ]; do
    if route -n 2>/dev/null | grep -q "^0.0.0.0" || ip route 2>/dev/null | grep -q "^default"; then
        log "Network is up after ${ELAPSED}s"
        break
    fi
    sleep 5
    ELAPSED=$((ELAPSED + 5))
done

if [ ${ELAPSED} -ge ${TIMEOUT} ]; then
    log "WARN: Network not available after ${TIMEOUT}s, continuing offline"
fi

# ---- Load device config -------------------------------------
if [ -f "${CONFIG_FILE}" ]; then
    . "${CONFIG_FILE}"
    log "Config loaded: DEVICE_ID=${DEVICE_ID}, BACKEND=${BACKEND_URL}"
else
    log "ERROR: No config file at ${CONFIG_FILE}"
    log "Creating default config — user must edit before next boot"
    cat > "${CONFIG_FILE}" <<'DEFAULTCFG'
# Secure4K Sidecar Device Configuration
# Edit these values, then reboot the camera.

# Unique device ID (assigned by backend on registration)
DEVICE_ID="UNREGISTERED"

# Backend ingest endpoint
BACKEND_URL="https://ingest.secure4k.com"

# API key for this device
API_KEY=""

# Capture interval in seconds (default: 60)
CAPTURE_INTERVAL=60

# RTSP enable attempt (1 = try to unlock, 0 = skip)
RTSP_ENABLE=1

# Frame quality (1-100, JPEG compression)
FRAME_QUALITY=75

# Max frames to buffer locally if offline
OFFLINE_BUFFER_MAX=500

# OTA update check interval in seconds (default: 3600 = 1hr)
OTA_CHECK_INTERVAL=3600

# MQTT broker (optional, blank = use HTTP POST)
MQTT_BROKER=""
MQTT_TOPIC="secure4k/frames"

# WiFi credentials for fallback AP connection
# (some cameras lose WiFi config on firmware patch)
WIFI_SSID=""
WIFI_PASS=""
DEFAULTCFG
    exit 1
fi

# ---- Attempt RTSP unlock ------------------------------------
if [ "${RTSP_ENABLE}" = "1" ]; then
    log "Attempting RTSP unlock..."
    if [ -x "${SCRIPTS_DIR}/rtsp_unlock.sh" ]; then
        "${SCRIPTS_DIR}/rtsp_unlock.sh" >> "${LOG_FILE}" 2>&1
    else
        log "WARN: rtsp_unlock.sh not found or not executable"
    fi
fi

# ---- Device registration / heartbeat ------------------------
if [ "${DEVICE_ID}" = "UNREGISTERED" ] && [ -n "${API_KEY}" ]; then
    log "Device unregistered, attempting registration..."
    if [ -x "${SCRIPTS_DIR}/register.sh" ]; then
        "${SCRIPTS_DIR}/register.sh" >> "${LOG_FILE}" 2>&1
        # Re-load config in case register.sh updated DEVICE_ID
        . "${CONFIG_FILE}"
    fi
fi

# ---- Start capture loop -------------------------------------
log "Starting capture daemon (interval=${CAPTURE_INTERVAL}s)"
"${SCRIPTS_DIR}/capture_loop.sh" &
CAPTURE_PID=$!
log "Capture loop PID: ${CAPTURE_PID}"

# ---- Start upload daemon ------------------------------------
log "Starting upload daemon"
"${SCRIPTS_DIR}/upload_daemon.sh" &
UPLOAD_PID=$!
log "Upload daemon PID: ${UPLOAD_PID}"

# ---- Start OTA checker --------------------------------------
log "Starting OTA update checker (interval=${OTA_CHECK_INTERVAL}s)"
"${SCRIPTS_DIR}/ota_check.sh" &
OTA_PID=$!
log "OTA checker PID: ${OTA_PID}"

# ---- Watchdog ------------------------------------------------
# If capture or upload dies, restart it.
log "Watchdog active — monitoring PIDs"
while true; do
    sleep 300

    if ! kill -0 "${CAPTURE_PID}" 2>/dev/null; then
        log "WARN: Capture loop died, restarting"
        "${SCRIPTS_DIR}/capture_loop.sh" &
        CAPTURE_PID=$!
    fi

    if ! kill -0 "${UPLOAD_PID}" 2>/dev/null; then
        log "WARN: Upload daemon died, restarting"
        "${SCRIPTS_DIR}/upload_daemon.sh" &
        UPLOAD_PID=$!
    fi

    # Log memory/disk health
    FREE_MEM=$(free 2>/dev/null | awk '/Mem:/{print $4}' || echo "unknown")
    DISK_USE=$(df "${SIDECAR_ROOT}" 2>/dev/null | awk 'NR==2{print $5}' || echo "unknown")
    log "Health: mem_free=${FREE_MEM}kB disk_use=${DISK_USE}"
done

#!/bin/sh
# Secure4K Sidecar bootstrap/watchdog - BusyBox/POSIX sh

SIDECAR_ROOT="/mnt/mmc01"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
SCRIPTS_DIR="${SIDECAR_ROOT}/scripts"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
PID_FILE="${SIDECAR_ROOT}/sidecar.pid"
LOCK_DIR="${SIDECAR_ROOT}/.sidecar.lock"
VERSION="0.1.0"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] [sidecar] $1" >> "${LOG_FILE}"; }

# mkdir is atomic on the local filesystem and prevents simultaneous boot hooks
# from both becoming watchdog parents.
if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
    if [ -f "${PID_FILE}" ]; then
        OLD_PID=$(cat "${PID_FILE}" 2>/dev/null)
        if [ -n "${OLD_PID}" ] && kill -0 "${OLD_PID}" 2>/dev/null; then
            log "Already running as PID ${OLD_PID}, exiting"
            exit 0
        fi
    fi
    log "Removing stale sidecar lock"
    rm -rf "${LOCK_DIR}"
    if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
        log "ERROR: could not acquire sidecar lock"
        exit 1
    fi
fi

echo $$ > "${PID_FILE}"
cleanup() {
    rm -f "${PID_FILE}"
    rmdir "${LOCK_DIR}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

log "Secure4K Sidecar v${VERSION} starting"

TIMEOUT=120
ELAPSED=0
while [ "${ELAPSED}" -lt "${TIMEOUT}" ]; do
    if route -n 2>/dev/null | grep -q '^0.0.0.0' || ip route 2>/dev/null | grep -q '^default'; then
        log "Network is up after ${ELAPSED}s"
        break
    fi
    sleep 5
    ELAPSED=$((ELAPSED + 5))
done
[ "${ELAPSED}" -lt "${TIMEOUT}" ] || log "WARN: network unavailable after ${TIMEOUT}s; continuing offline"

if [ ! -f "${CONFIG_FILE}" ]; then
    log "ERROR: missing ${CONFIG_FILE}; refusing unconfigured launch"
    exit 1
fi
. "${CONFIG_FILE}"
log "Config loaded device=${DEVICE_ID} backend=${BACKEND_URL}"

if [ "${RTSP_ENABLE:-0}" = "1" ] && [ -x "${SCRIPTS_DIR}/rtsp_unlock.sh" ]; then
    if ! "${SCRIPTS_DIR}/rtsp_unlock.sh" >> "${LOG_FILE}" 2>&1; then
        log "WARN: RTSP unlock unavailable; capture fallbacks will be used"
    fi
fi

if [ "${DEVICE_ID}" = "UNREGISTERED" ] && [ -n "${API_KEY:-}" ] && [ -x "${SCRIPTS_DIR}/register.sh" ]; then
    if "${SCRIPTS_DIR}/register.sh" >> "${LOG_FILE}" 2>&1; then
        . "${CONFIG_FILE}"
    else
        log "WARN: registration failed; capture can buffer while offline"
    fi
fi

start_capture() {
    "${SCRIPTS_DIR}/capture_loop.sh" &
    CAPTURE_PID=$!
    log "Capture loop PID=${CAPTURE_PID}"
}
start_upload() {
    "${SCRIPTS_DIR}/upload_daemon.sh" &
    UPLOAD_PID=$!
    log "Upload daemon PID=${UPLOAD_PID}"
}
start_ota() {
    "${SCRIPTS_DIR}/ota_check.sh" &
    OTA_PID=$!
    log "OTA checker PID=${OTA_PID}"
}

start_capture
start_upload
start_ota

log "Watchdog active"
while true; do
    sleep 300
    if ! kill -0 "${CAPTURE_PID}" 2>/dev/null; then
        log "WARN: capture loop died; restarting"
        start_capture
    fi
    if ! kill -0 "${UPLOAD_PID}" 2>/dev/null; then
        log "WARN: upload daemon died; restarting"
        start_upload
    fi
    if ! kill -0 "${OTA_PID}" 2>/dev/null; then
        log "WARN: OTA checker died; restarting"
        start_ota
    fi
    FREE_MEM=$(free 2>/dev/null | awk '/Mem:/{print $4}')
    [ -n "${FREE_MEM}" ] || FREE_MEM="unknown"
    DISK_USE=$(df "${SIDECAR_ROOT}" 2>/dev/null | awk 'NR==2{print $5}')
    [ -n "${DISK_USE}" ] || DISK_USE="unknown"
    log "Health mem_free=${FREE_MEM}kB disk_use=${DISK_USE}"
done

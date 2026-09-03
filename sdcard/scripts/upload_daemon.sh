#!/bin/sh
# Secure4K Sidecar - reliable upload daemon (BusyBox/POSIX sh)

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
BUFFER_DIR="${SIDECAR_ROOT}/buffer"
SENT_DIR="${SIDECAR_ROOT}/buffer/sent"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"

. "${CONFIG_FILE}"
mkdir -p "${SENT_DIR}"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] [upload] $1" >> "${LOG_FILE}"; }

is_online() {
    wget -q --spider --timeout=5 "${BACKEND_URL}/health" >/dev/null 2>&1
}

upload_http() {
    FRAME_PATH="$1"
    FRAME_NAME=$(basename "${FRAME_PATH}")
    TIMESTAMP=$(echo "${FRAME_NAME}" | sed 's/frame_//;s/\.jpg//')
    RESPONSE=$(wget -q -O - \
        --header="Authorization: Bearer ${API_KEY}" \
        --header="X-Device-ID: ${DEVICE_ID}" \
        --header="X-Timestamp: ${TIMESTAMP}" \
        --header="X-Sidecar-Version: 0.1.0" \
        --post-file="${FRAME_PATH}" \
        --timeout=30 \
        "${BACKEND_URL}/v1/frames/ingest" 2>&1)
    EXIT_CODE=$?
    if [ "${EXIT_CODE}" -eq 0 ]; then
        return 0
    fi
    log "HTTP upload failed exit=${EXIT_CODE} response=${RESPONSE}"
    return 1
}

upload_mqtt() {
    FRAME_PATH="$1"
    [ -n "${MQTT_BROKER:-}" ] || return 1
    command -v mosquitto_pub >/dev/null 2>&1 || return 1
    command -v base64 >/dev/null 2>&1 || return 1

    FRAME_NAME=$(basename "${FRAME_PATH}")
    TIMESTAMP=$(echo "${FRAME_NAME}" | sed 's/frame_//;s/\.jpg//')
    TOPIC="${MQTT_TOPIC:-secure4k/frames}/${DEVICE_ID}"
    FRAME_B64=$(base64 "${FRAME_PATH}" 2>/dev/null | tr -d '\n')
    [ -n "${FRAME_B64}" ] || return 1
    PAYLOAD="{\"device_id\":\"${DEVICE_ID}\",\"ts\":${TIMESTAMP},\"frame\":\"${FRAME_B64}\"}"

    echo "${PAYLOAD}" | mosquitto_pub -h "${MQTT_BROKER}" -t "${TOPIC}" -q 1 -s -W 10 2>/dev/null
}

process_buffer() {
    PENDING=$(find "${BUFFER_DIR}" -maxdepth 1 -name 'frame_*.jpg' -type f 2>/dev/null | sort)
    [ -n "${PENDING}" ] || return 0
    UPLOADED=0
    FAILED=0

    for FRAME in ${PENDING}; do
        [ -f "${FRAME}" ] || continue
        SENT=0
        if [ -n "${MQTT_BROKER:-}" ] && upload_mqtt "${FRAME}"; then
            SENT=1
        elif upload_http "${FRAME}"; then
            SENT=1
        fi

        if [ "${SENT}" -eq 1 ]; then
            if mv "${FRAME}" "${SENT_DIR}/"; then
                UPLOADED=$((UPLOADED + 1))
                FAILED=0
            else
                log "ERROR: accepted frame could not move to sent dir: ${FRAME}"
                return 1
            fi
        else
            FAILED=$((FAILED + 1))
            log "Upload failed for $(basename "${FRAME}") consecutive=${FAILED}"
            [ "${FAILED}" -ge 3 ] && break
        fi
    done

    SENT_COUNT=$(find "${SENT_DIR}" -name 'frame_*.jpg' -type f 2>/dev/null | wc -l)
    if [ "${SENT_COUNT}" -gt 50 ]; then
        PRUNE=$((SENT_COUNT - 50))
        find "${SENT_DIR}" -name 'frame_*.jpg' -type f 2>/dev/null | sort | head -n "${PRUNE}" | while IFS= read -r OLD; do
            [ -n "${OLD}" ] && rm -f "${OLD}"
        done
    fi

    log "Batch complete uploaded=${UPLOADED} failed=${FAILED}"
    [ "${FAILED}" -eq 0 ]
}

send_heartbeat() {
    FREE_MEM=$(free 2>/dev/null | awk '/Mem:/{print $4}')
    [ -n "${FREE_MEM}" ] || FREE_MEM=0
    DISK_USE=$(df "${SIDECAR_ROOT}" 2>/dev/null | awk 'NR==2{print $5}' | tr -d '%')
    [ -n "${DISK_USE}" ] || DISK_USE=0
    UPTIME_SEC=$(awk '{print int($1)}' /proc/uptime 2>/dev/null)
    [ -n "${UPTIME_SEC}" ] || UPTIME_SEC=0
    BUFFERED=$(find "${BUFFER_DIR}" -maxdepth 1 -name 'frame_*.jpg' -type f 2>/dev/null | wc -l)
    TEMP=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null)
    [ -n "${TEMP}" ] || TEMP=0
    HEARTBEAT="{\"device_id\":\"${DEVICE_ID}\",\"ts\":$(date +%s),\"mem_free_kb\":${FREE_MEM},\"disk_pct\":${DISK_USE},\"uptime_s\":${UPTIME_SEC},\"buffered_frames\":${BUFFERED},\"temp_mc\":${TEMP},\"version\":\"0.1.0\"}"
    wget -q -O /dev/null \
        --header="Authorization: Bearer ${API_KEY}" \
        --header="Content-Type: application/json" \
        --post-data="${HEARTBEAT}" \
        --timeout=10 \
        "${BACKEND_URL}/v1/devices/heartbeat" 2>/dev/null
}

increase_backoff() {
    if [ "${BACKOFF}" -lt "${MAX_BACKOFF}" ]; then
        BACKOFF=$((BACKOFF * 2))
        [ "${BACKOFF}" -gt "${MAX_BACKOFF}" ] && BACKOFF=${MAX_BACKOFF}
    fi
}

BACKOFF=10
MAX_BACKOFF=300
HEARTBEAT_COUNTER=0
log "Upload daemon started"

while true; do
    if is_online; then
        if process_buffer; then
            BACKOFF=10
            HEARTBEAT_COUNTER=$((HEARTBEAT_COUNTER + 1))
            if [ "${HEARTBEAT_COUNTER}" -ge 6 ]; then
                if ! send_heartbeat; then
                    log "WARN: heartbeat failed"
                fi
                HEARTBEAT_COUNTER=0
            fi
        else
            log "WARN: upload batch failed; backoff=${BACKOFF}s"
            increase_backoff
        fi
    else
        log "Offline; retaining buffered frames backoff=${BACKOFF}s"
        increase_backoff
    fi
    sleep "${BACKOFF}"
done

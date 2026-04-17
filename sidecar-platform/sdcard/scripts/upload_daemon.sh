#!/bin/sh
# ============================================================
# Secure4K Sidecar — Upload Daemon
# ============================================================
# Watches the buffer directory and uploads frames to the
# backend. Supports HTTP POST and MQTT. Handles offline
# buffering and retry with exponential backoff.
# ============================================================

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
BUFFER_DIR="${SIDECAR_ROOT}/buffer"
SENT_DIR="${SIDECAR_ROOT}/buffer/sent"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"

. "${CONFIG_FILE}"

mkdir -p "${SENT_DIR}"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [upload] $1" >> "${LOG_FILE}"
}

# ---- Network check -------------------------------------------
is_online() {
    # Quick connectivity check — ping backend or known DNS
    if wget -q --spider --timeout=5 "${BACKEND_URL}/health" 2>/dev/null; then
        return 0
    fi
    # Fallback: try to resolve any DNS
    if nslookup ingest.secure4k.com >/dev/null 2>&1; then
        return 0
    fi
    return 1
}

# ---- HTTP upload ---------------------------------------------
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

    if [ ${EXIT_CODE} -eq 0 ]; then
        return 0
    else
        log "HTTP upload failed: exit=${EXIT_CODE} response=${RESPONSE}"
        return 1
    fi
}

# ---- MQTT upload (if broker configured) ----------------------
upload_mqtt() {
    FRAME_PATH="$1"

    if [ -z "${MQTT_BROKER}" ]; then
        return 1
    fi

    if ! command -v mosquitto_pub >/dev/null 2>&1; then
        log "WARN: mosquitto_pub not available, falling back to HTTP"
        return 1
    fi

    FRAME_NAME=$(basename "${FRAME_PATH}")
    TIMESTAMP=$(echo "${FRAME_NAME}" | sed 's/frame_//;s/\.jpg//')
    TOPIC="${MQTT_TOPIC}/${DEVICE_ID}"

    # MQTT payload: base64-encode the frame + metadata JSON header
    FRAME_B64=$(base64 "${FRAME_PATH}" 2>/dev/null | tr -d '\n')
    PAYLOAD="{\"device_id\":\"${DEVICE_ID}\",\"ts\":${TIMESTAMP},\"frame\":\"${FRAME_B64}\"}"

    echo "${PAYLOAD}" | mosquitto_pub \
        -h "${MQTT_BROKER}" \
        -t "${TOPIC}" \
        -s \
        --quiet 2>/dev/null

    return $?
}

# ---- Process buffer ------------------------------------------
process_buffer() {
    PENDING=$(find "${BUFFER_DIR}" -maxdepth 1 -name "frame_*.jpg" | sort)
    COUNT=$(echo "${PENDING}" | grep -c "frame_" 2>/dev/null || echo 0)

    if [ "${COUNT}" -eq 0 ]; then
        return
    fi

    log "Processing ${COUNT} buffered frames"
    UPLOADED=0
    FAILED=0

    for FRAME in ${PENDING}; do
        [ ! -f "${FRAME}" ] && continue

        # Try MQTT first if configured, fall back to HTTP
        SENT=0
        if [ -n "${MQTT_BROKER}" ]; then
            if upload_mqtt "${FRAME}"; then
                SENT=1
            fi
        fi

        if [ "${SENT}" -eq 0 ]; then
            if upload_http "${FRAME}"; then
                SENT=1
            fi
        fi

        if [ "${SENT}" -eq 1 ]; then
            # Move to sent dir (keep briefly for dedup)
            mv "${FRAME}" "${SENT_DIR}/"
            UPLOADED=$((UPLOADED + 1))
        else
            FAILED=$((FAILED + 1))
            # On failure, stop processing batch — network likely down
            if [ "${FAILED}" -ge 3 ]; then
                log "3 consecutive failures, pausing uploads"
                break
            fi
        fi
    done

    log "Batch complete: uploaded=${UPLOADED} failed=${FAILED}"

    # Clean sent dir (keep last 50 for dedup reference)
    SENT_COUNT=$(find "${SENT_DIR}" -name "frame_*.jpg" 2>/dev/null | wc -l)
    if [ "${SENT_COUNT}" -gt 50 ]; then
        PRUNE=$((SENT_COUNT - 50))
        find "${SENT_DIR}" -name "frame_*.jpg" | sort | head -n "${PRUNE}" | xargs rm -f
    fi
}

# ---- Heartbeat -----------------------------------------------
send_heartbeat() {
    FREE_MEM=$(free 2>/dev/null | awk '/Mem:/{print $4}' || echo "0")
    DISK_USE=$(df "${SIDECAR_ROOT}" 2>/dev/null | awk 'NR==2{print $5}' | tr -d '%' || echo "0")
    UPTIME_SEC=$(awk '{print int($1)}' /proc/uptime 2>/dev/null || echo "0")
    BUFFERED=$(find "${BUFFER_DIR}" -maxdepth 1 -name "frame_*.jpg" 2>/dev/null | wc -l)
    TEMP=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null || echo "0")

    HEARTBEAT="{\"device_id\":\"${DEVICE_ID}\",\"ts\":$(date +%s),\"mem_free_kb\":${FREE_MEM},\"disk_pct\":${DISK_USE},\"uptime_s\":${UPTIME_SEC},\"buffered_frames\":${BUFFERED},\"temp_mc\":${TEMP},\"version\":\"0.1.0\"}"

    wget -q -O /dev/null \
        --header="Authorization: Bearer ${API_KEY}" \
        --header="Content-Type: application/json" \
        --post-data="${HEARTBEAT}" \
        --timeout=10 \
        "${BACKEND_URL}/v1/devices/heartbeat" 2>/dev/null

    if [ $? -eq 0 ]; then
        log "Heartbeat sent (mem=${FREE_MEM}kB disk=${DISK_USE}% buf=${BUFFERED})"
    fi
}

# ---- Main loop -----------------------------------------------
BACKOFF=10
MAX_BACKOFF=300
HEARTBEAT_COUNTER=0
HEARTBEAT_EVERY=6   # Send heartbeat every 6th cycle (~60s at 10s interval)

log "Upload daemon started"

while true; do
    if is_online; then
        process_buffer
        BACKOFF=10  # Reset backoff on success

        HEARTBEAT_COUNTER=$((HEARTBEAT_COUNTER + 1))
        if [ "${HEARTBEAT_COUNTER}" -ge "${HEARTBEAT_EVERY}" ]; then
            send_heartbeat
            HEARTBEAT_COUNTER=0
        fi
    else
        log "Offline — buffering locally (backoff=${BACKOFF}s)"
        # Exponential backoff
        if [ "${BACKOFF}" -lt "${MAX_BACKOFF}" ]; then
            BACKOFF=$((BACKOFF * 2))
        fi
    fi

    sleep "${BACKOFF}"
done

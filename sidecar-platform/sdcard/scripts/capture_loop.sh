#!/bin/sh
# ============================================================
# Secure4K Sidecar — Frame Capture Loop
# ============================================================
# Captures frames from the camera's video pipeline and
# stores them in a local buffer directory for the upload
# daemon to process.
#
# Capture methods (tried in priority order):
#   1. RTSP stream snapshot via ffmpeg/avconv
#   2. HTTP snapshot endpoint (common on patched Tuya)
#   3. Direct V4L2 capture (if /dev/video0 exists)
#   4. Read latest file from camera's own recording dir
# ============================================================

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
BUFFER_DIR="${SIDECAR_ROOT}/buffer"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
CAPTURE_METHOD=""

. "${CONFIG_FILE}"

mkdir -p "${BUFFER_DIR}"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [capture] $1" >> "${LOG_FILE}"
}

# ---- Detect best capture method -----------------------------
detect_capture_method() {
    # Method 1: RTSP (most reliable if unlocked)
    CAMERA_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "127.0.0.1")

    # Check common RTSP ports
    for PORT in 554 6554 8554; do
        if nc -z -w2 "${CAMERA_IP}" "${PORT}" 2>/dev/null; then
            RTSP_URL="rtsp://${CAMERA_IP}:${PORT}/stream_0"
            # Verify we can actually grab a frame
            if command -v ffmpeg >/dev/null 2>&1; then
                CAPTURE_METHOD="rtsp_ffmpeg"
                log "Detected: RTSP on port ${PORT} + ffmpeg"
                return 0
            elif command -v avconv >/dev/null 2>&1; then
                CAPTURE_METHOD="rtsp_avconv"
                log "Detected: RTSP on port ${PORT} + avconv"
                return 0
            fi
        fi
    done

    # Method 2: HTTP snapshot (Tuya patched cameras expose this)
    for SNAP_PORT in 8080 80 8090; do
        SNAP_URL="http://127.0.0.1:${SNAP_PORT}"
        HTTP_CODE=$(wget -q -O /dev/null -S "${SNAP_URL}" 2>&1 | awk '/HTTP/{print $2}' | tail -1)
        if [ "${HTTP_CODE}" = "200" ]; then
            CAPTURE_METHOD="http_snapshot"
            log "Detected: HTTP snapshot on port ${SNAP_PORT}"
            return 0
        fi
    done

    # Method 3: V4L2 direct capture
    if [ -c /dev/video0 ]; then
        if command -v v4l2grab >/dev/null 2>&1 || command -v fswebcam >/dev/null 2>&1; then
            CAPTURE_METHOD="v4l2"
            log "Detected: V4L2 /dev/video0"
            return 0
        fi
    fi

    # Method 4: Scrape camera's own recording directory
    # Most cameras write MP4/JPG to a known path on the SD card
    for REC_DIR in \
        "/mnt/mmc01/record" \
        "/mnt/mmc01/DCIM" \
        "/mnt/sdcard/record" \
        "/tmp/record" \
        "/mnt/mmc01/tuya_record"; do
        if [ -d "${REC_DIR}" ]; then
            FILE_COUNT=$(find "${REC_DIR}" -name "*.jpg" -o -name "*.mp4" 2>/dev/null | wc -l)
            if [ "${FILE_COUNT}" -gt 0 ]; then
                CAPTURE_METHOD="file_scrape"
                SCRAPE_DIR="${REC_DIR}"
                log "Detected: File scrape from ${REC_DIR} (${FILE_COUNT} files)"
                return 0
            fi
        fi
    done

    log "ERROR: No capture method available"
    return 1
}

# ---- Capture functions ---------------------------------------

capture_rtsp_ffmpeg() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    ffmpeg -y -rtsp_transport tcp \
        -i "${RTSP_URL}" \
        -frames:v 1 \
        -q:v "${FRAME_QUALITY:-75}" \
        "${OUTFILE}" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "${OUTFILE}" ]; then
        echo "${OUTFILE}"
        return 0
    fi
    rm -f "${OUTFILE}"
    return 1
}

capture_rtsp_avconv() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    avconv -y -rtsp_transport tcp \
        -i "${RTSP_URL}" \
        -frames:v 1 \
        -q:v 5 \
        "${OUTFILE}" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "${OUTFILE}" ]; then
        echo "${OUTFILE}"
        return 0
    fi
    rm -f "${OUTFILE}"
    return 1
}

capture_http_snapshot() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    wget -q -O "${OUTFILE}" "${SNAP_URL}" 2>/dev/null
    if [ $? -eq 0 ] && [ -s "${OUTFILE}" ]; then
        echo "${OUTFILE}"
        return 0
    fi
    rm -f "${OUTFILE}"
    return 1
}

capture_v4l2() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    if command -v fswebcam >/dev/null 2>&1; then
        fswebcam -d /dev/video0 -r 1280x720 --jpeg "${FRAME_QUALITY:-75}" \
            --no-banner "${OUTFILE}" 2>/dev/null
    elif command -v v4l2grab >/dev/null 2>&1; then
        v4l2grab -d /dev/video0 -o "${OUTFILE}" 2>/dev/null
    fi
    if [ $? -eq 0 ] && [ -s "${OUTFILE}" ]; then
        echo "${OUTFILE}"
        return 0
    fi
    rm -f "${OUTFILE}"
    return 1
}

capture_file_scrape() {
    # Find the most recently modified image/video file
    LATEST=$(find "${SCRAPE_DIR}" \( -name "*.jpg" -o -name "*.jpeg" \) \
        -newer "${BUFFER_DIR}/.last_scrape" 2>/dev/null | head -1)

    if [ -z "${LATEST}" ]; then
        # No new JPEGs — try extracting frame from latest MP4
        LATEST_MP4=$(find "${SCRAPE_DIR}" -name "*.mp4" \
            -newer "${BUFFER_DIR}/.last_scrape" 2>/dev/null | head -1)
        if [ -n "${LATEST_MP4}" ] && command -v ffmpeg >/dev/null 2>&1; then
            OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
            ffmpeg -y -i "${LATEST_MP4}" -frames:v 1 -q:v 5 "${OUTFILE}" 2>/dev/null
            if [ $? -eq 0 ] && [ -s "${OUTFILE}" ]; then
                touch "${BUFFER_DIR}/.last_scrape"
                echo "${OUTFILE}"
                return 0
            fi
            rm -f "${OUTFILE}"
        fi
        return 1
    fi

    # Copy the JPEG to our buffer with standardized naming
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    cp "${LATEST}" "${OUTFILE}"
    touch "${BUFFER_DIR}/.last_scrape"
    echo "${OUTFILE}"
    return 0
}

# ---- Buffer management --------------------------------------
enforce_buffer_limit() {
    FRAME_COUNT=$(find "${BUFFER_DIR}" -name "frame_*.jpg" 2>/dev/null | wc -l)
    MAX=${OFFLINE_BUFFER_MAX:-500}
    if [ "${FRAME_COUNT}" -gt "${MAX}" ]; then
        EXCESS=$((FRAME_COUNT - MAX))
        find "${BUFFER_DIR}" -name "frame_*.jpg" | sort | head -n "${EXCESS}" | xargs rm -f
        log "Buffer pruned: removed ${EXCESS} oldest frames"
    fi
}

# ---- Main loop -----------------------------------------------
touch "${BUFFER_DIR}/.last_scrape"
detect_capture_method

if [ -z "${CAPTURE_METHOD}" ]; then
    log "FATAL: No capture method detected. Waiting 300s and retrying..."
    sleep 300
    exec "$0"
fi

CONSECUTIVE_FAILURES=0
MAX_FAILURES=10

while true; do
    case "${CAPTURE_METHOD}" in
        rtsp_ffmpeg)    FRAME=$(capture_rtsp_ffmpeg)    ;;
        rtsp_avconv)    FRAME=$(capture_rtsp_avconv)    ;;
        http_snapshot)  FRAME=$(capture_http_snapshot)   ;;
        v4l2)           FRAME=$(capture_v4l2)           ;;
        file_scrape)    FRAME=$(capture_file_scrape)    ;;
    esac

    if [ -n "${FRAME}" ]; then
        FSIZE=$(wc -c < "${FRAME}" 2>/dev/null || echo 0)
        log "Captured: $(basename ${FRAME}) (${FSIZE} bytes) via ${CAPTURE_METHOD}"
        CONSECUTIVE_FAILURES=0
    else
        CONSECUTIVE_FAILURES=$((CONSECUTIVE_FAILURES + 1))
        log "WARN: Capture failed (${CONSECUTIVE_FAILURES}/${MAX_FAILURES})"

        if [ "${CONSECUTIVE_FAILURES}" -ge "${MAX_FAILURES}" ]; then
            log "Too many failures, re-detecting capture method..."
            CAPTURE_METHOD=""
            detect_capture_method
            CONSECUTIVE_FAILURES=0
            if [ -z "${CAPTURE_METHOD}" ]; then
                log "FATAL: Still no method. Sleeping 600s."
                sleep 600
            fi
        fi
    fi

    enforce_buffer_limit
    sleep "${CAPTURE_INTERVAL:-60}"
done

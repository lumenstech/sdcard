#!/bin/sh
# Secure4K Sidecar - bounded frame capture loop (BusyBox/POSIX sh)

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
BUFFER_DIR="${SIDECAR_ROOT}/buffer"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
CAPTURE_METHOD=""

. "${CONFIG_FILE}"
mkdir -p "${BUFFER_DIR}"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] [capture] $1" >> "${LOG_FILE}"; }

buffer_count() {
    find "${BUFFER_DIR}" -maxdepth 1 -name 'frame_*.jpg' -type f 2>/dev/null | wc -l
}

buffer_has_capacity() {
    COUNT=$(buffer_count)
    MAX=${OFFLINE_BUFFER_MAX:-500}
    if [ "${COUNT}" -ge "${MAX}" ]; then
        log "WARN: buffer full (${COUNT}/${MAX}); capture paused until uploads drain"
        return 1
    fi
    return 0
}

detect_capture_method() {
    CAMERA_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -n "${CAMERA_IP}" ] || CAMERA_IP="127.0.0.1"

    for PORT in 554 6554 8554; do
        if nc -z -w 2 "${CAMERA_IP}" "${PORT}" 2>/dev/null; then
            RTSP_URL="rtsp://${CAMERA_IP}:${PORT}/stream_0"
            if command -v ffmpeg >/dev/null 2>&1; then
                CAPTURE_METHOD="rtsp_ffmpeg"; log "Detected RTSP ${PORT} + ffmpeg"; return 0
            fi
            if command -v avconv >/dev/null 2>&1; then
                CAPTURE_METHOD="rtsp_avconv"; log "Detected RTSP ${PORT} + avconv"; return 0
            fi
        fi
    done

    for SNAP_PORT in 8080 80 8090; do
        SNAP_URL="http://127.0.0.1:${SNAP_PORT}"
        if wget -q -O /dev/null --timeout=5 "${SNAP_URL}" 2>/dev/null; then
            CAPTURE_METHOD="http_snapshot"; log "Detected HTTP snapshot ${SNAP_PORT}"; return 0
        fi
    done

    if [ -c /dev/video0 ]; then
        if command -v fswebcam >/dev/null 2>&1 || command -v v4l2grab >/dev/null 2>&1; then
            CAPTURE_METHOD="v4l2"; log "Detected V4L2 /dev/video0"; return 0
        fi
    fi

    for REC_DIR in "/mnt/mmc01/record" "/mnt/mmc01/DCIM" "/mnt/sdcard/record" "/tmp/record" "/mnt/mmc01/tuya_record"; do
        if [ -d "${REC_DIR}" ]; then
            FILE_COUNT=$(find "${REC_DIR}" \( -name '*.jpg' -o -name '*.mp4' \) -type f 2>/dev/null | wc -l)
            if [ "${FILE_COUNT}" -gt 0 ]; then
                CAPTURE_METHOD="file_scrape"; SCRAPE_DIR="${REC_DIR}"; log "Detected file scrape ${REC_DIR}"; return 0
            fi
        fi
    done
    log "ERROR: no capture method available"
    return 1
}

capture_rtsp_ffmpeg() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    ffmpeg -y -rw_timeout 10000000 -rtsp_transport tcp -i "${RTSP_URL}" -frames:v 1 -q:v 5 "${OUTFILE}" 2>/dev/null
    [ $? -eq 0 ] && [ -s "${OUTFILE}" ] && { echo "${OUTFILE}"; return 0; }
    rm -f "${OUTFILE}"; return 1
}

capture_rtsp_avconv() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    avconv -y -rtsp_transport tcp -i "${RTSP_URL}" -frames:v 1 -q:v 5 "${OUTFILE}" 2>/dev/null
    [ $? -eq 0 ] && [ -s "${OUTFILE}" ] && { echo "${OUTFILE}"; return 0; }
    rm -f "${OUTFILE}"; return 1
}

capture_http_snapshot() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    wget -q -O "${OUTFILE}" --timeout=10 "${SNAP_URL}" 2>/dev/null
    [ $? -eq 0 ] && [ -s "${OUTFILE}" ] && { echo "${OUTFILE}"; return 0; }
    rm -f "${OUTFILE}"; return 1
}

capture_v4l2() {
    OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
    if command -v fswebcam >/dev/null 2>&1; then
        fswebcam -d /dev/video0 -r 1280x720 --jpeg "${FRAME_QUALITY:-75}" --no-banner "${OUTFILE}" 2>/dev/null
    else
        v4l2grab -d /dev/video0 -o "${OUTFILE}" 2>/dev/null
    fi
    [ $? -eq 0 ] && [ -s "${OUTFILE}" ] && { echo "${OUTFILE}"; return 0; }
    rm -f "${OUTFILE}"; return 1
}

capture_file_scrape() {
    LATEST=$(find "${SCRAPE_DIR}" \( -name '*.jpg' -o -name '*.jpeg' \) -newer "${BUFFER_DIR}/.last_scrape" -type f 2>/dev/null | head -n 1)
    if [ -n "${LATEST}" ]; then
        OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
        cp "${LATEST}" "${OUTFILE}" || return 1
        touch "${BUFFER_DIR}/.last_scrape"
        echo "${OUTFILE}"; return 0
    fi
    LATEST_MP4=$(find "${SCRAPE_DIR}" -name '*.mp4' -newer "${BUFFER_DIR}/.last_scrape" -type f 2>/dev/null | head -n 1)
    if [ -n "${LATEST_MP4}" ] && command -v ffmpeg >/dev/null 2>&1; then
        OUTFILE="${BUFFER_DIR}/frame_$(date '+%s').jpg"
        ffmpeg -y -i "${LATEST_MP4}" -frames:v 1 -q:v 5 "${OUTFILE}" 2>/dev/null
        [ $? -eq 0 ] && [ -s "${OUTFILE}" ] && { touch "${BUFFER_DIR}/.last_scrape"; echo "${OUTFILE}"; return 0; }
        rm -f "${OUTFILE}"
    fi
    return 1
}

touch "${BUFFER_DIR}/.last_scrape"
detect_capture_method || true
CONSECUTIVE_FAILURES=0

while true; do
    if ! buffer_has_capacity; then
        sleep "${CAPTURE_INTERVAL:-60}"
        continue
    fi
    if [ -z "${CAPTURE_METHOD}" ]; then
        sleep 60
        detect_capture_method || true
        continue
    fi

    FRAME=""
    case "${CAPTURE_METHOD}" in
        rtsp_ffmpeg) FRAME=$(capture_rtsp_ffmpeg) ;;
        rtsp_avconv) FRAME=$(capture_rtsp_avconv) ;;
        http_snapshot) FRAME=$(capture_http_snapshot) ;;
        v4l2) FRAME=$(capture_v4l2) ;;
        file_scrape) FRAME=$(capture_file_scrape) ;;
    esac

    if [ -n "${FRAME}" ]; then
        FSIZE=$(wc -c < "${FRAME}" 2>/dev/null || echo 0)
        log "Captured $(basename "${FRAME}") (${FSIZE} bytes) via ${CAPTURE_METHOD}"
        CONSECUTIVE_FAILURES=0
    else
        CONSECUTIVE_FAILURES=$((CONSECUTIVE_FAILURES + 1))
        log "WARN: capture failed (${CONSECUTIVE_FAILURES}/10)"
        if [ "${CONSECUTIVE_FAILURES}" -ge 10 ]; then
            CAPTURE_METHOD=""
            CONSECUTIVE_FAILURES=0
            detect_capture_method || true
        fi
    fi
    sleep "${CAPTURE_INTERVAL:-60}"
done

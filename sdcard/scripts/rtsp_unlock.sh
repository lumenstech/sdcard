#!/bin/sh
# Secure4K Sidecar - conservative RTSP detection/enablement (BusyBox/POSIX sh)
# Pilot rule: do not write outside /mnt/mmc01 and do not alter host firewall.

SIDECAR_ROOT="/mnt/mmc01"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
CHIP_INFO_FILE="${SIDECAR_ROOT}/config/chipinfo.txt"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] [rtsp_unlock] $1" >> "${LOG_FILE}"; }

port_open() { nc -z -w 2 127.0.0.1 "$1" >/dev/null 2>&1; }

detect_soc() {
    SOC="unknown"
    if [ -f /proc/cpuinfo ]; then
        if grep -qi 'Ingenic' /proc/cpuinfo 2>/dev/null; then SOC="ingenic";
        elif grep -qi 'hi3518' /proc/cpuinfo 2>/dev/null; then SOC="hisilicon"; fi
    fi
    if [ "${SOC}" = "unknown" ]; then
        [ -d /proc/jz ] && SOC="ingenic"
        [ -e /dev/hi_mipi ] && SOC="hisilicon"
        [ -d /proc/umap ] && SOC="hisilicon"
        [ -e /dev/gk ] && SOC="goke"
    fi
    if [ "${SOC}" = "unknown" ]; then
        if ps 2>/dev/null | grep -i 'ppsapp\|ppstrong' | grep -v grep >/dev/null 2>&1; then SOC="tuya_rts"; fi
    fi
    log "SoC detected: ${SOC}"
    printf 'soc=%s\ndetected_at=%s\n' "${SOC}" "$(date '+%Y-%m-%d %H:%M:%S')" > "${CHIP_INFO_FILE}"
    echo "${SOC}"
}

unlock_tuya_rts() {
    PATCHED_BIN="${SIDECAR_ROOT}/config/tycam"
    if [ ! -x "${PATCHED_BIN}" ]; then
        log "Tuya RTSP helper absent; leaving stock process untouched"
        return 1
    fi
    STOCK_PID=$(pidof ppsapp 2>/dev/null || pidof ppstrong 2>/dev/null)
    if [ -n "${STOCK_PID}" ]; then
        log "Stopping stock camera process PID=${STOCK_PID} for packaged RTSP helper"
        kill "${STOCK_PID}" 2>/dev/null || return 1
        sleep 2
    fi
    "${PATCHED_BIN}" &
    sleep 5
    if port_open 554; then log "RTSP enabled on 554"; return 0; fi
    log "WARN: packaged Tuya helper did not expose RTSP"
    return 1
}

unlock_ingenic() {
    if command -v rtspd >/dev/null 2>&1; then
        rtspd -p 554 &
        sleep 3
        if port_open 554; then log "RTSP enabled using existing rtspd"; return 0; fi
    fi
    # Only the SD-card-local flag is permitted in pilot mode.
    touch "${SIDECAR_ROOT}/rtsp_enable" 2>/dev/null || true
    log "WARN: no verified Ingenic RTSP service; SD-local enable flag written"
    return 1
}

unlock_existing_service() {
    # Do not scan/execute arbitrary firmware binaries or modify iptables in pilot.
    for PORT in 554 8554 6554; do
        if port_open "${PORT}"; then log "Existing RTSP service found on ${PORT}"; return 0; fi
    done
    log "WARN: no existing RTSP service; leaving firmware unchanged"
    return 1
}

SOC=$(detect_soc)
case "${SOC}" in
    tuya_rts) unlock_tuya_rts ;;
    ingenic) unlock_ingenic ;;
    hisilicon|goke) unlock_existing_service ;;
    *) log "Unknown SoC; RTSP unlock skipped"; exit 1 ;;
esac

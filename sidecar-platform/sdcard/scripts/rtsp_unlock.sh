#!/bin/sh
# ============================================================
# Secure4K Sidecar — RTSP Unlock
# ============================================================
# Attempts to enable local RTSP streaming by detecting the
# camera's SoC and applying known patches.
#
# Supported chipsets:
#   - Realtek RTS3903N (Tuya branded cameras)
#   - Ingenic T20/T21/T31 (budget IP cameras)
#   - HiSilicon Hi3518EV200/300 (common doorbells)
#   - Goke GK7102/GK7205V200 (newer cheap cameras)
# ============================================================

SIDECAR_ROOT="/mnt/mmc01"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
CHIP_INFO_FILE="${SIDECAR_ROOT}/config/chipinfo.txt"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [rtsp_unlock] $1" >> "${LOG_FILE}"
}

# ---- SoC detection -------------------------------------------
detect_soc() {
    SOC="unknown"

    # Method 1: /proc/cpuinfo
    if [ -f /proc/cpuinfo ]; then
        if grep -qi "Ingenic" /proc/cpuinfo 2>/dev/null; then
            SOC="ingenic"
        elif grep -qi "hi3518" /proc/cpuinfo 2>/dev/null; then
            SOC="hisilicon"
        fi
    fi

    # Method 2: Check for chipset-specific device nodes
    if [ "${SOC}" = "unknown" ]; then
        [ -d /proc/jz ] && SOC="ingenic"
        [ -e /dev/hi_mipi ] && SOC="hisilicon"
        [ -d /proc/umap ] && SOC="hisilicon"
        [ -e /dev/gk ] && SOC="goke"
    fi

    # Method 3: Check binary signatures in running processes
    if [ "${SOC}" = "unknown" ]; then
        PPSAPP=$(ps | grep -i "ppsapp\|ppstrong" | grep -v grep)
        if [ -n "${PPSAPP}" ]; then
            SOC="tuya_rts"
        fi
    fi

    # Method 4: Firmware strings
    if [ "${SOC}" = "unknown" ]; then
        for FW_FILE in /usr/lib/*.so /home/app/* /system/bin/*; do
            [ ! -f "${FW_FILE}" ] && continue
            if strings "${FW_FILE}" 2>/dev/null | grep -qi "rts3903"; then
                SOC="tuya_rts"
                break
            elif strings "${FW_FILE}" 2>/dev/null | grep -qi "gk720"; then
                SOC="goke"
                break
            fi
        done
    fi

    log "SoC detected: ${SOC}"
    echo "soc=${SOC}" > "${CHIP_INFO_FILE}"
    echo "detected_at=$(date '+%Y-%m-%d %H:%M:%S')" >> "${CHIP_INFO_FILE}"
    echo "${SOC}"
}

# ---- Tuya RTS3903N unlock ------------------------------------
unlock_tuya_rts() {
    log "Applying Tuya RTS3903N RTSP patch..."

    # The patched tycam binary (if present on SD card) replaces
    # the stock ppsapp and exposes RTSP on port 554
    PATCHED_BIN="${SIDECAR_ROOT}/config/tycam"
    if [ -x "${PATCHED_BIN}" ]; then
        # Kill stock app, launch patched version
        STOCK_PID=$(pidof ppsapp 2>/dev/null || pidof ppstrong 2>/dev/null)
        if [ -n "${STOCK_PID}" ]; then
            log "Stopping stock ppsapp (PID ${STOCK_PID})"
            kill "${STOCK_PID}" 2>/dev/null
            sleep 2
        fi
        "${PATCHED_BIN}" &
        sleep 5
        # Verify RTSP is up
        if nc -z -w2 127.0.0.1 554 2>/dev/null; then
            log "SUCCESS: RTSP on port 554"
            return 0
        fi
    fi

    # Alternative: LD_PRELOAD hook to intercept and expose stream
    HOOK_LIB="${SIDECAR_ROOT}/config/librtsp_hook.so"
    if [ -f "${HOOK_LIB}" ]; then
        export LD_PRELOAD="${HOOK_LIB}"
        log "LD_PRELOAD hook applied — RTSP should appear on next ppsapp restart"
        return 0
    fi

    log "WARN: No Tuya RTS patch binary or hook found"
    return 1
}

# ---- Ingenic unlock ------------------------------------------
unlock_ingenic() {
    log "Applying Ingenic T-series RTSP patch..."

    # Ingenic cameras often have rtspd available but not started
    if command -v rtspd >/dev/null 2>&1; then
        rtspd -p 554 &
        sleep 3
        if nc -z -w2 127.0.0.1 554 2>/dev/null; then
            log "SUCCESS: rtspd started on port 554"
            return 0
        fi
    fi

    # Some Ingenic FW checks for a flag file to enable RTSP
    for FLAG_PATH in \
        "/mnt/mmc01/rtsp_enable" \
        "/tmp/.rtsp_enable" \
        "/system/sdcard/config/rtsp_server.conf"; do
        touch "${FLAG_PATH}" 2>/dev/null
    done

    log "WARN: Ingenic flag files written, RTSP may start after next boot"
    return 1
}

# ---- HiSilicon unlock ----------------------------------------
unlock_hisilicon() {
    log "Applying HiSilicon RTSP patch..."

    # Check for mini_snmpd or rtspd in firmware
    for BIN in rtspd live555 mini_httpd; do
        FOUND=$(find / -name "${BIN}" -type f 2>/dev/null | head -1)
        if [ -n "${FOUND}" ] && [ -x "${FOUND}" ]; then
            "${FOUND}" &
            sleep 3
            log "Started ${BIN}"
        fi
    done

    # HiSilicon: try to open firewall for RTSP
    if command -v iptables >/dev/null 2>&1; then
        iptables -I INPUT -p tcp --dport 554 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p tcp --dport 8554 -j ACCEPT 2>/dev/null
        log "Firewall opened for RTSP ports"
    fi

    if nc -z -w2 127.0.0.1 554 2>/dev/null; then
        log "SUCCESS: RTSP on port 554"
        return 0
    fi

    log "WARN: HiSilicon unlock incomplete"
    return 1
}

# ---- Goke unlock ---------------------------------------------
unlock_goke() {
    log "Applying Goke RTSP patch..."
    # Goke cameras with OpenIPC support can be reflashed
    # For now, try the same patterns as HiSilicon
    unlock_hisilicon
    return $?
}

# ---- Main ----------------------------------------------------
SOC=$(detect_soc)

case "${SOC}" in
    tuya_rts)   unlock_tuya_rts   ;;
    ingenic)    unlock_ingenic    ;;
    hisilicon)  unlock_hisilicon  ;;
    goke)       unlock_goke       ;;
    *)
        log "Unknown SoC (${SOC}) — RTSP unlock skipped"
        log "Capture will fall back to file scrape or HTTP snapshot"
        exit 1
        ;;
esac

#!/bin/sh
# ============================================================
# Secure4K Sidecar — OTA Update Checker
# ============================================================
# Periodically checks the backend for script updates.
# Downloads a tarball, verifies checksum, extracts, and
# signals the watchdog to restart daemons.
# ============================================================

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
UPDATE_DIR="${SIDECAR_ROOT}/updates"
CURRENT_VERSION="0.1.0"

. "${CONFIG_FILE}"

mkdir -p "${UPDATE_DIR}"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ota] $1" >> "${LOG_FILE}"
}

check_update() {
    RESPONSE=$(wget -q -O - \
        --header="Authorization: Bearer ${API_KEY}" \
        --header="X-Device-ID: ${DEVICE_ID}" \
        --header="X-Current-Version: ${CURRENT_VERSION}" \
        --timeout=15 \
        "${BACKEND_URL}/v1/ota/check" 2>&1)

    if [ $? -ne 0 ]; then
        return 1
    fi

    # Parse response
    NEW_VERSION=$(echo "${RESPONSE}" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    DOWNLOAD_URL=$(echo "${RESPONSE}" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    CHECKSUM=$(echo "${RESPONSE}" | sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

    if [ -z "${NEW_VERSION}" ] || [ "${NEW_VERSION}" = "${CURRENT_VERSION}" ]; then
        return 1
    fi
    # Without a URL there is nothing to download. The backend returns the
    # bare version (no url, no sha256) when no manifest exists; that's the
    # "no update available" path and must not error.
    if [ -z "${DOWNLOAD_URL}" ]; then
        return 1
    fi
    # Pilot policy: refuse to apply unverified bundles.
    if [ -z "${CHECKSUM}" ]; then
        log "ERROR: manifest missing sha256 — refusing to apply update"
        return 1
    fi

    log "Update available: ${CURRENT_VERSION} -> ${NEW_VERSION}"

    # If the URL is a backend-relative path (/v1/ota/bundle/...), prefix the
    # configured BACKEND_URL.
    case "${DOWNLOAD_URL}" in
        /*) DOWNLOAD_URL="${BACKEND_URL}${DOWNLOAD_URL}" ;;
    esac

    # Download update tarball
    TARBALL="${UPDATE_DIR}/sidecar-${NEW_VERSION}.tar.gz"
    wget -q -O "${TARBALL}" \
        --header="Authorization: Bearer ${API_KEY}" \
        --timeout=60 \
        "${DOWNLOAD_URL}" 2>/dev/null

    if [ $? -ne 0 ] || [ ! -s "${TARBALL}" ]; then
        log "ERROR: Download failed"
        rm -f "${TARBALL}"
        return 1
    fi

    # Verify checksum. NO checksum tool means we MUST refuse the update —
    # anything else risks shipping unverified code to customer hardware.
    SUMCMD=""
    if command -v sha256sum >/dev/null 2>&1; then
        SUMCMD="sha256sum"
    elif command -v openssl >/dev/null 2>&1; then
        SUMCMD="openssl"
    fi
    if [ -z "${SUMCMD}" ]; then
        log "ERROR: no sha256sum/openssl available — refusing update"
        rm -f "${TARBALL}"
        return 1
    fi

    if [ "${SUMCMD}" = "sha256sum" ]; then
        ACTUAL=$(sha256sum "${TARBALL}" | awk '{print $1}')
    else
        ACTUAL=$(openssl dgst -sha256 "${TARBALL}" | awk '{print $NF}')
    fi
    if [ "${ACTUAL}" != "${CHECKSUM}" ]; then
        log "ERROR: Checksum mismatch (expected=${CHECKSUM} got=${ACTUAL})"
        rm -f "${TARBALL}"
        return 1
    fi
    log "Checksum verified"

    # Backup current scripts
    cp -r "${SIDECAR_ROOT}/scripts" "${UPDATE_DIR}/scripts_backup_${CURRENT_VERSION}"

    # Extract update
    tar -xzf "${TARBALL}" -C "${SIDECAR_ROOT}/" 2>/dev/null
    if [ $? -ne 0 ]; then
        log "ERROR: Extraction failed, restoring backup"
        rm -rf "${SIDECAR_ROOT}/scripts"
        mv "${UPDATE_DIR}/scripts_backup_${CURRENT_VERSION}" "${SIDECAR_ROOT}/scripts"
        rm -f "${TARBALL}"
        return 1
    fi

    # Make scripts executable
    chmod +x "${SIDECAR_ROOT}/scripts/"*.sh 2>/dev/null

    log "SUCCESS: Updated to ${NEW_VERSION}"
    log "Signaling watchdog for daemon restart..."

    # Kill capture and upload daemons — watchdog in initrun.sh will restart them
    pkill -f capture_loop.sh 2>/dev/null
    pkill -f upload_daemon.sh 2>/dev/null

    # Cleanup
    rm -f "${TARBALL}"
    rm -rf "${UPDATE_DIR}/scripts_backup_${CURRENT_VERSION}"

    return 0
}

# ---- Main loop -----------------------------------------------
log "OTA checker started (current=${CURRENT_VERSION}, interval=${OTA_CHECK_INTERVAL}s)"

while true; do
    sleep "${OTA_CHECK_INTERVAL:-3600}"
    check_update
done

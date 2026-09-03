#!/bin/sh
# Secure4K Sidecar - checksum-enforced staged OTA (BusyBox/POSIX sh)

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"
UPDATE_DIR="${SIDECAR_ROOT}/updates"
VERSION_FILE="${SIDECAR_ROOT}/config/version"

. "${CONFIG_FILE}"
mkdir -p "${UPDATE_DIR}"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ota] $1" >> "${LOG_FILE}"; }

current_version() {
    if [ -s "${VERSION_FILE}" ]; then
        cat "${VERSION_FILE}"
    else
        echo "0.1.0"
    fi
}

sha256_file() {
    FILE="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${FILE}" | awk '{print $1}'
        return $?
    fi
    if command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "${FILE}" 2>/dev/null | awk '{print $NF}'
        return $?
    fi
    return 1
}

check_update() {
    CURRENT_VERSION=$(current_version)
    RESPONSE=$(wget -q -O - \
        --header="Authorization: Bearer ${API_KEY}" \
        --header="X-Device-ID: ${DEVICE_ID}" \
        --header="X-Current-Version: ${CURRENT_VERSION}" \
        --timeout=15 \
        "${BACKEND_URL}/v1/ota/check" 2>&1)
    [ $? -eq 0 ] || { log "WARN: OTA check failed"; return 1; }

    NEW_VERSION=$(echo "${RESPONSE}" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    DOWNLOAD_URL=$(echo "${RESPONSE}" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    CHECKSUM=$(echo "${RESPONSE}" | sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

    [ -n "${NEW_VERSION}" ] || return 1
    [ "${NEW_VERSION}" != "${CURRENT_VERSION}" ] || return 1
    if [ -z "${DOWNLOAD_URL}" ] || [ -z "${CHECKSUM}" ]; then
        log "ERROR: OTA metadata missing URL or SHA-256; refusing update"
        return 1
    fi

    TARBALL="${UPDATE_DIR}/sidecar-${NEW_VERSION}.tar.gz"
    STAGE="${UPDATE_DIR}/stage-${NEW_VERSION}"
    rm -rf "${STAGE}"
    mkdir -p "${STAGE}"

    if ! wget -q -O "${TARBALL}" --header="Authorization: Bearer ${API_KEY}" --timeout=60 "${DOWNLOAD_URL}" 2>/dev/null; then
        log "ERROR: OTA download failed"
        rm -f "${TARBALL}"
        rm -rf "${STAGE}"
        return 1
    fi

    ACTUAL=$(sha256_file "${TARBALL}")
    if [ $? -ne 0 ] || [ -z "${ACTUAL}" ]; then
        log "ERROR: no SHA-256 implementation available; refusing update"
        rm -f "${TARBALL}"
        rm -rf "${STAGE}"
        return 1
    fi
    if [ "${ACTUAL}" != "${CHECKSUM}" ]; then
        log "ERROR: OTA checksum mismatch expected=${CHECKSUM} actual=${ACTUAL}"
        rm -f "${TARBALL}"
        rm -rf "${STAGE}"
        return 1
    fi

    if tar -tzf "${TARBALL}" 2>/dev/null | grep -E '(^/|(^|/)\.\.(/|$))' >/dev/null 2>&1; then
        log "ERROR: unsafe path in OTA archive"
        rm -f "${TARBALL}"
        rm -rf "${STAGE}"
        return 1
    fi
    if ! tar -xzf "${TARBALL}" -C "${STAGE}" 2>/dev/null; then
        log "ERROR: OTA extraction failed"
        rm -f "${TARBALL}"
        rm -rf "${STAGE}"
        return 1
    fi

    if [ ! -d "${STAGE}/scripts" ]; then
        log "ERROR: OTA archive missing scripts/"
        rm -f "${TARBALL}"
        rm -rf "${STAGE}"
        return 1
    fi

    NEW_SCRIPTS="${UPDATE_DIR}/scripts.new"
    PREVIOUS="${UPDATE_DIR}/scripts.previous"
    rm -rf "${NEW_SCRIPTS}"
    cp -r "${STAGE}/scripts" "${NEW_SCRIPTS}" || return 1
    chmod +x "${NEW_SCRIPTS}"/*.sh 2>/dev/null || {
        log "ERROR: OTA scripts are not executable"
        rm -rf "${NEW_SCRIPTS}" "${STAGE}"
        rm -f "${TARBALL}"
        return 1
    }

    rm -rf "${PREVIOUS}"
    if ! mv "${SIDECAR_ROOT}/scripts" "${PREVIOUS}"; then
        log "ERROR: could not preserve previous scripts"
        rm -rf "${NEW_SCRIPTS}" "${STAGE}"
        rm -f "${TARBALL}"
        return 1
    fi
    if ! mv "${NEW_SCRIPTS}" "${SIDECAR_ROOT}/scripts"; then
        log "ERROR: OTA activation failed; rolling back"
        mv "${PREVIOUS}" "${SIDECAR_ROOT}/scripts" 2>/dev/null
        rm -rf "${STAGE}"
        rm -f "${TARBALL}"
        return 1
    fi

    echo "${NEW_VERSION}" > "${VERSION_FILE}.tmp" || {
        log "ERROR: version marker write failed; rolling back"
        rm -rf "${SIDECAR_ROOT}/scripts"
        mv "${PREVIOUS}" "${SIDECAR_ROOT}/scripts" 2>/dev/null
        return 1
    }
    mv "${VERSION_FILE}.tmp" "${VERSION_FILE}"
    log "SUCCESS: activated OTA ${CURRENT_VERSION} -> ${NEW_VERSION}; previous scripts retained at ${PREVIOUS}"

    pkill -f capture_loop.sh 2>/dev/null || true
    pkill -f upload_daemon.sh 2>/dev/null || true

    rm -f "${TARBALL}"
    rm -rf "${STAGE}"
    return 0
}

log "OTA checker started interval=${OTA_CHECK_INTERVAL:-3600}s"
while true; do
    sleep "${OTA_CHECK_INTERVAL:-3600}"
    check_update || true
done

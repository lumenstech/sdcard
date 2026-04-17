#!/bin/sh
# ============================================================
# Secure4K Sidecar — Device Registration
# ============================================================

SIDECAR_ROOT="/mnt/mmc01"
CONFIG_FILE="${SIDECAR_ROOT}/config/device.conf"
LOG_FILE="${SIDECAR_ROOT}/sidecar.log"

. "${CONFIG_FILE}"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [register] $1" >> "${LOG_FILE}"
}

# Gather device fingerprint
MAC=$(cat /sys/class/net/wlan0/address 2>/dev/null || cat /sys/class/net/eth0/address 2>/dev/null || echo "00:00:00:00:00:00")
CHIP_INFO=""
[ -f "${SIDECAR_ROOT}/config/chipinfo.txt" ] && CHIP_INFO=$(cat "${SIDECAR_ROOT}/config/chipinfo.txt" | tr '\n' ',' | sed 's/,$//')
KERNEL=$(uname -r 2>/dev/null || echo "unknown")
TOTAL_MEM=$(free 2>/dev/null | awk '/Mem:/{print $2}' || echo "0")

PAYLOAD="{\"api_key\":\"${API_KEY}\",\"mac\":\"${MAC}\",\"chip_info\":\"${CHIP_INFO}\",\"kernel\":\"${KERNEL}\",\"mem_kb\":${TOTAL_MEM},\"sidecar_version\":\"0.1.0\"}"

log "Registering device (MAC=${MAC})..."

RESPONSE=$(wget -q -O - \
    --header="Content-Type: application/json" \
    --header="Authorization: Bearer ${API_KEY}" \
    --post-data="${PAYLOAD}" \
    --timeout=30 \
    "${BACKEND_URL}/v1/devices/register" 2>&1)

if [ $? -ne 0 ]; then
    log "ERROR: Registration failed: ${RESPONSE}"
    exit 1
fi

# Parse device_id from JSON response (minimal jq alternative)
NEW_DEVICE_ID=$(echo "${RESPONSE}" | sed -n 's/.*"device_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

if [ -z "${NEW_DEVICE_ID}" ]; then
    log "ERROR: No device_id in response: ${RESPONSE}"
    exit 1
fi

# Update config file with assigned device ID
sed -i "s/^DEVICE_ID=.*/DEVICE_ID=\"${NEW_DEVICE_ID}\"/" "${CONFIG_FILE}"
log "SUCCESS: Registered as ${NEW_DEVICE_ID}"

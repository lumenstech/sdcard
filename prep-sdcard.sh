#!/bin/bash
# ============================================================
# Secure4K Sidecar — SD Card Prep Tool
# ============================================================
# Run this on your Mac/Linux workstation to prepare an SD card
# for a customer's camera.
#
# Usage: ./prep-sdcard.sh /Volumes/SDCARD
# ============================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SDCARD_SRC="${SCRIPT_DIR}/sdcard"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
echo "╔══════════════════════════════════════════════╗"
echo "║   Secure4K Sidecar — SD Card Prep Tool      ║"
echo "╚══════════════════════════════════════════════╝"
echo -e "${NC}"

if [ $# -lt 1 ]; then
    echo -e "${YELLOW}Usage: $0 /path/to/mounted/sdcard${NC}"
    echo ""
    echo "Available drives:"
    if [[ "$OSTYPE" == "darwin"* ]]; then
        ls -d /Volumes/*/ 2>/dev/null | grep -v "Macintosh"
    else
        lsblk -o NAME,SIZE,MOUNTPOINT | grep -E "sd[a-z][0-9]"
    fi
    exit 1
fi

TARGET="$1"

if [ ! -d "${TARGET}" ]; then
    echo -e "${RED}ERROR: ${TARGET} is not a valid directory${NC}"
    exit 1
fi

if [ ! -w "${TARGET}" ]; then
    echo -e "${RED}ERROR: ${TARGET} is not writable${NC}"
    exit 1
fi

echo -e "${GREEN}Target: ${TARGET}${NC}"
echo ""

echo -e "${CYAN}Device Configuration${NC}"
echo "─────────────────────"

read -rp "Backend URL [https://ingest.secure4k.com]: " BACKEND_URL
BACKEND_URL="${BACKEND_URL:-https://ingest.secure4k.com}"

read -rp "API Key: " API_KEY
if [ -z "${API_KEY}" ]; then
    echo -e "${RED}ERROR: API key is required${NC}"
    exit 1
fi

read -rp "Capture interval in seconds [60]: " CAPTURE_INTERVAL
CAPTURE_INTERVAL="${CAPTURE_INTERVAL:-60}"

read -rp "Enable RTSP unlock attempt? [Y/n]: " RTSP_ENABLE
if [[ "${RTSP_ENABLE}" =~ ^[Nn] ]]; then
    RTSP_ENABLE=0
else
    RTSP_ENABLE=1
fi

read -rp "MQTT broker (blank for HTTP-only): " MQTT_BROKER

echo ""
echo -e "${CYAN}WiFi Fallback (optional)${NC}"
read -rp "WiFi SSID: " WIFI_SSID
if [ -n "${WIFI_SSID}" ]; then
    read -rsp "WiFi Password: " WIFI_PASS
    echo ""
fi

echo ""
echo -e "${YELLOW}Configuration Summary:${NC}"
echo "  Backend:    ${BACKEND_URL}"
echo "  API Key:    ${API_KEY:0:8}..."
echo "  Interval:   ${CAPTURE_INTERVAL}s"
echo "  RTSP:       $([ ${RTSP_ENABLE} -eq 1 ] && echo 'enabled' || echo 'disabled')"
echo "  MQTT:       ${MQTT_BROKER:-none}"
echo "  WiFi:       ${WIFI_SSID:-none}"
echo ""

read -rp "Flash to ${TARGET}? [y/N]: " CONFIRM
if [[ ! "${CONFIRM}" =~ ^[Yy] ]]; then
    echo "Cancelled."
    exit 0
fi

echo ""
echo -e "${CYAN}Copying sidecar files...${NC}"

rm -rf "${TARGET}/scripts" "${TARGET}/config" "${TARGET}/buffer" "${TARGET}/updates"
rm -f "${TARGET}/initrun.sh" "${TARGET}/run.sh" "${TARGET}/custom.sh"
rm -f "${TARGET}/sidecar.log" "${TARGET}/sidecar.pid"

cp -r "${SDCARD_SRC}/scripts" "${TARGET}/"
mkdir -p "${TARGET}/config" "${TARGET}/buffer" "${TARGET}/updates"

cp "${SDCARD_SRC}/initrun.sh" "${TARGET}/initrun.sh"
# FAT32 does not reliably support Unix symlinks. Use real boot-hook copies.
cp "${SDCARD_SRC}/initrun.sh" "${TARGET}/run.sh"
cp "${SDCARD_SRC}/initrun.sh" "${TARGET}/custom.sh"
cp "${SDCARD_SRC}/config/version" "${TARGET}/config/version"

chmod +x "${TARGET}/initrun.sh" "${TARGET}/run.sh" "${TARGET}/custom.sh"
chmod +x "${TARGET}/scripts/"*.sh

echo -e "${CYAN}Writing device config...${NC}"

cat > "${TARGET}/config/device.conf" <<EOF
# Secure4K Sidecar — Auto-generated config
# Created: $(date '+%Y-%m-%d %H:%M:%S')

DEVICE_ID="UNREGISTERED"
BACKEND_URL="${BACKEND_URL}"
API_KEY="${API_KEY}"
CAPTURE_INTERVAL=${CAPTURE_INTERVAL}
RTSP_ENABLE=${RTSP_ENABLE}
FRAME_QUALITY=75
OFFLINE_BUFFER_MAX=500
OTA_CHECK_INTERVAL=3600
MQTT_BROKER="${MQTT_BROKER}"
MQTT_TOPIC="secure4k/frames"
WIFI_SSID="${WIFI_SSID}"
WIFI_PASS="${WIFI_PASS:-}"
EOF

echo ""
echo -e "${CYAN}Verifying...${NC}"

FILE_COUNT=$(find "${TARGET}/scripts" -name "*.sh" | wc -l)
echo "  Scripts:     ${FILE_COUNT} files"
echo "  Config:      $([ -f "${TARGET}/config/device.conf" ] && echo 'OK' || echo 'MISSING')"
echo "  Version:     $([ -f "${TARGET}/config/version" ] && cat "${TARGET}/config/version" || echo 'MISSING')"
echo "  Buffer:      $([ -d "${TARGET}/buffer" ] && echo 'OK' || echo 'MISSING')"
echo "  Updates:     $([ -d "${TARGET}/updates" ] && echo 'OK' || echo 'MISSING')"
echo "  initrun.sh:  $([ -f "${TARGET}/initrun.sh" ] && echo 'OK' || echo 'MISSING')"
echo "  run.sh:      $([ -f "${TARGET}/run.sh" ] && echo 'OK' || echo 'MISSING')"
echo "  custom.sh:   $([ -f "${TARGET}/custom.sh" ] && echo 'OK' || echo 'MISSING')"

echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║   SD card ready!                             ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════╝${NC}"
echo ""
echo "Next steps:"
echo "  1. Safely eject the SD card"
echo "  2. Insert into the pilot doorbell/IP camera"
echo "  3. Power cycle the camera"
echo "  4. Wait 2-3 minutes for boot + registration"
echo "  5. Check backend for the new device and first frame"
echo ""

if [[ "$OSTYPE" == "darwin"* ]]; then
    read -rp "Eject ${TARGET} now? [y/N]: " EJECT
    if [[ "${EJECT}" =~ ^[Yy] ]]; then
        diskutil eject "${TARGET}" 2>/dev/null && echo -e "${GREEN}Ejected.${NC}" || echo -e "${YELLOW}Please eject manually.${NC}"
    fi
fi

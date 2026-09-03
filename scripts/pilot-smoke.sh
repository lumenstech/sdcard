#!/bin/sh
set -eu

BASE_URL=${BASE_URL:-http://localhost:8090}
API_KEY=${API_KEY:-${VALID_API_KEYS:-}}
[ -n "${API_KEY}" ] || { echo "API_KEY or VALID_API_KEYS is required" >&2; exit 2; }
API_KEY=$(printf '%s' "${API_KEY}" | cut -d, -f1)
MAC=${PILOT_MAC:-02:44:4b:00:00:01}
FRAME=${PILOT_FRAME:-}
TMP_FRAME=""

cleanup() { [ -n "${TMP_FRAME}" ] && rm -f "${TMP_FRAME}"; }
trap cleanup EXIT INT TERM

if [ -z "${FRAME}" ]; then
    TMP_FRAME=$(mktemp)
    # Minimal JPEG-like pilot payload; storage/inference fallback does not require decode.
    printf '\377\330SECURE4K-PILOT\377\331' > "${TMP_FRAME}"
    FRAME=${TMP_FRAME}
fi

health=$(curl -fsS --max-time 10 "${BASE_URL}/health")
printf '%s\n' "health: ${health}"

reg=$(curl -fsS --max-time 15 -X POST "${BASE_URL}/v1/devices/register" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H 'Content-Type: application/json' \
    -d "{\"api_key\":\"${API_KEY}\",\"mac\":\"${MAC}\",\"chip_info\":\"pilot-smoke\",\"kernel\":\"test\",\"mem_kb\":65536,\"sidecar_version\":\"0.1.0\"}")
device_id=$(printf '%s' "${reg}" | sed -n 's/.*"device_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ -n "${device_id}" ] || { echo "registration did not return device_id: ${reg}" >&2; exit 1; }
printf '%s\n' "registered: ${device_id}"

reg2=$(curl -fsS --max-time 15 -X POST "${BASE_URL}/v1/devices/register" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H 'Content-Type: application/json' \
    -d "{\"api_key\":\"${API_KEY}\",\"mac\":\"${MAC}\",\"chip_info\":\"pilot-smoke\",\"kernel\":\"test\",\"mem_kb\":65536,\"sidecar_version\":\"0.1.0\"}")
device_id2=$(printf '%s' "${reg2}" | sed -n 's/.*"device_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ "${device_id}" = "${device_id2}" ] || { echo "registration is not idempotent" >&2; exit 1; }

ts=$(date +%s)
first=$(curl -fsS --max-time 15 -X POST "${BASE_URL}/v1/frames/ingest" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "X-Device-ID: ${device_id}" \
    -H "X-Timestamp: ${ts}" \
    --data-binary "@${FRAME}")
printf '%s\n' "first ingest: ${first}"

second=$(curl -fsS --max-time 15 -X POST "${BASE_URL}/v1/frames/ingest" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "X-Device-ID: ${device_id}" \
    -H "X-Timestamp: ${ts}" \
    --data-binary "@${FRAME}")
printf '%s\n' "duplicate ingest: ${second}"
printf '%s' "${second}" | grep -q '"status":"duplicate"' || { echo "duplicate ingest was not acknowledged idempotently" >&2; exit 1; }

sleep 2
events=$(curl -fsS --max-time 15 "${BASE_URL}/v1/events?device_id=${device_id}&limit=20" -H "Authorization: Bearer ${API_KEY}")
printf '%s\n' "events: ${events}"
printf '%s' "${events}" | grep -q '"count":1' || { echo "expected exactly one persisted event for duplicate upload" >&2; exit 1; }

echo "pilot smoke: PASS"

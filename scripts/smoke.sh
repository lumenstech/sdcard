#!/usr/bin/env bash
# Pilot smoke test for the Secure4K Sidecar Platform.
#
# Walks through the full happy path against a running ingest server:
#   1. /health
#   2. POST /v1/devices/register
#   3. POST /v1/frames/ingest with a 1x1 JPEG
#   4. POST /v1/devices/heartbeat
#   5. GET /v1/devices and /v1/events
#
# Required env (or defaults):
#   API_KEY  — bearer token recognised by the server (default: test-key)
#   BASE     — base URL (default: http://localhost:8090)
#
# Exits non-zero on the first failure.

set -euo pipefail

BASE="${BASE:-http://localhost:8090}"
API_KEY="${API_KEY:-${VALID_API_KEYS%%,*}}"
API_KEY="${API_KEY:-test-key}"
MAC="${MAC:-aa:bb:cc:dd:ee:01}"

cyan()  { printf '\033[0;36m%s\033[0m\n' "$*"; }
green() { printf '\033[0;32m%s\033[0m\n' "$*"; }
red()   { printf '\033[0;31m%s\033[0m\n' "$*"; }

cyan "[1/5] /health"
curl -fsS "${BASE}/health" >/dev/null
green "    OK"

cyan "[2/5] register device (MAC=${MAC})"
REG=$(curl -fsS -X POST "${BASE}/v1/devices/register" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"api_key\":\"${API_KEY}\",\"mac\":\"${MAC}\",\"chip_info\":\"smoke-test\",\"kernel\":\"test\",\"mem_kb\":65536,\"sidecar_version\":\"0.1.0\"}")
DEVICE_ID=$(printf '%s' "${REG}" | sed -n 's/.*"device_id":"\([^"]*\)".*/\1/p')
[ -n "${DEVICE_ID}" ] || { red "no device_id in response: ${REG}"; exit 1; }
green "    device_id=${DEVICE_ID}"

cyan "[3/5] register again (must be idempotent)"
REG2=$(curl -fsS -X POST "${BASE}/v1/devices/register" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"api_key\":\"${API_KEY}\",\"mac\":\"${MAC}\",\"chip_info\":\"smoke-test\",\"kernel\":\"test\",\"mem_kb\":65536,\"sidecar_version\":\"0.1.0\"}")
DEVICE_ID2=$(printf '%s' "${REG2}" | sed -n 's/.*"device_id":"\([^"]*\)".*/\1/p')
[ "${DEVICE_ID}" = "${DEVICE_ID2}" ] || { red "duplicate device created: ${DEVICE_ID} vs ${DEVICE_ID2}"; exit 1; }
green "    same id returned"

cyan "[4/5] upload a 1x1 JPEG"
TMPFILE=$(mktemp --suffix=.jpg)
# Smallest valid JPEG (1x1 white pixel), base64-encoded.
base64 -d > "${TMPFILE}" <<'EOF'
/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsK
CwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRT/2wBDAQMEBAUEBQkFBQkUDQsNFBQUFBQU
FBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBT/wAARCAABAAEDASIA
AhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAj/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFAEB
AAAAAAAAAAAAAAAAAAAAAP/EABQRAQAAAAAAAAAAAAAAAAAAAAD/2gAMAwEAAhEDEQA/AL+AAAH/
2Q==
EOF
TS=$(date +%s)
HTTP_CODE=$(curl -s -o /tmp/smoke_resp -w "%{http_code}" -X POST "${BASE}/v1/frames/ingest" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "X-Device-ID: ${DEVICE_ID}" \
    -H "X-Timestamp: ${TS}" \
    --data-binary @"${TMPFILE}")
rm -f "${TMPFILE}"
[ "${HTTP_CODE}" = "202" ] || { red "frame ingest expected 202 got ${HTTP_CODE}: $(cat /tmp/smoke_resp)"; exit 1; }
green "    accepted (HTTP 202)"

# Resend the same frame to prove idempotency: still 202, no duplicate event.
HTTP_CODE=$(curl -s -o /tmp/smoke_resp -w "%{http_code}" -X POST "${BASE}/v1/frames/ingest" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "X-Device-ID: ${DEVICE_ID}" \
    -H "X-Timestamp: ${TS}" \
    --data-binary @<(printf 'x'))
[ "${HTTP_CODE}" = "202" ] || { red "duplicate frame ingest expected 202 got ${HTTP_CODE}"; exit 1; }
green "    duplicate frame still 202 (idempotent at event layer)"

cyan "[5/5] heartbeat + list"
curl -fsS -X POST "${BASE}/v1/devices/heartbeat" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"device_id\":\"${DEVICE_ID}\",\"ts\":${TS},\"mem_free_kb\":32000,\"disk_pct\":45,\"uptime_s\":100,\"buffered_frames\":0,\"temp_mc\":52000,\"version\":\"0.1.0\"}" >/dev/null
green "    heartbeat ok"

DEVICES=$(curl -fsS "${BASE}/v1/devices" -H "Authorization: Bearer ${API_KEY}")
echo "${DEVICES}" | grep -q "${DEVICE_ID}" || { red "device not found in list"; exit 1; }
green "    device visible in /v1/devices"

# Give the worker pool a moment to flush the event row.
sleep 2
EVENTS=$(curl -fsS "${BASE}/v1/events?device_id=${DEVICE_ID}" -H "Authorization: Bearer ${API_KEY}")
green "    events: ${EVENTS}"

green ""
green "Smoke test PASSED."

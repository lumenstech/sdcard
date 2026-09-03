#!/bin/sh
set -eu

BASE_URL=${BASE_URL:-http://localhost:8090}
API_KEY=${API_KEY:-${VALID_API_KEYS:-}}
[ -n "${API_KEY}" ] || { echo "API_KEY or VALID_API_KEYS is required" >&2; exit 2; }
API_KEY=$(printf '%s' "${API_KEY}" | cut -d, -f1)
DEVICE_ID=${DEVICE_ID:-}
DURATION_MINUTES=${DURATION_MINUTES:-60}
INTERVAL_SECONDS=${INTERVAL_SECONDS:-60}
FRAME=${PILOT_FRAME:-}
TMP_FRAME=""

cleanup() { [ -n "${TMP_FRAME}" ] && rm -f "${TMP_FRAME}"; }
trap cleanup EXIT INT TERM

[ -n "${DEVICE_ID}" ] || { echo "DEVICE_ID is required for soak test" >&2; exit 2; }
case "${DURATION_MINUTES}" in *[!0-9]*|'') echo "DURATION_MINUTES must be an integer" >&2; exit 2;; esac
case "${INTERVAL_SECONDS}" in *[!0-9]*|'') echo "INTERVAL_SECONDS must be an integer" >&2; exit 2;; esac

if [ -z "${FRAME}" ]; then
    TMP_FRAME=$(mktemp)
    printf '\377\330SECURE4K-SOAK\377\331' > "${TMP_FRAME}"
    FRAME=${TMP_FRAME}
fi

END=$(( $(date +%s) + DURATION_MINUTES * 60 ))
SENT=0
FAILED=0
while [ "$(date +%s)" -lt "${END}" ]; do
    TS=$(date +%s)
    if curl -fsS --max-time 20 -X POST "${BASE_URL}/v1/frames/ingest" \
        -H "Authorization: Bearer ${API_KEY}" \
        -H "X-Device-ID: ${DEVICE_ID}" \
        -H "X-Timestamp: ${TS}" \
        --data-binary "@${FRAME}" >/dev/null; then
        SENT=$((SENT + 1))
    else
        FAILED=$((FAILED + 1))
        echo "soak upload failed ts=${TS} failed=${FAILED}" >&2
    fi
    curl -fsS --max-time 10 "${BASE_URL}/health" >/dev/null || { echo "health check failed" >&2; FAILED=$((FAILED + 1)); }
    sleep "${INTERVAL_SECONDS}"
done

echo "soak complete sent=${SENT} failed=${FAILED} duration_minutes=${DURATION_MINUTES}"
[ "${FAILED}" -eq 0 ] || exit 1

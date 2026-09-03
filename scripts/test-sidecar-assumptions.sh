#!/bin/sh
set -eu

fail() { echo "FAIL: $1" >&2; exit 1; }

# Pending evidence must never be pruned by the capture loop.
if grep -E 'frame_.*jpg.*(xargs +rm|rm +-f)' sdcard/scripts/capture_loop.sh >/dev/null 2>&1; then
    fail "capture loop contains pending-frame deletion"
fi
grep -q 'buffer full' sdcard/scripts/capture_loop.sh || fail "buffer-full pause is missing"

# Network calls in sidecar HTTP paths must be timeout bounded.
grep -q -- '--timeout=30' sdcard/scripts/upload_daemon.sh || fail "frame upload timeout missing"
grep -q -- '--timeout=10' sdcard/scripts/upload_daemon.sh || fail "heartbeat timeout missing"
grep -q -- '--timeout=60' sdcard/scripts/ota_check.sh || fail "OTA download timeout missing"

# OTA must fail closed when no checksum implementation is available.
grep -q 'no SHA-256 implementation available; refusing update' sdcard/scripts/ota_check.sh || fail "OTA checksum fail-closed guard missing"
grep -q 'scripts.previous' sdcard/scripts/ota_check.sh || fail "OTA rollback copy missing"

# Pilot RTSP helper must not alter firmware paths or firewall state.
if grep -E '/tmp/\.rtsp_enable|/system/sdcard|iptables +-I' sdcard/scripts/rtsp_unlock.sh >/dev/null 2>&1; then
    fail "RTSP helper mutates non-SD-card state"
fi

echo "sidecar reliability assumptions: PASS"

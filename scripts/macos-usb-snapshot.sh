#!/bin/bash
set -euo pipefail

OUT="${1:-usb-snapshot-$(date +%Y%m%d-%H%M%S).txt}"
{
  echo "Secure4K/BoilerCam USB snapshot"
  echo "timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo
  echo "=== system_profiler SPUSBDataType ==="
  system_profiler SPUSBDataType 2>&1 || true
  echo
  echo "=== ioreg USB tree ==="
  ioreg -p IOUSB -l -w 0 2>&1 || true
} | tee "$OUT"

echo

echo "Saved: $OUT"

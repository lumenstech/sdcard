#!/bin/bash
set -euo pipefail

if [ "${1:-}" = "--list" ] || [ $# -eq 0 ]; then
  echo "Candidate serial devices:"
  ls -1 /dev/cu.usb* /dev/cu.SLAB* /dev/cu.wch* 2>/dev/null || true
  exit 0
fi

PORT="$1"
BAUD="${2:-115200}"
OUT="${3:-hi3518ev200-boot-$(date +%Y%m%d-%H%M%S).log}"

[ -e "$PORT" ] || { echo "Serial port not found: $PORT" >&2; exit 1; }

stty -f "$PORT" "$BAUD" cs8 -cstopb -parenb -ixon -ixoff raw

echo "Capturing $PORT at ${BAUD} 8N1"
echo "Output: $OUT"
echo "Power-cycle the camera now. Press Ctrl-C when the boot log is complete."

cleanup() {
  stty -f "$PORT" sane 2>/dev/null || true
  echo
  echo "Saved: $OUT"
}
trap cleanup EXIT INT TERM

cat "$PORT" | tee "$OUT"

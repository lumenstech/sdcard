#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <camera-ip>" >&2
  exit 2
fi

IP=$1
OUTROOT=${HOME}/Secure4K-Hardware-Research/hi3518ev200/network
STAMP=$(date +%Y%m%d-%H%M%S)
OUT=${OUTROOT}/${IP}-${STAMP}
mkdir -p "$OUT"

echo "Camera: $IP" | tee "$OUT/summary.txt"
echo "Captured: $(date)" | tee -a "$OUT/summary.txt"

{
  echo "=== ARP / neighbor ==="
  arp -n "$IP" 2>&1 || true
  echo
  echo "=== ping ==="
  ping -c 2 -W 1000 "$IP" 2>&1 || true
} > "$OUT/network.txt"

PORTS="21 22 23 53 80 81 443 554 8000 8080 8081 8554 8899 9000 10000 32100 34567 37777"
: > "$OUT/ports.txt"
for PORT in $PORTS; do
  if nc -G 1 -z "$IP" "$PORT" >/dev/null 2>&1; then
    echo "$PORT open" | tee -a "$OUT/ports.txt"
  fi
done

for SCHEME in http https; do
  curl -k -sS --connect-timeout 2 --max-time 4 -D "$OUT/${SCHEME}-headers.txt" \
    -o "$OUT/${SCHEME}-body.bin" "${SCHEME}://${IP}/" 2>"$OUT/${SCHEME}-error.txt" || true
done

if command -v ffprobe >/dev/null 2>&1; then
  : > "$OUT/rtsp.txt"
  for PATHPART in / /live /stream1 /11 /live/ch00_0 /Streaming/Channels/101; do
    URL="rtsp://${IP}${PATHPART}"
    if ffprobe -v error -rtsp_transport tcp -timeout 2000000 \
      -show_entries stream=codec_name,width,height -of compact=p=0:nk=1 \
      "$URL" >"$OUT/ffprobe.tmp" 2>/dev/null; then
      echo "$URL" | tee -a "$OUT/rtsp.txt"
      cat "$OUT/ffprobe.tmp" | tee -a "$OUT/rtsp.txt"
    fi
  done
  rm -f "$OUT/ffprobe.tmp"
else
  echo "ffprobe not installed; RTSP URL tests skipped" >> "$OUT/summary.txt"
fi

OPEN=$(cat "$OUT/ports.txt" 2>/dev/null || true)
{
  echo
  echo "Open ports:"
  if [ -n "$OPEN" ]; then
    printf '%s\n' "$OPEN"
  else
    echo "none found in common-port probe"
  fi
  echo
  echo "Evidence directory: $OUT"
} | tee -a "$OUT/summary.txt"

#!/bin/sh
set -eu

VERSION="${VERSION:-$(cat sdcard/config/version)}"
NAME="secure4k-sidecar-doorbell-pilot-v${VERSION}"
DIST="dist/${NAME}"
ZIP="dist/${NAME}.zip"
SHA="${ZIP}.sha256"

rm -rf "$DIST" "$ZIP" "$SHA"
mkdir -p "$DIST/config" "$DIST/scripts" "$DIST/buffer" "$DIST/updates"

cp sdcard/initrun.sh "$DIST/initrun.sh"
cp sdcard/initrun.sh "$DIST/run.sh"
cp sdcard/initrun.sh "$DIST/custom.sh"
cp sdcard/scripts/*.sh "$DIST/scripts/"
cp sdcard/config/version "$DIST/config/version"
cp sdcard/config/device.conf "$DIST/config/device.conf.example"

chmod +x "$DIST/initrun.sh" "$DIST/run.sh" "$DIST/custom.sh" "$DIST/scripts/"*.sh

cat > "$DIST/RELEASE-MANIFEST.txt" <<EOF
Secure4K Sidecar Doorbell Pilot
Version: ${VERSION}
Target: Chinese doorbell/IP camera pilot
Mode: SD-card sidecar; does not replace camera firmware
Default transport: HTTP (MQTT optional)
Expected mount point on camera: /mnt/mmc01
Filesystem: FAT32-compatible; boot hooks are ordinary files, not symlinks

Required first-pilot validation:
- boot hook executes
- stable registration across reboot
- one supported capture path succeeds
- offline buffer grows without deleting pending evidence
- reconnect drains pending frames
- duplicate retries do not duplicate events
- Ollama outage does not drop evidence
- 24-hour one-device soak passes
EOF

mkdir -p dist
(
  cd dist
  zip -qr "${NAME}.zip" "${NAME}"
)

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$ZIP" > "$SHA"
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "$ZIP" > "$SHA"
else
  echo "ERROR: sha256sum or shasum is required" >&2
  exit 1
fi

printf '%s\n' "Built $ZIP"
printf '%s\n' "Checksum: $SHA"

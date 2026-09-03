# Mac mini SD provisioning with Codex

This runbook prepares the already-mounted microSD card for the Secure4K Sidecar doorbell/IP camera pilot.

## Safety rules

- Do not erase or repartition any disk automatically.
- Only target a mounted volume under `/Volumes` that `diskutil` identifies as removable or external.
- The provisioner requires an existing FAT32/MS-DOS filesystem.
- Back up the current contents before replacing the Secure4K files.
- Do not touch the Mac internal disk, external SSDs, or any unrelated volume.

## Codex workflow

1. Clone or update `https://github.com/lumenstech/sdcard` and use `main`.
2. Run `diskutil list` and `ls -la /Volumes`.
3. Identify the mounted microSD by removable/external status and capacity. Do not guess from name alone.
4. Run `diskutil info <mount-path>` and confirm it is the intended SD card and FAT32/MS-DOS.
5. If it is not FAT32/MS-DOS, stop and report the exact device and current filesystem. Do not erase it without explicit approval.
6. Determine a backend URL reachable by the camera network. If no deployed backend is reachable yet, use the Mac mini LAN IP with port 8090 only if the backend will be running there and macOS firewall/network settings allow the camera to reach it.
7. Use the pilot API key configured in the backend. Never invent or commit a real API key.
8. Provision with:

```sh
BACKEND_URL="http://<reachable-host>:8090" \
API_KEY="<pilot-key>" \
bash scripts/provision-mounted-sd-macos.sh "/Volumes/<CARD>"
```

9. Inspect the written card and confirm all required files exist and `config/version` is `0.1.0`.
10. Show a redacted copy of `config/device.conf` with the API key replaced by `<redacted>`.
11. Run `sync` and eject the exact volume with `diskutil eject`.
12. Report the backup path, SD device node, mount name, release version, backend URL, and the exact next physical step.

## Expected card root

```text
/
├── initrun.sh
├── run.sh
├── custom.sh
├── RELEASE-MANIFEST.txt
├── config/
│   ├── version
│   └── device.conf
├── scripts/
│   ├── capture_loop.sh
│   ├── upload_daemon.sh
│   ├── register.sh
│   ├── ota_check.sh
│   └── rtsp_unlock.sh
├── buffer/
└── updates/
```

All three boot hook names are ordinary files for FAT32 compatibility.

## First physical test

After ejecting the card, insert it in the Chinese doorbell/IP camera and power-cycle the camera. Do not alter the stock firmware. After several minutes, remove/mount the card only if needed and inspect `sidecar.log`. The first success criterion is proof that one of the SD boot hooks executed.

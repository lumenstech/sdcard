# BoilerCam Hi3518E V200 hardware bring-up

## Target hardware

Observed on the physical doorbell PCB:

- Main SoC: HiSilicon `HI3518ERBCV200` / Hi3518E V200 family.
- PCB marking: `TFC_M12_MAIN`, date `2019-02`.
- Four exposed serial test pads are silkscreened `VCC`, `TX`, `RX`, `GND`.
- microSD slot is present and confirmed functional for vendor storage.
- micro-USB connector is present; its data wiring is not yet confirmed.
- External 2.4 GHz antenna/module is present; Wi-Fi chipset is not yet identified.
- SPI NOR appears to be a 64-Mbit/8-MiB 25LQ64-class device, but the exact part and operating voltage MUST be confirmed from the chip marking / JEDEC ID before using an external programmer.

The Hi3518E V200 is a Linux-capable IP-camera SoC with H.264/JPEG hardware video blocks, SDIO, USB 2.0 host/device, UART, GPIO and SPI NOR support. OpenIPC currently supports the Hi3518EV200 family.

## Product objective

Do not preserve the obsolete mobile-app dependency as the product architecture.

Desired BoilerCam path:

```text
Hi3518EV200 camera
  -> open/local firmware
  -> Wi-Fi
  -> RTSP / JPEG / event endpoint
  -> BoilerCam/Secure4K bridge on Mac mini
  -> existing Secure4K :8090 ingest
  -> evidence + events + analytics
```

On-camera analytics are not required for the pilot. The camera only needs to become a reliable, locally reachable video/event source.

## Stop conditions

Do not write to SPI NOR until all of these are true:

1. UART boot log has been captured.
2. The exact SPI NOR size has been confirmed by U-Boot/JEDEC ID.
3. A complete factory flash backup has been made at least twice.
4. The two backups have matching SHA-256 hashes.
5. Camera sensor model is identified.
6. Wi-Fi chipset is identified.
7. Compatible OpenIPC sensor and Wi-Fi support have been confirmed.

Do not connect the USB-UART adapter VCC pin to the camera. Power the camera from its normal power input and connect UART `GND`, `TX`, and `RX` only.

## Phase 1 - passive UART capture

### Hardware

Use a 3.3 V TTL USB-UART adapter such as CP2102, FT232 or CH340.

Before connecting:

1. With camera powered off, confirm the pad labeled `GND` has continuity to the board ground / USB shield.
2. Power camera normally.
3. Measure the pad labeled `VCC`; record the voltage. Expect approximately 3.3 V but do not assume it.
4. Power camera off again.
5. Connect camera `GND` -> adapter `GND`.
6. Connect camera `TX` -> adapter `RX`.
7. Leave adapter TX disconnected for the first boot capture.
8. Do NOT connect adapter VCC.

### macOS capture

List ports:

```sh
sh scripts/macos-uart-capture.sh --list
```

Then capture at the usual HiSilicon console speed:

```sh
sh scripts/macos-uart-capture.sh /dev/cu.usbserial-XXXX 115200
```

Power-cycle the camera while capture is running.

If output is unreadable, retry 57600, 38400 and 9600 before changing wiring.

Save the complete boot log.

### Required information from the stock boot log

Extract:

- U-Boot version and prompt behavior.
- RAM size.
- SPI NOR manufacturer, JEDEC ID and total size.
- existing MTD partition table.
- kernel version and command line.
- root filesystem type.
- image sensor model / sensor driver.
- Wi-Fi USB/SDIO chipset and driver.
- GPIO messages relating to PIR, LEDs, reset and button.
- SD-card mount point.
- whether USB gadget/device mode appears.

Use:

```sh
sh scripts/summarize-hi3518-bootlog.sh path/to/boot.log
```

## Phase 2 - interactive UART

Only after a clean passive log:

1. Power off.
2. Add adapter `TX` -> camera `RX`.
3. Start 115200 8N1 terminal.
4. Power on and try to interrupt autoboot.

If stock U-Boot gives a prompt, run read-only commands first:

```text
help
version
printenv
bdinfo
sf probe 0
mmc list
mmc info
usb start
```

Do not run `sf erase`, `sf write`, `saveenv`, `mw` over boot regions or any vendor update command at this stage.

If the stock bootloader is password protected or cannot be interrupted, proceed to Phase 3 rather than bypassing it by hardware shorts.

## Phase 3 - RAM-load an unlocked U-Boot

OpenIPC's BURN utility explicitly supports `hi3518ev200` and can load a compatible U-Boot into SoC RAM without first replacing the SPI firmware.

Reference projects:

- OpenIPC BURN: `https://github.com/OpenIPC/burn`
- Similar Hi3518EV200 camera bring-up: `https://github.com/OpenIPC/device-cip-37210`

Important: OpenIPC documents macOS issues with BURN. Start on the Mac mini, but if serial handoff is unreliable, use a Linux machine or a Linux VM with direct USB-UART passthrough. Do not change the camera just to work around a Mac serial issue.

Use only the current OpenIPC `u-boot-hi3518ev200-universal.bin` associated with the BURN project/release.

The objective of this phase is an unlocked U-Boot running from RAM. It is NOT yet to write the OpenIPC bootloader to SPI.

Once at the RAM-loaded OpenIPC prompt, collect:

```text
version
printenv
bdinfo
sf probe 0
mmc list
mmc info
usb start
```

Record the exact flash size returned by `sf probe 0`.

## Phase 4 - factory firmware backup

The factory image is the recovery asset and may also contain calibration, sensor configuration, Wi-Fi settings and device-specific data.

Preferred backup path:

1. Boot the OpenIPC U-Boot in RAM.
2. Read the whole SPI NOR into RAM with `sf read` using the exact size detected by `sf probe`.
3. Copy the image to microSD using a method supported by that U-Boot (`fatwrite` if available, otherwise raw `mmc write` using a documented offset).
4. Read the dump on the Mac mini.
5. Repeat the backup without booting factory firmware between captures.
6. Compare hashes.

Do not assume 16 MiB commands from another camera. This board visually appears to use a 64-Mbit / 8-MiB-class NOR device, so the actual `sf probe` result controls all lengths.

Store backups outside Git:

```text
~/Secure4K-Hardware-Backups/<camera-id>/factory-fullflash-1.bin
~/Secure4K-Hardware-Backups/<camera-id>/factory-fullflash-2.bin
```

Then:

```sh
shasum -a 256 factory-fullflash-1.bin factory-fullflash-2.bin
cmp factory-fullflash-1.bin factory-fullflash-2.bin
```

Never commit the factory dump to a public repository because it may contain unique device identifiers or credentials.

## Phase 5 - inspect the factory image

On the Mac/Linux workstation:

```sh
file factory-fullflash-1.bin
strings -a factory-fullflash-1.bin | grep -Ei 'sensor|ov[0-9]|jx|sc[0-9]|rtl|7601|wifi|wlan|uboot|linux|mtd'
```

Use `binwalk` where available to locate:

- U-Boot.
- Linux kernel / uImage.
- SquashFS/JFFS2/CramFS.
- sensor `.ko` modules or configuration.
- Wi-Fi kernel modules.

This is where we identify the exact image sensor and Wi-Fi module if the boot log did not already reveal them.

## Phase 6 - evaluate OpenIPC compatibility

OpenIPC supports the Hi3518EV200 SoC and many sensors used on this platform, including OV9732, JXH62, JXF22/JXF23, SC2135/SC2235 and others. It also has known Hi3518EV200 devices using RTL8188EU/FU-class USB Wi-Fi.

Do not flash until the exact sensor and Wi-Fi chipset from THIS board are known.

For the initial BoilerCam pilot, prefer the 8 MiB/lite OpenIPC image if the board confirms an 8 MiB NOR. Do not flash a 16 MiB image to an 8 MiB part.

## Phase 7 - controlled OpenIPC installation

Only after the backup and compatibility gates pass:

1. Keep the recovery dump offline.
2. Install the OpenIPC U-Boot appropriate for Hi3518EV200.
3. Set the NOR size that matches `sf probe`.
4. Install the matching Hi3518EV200 lite kernel/rootfs image.
5. Configure the identified sensor.
6. Configure the identified Wi-Fi driver.
7. Configure a dedicated BoilerCam 2.4 GHz SSID.
8. Disable vendor cloud services because factory firmware is no longer used.
9. Bring up local RTSP/JPEG first.

Success gate:

```text
camera boots reliably
camera joins local Wi-Fi
camera has stable IP/DHCP reservation
RTSP returns live video for >= 1 hour
snapshot can be pulled repeatedly
reboot returns to service without manual intervention
```

## Phase 8 - BoilerCam/Secure4K integration

Do not put the complete Secure4K stack on the Hi3518EV200. Keep the camera lightweight.

Recommended split:

### Camera

- OpenIPC.
- H.264 RTSP main/sub stream.
- optional JPEG snapshot endpoint.
- optional GPIO event hook for PIR/doorbell button.
- local SD recording only if useful.

### Mac mini / Secure4K bridge

- discover/register camera.
- maintain RTSP connection.
- sample frames with FFmpeg.
- submit frames to existing Secure4K `:8090` ingest.
- preserve timestamps.
- reconnect automatically.
- map GPIO/PIR events later after basic video is stable.

## USB investigation

The Hi3518E V200 SoC supports USB 2.0 host/device, but the external micro-USB connector may be power-only or may be wired to USB data.

Before and after connecting the powered-off camera to the Mac through the micro-USB connector, run:

```sh
sh scripts/macos-usb-snapshot.sh
```

Do not power the board simultaneously from two sources unless the power path has been verified.

If a new USB device appears, preserve its VID/PID and descriptors. It may provide another recovery/engineering path. If nothing enumerates, use UART as the primary route.

## Why this is now the primary plan

The failed `initrun.sh`, `run.sh`, `custom.sh`, and `ubia_test` experiments only proved that the stock firmware does not execute arbitrary files from SD. They do not matter once the board can be controlled at its bootloader.

The exposed UART pads plus a supported Hi3518EV200 SoC are a much stronger engineering path than attempting to recover a discontinued mobile-app pairing flow.

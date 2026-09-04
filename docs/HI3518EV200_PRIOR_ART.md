# Hi3518EV200 BoilerCam prior art

This note records public projects and field reports that are directly relevant to repurposing the `HI3518ERBCV200` / Hi3518E V200 doorbell hardware as a local BoilerCam / Secure4K camera.

It supplements `HI3518EV200_BOILERCAM_BRINGUP.md`.

## Strongest match: Hi Tone Hi3518EV200 video doorbell

OpenIPC firmware issue #1646 documents a Hi Tone video doorbell based on the same HiSilicon Hi3518EV200 SoC:

- https://github.com/OpenIPC/firmware/issues/1646

The report is unusually relevant to our board because its stock boot log showed:

- U-Boot `2010.06`
- UART at `115200`
- 8 MiB SPI NOR (`GD25LQ64C`, JEDEC bytes `0xc8 0x60 0x17`)
- working microSD initialization
- Huawei LiteOS rather than normal Linux userspace
- UBIA firmware/runtime strings
- `Press Ctrl+C to stop autoboot`
- camera initialization occurring inside U-Boot

The reporter initially removed the camera module and the boot process appeared to stall. After reattaching the camera, U-Boot completed sensor initialization and the interactive console became reachable with Ctrl+C. Therefore our first UART captures should be performed with the image-sensor/camera board connected.

The same log contains an important warning about sensor identification: U-Boot printed an `OV9732 (MIPI)` initialization success message, but later LiteOS runtime output referenced `sc1245`. We must not identify our sensor from a single boot banner. The exact sensor must be confirmed from our own boot log, the physical sensor/module marking, and/or the factory firmware contents.

The Hi Tone log also shows UBIA runtime messages. Our camera independently created `ubia_record.db` on microSD. That makes this report a strong firmware-family match, but not proof that the PCB, sensor, Wi-Fi module, GPIO map, flash chip, or partition map is identical.

### Similar doorbell firmware extraction comment

A January 2026 comment on the same issue describes extracting the SPI image from a similar Hi3518EV200 video doorbell through U-Boot by reading SPI into RAM and writing raw blocks to microSD:

- https://github.com/OpenIPC/firmware/issues/1646#issuecomment-3760504702

That comment used a **16 MiB** flash length. Do **not** reuse that length on our board. The technique is useful only after our own `sf probe 0` establishes the exact flash size and after the SD write syntax for our active U-Boot has been verified.

The stock 2010-era U-Boot in the Hi Tone report also had old MMC command syntax. This is another reason to prefer a RAM-loaded current OpenIPC U-Boot for the backup operation if the vendor U-Boot is incomplete or awkward.

## OpenIPC Smartwares CIP-37210 conversion

OpenIPC maintains a complete conversion project for the Smartwares CIP-37210:

- https://github.com/OpenIPC/device-cip-37210
- https://github.com/OpenIPC/wiki/blob/master/en/device-smartwares-cip-37210.md

Relevant characteristics:

- Hi3518EV200
- OV9732 sensor
- RTL8188FU Wi-Fi
- microSD
- no Ethernet
- USB-UART recovery
- stock U-Boot restrictions/password protection
- OpenIPC BURN used to load an unlocked U-Boot
- factory backup / rollback workflow
- local OpenIPC firmware and RTSP result

This is the best procedural reference for our UART -> RAM U-Boot -> backup -> OpenIPC path, but its flash is 16 MiB. Never copy its erase/write lengths or `setnor16m` commands to our camera unless our own hardware is independently confirmed as 16 MiB.

## OpenIPC BURN

OpenIPC BURN explicitly supports `hi3518ev200` and can upload a U-Boot binary through the HiSilicon serial bootstrap path:

- https://github.com/OpenIPC/burn

The project notes known macOS problems. Start with the Mac mini, but use Linux with direct USB-UART access if serial handoff is unreliable.

For this project the purpose of BURN is initially to get an unlocked U-Boot in RAM, not to write a bootloader to SPI.

## Other completed Hi3518EV200 OpenIPC devices

The OpenIPC builder lists several adapted/completed Hi3518EV200 devices:

- https://github.com/OpenIPC/builder

Especially useful references include:

- Smartwares CIP-37210 — OV9732 + RTL8188FU
- Switcam HS303 v1 — JXF22 + RTL8188FU
- Switcam HS303 v2 — OV9732 + RTL8188EU
- Qtech QVC-IPC-136W — OV9732 + RTL8188EU
- VStarcam C8892WIP — AR0237 + MT7601U

These prove that this SoC family, multiple common sensors, USB Wi-Fi modules, microSD, and RTSP through OpenIPC are established paths. They do not establish which sensor or Wi-Fi chipset our TFC_M12_MAIN board uses.

## 8 MiB Hi3518EV200 OpenIPC evidence

OpenIPC issue #933 documents a Hi3518EV200 camera with an 8 MiB NOR layout running OpenIPC and producing RTSP video:

- https://github.com/OpenIPC/firmware/issues/933

The device also had an RTL8188FU Wi-Fi module, IR LEDs / IR cut, and PIR-related GPIO work. This is a particularly useful reference for the BoilerCam feature set because it combines:

- 8 MiB NOR
- Wi-Fi
- RTSP
- IR hardware
- PIR / GPIO investigation

It also demonstrates that image quality, ISP tuning, Wi-Fi module naming, and GPIO mapping can remain device-specific even after the base SoC boots OpenIPC.

## VStarcam Hi3518EV200 + GC2023 + MT7601U conversion

OpenIPC documents a verified VStarcam conversion:

- https://github.com/OpenIPC/wiki/blob/master/en/device-vstarcam-hi3518ev200-gc2023.md

This is useful if our external Wi-Fi module turns out to be MediaTek MT7601U rather than Realtek. It also shows that a nominally supported sensor can still require device-specific initialization or vendor ISP information, reinforcing the need to preserve and inspect the factory firmware before replacement.

## Hi3518EV200 doorbell customization project

`phpcoder/doorbell-customize` targets HiSilicon Linux video doorbells and adds local MQTT / automation functionality:

- https://github.com/phpcoder/doorbell-customize

It covers Gwelltimes / Yoosee-style Linux firmware, not the UBIA/LiteOS family indicated by our current evidence, so its update packages must not be used on our hardware. Its local-event architecture is still useful later for PIR / button integration.

## Yi Hi3518EV200 local-camera projects

Several Yi projects prove lightweight local services on Hi3518EV200:

- https://github.com/alienatedsec/yi-hack-v5
- https://github.com/pawelma/yi-hack-v5-slim
- https://github.com/Necromix/yi-vencrtsp
- https://github.com/Necromix/yi-vencrtsp_v2

They provide examples of:

- RTSP main/sub streams
- JPEG/snapshot capability
- MQTT events
- SSH/telnet service
- cloud-disabled operation
- keeping the camera workload small

They are architecture/code references only. Their SD autorun/recovery mechanism is specific to Yi firmware and must not be assumed to work on our UBIA/LiteOS doorbell.

## Generic 8 MiB Hi3518EV200 reverse-engineering example

Jurgis Balciunas documented reverse engineering a Chinese mini IP camera using:

- Hi3518EV200
- UART 115200
- U-Boot 2010.06
- 8 MiB SPI NOR

Reference:

- https://jurgis.me/2019/07/24/hacking-chinese-mini-ip-camera/

This is useful as an additional confirmation that the stock bootloader/flash topology we expect is common on 2018-2019 low-cost Hi3518EV200 products.

## OpenIPC support references

Supported sensors:

- https://github.com/OpenIPC/wiki/blob/master/en/guide-supported-sensors.md

Supported/adapted devices:

- https://github.com/OpenIPC/wiki/blob/master/en/guide-supported-devices.md

Open-source HiSilicon support layer:

- https://github.com/OpenIPC/openhisilicon

The Hi3518EV200 belongs to the Hi3516CV200-family generation in the OpenIPC/OpenHisilicon codebase.

## Bench implications for TFC_M12_MAIN

Until UART is available, do not perform any additional SD autorun guessing or vendor-cloud recovery work.

When the 3.3 V USB-UART adapter is available:

1. Leave the camera/sensor board connected.
2. Verify camera UART ground and signal voltage before attaching the adapter.
3. Connect receive-only first: camera GND -> adapter GND, camera TX -> adapter RX. Do not connect VCC.
4. Power the camera from its normal source.
5. Capture a complete 115200 8N1 boot log without transmitting anything.
6. Preserve the unedited log.
7. Run `scripts/summarize-hi3518-bootlog.sh` on the capture.
8. Compare the output with OpenIPC issue #1646, especially U-Boot date/version, flash ID, LiteOS/UBIA strings, sensor messages, and MMC behavior.
9. Only after the passive log, connect adapter TX -> camera RX and attempt Ctrl+C during boot.
10. Run read-only discovery first: `help`, `version`, `printenv`, `bdinfo`, `sf probe 0`, and the MMC/USB listing commands actually supported by that U-Boot.
11. If stock U-Boot is inaccessible or too limited, use OpenIPC BURN to RAM-load the current Hi3518EV200 universal U-Boot.
12. Before any SPI write, obtain two complete factory dumps using the exact size returned by `sf probe 0` and verify identical SHA-256 hashes.

### Important stop rule

Commands in third-party examples that contain a literal `0x1000000`, `16MB`, `setnor16m`, or other flash-size-dependent values are **not instructions for our board**. They remain reference material only until our own SPI device has been identified.
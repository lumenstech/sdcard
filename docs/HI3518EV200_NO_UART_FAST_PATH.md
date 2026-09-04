# Hi3518EV200 BoilerCam - no-UART fast path

This is the current preferred bring-up path for the `TFC_M12_MAIN` Hi3518EV200 doorbell.

The product goal remains unchanged: the doorbell becomes a local video/event source for the existing BoilerCam / Secure4K backend. The obsolete vendor app is not part of the finished product.

## Why the order changed

Research found a near-exact XBell / UBIA / Huawei LiteOS doorbell family and public source for the matching XBell Android client (`cn.ubia.XBell`). The client contains the exact provisioning logic used by this firmware generation.

For the first attempt we therefore do not need to wait for a UART adapter. UART is now the fallback if software provisioning and the stock update path fail.

## Fast path

### 1. Provision the camera onto an isolated 2.4 GHz network

Use a dedicated 2.4 GHz SSID with a simple WPA2 password for the first test. Keep the Mac mini on the same LAN.

The public XBell source shows the provisioning screen runs three mechanisms in parallel:

1. UBIA `WiFiDirectConfig.StartnetConfig()`
2. HiSilicon HiLink
3. acoustic provisioning through `voice.encoder.DataEncoder` / `VoicePlayer`

The acoustic channel uses 19 tones:

- base: 4000 Hz
- spacing: 150 Hz
- range: 4000 through 6700 Hz

The source constructs the logical token as:

`<ssid-length><ssid><password-length><password><callback-port><callback-ip>`

and then passes it to `DataEncoder.encodeString()` and `VoicePlayer.play()`.

The exact source is preserved upstream at:

- https://github.com/badboy-tian/UBell
- `app/src/main/java/cn/ubia/adddevice/SearchCarmeraFragmentActivity.java`

XBell is only a one-time provisioning/recovery tool here. It is not the intended BoilerCam runtime.

### 2. Confirm it joined the LAN

As soon as the camera associates:

- note its DHCP lease, IP and MAC address;
- keep the camera isolated from the production LAN;
- run `scripts/macos-ubia-lan-probe.sh <camera-ip>` from the Mac mini.

The probe is intentionally local-only. It checks common HTTP/RTSP/service ports and records basic network evidence without changing the camera.

### 3. Take the shortest video path that works

Try in this order:

1. native RTSP if exposed;
2. HTTP/MJPEG/JPEG endpoint if exposed;
3. local UBIA/TUTK H.264 stream using the recovered client protocol;
4. stock authenticated firmware-update channel to install a local service or replacement firmware;
5. UART/OpenIPC only if the network paths fail.

The pilot does not require OpenIPC if the stock firmware can be made to provide a stable local H.264/JPEG feed without cloud dependence.

### 4. Use the stock firmware-update mechanism as the software takeover path

The XBell client defines:

- firmware check request: `0x1215`
- firmware check response: `0x1216`
- firmware update request: `0x1217`
- firmware update response: `0x1218`

The update request includes:

- version
- file type (`1=home`, `2=rootfs`, `3=kernel`)
- file size
- MD5
- HTTP URL

A reverse-engineered XBell/UBIA doorbell was observed downloading its update from the URL supplied by this command.

This matters because once the camera is authenticated and on the LAN, a local HTTP server on the Mac can potentially replace the dead vendor update server.

Do not assume an arbitrary OpenIPC image can be supplied directly. The related XBell firmware uses a HiSilicon decoding stage during boot. The next research target is the `file_type=1` home update path: determine whether it can update the writable application/home area independently of the encoded LiteOS core.

If `home` can be replaced independently, that is likely the least invasive route to adding a local RTSP/JPEG/bridge process while keeping the working stock bootloader, sensor initialization and Wi-Fi support.

### 5. Capture the provisioning/update traffic

If XBell successfully provisions or connects to the camera, capture the LAN traffic during:

- initial discovery;
- camera authentication;
- live-view start;
- snapshot request;
- firmware-check request.

The purpose is to reproduce only the minimum local protocol needed by BoilerCam. We do not need to recreate the vendor cloud.

## Mac helper workflow

Generate/inspect the exact logical XBell acoustic token:

```sh
python3 scripts/xbell-pairing-payload.py \
  --ssid BoilerCam-Lab \
  --password 'test-password' \
  --source-ip 192.168.10.2 \
  --source-port 32100
```

After the camera appears on the LAN:

```sh
sh scripts/macos-ubia-lan-probe.sh 192.168.10.50
```

Store captures under:

`~/Secure4K-Hardware-Research/hi3518ev200/network/`

## What counts as success for this phase

We do not need root or OpenIPC yet.

This phase succeeds if we achieve any one of these:

- camera obtains a local IP;
- camera answers a local service/stream request;
- XBell can display live H.264 while the camera is on our isolated LAN;
- we can reproduce the authenticated local session outside the cloud;
- the camera accepts a locally hosted, known-good vendor-style update request.

Any of those gives us a software foothold and lets us continue without UART.

## UART fallback

Use UART only if:

- the exact XBell provisioning mechanisms all fail to associate the camera; or
- the camera associates but exposes no usable local control/stream/update path.

At that point the existing `HI3518EV200_BOILERCAM_BRINGUP.md` procedure remains available.

The only retained anti-brick rule is simple: do not issue unknown raw SPI erase/write operations. That does not block any of the software-first work above.
# Near-exact XBell / UBIA Hi3518EV200 doorbell match

This is currently the closest publicly documented hardware/firmware family match found for the BoilerCam candidate board `TFC_M12_MAIN`.

## Public reverse-engineering series

BoredPentester published a multi-part teardown and reverse-engineering series of an XBell-controlled smart doorbell:

- Part 1: https://boredpentester.com/assessing-the-security-of-a-smart-doorbell-part-1/
- Part 3 hardware/UART/firmware: https://boredpentester.com/private-assessing-the-security-of-a-smart-doorbell-part-3/
- Part 4 bootloader: https://boredpentester.com/assessing-the-security-of-a-smart-doorbell-part-4/
- Part 5 LiteOS: https://boredpentester.com/smart-doorbell-security-part-5-liteos-analysis/

The app used in that analysis is `XBell_v1.0.106`.

The torn-down unit exposed two PCBs and a serial port. The reported boot log is:

- U-Boot `2010.06` built June 2018
- SPI NOR JEDEC `ef 60 17`
- `W25Q64FW`
- 8 MiB SPI NOR
- HiSilicon Hi3518 family
- `OV9732 (MIPI)` initialization in U-Boot
- `Press Ctrl+C to stop autoboot`
- Huawei LiteOS shell
- UBIA runtime functions such as `ubia_get_local_mgmt`
- UART console at 115200

The vendor firmware update filename in the analysis ended in `-9732`, further supporting OV9732 use in that specific unit.

## Matching retail XBell doorbell specification

Multiple surviving retail listings for the same 2018-era XBell battery-doorbell class specify:

- `Hi3518EV200 + OV9732`
- 720p H.264 video
- 2 x 18650 cells
- PIR
- IR/night vision
- 2.4 GHz 802.11 b/g/n Wi-Fi
- TF/microSD storage
- XBell iOS/Android application
- approximately 144 x 74 x 32 mm enclosure

This is a substantially closer match than a generic Hi3518EV200 IP camera because it aligns with the SoC, product type, PIR, IR, storage, power style, app ecosystem, UBIA software family, LiteOS, 2018-2019 generation, and likely 8 MiB flash class.

## Relationship to our TFC_M12_MAIN board

Known on our physical board:

- `HI3518ERBCV200`
- `TFC_M12_MAIN`
- `2019-02`
- PIR
- IR LEDs
- microphone / speaker / buzzer-related hardware
- external 2.4 GHz antenna/module
- microSD
- micro-USB
- SPI NOR visually consistent with a 25Q64-class package
- exposed `VCC / TX / RX / GND` UART pads
- stock firmware creates `ubia_record.db`

The overlap with the XBell/UBIA reverse-engineered doorbell is therefore strong enough that it should now be our primary comparison target during first UART capture.

However, there is still no public indexed result found for the literal PCB string `TFC_M12_MAIN`. We must not call the boards identical until the PCB layout/photos or boot fingerprints match.

## Fingerprints to compare immediately when UART is available

During passive 115200 8N1 boot capture, compare specifically for:

1. `U-Boot 2010.06`
2. flash ID ending in `60 17`
3. `W25Q64FW`, `GD25LQ64C`, or another actual 8 MiB NOR identification
4. `SPI Nor total size: 8MB`
5. `uboot-other-mipi-init`
6. `OV9732 (MIPI)` or another sensor banner
7. `Press Ctrl+C to stop autoboot`
8. `Huawei LiteOS`
9. `ULOG_INFO`
10. `ubia_get_local_mgmt`
11. `ubia_*` peripheral/video/network functions
12. any package/model identifiers

A match across U-Boot generation + 8 MiB NOR + LiteOS + UBIA + sensor would be strong evidence that the BoredPentester/XBell firmware family is directly related to this unit.

## Important difference to keep in mind

The exact NOR vendor is not important for family identification. One related doorbell report used `W25Q64FW`; another Hi3518EV200 UBIA/LiteOS doorbell used `GD25LQ64C`. Our board must be identified from its own JEDEC response.

Likewise, sensor detection must come from our board. `OV9732` is now a leading hypothesis, not an assumption.

## Bench procedure remains non-destructive

No change to the safety gates:

- passive UART first
- no UART VCC connection
- no SPI erase/write
- no `saveenv`
- identify flash size
- identify sensor
- identify Wi-Fi chipset
- make two matching full factory dumps
- preserve dumps outside Git
- only then evaluate permanent OpenIPC installation

The value of this near-match is that it gives us a much more specific expected boot fingerprint and may let us recognize the stock firmware family within the first seconds of UART output.
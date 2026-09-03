#!/bin/sh
set -eu

LOG="${1:-}"
[ -n "$LOG" ] && [ -f "$LOG" ] || { echo "Usage: $0 boot.log" >&2; exit 1; }

section() {
  echo
  echo "=== $1 ==="
}

section "U-Boot / bootloader"
grep -Ein 'u-boot|bootloader|hit any key|autoboot|hisilicon|hi3518|version' "$LOG" || true

section "SPI NOR / flash"
grep -Ein 'spi|nor|jedec|flash|mtd|partition|mx25|w25|gd25|25lq|en25|xm25' "$LOG" || true

section "Kernel / rootfs"
grep -Ein 'linux version|kernel command line|cmdline|root=|squashfs|jffs|cramfs|ubifs|yaffs|mount' "$LOG" || true

section "Image sensor / ISP"
grep -Ein 'sensor|ov[0-9]{4}|sc[0-9]{4}|jx[a-z0-9]+|gc[0-9]{4}|imx[0-9]+|isp|mipi|dvp' "$LOG" || true

section "Wi-Fi / network"
grep -Ein 'wifi|wlan|802\.11|rtl8|8188|7601|mt760|sdio|usb.*wifi|ra0|wlan0|eth0' "$LOG" || true

section "SD / MMC"
grep -Ein 'mmc|sdcard|sd card|fat|vfat|mmcblk' "$LOG" || true

section "USB"
grep -Ein 'usb|gadget|dwc|ehci|ohci' "$LOG" || true

section "GPIO / PIR / button / LED"
grep -Ein 'gpio|pir|button|key|doorbell|led|ircut|ir-cut|ir led' "$LOG" || true

section "Shell / console"
grep -Ein 'login:|password:|console|tty|telnet|shell|busybox|/bin/sh|/bin/ash' "$LOG" || true

# digits-recovery

Minimal recovery server that runs when the phone fails to boot. Provides factory reset and "try again" options via a web UI.

## When it runs

Recovery mode activates in three situations:

- **Boot failure:** The initramfs tracks a boot counter on the data partition. If it reaches 3 consecutive failed boots, recovery starts automatically.
- **Factory reset:** When triggered from the web UI or by dialing `*#00000#`, digitsd sets the counter to the threshold and reboots into recovery.
- **Numpad panic button:** Holding the keypad's `*` key during boot causes `digits-panic-check` to write `/data/digits/recovery-mode` and reboot; the initramfs sees the flag and enters recovery mode on the next boot.

See [docs/architecture/updates-and-recovery.md](../../docs/architecture/updates-and-recovery.md) for the full design.

## What it does

1. Starts a WiFi AP (SSID: `Digits-Recovery`) at `192.168.4.1`
2. Serves a web UI with two options:
   - **Try Again** -- clears the boot counter and reboots normally
   - **Factory Reset** -- restores rootfs from the recovery partition's compressed image, formats the data partition, restores the skeleton directory structure, and reboots into first-boot setup

## Key design constraint

The recovery binary runs from the recovery partition (p3), not from the main rootfs (p2). It uses statically linked tools (`zstd`, `dd`, `mkfs.ext4`, `hostapd`, `dnsmasq`) bundled on the recovery partition, because the rootfs may be overwritten during factory reset.

## Build

```bash
make build       # cross-compile to linux/arm64
make build-local # native build
make test
```

Pure Go, no CGO dependencies.

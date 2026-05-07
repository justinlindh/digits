---
name: deploy-phone
description: Use when deploying built binaries to phone devices over SSH, updating digitsd or other Pi binaries on one or more phones, or when the user says "deploy" in the context of phone hardware
---

# Deploy to Phone

Build and deploy Pi binaries to phone devices over SSH. Handles the read-only rootfs remount cycle.

## Deployable Binaries

| Binary | Build dir | Build command | Output path | Install destination | Service restart |
|--------|-----------|---------------|-------------|---------------------|-----------------|
| `digitsd` | `pi/digitsd/` | `make build` | `bin/digitsd` | `/usr/local/bin/digitsd` | `digitsd` |
| diagnostic tools | `pi/digitsd/` | `GOOS=linux GOARCH=arm64 go build -o bin/<name> ./cmd/<name>` | `bin/<name>` | `/tmp/<name>` | none |

Diagnostic tools: `alsatest`, `latbench`, `latclient`, `memprofile`, `pipetest`, `dmixlat`, `clocksync`. Deploy to `/tmp/` -- they are throwaway debugging utilities, not permanent system binaries.

`digitsd` handles all modes via the `--mode` flag: `normal` (default), `recovery`, `setup`, and `gpclk0`. There is no separate `digits-setup` or `digits-recovery` binary.

**Build notes:**
- `digitsd` uses a Docker builder container. Docker must be running. The `make build` target runs the embed step automatically.

## Recovery Partition

The recovery partition (`/dev/mmcblk0p3`) is a self-contained rootfs that boots as PID 1 when the boot counter hits 3. It has its own binary at `/digits-recovery` (symlinked from `/sbin/init`). This is the same `digitsd` binary running in `--mode=recovery` (auto-detected via PID 1).

**The recovery partition binary is separate from the rootfs `/usr/local/bin/digitsd`.** Deploying digitsd to the rootfs does NOT update recovery mode. You must deploy to both locations if the change affects recovery behavior.

**Deploy sequence (from normal mode, device must NOT be in recovery):**

```bash
# 1. Copy binary to /tmp
sshpass -p <password> scp <local-digitsd-binary> <user>@<ip>:/tmp/digitsd-recovery

# 2. Mount recovery partition, replace binary, unmount
sshpass -p <password> ssh <user>@<ip> 'sudo mkdir -p /mnt/recovery && sudo mount /dev/mmcblk0p3 /mnt/recovery && sudo cp /tmp/digitsd-recovery /mnt/recovery/digits-recovery && sudo chmod 755 /mnt/recovery/digits-recovery && sudo umount /mnt/recovery && rm /tmp/digitsd-recovery && echo DONE'
```

**To test recovery after deploy:**
```bash
sshpass -p <password> ssh <user>@<ip> 'sudo sh -c "echo 3 > /data/digits/boot-counter" && sudo reboot'
```

The device reboots into recovery mode. Connect to the `Digits-Recovery` WiFi AP and access the web UI at `http://192.168.4.1/`. The `/debug` endpoint shows serial events and audio state.

**To return to normal mode from recovery:** POST to `http://192.168.4.1/try-again` (clears boot counter and reboots), or connect to the AP via nmcli and curl it:
```bash
nmcli device wifi connect "Digits-Recovery"
curl -X POST http://192.168.4.1/try-again
nmcli connection delete "Digits-Recovery"
```

**Tones on the recovery partition:** The recovery binary loads tones from the same `--tones` path, which on the device resolves to `/data/digits/tones/` (the data partition is mounted at `/data` in recovery). DTMF WAVs and voice clips must be present there.

## Deployable Config Files

`/data` is its own writable partition (`/dev/mmcblk0p4`, mounted `rw,noatime`), so anything that lives under `/data/...` does **not** need the rootfs remount cycle: scp, `sudo cp`, done.

| File | Source path in repo | Install destination | Apply step |
|------|---------------------|---------------------|------------|
| ALSA mixer state | `pi/digits_mixer_v{1,2}.state` (pick by PCB version of the target phone) | `/data/digits_mixer.state` | `sudo alsactl restore <card> -f /data/digits_mixer.state` |

`<card>` is `digitscodec` for V2 (TLV320AIC3104 onboard) and `Zero` for V1 (Codec Zero HAT). Confirm before applying with `amixer -c digitscodec info` (V2) or `amixer -c Zero info` (V1). Image build (`tools/build-image.sh`) picks the right `_v1`/`_v2` source by PCB mode and copies it on first flash; for an in-place update you have to pick the matching source file yourself.

`alsactl restore` may print `alsa-lib main.c:...(snd_use_case_mgr_open) error: failed to import hw:N use case configuration -2` -- harmless. The numid-keyed control values still apply.

After the restore, **verify control values with `amixer cget name='<control>'`, not `sget`.** TLV320AIC3104 has no UCM mapping, so simple-control lookups (`sget`) fail with "Unable to find simple control" even when the underlying kcontrol exists and is correct.

## Device Info

Phone IPs, SSH credentials, and device details are in `CLAUDE.local.md`. Read it before deploying.

## Deploy Sequence

For each target phone:

```bash
# 1. Copy binary to /tmp (writable without remount)
sshpass -p <password> scp <local-binary> <user>@<ip>:/tmp/<binary-name>

# 2. Remount rw, install binary, restart service, then remount ro
#    Restart BEFORE remount ro -- the running service holds the old binary's
#    inode via mmap and the kernel rejects remount,ro until that inode is
#    released. Restarting the service drops the old mmap; remount ro then
#    succeeds. digitsd's Extract() also re-attempts remount ro on startup.
sshpass -p <password> ssh <user>@<ip> 'sudo mount -o remount,rw / && sudo mv /tmp/<binary-name> <install-destination> && sudo chmod 755 <install-destination> && sudo systemctl restart <service> && sleep 2 && sudo mount -o remount,ro /'
```

If no service restart is needed (e.g. deploying to the recovery partition), just remount ro directly after the install:

```bash
sshpass -p <password> ssh <user>@<ip> 'sudo mount -o remount,rw / && sudo mv /tmp/<binary-name> <install-destination> && sudo chmod 755 <install-destination> && sudo mount -o remount,ro /'
```

For diagnostic tools going to `/tmp/`, skip the remount cycle entirely -- just scp and run.

## Post-Deploy Verification

After deploying, verify:

```bash
# Check service is running (for binaries with a service)
sshpass -p <password> ssh <user>@<ip> 'sudo systemctl status <service>'

# Confirm rootfs is read-only
sshpass -p <password> ssh <user>@<ip> 'mount | grep "on / "'
```

The `mount` output must show `ro` -- if it shows `rw`, the old service process likely still holds the old binary open. Restart the service and retry:

```bash
sshpass -p <password> ssh <user>@<ip> 'sudo systemctl restart <service> && sleep 2 && sudo sync && sudo mount -o remount,ro /'
```

The kernel rejects `remount,ro` on ext4 when any process has a deleted inode mmap'd on that filesystem. Replacing a running binary creates a deleted inode (link count 0) held by the running process. Once the process restarts and the old mmap is released, the remount succeeds.

## Multi-Phone Deploy

When deploying to all phones, run the deploy sequence for each phone listed in `CLAUDE.local.md`. Build the binary once, deploy to each phone sequentially.

## Common Mistakes

- **Leaving rootfs in rw** -- always verify `ro` after deploy. Check with `mount | grep "on / "`.
- **Deploying diagnostic tools to /usr/local/bin** -- they belong in `/tmp/`, not on the permanent rootfs.
- **Running `make build` without Docker** -- digitsd requires the Docker builder. Use `make build-local` only if you have a local cross-compile toolchain.

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
| `digits-recovery` | `pi/digits-recovery/` | `make build` | `bin/digits-recovery` | `/usr/local/bin/digits-recovery` | none |
| `digits-setup` | `pi/digits-setup/` | `make build-pi` | `digits-setup-arm64` | `/usr/local/bin/digits-setup` | none |
| diagnostic tools | `pi/digitsd/` | `GOOS=linux GOARCH=arm64 go build -o bin/<name> ./cmd/<name>` | `bin/<name>` | `/tmp/<name>` | none |

Diagnostic tools: `alsatest`, `latbench`, `latclient`, `memprofile`, `pipetest`, `dmixlat`, `clocksync`. Deploy to `/tmp/` -- they are throwaway debugging utilities, not permanent system binaries.

**Build notes:**
- `digitsd` uses a Docker builder container. Docker must be running. The `make build` target runs the embed step automatically.
- `digits-recovery` and `digits-setup` are simple cross-compiles, no Docker needed.
- `digits-setup` output binary is named `digits-setup-arm64`, not `digits-setup`.

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

If no service restart is needed (e.g. digits-recovery, digits-setup), just remount ro directly after the install:

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

- **Forgetting `make build-pi`** for digits-setup -- `make build` produces a host binary, not ARM64.
- **Leaving rootfs in rw** -- always verify `ro` after deploy. Check with `mount | grep "on / "`.
- **Deploying diagnostic tools to /usr/local/bin** -- they belong in `/tmp/`, not on the permanent rootfs.
- **Running `make build` without Docker** -- digitsd requires the Docker builder. Use `make build-local` only if you have a local cross-compile toolchain.

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

## Device Info

Phone IPs, SSH credentials, and device details are in `CLAUDE.local.md`. Read it before deploying.

## Deploy Sequence

For each target phone:

```bash
# 1. Copy binary to /tmp (writable without remount)
sshpass -p <password> scp <local-binary> <user>@<ip>:/tmp/<binary-name>

# 2. Remount rw, install, remount ro, restart (single SSH session)
sshpass -p <password> ssh <user>@<ip> 'sudo mount -o remount,rw / && sudo mv /tmp/<binary-name> <install-destination> && sudo chmod 755 <install-destination> && sudo mount -o remount,ro / && sudo systemctl restart <service>'
```

If no service restart is needed, omit the `systemctl restart` but **always remount back to ro**.

For diagnostic tools going to `/tmp/`, skip the remount cycle entirely -- just scp and run.

## Post-Deploy Verification

After deploying, verify:

```bash
# Check service is running (for binaries with a service)
sshpass -p <password> ssh <user>@<ip> 'sudo systemctl status <service>'

# Confirm rootfs is read-only
sshpass -p <password> ssh <user>@<ip> 'mount | grep "on / "'
```

The `mount` output must show `ro` -- if it shows `rw`, remount immediately:

```bash
sshpass -p <password> ssh <user>@<ip> 'sudo mount -o remount,ro /'
```

## Multi-Phone Deploy

When deploying to all phones, run the deploy sequence for each phone listed in `CLAUDE.local.md`. Build the binary once, deploy to each phone sequentially.

## Common Mistakes

- **Forgetting `make build-pi`** for digits-setup -- `make build` produces a host binary, not ARM64.
- **Leaving rootfs in rw** -- always verify `ro` after deploy. Check with `mount | grep "on / "`.
- **Deploying diagnostic tools to /usr/local/bin** -- they belong in `/tmp/`, not on the permanent rootfs.
- **Running `make build` without Docker** -- digitsd requires the Docker builder. Use `make build-local` only if you have a local cross-compile toolchain.

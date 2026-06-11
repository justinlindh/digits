#!/bin/bash
# digits-first-boot.sh — first-boot device identity initialization
#
# Runs once on first boot (detected by absence of /data/.initialized).
# Generates a unique hostname, SSH host keys, and device identity based
# on the Pi's serial number.
#
# Controlled by: digits-first-boot.service
# Condition:     ConditionPathExists=!/data/.initialized
#
# NOTE: Root filesystem is mounted read-only. This script temporarily
# remounts rw to update /etc/hostname and /etc/hosts, then restores ro.

set -euo pipefail

log() {
    echo "[digits-first-boot] $*" | tee -a /var/log/digits-first-boot.log
}

log "Starting first-boot initialization"

# --- 0. Temporarily remount root rw for hostname/hostapd writes ---
log "Remounting root filesystem read-write"
mount -o remount,rw /

# Ensure we always restore ro, even on failure
restore_ro() {
    log "Remounting root filesystem read-only"
    sync
    mount -o remount,ro / 2>/dev/null || log "WARNING: could not restore ro mount"
}
trap restore_ro EXIT

# --- 1. Generate a stable hostname (digits-XXXX) from the Pi serial ---
# Derive the suffix from the unchanging hardware serial, not /dev/urandom, so
# the hostname is identical every time first-boot runs. A factory reset
# restores the rootfs and wipes /data, leaving no writable store to remember a
# random name, so a random suffix would change on every reset and the same
# physical device (same MAC) would churn between hostnames in the network's
# client list. The serial is stable across resets, so the name never changes.
# The `|| true` keeps a failed read (no Serial line, missing device-tree node)
# from tripping `set -e` before the fallbacks below can run.
SERIAL=$(grep -m1 -iE '^Serial' /proc/cpuinfo 2>/dev/null | awk '{print $NF}' | tr -d '[:space:]') || true
if [[ -z "${SERIAL}" || "${SERIAL}" =~ ^0+$ ]]; then
    # /proc/cpuinfo had no usable serial; try the device tree.
    SERIAL=$(tr -d '\0' < /proc/device-tree/serial-number 2>/dev/null) || true
fi
if [[ -n "${SERIAL}" && ! "${SERIAL}" =~ ^0+$ ]]; then
    # Use the WHOLE board serial (only cosmetic leading zeros trimmed), not a
    # short slice. The serial is a unique per-board hardware ID, so the full
    # value gives distinct hostnames across identical units; a 4-char slice
    # (16 bits) would risk collisions across a fleet. Pure-bash leading-zero
    # strip: remove the prefix that precedes the first non-zero char.
    HEX_SUFFIX="${SERIAL#"${SERIAL%%[!0]*}"}"
else
    # Last resort if no serial is readable; logged so the churn cause is clear.
    log "WARNING: no usable Pi serial; falling back to a random hostname suffix"
    HEX_SUFFIX=$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')
fi
HOSTNAME="digits-${HEX_SUFFIX}"

log "Setting hostname: ${HOSTNAME}"
echo "${HOSTNAME}" > /etc/hostname
hostname "${HOSTNAME}"

# Update /etc/hosts to reflect new hostname
if grep -q "127.0.1.1" /etc/hosts; then
    sed -i "s/^127\.0\.1\.1.*/127.0.1.1\t${HOSTNAME}/" /etc/hosts
else
    echo "127.0.1.1	${HOSTNAME}" >> /etc/hosts
fi

# --- 2. Generate SSH host keys ---
# Keys are written to /etc/ssh on the (currently rw) rootfs. /etc/ssh is NOT a
# bind mount: the generated fstab only binds /var/log, /tmp, and /home/digits.
# Production images keep SSH disabled, so these keys go unused unless an admin
# later enables developer mode, which regenerates host keys on /data and points
# /etc/ssh/ssh_host_* symlinks at them (see the digits-devmode helper and the
# --dev path in build-image.sh).
log "Generating SSH host keys"
ssh-keygen -A

# --- 3. Generate globally unique device ID (UUID v4) ---
DEVICE_UUID=$(cat /proc/sys/kernel/random/uuid)
log "Device UUID: ${DEVICE_UUID}"
mkdir -p /data/digits
echo "${DEVICE_UUID}" > /data/digits/device-id
chown digits:digits /data/digits/device-id

# Keep a short suffix for the AP SSID (last 4 chars of UUID)
DEVICE_ID="${DEVICE_UUID: -4}"
log "Device ID (SSID suffix): ${DEVICE_ID}"

# --- 4. Update hostapd config SSID to Digits-XXXX ---
SSID="Digits-${DEVICE_ID}"
HOSTAPD_CONF="/etc/hostapd/digits-ap.conf"

if [[ -f "${HOSTAPD_CONF}" ]]; then
    log "Updating hostapd SSID to: ${SSID}"
    sed -i "s/^ssid=.*/ssid=${SSID}/" "${HOSTAPD_CONF}"
else
    log "WARNING: hostapd config not found at ${HOSTAPD_CONF}, skipping SSID update"
fi

# --- 5. Mark as initialized ---
log "Writing /data/.initialized flag"
date -Iseconds > /data/.initialized

log "First-boot initialization complete. Hostname: ${HOSTNAME}, Device ID: ${DEVICE_ID}"

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

# --- 1. Generate unique hostname (digits-XXXX, 4 random hex chars) ---
HEX_SUFFIX=$(head -c 2 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c 4)
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
# /etc/ssh is bind-mounted from /data/ssh, so keys land on writable /data
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

#!/usr/bin/env bash
# ⚠️ DEPRECATED — WM8960 HAT replaced by Raspberry Pi Codec Zero (DA7212).
# The Codec Zero uses mainline driver (dtoverlay=rpi-codeczero), no setup script needed.
# This file is kept for reference only.
#
# setup_wm8960.sh — Install WM8960 Audio HAT driver on Raspberry Pi
#
# ⚠️  KERNEL REQUIREMENT: The WM8960 driver requires kernel 6.6.x.
#     Kernel 6.12+ has a broken MCLK clock config that prevents audio.
#     If running 6.12+, downgrade first:
#       sudo rpi-update a3029486d6e4   # installs 6.6.51 (stable_20241008)
#       sudo reboot
#
# Also requires dtparam=audio=off in /boot/firmware/config.txt
# (onboard audio conflicts with WM8960 I2S clock).
set -euo pipefail

echo "=== WM8960 Audio HAT Setup ==="

# Check kernel version
KVER=$(uname -r)
KMAJOR=$(echo "$KVER" | cut -d. -f1)
KMINOR=$(echo "$KVER" | cut -d. -f2)
if [ "$KMAJOR" -gt 6 ] || { [ "$KMAJOR" -eq 6 ] && [ "$KMINOR" -gt 6 ]; }; then
    echo "ERROR: Kernel $KVER detected. WM8960 requires kernel 6.6.x or earlier."
    echo "Downgrade with: sudo rpi-update a3029486d6e4 && sudo reboot"
    exit 1
fi

# 1. Install dependencies
echo "[1/4] Installing apt dependencies..."
sudo apt-get update
sudo apt-get install -y git i2c-tools libasound2-plugins

# 2. Enable I2C and I2S via raspi-config (non-interactive)
echo "[2/4] Enabling I2C and I2S interfaces..."
sudo raspi-config nonint do_i2c 0      # 0 = enable
# I2S doesn't have a raspi-config toggle; handled by the driver overlay

# 3. Clone and install Waveshare WM8960 driver
echo "[3/4] Cloning and installing WM8960 driver..."
DRIVER_DIR="/tmp/WM8960-Audio-HAT"
if [ -d "$DRIVER_DIR" ]; then
    rm -rf "$DRIVER_DIR"
fi
git clone https://github.com/waveshare/WM8960-Audio-HAT.git "$DRIVER_DIR"
cd "$DRIVER_DIR"
sudo ./install.sh

# 4. Prompt for reboot
echo ""
echo "[4/4] Installation complete."
echo "A reboot is required for the audio driver to take effect."
read -rp "Reboot now? [y/N] " answer
if [[ "${answer,,}" == "y" ]]; then
    sudo reboot
else
    echo "Please reboot manually before using the audio HAT."
fi

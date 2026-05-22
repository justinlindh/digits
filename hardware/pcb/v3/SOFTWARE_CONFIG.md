# Software configuration required for PCB v2

Hardware-facing software settings that must be in place for the carrier board to operate. None of these are compile-time constants in firmware — they are system configuration on the Pi side. Keep this file in sync with `pi/image/` so image builds apply them automatically.

## Pi-side (Raspberry Pi Zero 2 W)

### 1. UART0 must be dedicated to RP2040 communication

The Pi's primary UART (`GPIO14/TXD`, `GPIO15/RXD`) on header pins 8 and 10 is wired to the RP2040 for serial control. By default the Pi kernel uses this for console output, which would spam bytes into the RP2040 every boot.

Required changes in `/boot/firmware/config.txt`:

```
enable_uart=1
dtoverlay=disable-bt
```

Required changes in `/boot/firmware/cmdline.txt`:

Remove the `console=serial0,115200` token if present. The Pi Zero 2 W routes `PL011` to the Bluetooth modem by default; `disable-bt` releases `PL011` to the header pins. Alternative `dtoverlay=miniuart-bt` exists but is clock-scaling-sensitive — prefer `disable-bt`.

### 2. I2S codec overlay

The TLV320AIC3104 uses the mainline `tlv320aic3x` ASoC driver, bound via device-tree overlay. The overlay must:

- Set `reset-gpios = <&gpio 22 GPIO_ACTIVE_LOW>` (hardware CODEC_RESET line from Pi GPIO22)
- Configure PLL source (BCLK-driven is the design; MCLK from GPCLK0 is the optional fallback)
- Bind to I2C1 address 0x18
- Route PCM: `GPIO18` = BCLK, `GPIO19` = LRCLK (WCLK), `GPIO20` = PCM input (from codec), `GPIO21` = PCM output (to codec)

A minimal overlay example lives in `pi/image/rootfs-overlay/` when the Pi image build lands.

### 3. GPCLK0 for optional codec MCLK

`GPIO4` is wired to codec MCLK as a fallback. If BCLK-driven PLL is used (normal case), leave `GPCLK0` unconfigured. If needed, enable via:

```
dtoverlay=audremap
```

or program the clock manager directly via `clk-bcm2835`.

### 4. SWD for RP2040 flashing

OpenOCD's `raspberrypi-native` configuration uses `GPIO24` for SWDIO and `GPIO25` for SWCLK — matches the carrier board wiring as long as the upstream default config is used. No Pi-side overrides required, but the OpenOCD command line must reference the `raspberrypi-native` adapter in the firmware build scripts.

### 5. HAT EEPROM probe at boot

The Pi probes `GPIO0/1` (pins 27/28, I2C0) for a HAT EEPROM at boot. The carrier board does not populate anything on these pins, so the probe times out and boot continues. No configuration change required, but the timeout does add ~1 second to boot; `force_eeprom_read=0` in `config.txt` can suppress it.

## Firmware-side (RP2040)

### 6. TESTEN must stay low in software

The RP2040 schematic ties pin 19 (TESTEN) to GND with a stitching via. Firmware should not reconfigure this pin. Per datasheet §2.6, TESTEN held low is mandatory for normal operation; floating or pulled high enables production-test mode.

### 7. Codec internal DVDD LDO must remain disabled

The TLV320AIC3104 has an internal DVDD LDO (Page 0 Register 89). By default it is OFF. The external XC6206P182 (U7) provides 1.8V to DVDD (pin 32). Firmware must not enable the internal LDO — doing so would fight the external regulator and cause brownouts on the digital rail.

### 8. Ringer idle state

Firmware `ringer.c` must drive both `RINGER_IN1` and `RINGER_IN2` low at init. The DRV8871 is in sleep mode when both inputs are low (~50 µs wake-up). No external pull-downs are populated; a brief RP2040 reset window leaves the driver inputs floating, but RP2040 reset is short enough that the bell hammer does not respond measurably.

## Bring-up checklist

- [ ] Confirm `/boot/firmware/config.txt` has `enable_uart=1` and `dtoverlay=disable-bt`
- [ ] Confirm `/boot/firmware/cmdline.txt` does not contain `console=serial0,115200`
- [ ] Verify codec overlay is loaded: `dmesg | grep tlv320`
- [ ] Verify I2C codec responds at 0x18: `i2cdetect -y 1`
- [ ] Verify SWD link to RP2040: `openocd -f interface/raspberrypi-native.cfg -f target/rp2040.cfg -c "init; exit"`
- [ ] Verify `alsamixer` shows the codec card
- [ ] Verify UART0 Pi↔RP2040 round-trip at expected baud

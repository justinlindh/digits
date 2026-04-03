# Codec Zero (DA7212) Mixer Configuration

## Problem

The Raspberry Pi Codec Zero (DA7212 codec) has ~97 ALSA mixer controls. On reboot or driver reinitialization, critical routing switches can reset to `off`, causing silent audio output even though volume levels look correct.

## Critical Mixer Switches

Three switches must be `on` for audio to reach the earpiece via the Lineout (SPKR OUT screw terminals):

| numid | Control Name | What It Does |
|-------|-------------|--------------|
| 29 | `Lineout Switch` | Master output enable for the Lineout port |
| 87 | `Mixout Left DAC Left Switch` | Routes left DAC output to the left mixout stage |
| 94 | `Mixout Right DAC Right Switch` | Routes right DAC output to the right mixout stage |

Without all three `on`, the DAC generates audio internally but nothing reaches the physical output. This is the most common cause of "everything looks fine but no sound."

## Signal Path

```
Digital audio (aplay) → DAC → Mixout (switches 87/94) → Lineout (switch 29) → SPKR OUT terminals → earpiece
```

## Key Volume Controls

| Control | Current Setting | Notes |
|---------|----------------|-------|
| `Mic 1 Volume` | 5 | External electret mic via 3.5mm TRS jack |
| `Mixin PGA Volume` | L=7, R=5 | Pre-ADC gain |
| `ADC Volume` | 114 | Analog-to-digital converter level |
| `DAC Volume` | 111 (-0.75dB) | Digital-to-analog converter level |
| `Lineout Volume` | 50 (+2dB) | Final output stage volume |

## Files

| File | Location on Pi | Purpose |
|------|---------------|---------|
| `restore_mixer.sh` | `/home/digits/restore_mixer.sh` | Restores mixer state + verifies critical switches |
| `digits_mixer.state` | `/home/digits/digits_mixer.state` | Full ALSA state dump (all 97 controls) |
| `digits-mixer.service` | `/etc/systemd/system/digits-mixer.service` | Runs restore on boot after `sound.target` |

## Boot Sequence

1. Pi boots → Codec Zero driver loads → mixer initializes with (possibly wrong) defaults
2. `digits-mixer.service` runs `restore_mixer.sh`
3. Script loads saved state via `alsactl restore`
4. Script explicitly verifies switches 29, 87, 94 are `on`
5. Audio path is guaranteed ready before `dtmf-uart.service` starts playing tones

## Installation (New Board)

```bash
# Copy files from repo
cp pi/restore_mixer.sh /home/digits/restore_mixer.sh
cp pi/digits_mixer.state /home/digits/digits_mixer.state
chmod +x /home/digits/restore_mixer.sh

# Install systemd service
sudo cp pi/digits-mixer.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable digits-mixer.service

# Verify it works
sudo systemctl start digits-mixer.service
systemctl status digits-mixer.service
```

## Re-Saving After Tuning

If you adjust mixer levels and want to persist the changes:

```bash
sudo alsactl store 1 -f /home/digits/digits_mixer.state
```

Then copy the updated state file back to the repo:

```bash
# From the dev machine:
scp digits@<pi-ip>:/home/digits/digits_mixer.state ~/src/digits/pi/digits_mixer.state
cd ~/src/digits && git add pi/digits_mixer.state && git commit -m "update mixer state" && git push
```

## Debugging Silent Audio

1. Check the service ran: `systemctl status digits-mixer.service`
2. Check critical switches: `amixer -c 1 cget numid=29 && amixer -c 1 cget numid=87 && amixer -c 1 cget numid=94`
3. All three should show `values=on`
4. If not, run: `sudo /home/digits/restore_mixer.sh`
5. Test with: `speaker-test -D default -c 1 -t sine -f 440 -l 1`

## DAC Keepalive

The DA7212 has a power-down mode that adds ~500ms latency on the first sound after silence. To prevent this, run a silence stream:

```bash
nohup aplay -D default -t raw -f S16_LE -r 48000 -c 1 /dev/zero > /dev/null 2>&1 &
```

This should be added to a systemd service (TODO) to persist across reboots. Without it, the first DTMF keypress after idle has a noticeable delay.

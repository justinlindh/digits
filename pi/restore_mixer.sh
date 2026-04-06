#!/bin/bash
# Restore Digits mixer state for Codec Zero (DA7212)
# Run on boot via systemd or manually after mixer resets.
#
# Saved state file contains ALL mixer settings.
# To re-save after tuning: sudo alsactl store -f /home/digits/digits_mixer.state <card_number>

STATE_FILE="/home/digits/digits_mixer.state"

if [ ! -f "$STATE_FILE" ]; then
    echo "ERROR: $STATE_FILE not found" >&2
    exit 1
fi

# Detect Codec Zero card number dynamically
CARD=$(grep -l 'RPi_Codec_Zero\|RPi Codec Zero' /proc/asound/card*/id 2>/dev/null | head -1 | grep -o '[0-9]*')
if [ -z "$CARD" ]; then
    # Fallback: search by short name
    CARD=$(awk '/\[Zero/{gsub(/[^0-9]/,"",$1); print $1; exit}' /proc/asound/cards)
fi
if [ -z "$CARD" ]; then
    echo "ERROR: Codec Zero card not found" >&2
    exit 1
fi
echo "Codec Zero detected as card $CARD"

alsactl restore "$CARD" -f "$STATE_FILE"
echo "Mixer state restored from $STATE_FILE"

# Verify critical routing switches are on
# 29=Lineout, 87=MixoutL-DACL, 94=MixoutR-DACR, 27=ADC, 26=MixinPGA, 77=MixinR-Mic2, 24=Mic2
for numid in 29 87 94 27 26 77 24; do
    val=$(amixer -c "$CARD" cget numid=$numid 2>/dev/null | grep ": values=" | sed "s/.*values=//")
    case "$val" in
        on|on,on) ;;
        *)
            echo "WARNING: numid=$numid is $val, forcing on"
            amixer -c "$CARD" cset numid=$numid on >/dev/null 2>&1 || \
            amixer -c "$CARD" cset numid=$numid on,on >/dev/null 2>&1
            ;;
    esac
done

echo "Critical switches verified: Lineout(29) MixoutL-DACL(87) MixoutR-DACR(94) ADC(27) MixinPGA(26) MixinR-Mic2(77) Mic2(24)"

#!/bin/bash
# Quick mic test — records from jack mic only (onboard MEMS disabled)
amixer -c Zero sset 'Onboard MIC' off > /dev/null 2>&1
amixer -c Zero sset 'Mixin Left Mic 2' off > /dev/null 2>&1
amixer -c Zero sset 'Mixin Right Mic 2' off > /dev/null 2>&1
amixer -c Zero sset 'Mic 1 Amp Source MUX' 'MIC_P' > /dev/null 2>&1
amixer -c Zero sset 'Mic 1' 100% on > /dev/null 2>&1
amixer -c Zero sset 'Mixin Left Mic 1' on > /dev/null 2>&1
amixer -c Zero sset 'Mixin PGA' 80% on > /dev/null 2>&1
amixer -c Zero sset 'ADC' 95% on > /dev/null 2>&1
amixer -c Zero sset 'MIC Jack' on > /dev/null 2>&1

echo "Onboard mic OFF. Recording 10s from JACK MIC ONLY..."
echo "Talk into the handset. Ctrl+C to stop early."
arecord -D plughw:Zero -f S16_LE -r 48000 -c 1 -d 10 /tmp/mic_jack_test.wav
echo "Done. File: /tmp/mic_jack_test.wav"

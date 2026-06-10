# digitsd commands

`digitsd` is the daemon. The remaining directories are hardware-debug and
profiling tools that are not built by default and never deployed to a device.
They exist for codec bring-up and on-bench diagnosis.

| Binary | Purpose |
|--------|---------|
| `digitsd` | The phone daemon. Modes: normal, gpclk0, recovery, setup. |
| `alsatest` | Plays a 440 Hz sine through ALSA for a few seconds. The first-line "is the codec producing sound at all" check during V1/V2 bring-up. |
| `dmixlat` | Measures actual ALSA output latency on the device via `snd_pcm_delay()` (cgo). Used to characterize the playback buffer depth on real hardware. |
| `latclient` | Drives a real WebRTC call against signald and reports end-to-end audio latency. Pairs with the daemon's Unix socket server, which auto-answers calls from this client. |
| `memprofile` | Memory calibration harness: opens pion WebRTC peer connections and reports heap/sys growth, used to size the per-call memory budget on the Pi Zero 2 W. |

Build one with `go build ./cmd/<name>` from `pi/digitsd`. `dmixlat` needs
`libasound2-dev` for its cgo ALSA binding.

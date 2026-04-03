package main

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

// Measures the actual output latency of ALSA playback by checking snd_pcm_delay()
// after writes. This tells us how many samples are buffered between our write
// and the DAC output.

func main() {
	device := "default"
	if len(os.Args) > 1 {
		device = os.Args[1]
	}

	fmt.Printf("=== ALSA Latency Measurement ===\n")
	fmt.Printf("Device: %s\n\n", device)

	cfg := audio.DefaultPlaybackConfig()
	cfg.Device = device

	pb, err := audio.NewPlayback(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewPlayback: %v\n", err)
		os.Exit(1)
	}
	defer pb.Close()

	// Get the underlying handle for snd_pcm_delay
	handle := pb.Handle()

	freq := 440.0
	frameSize := cfg.FrameSize // 960
	totalFrames := 150         // 3 seconds

	fmt.Printf("Writing %d frames of %d samples through %q...\n\n", totalFrames, frameSize, device)

	var maxDelay, totalDelay C.snd_pcm_sframes_t
	var measurements int

	for f := 0; f < totalFrames; f++ {
		frame := make([]int16, frameSize)
		for i := range frame {
			sample := f*frameSize + i
			t := float64(sample) / float64(cfg.SampleRate)
			frame[i] = int16(16000 * math.Sin(2*math.Pi*freq*t))
		}

		t0 := time.Now()
		if err := pb.WriteFrame(frame); err != nil {
			fmt.Fprintf(os.Stderr, "write %d: %v\n", f, err)
			continue
		}
		writeTime := time.Since(t0)

		// Measure delay after write
		var delay C.snd_pcm_sframes_t
		if rc := C.snd_pcm_delay((*C.snd_pcm_t)(handle), &delay); rc == 0 {
			totalDelay += delay
			measurements++
			if delay > maxDelay {
				maxDelay = delay
			}

			if f < 5 || f%25 == 0 {
				latMs := float64(delay) / float64(cfg.SampleRate) * 1000
				fmt.Printf("  frame %3d: write=%6s delay=%d samples (%.1fms)\n",
					f, writeTime.Round(time.Microsecond), delay, latMs)
			}
		}
	}

	if measurements > 0 {
		avgDelay := float64(totalDelay) / float64(measurements)
		avgLatMs := avgDelay / float64(cfg.SampleRate) * 1000
		maxLatMs := float64(maxDelay) / float64(cfg.SampleRate) * 1000
		fmt.Printf("\n=== Results ===\n")
		fmt.Printf("Avg output delay: %.0f samples (%.1fms)\n", avgDelay, avgLatMs)
		fmt.Printf("Max output delay: %d samples (%.1fms)\n", maxDelay, maxLatMs)
	}
}

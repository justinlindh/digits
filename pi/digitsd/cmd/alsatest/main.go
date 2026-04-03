package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

func main() {
	device := "default"
	if len(os.Args) > 1 {
		device = os.Args[1]
	}

	fmt.Printf("ALSA playback test — 440Hz sine, 3 seconds, device=%q\n", device)

	cfg := audio.DefaultPlaybackConfig()
	cfg.Device = device
	fmt.Printf("Config: device=%q rate=%d channels=%d frameSize=%d\n",
		cfg.Device, cfg.SampleRate, cfg.Channels, cfg.FrameSize)

	pb, err := audio.NewPlayback(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewPlayback: %v\n", err)
		os.Exit(1)
	}
	defer pb.Close()

	freq := 440.0
	duration := 3 * time.Second
	totalFrames := int(duration.Seconds()) * cfg.SampleRate / cfg.FrameSize

	fmt.Printf("Playing %d frames (%v)...\n", totalFrames, duration)
	start := time.Now()

	for f := 0; f < totalFrames; f++ {
		frame := make([]int16, cfg.FrameSize)
		for i := range frame {
			sample := f*cfg.FrameSize + i
			t := float64(sample) / float64(cfg.SampleRate)
			frame[i] = int16(16000 * math.Sin(2*math.Pi*freq*t))
		}
		if err := pb.WriteFrame(frame); err != nil {
			fmt.Fprintf(os.Stderr, "WriteFrame %d: %v\n", f, err)
		}
	}

	elapsed := time.Since(start)
	fmt.Printf("Done. Wall time: %v (audio: %v, overhead: %v)\n", 
		elapsed.Round(time.Millisecond), duration, (elapsed - duration).Round(time.Millisecond))
}

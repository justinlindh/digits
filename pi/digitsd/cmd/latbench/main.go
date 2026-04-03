package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
)

// Latency benchmark: encode a sine wave to Opus packets, then decode and
// play through the exact same ALSA path digitsd uses.  Measures time at
// each stage and prints a breakdown.

func main() {
	const (
		sampleRate = 48000
		frameMs    = 20
		frameSize  = sampleRate * frameMs / 1000 // 960
		bitrate    = 24000
		freq       = 440.0
		durSec     = 3
		numFrames  = durSec * 1000 / frameMs // 150
	)

	fmt.Println("=== Latency Benchmark ===")
	fmt.Printf("Frames: %d × %dms = %ds of 440Hz sine\n", numFrames, frameMs, durSec)
	fmt.Println()

	// 1. Encode all frames to Opus packets (simulating what the browser sends)
	enc, err := codec.NewEncoder(sampleRate, 1, bitrate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoder: %v\n", err)
		os.Exit(1)
	}

	dec, err := codec.NewDecoder(sampleRate, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decoder: %v\n", err)
		os.Exit(1)
	}

	// Generate PCM + encode to Opus
	packets := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		pcm := make([]int16, frameSize)
		for i := range pcm {
			t := float64(f*frameSize+i) / float64(sampleRate)
			pcm[i] = int16(16000 * math.Sin(2*math.Pi*freq*t))
		}
		pkt, err := enc.Encode(pcm)
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode frame %d: %v\n", f, err)
			os.Exit(1)
		}
		packets[f] = pkt
	}
	fmt.Printf("Encoded %d Opus packets (avg %d bytes)\n", len(packets), len(packets[0]))

	// 2. Open ALSA playback (same config as digitsd)
	cfg := audio.DefaultPlaybackConfig()
	pb, err := audio.NewPlayback(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "playback: %v\n", err)
		os.Exit(1)
	}
	defer pb.Close()

	// 3. Decode + write, measuring each step
	var totalDecode, totalWrite time.Duration
	var decodeMax, writeMax time.Duration
	var firstWriteAt time.Time

	fmt.Println("\nPlaying through decode → ALSA (same path as digitsd)...")
	start := time.Now()

	for i, pkt := range packets {
		// Decode
		t0 := time.Now()
		pcm, err := dec.Decode(pkt)
		decElapsed := time.Since(t0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode %d: %v\n", i, err)
			continue
		}
		totalDecode += decElapsed
		if decElapsed > decodeMax {
			decodeMax = decElapsed
		}

		// ALSA write
		t1 := time.Now()
		if err := pb.WriteFrame(pcm); err != nil {
			fmt.Fprintf(os.Stderr, "write %d: %v\n", i, err)
		}
		wrElapsed := time.Since(t1)
		totalWrite += wrElapsed
		if wrElapsed > writeMax {
			writeMax = wrElapsed
		}

		if i == 0 {
			firstWriteAt = time.Now()
		}

		// Print every 25 frames (500ms)
		if (i+1)%25 == 0 {
			fmt.Printf("  frame %3d: decode=%6s write=%6s (cum decode=%s write=%s)\n",
				i+1, decElapsed.Round(time.Microsecond), wrElapsed.Round(time.Microsecond),
				totalDecode.Round(time.Microsecond), totalWrite.Round(time.Microsecond))
		}
	}

	wallTime := time.Since(start)
	audioTime := time.Duration(numFrames*frameMs) * time.Millisecond

	fmt.Println()
	fmt.Println("=== Results ===")
	fmt.Printf("Audio duration:     %v\n", audioTime)
	fmt.Printf("Wall time:          %v\n", wallTime.Round(time.Millisecond))
	fmt.Printf("First write:        %v after start\n", firstWriteAt.Sub(start).Round(time.Microsecond))
	fmt.Printf("Decode total:       %v (avg %v, max %v)\n",
		totalDecode.Round(time.Microsecond),
		(totalDecode / time.Duration(numFrames)).Round(time.Microsecond),
		decodeMax.Round(time.Microsecond))
	fmt.Printf("ALSA write total:   %v (avg %v, max %v)\n",
		totalWrite.Round(time.Microsecond),
		(totalWrite / time.Duration(numFrames)).Round(time.Microsecond),
		writeMax.Round(time.Microsecond))
	fmt.Printf("Overhead:           %v\n", (wallTime - audioTime).Round(time.Millisecond))

	if wallTime > audioTime+200*time.Millisecond {
		fmt.Println("\n⚠️  Wall time significantly exceeds audio duration — ALSA writes are blocking too long")
	} else {
		fmt.Println("\n✅ Playback keeping up with real-time")
	}
}

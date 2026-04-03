package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
)

func main() {
	rate := 48000
	dur := 3
	freq := 440.0
	frameSize := 960

	total := rate * dur
	input := make([]int16, total)
	for i := range input {
		input[i] = int16(16000 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}

	enc, _ := codec.NewEncoder(rate, 1, 24000)
	dec, _ := codec.NewDecoder(rate, 1)

	var output []int16
	for i := 0; i+frameSize <= len(input); i += frameSize {
		frame := input[i : i+frameSize]
		encoded, err := enc.Encode(frame)
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			continue
		}
		decoded, err := dec.Decode(encoded)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode: %v\n", err)
			continue
		}
		output = append(output, decoded...)
	}

	f, _ := os.Create("/tmp/pipeline_output.raw")
	binary.Write(f, binary.LittleEndian, output)
	f.Close()
	fmt.Printf("Opus roundtrip: %d → %d samples\n", len(input), len(output))
}

package audio

/*
#cgo CFLAGS: -I${SRCDIR}/../../../rnnoise
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/../../../rnnoise/lib/x86_64 -lrnnoise -lm
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/../../../rnnoise/lib/aarch64 -lrnnoise -lm
#include "rnnoise.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Denoiser wraps RNNoise's DenoiseState for real-time noise suppression.
type Denoiser struct {
	state *C.DenoiseState
}

// NewDenoiser allocates and initializes a new RNNoise DenoiseState using the
// default built-in model.
func NewDenoiser() (*Denoiser, error) {
	st := C.rnnoise_create(nil)
	if st == nil {
		return nil, fmt.Errorf("rnnoise_create returned nil")
	}
	return &Denoiser{state: st}, nil
}

// Process denoises a PCM frame. The input should be 960 samples (20ms at 48kHz).
// RNNoise operates on 480-sample (10ms) chunks, so we make two passes.
// Returns a new slice of the same length as pcm.
func (d *Denoiser) Process(pcm []int16) []int16 {
	out := make([]int16, len(pcm))
	for i := 0; i+RNNoiseFrameSize <= len(pcm); i += RNNoiseFrameSize {
		chunk := pcm[i : i+RNNoiseFrameSize]
		floats := int16ToFloat32(chunk)
		C.rnnoise_process_frame(d.state, (*C.float)(unsafe.Pointer(&floats[0])), (*C.float)(unsafe.Pointer(&floats[0])))
		converted := float32ToInt16(floats)
		copy(out[i:], converted)
	}
	return out
}

// Close frees the underlying RNNoise state. Must be called when done.
func (d *Denoiser) Close() {
	if d.state != nil {
		C.rnnoise_destroy(d.state)
		d.state = nil
	}
}

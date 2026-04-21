package codec

import (
	"fmt"

	"gopkg.in/hraban/opus.v2"
)

// FrameSize is 20ms at 48kHz (960 samples).
const FrameSize = 960

// Encoder wraps an Opus encoder for VoIP use at 48kHz mono.
type Encoder struct {
	enc *opus.Encoder
	buf []byte
}

// NewEncoder creates an Opus encoder.
// sampleRate: 48000, channels: 1, bitrate: 24000
func NewEncoder(sampleRate, channels, bitrate int) (*Encoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("opus.NewEncoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, fmt.Errorf("SetBitrate: %w", err)
	}
	if err := enc.SetComplexity(5); err != nil {
		return nil, fmt.Errorf("SetComplexity: %w", err)
	}
	if err := enc.SetInBandFEC(true); err != nil {
		return nil, fmt.Errorf("SetInBandFEC: %w", err)
	}
	// DTX: when the pipeline sends zero-sample (muted) frames, the encoder
	// emits SID (silence-indicator) comfort-noise packets. The receiving
	// decoder regenerates low-level background noise, matching 90s POTS
	// silent-hold semantics while the host is in the ADD_PARTY flow.
	if err := enc.SetDTX(true); err != nil {
		return nil, fmt.Errorf("SetDTX: %w", err)
	}
	return &Encoder{
		enc: enc,
		buf: make([]byte, 1024),
	}, nil
}

// Encode encodes a frame of PCM samples (should be FrameSize=960) into an Opus packet.
// Returns a copy of the encoded data.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("encode: empty input")
	}
	n, err := e.enc.Encode(pcm, e.buf)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	out := make([]byte, n)
	copy(out, e.buf[:n])
	return out, nil
}

// Decoder wraps an Opus decoder for 48kHz mono.
type Decoder struct {
	dec *opus.Decoder
	buf []int16
}

// NewDecoder creates an Opus decoder.
// sampleRate: 48000, channels: 1
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("opus.NewDecoder: %w", err)
	}
	return &Decoder{
		dec: dec,
		buf: make([]int16, FrameSize),
	}, nil
}

// Decode decodes an Opus packet into PCM samples.
// Returns a slice of the internal buffer — valid only until the next
// Decode call. The caller must consume or copy the data before then.
// This is safe for our pipeline: WritePlayback(snd_pcm_writei) blocks
// until ALSA consumes the samples, so the buffer is always consumed
// before the next ReadRTP→Decode cycle.
func (d *Decoder) Decode(data []byte) ([]int16, error) {
	n, err := d.dec.Decode(data, d.buf)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return d.buf[:n], nil
}

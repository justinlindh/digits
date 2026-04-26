package audio

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"unsafe"
)

// ErrUnderrun is returned by WriteFrame when an ALSA buffer underrun
// occurred and was recovered. The frame was still written successfully,
// but the caller should drain stale data to re-sync.
var ErrUnderrun = errors.New("alsa: buffer underrun recovered")

// codecConfig holds the detected codec's ALSA identifiers and mixer control names.
type codecConfig struct {
	CardName        string // ALSA card name for amixer/alsactl -c
	CaptureDevice   string // ALSA device for capture
	PlaybackDevice  string // ALSA device for playback
	MixerName       string // Volume mixer control name (PCM vs Lineout)
	ALSAMin         int    // Volume range minimum
	ALSAMax         int    // Volume range maximum
}

var (
	detectedCodec *codecConfig
	detectOnce    sync.Once
)

// detectCodec probes ALSA for the onboard TLV320AIC3104 ("digitscodec").
// Falls back to the Codec Zero HAT ("Zero") if not found.
func detectCodec() *codecConfig {
	detectOnce.Do(func() {
		// Try onboard TLV320AIC3104 first
		out, err := exec.Command("amixer", "-c", "digitscodec", "info").CombinedOutput()
		if err == nil && strings.Contains(string(out), "digitscodec") {
			slog.Info("audio: detected onboard TLV320AIC3104 codec")
			// V2 codec is fed a 12.288 MHz MCLK from Pi GPCLK0. At 48 kHz the
			// chip uses PLL-bypass mode and passes MCLK jitter through to the
			// audio stage; at 44.1 kHz it uses PLL-multiplication, whose loop
			// filter rejects the same jitter. The "digits_capture" and
			// "digits_playback" PCMs in /etc/asound.conf pin the hardware to
			// 44.1 kHz and expose any rate to apps via libasound's plug
			// resampler, so the rest of the pipeline (RNNoise, Opus framing)
			// can keep operating at 48 kHz unchanged.
			detectedCodec = &codecConfig{
				CardName:       "digitscodec",
				CaptureDevice:  "digits_capture",
				PlaybackDevice: "digits_playback",
				MixerName:      "PCM",
				ALSAMin:        40,
				ALSAMax:        115,
			}
			return
		}

		// Fall back to Codec Zero HAT (DA7212): runs cleanly at 48 kHz so we
		// can talk to the hardware directly via plughw for both directions,
		// matching the prior single-device V1 behavior exactly.
		slog.Info("audio: using Codec Zero HAT (DA7212)")
		detectedCodec = &codecConfig{
			CardName:       "Zero",
			CaptureDevice:  "plughw:CARD=Zero,DEV=0",
			PlaybackDevice: "plughw:CARD=Zero,DEV=0",
			MixerName:      "Lineout",
			ALSAMin:        20,
			ALSAMax:        58,
		}
	})
	return detectedCodec
}

// CodecCardName returns the ALSA card name for the detected codec.
func CodecCardName() string { return detectCodec().CardName }

// CodecDeviceName returns the ALSA capture device identifier for the detected
// codec. Retained for backward compatibility; new code should use the more
// specific CodecCaptureDevice / CodecPlaybackDevice helpers.
func CodecDeviceName() string { return detectCodec().CaptureDevice }

// CodecCaptureDevice returns the ALSA device the capture pipeline should open.
func CodecCaptureDevice() string { return detectCodec().CaptureDevice }

// CodecPlaybackDevice returns the ALSA device the playback pipeline should open.
func CodecPlaybackDevice() string { return detectCodec().PlaybackDevice }

// CodecMixerName returns the mixer control name for volume adjustment.
func CodecMixerName() string { return detectCodec().MixerName }

// CodecALSARange returns the min/max ALSA values for volume mapping.
func CodecALSARange() (int, int) { c := detectCodec(); return c.ALSAMin, c.ALSAMax }

// Config holds ALSA device parameters.
type Config struct {
	Device     string
	SampleRate int
	Channels   int
	FrameSize  int // samples per frame per channel (960 = 20ms at 48kHz)
}

// DefaultCaptureConfig returns config for the detected codec: stereo capture at 48kHz.
// V1 codec runs cleanly at 48 kHz natively; V2 capture goes through a
// /etc/asound.conf-defined plug device that resamples 44.1 kHz hardware up to
// the requested rate, so the pipeline downstream sees 48 kHz either way.
func DefaultCaptureConfig() Config {
	return Config{
		Device:     CodecCaptureDevice(),
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
	}
}

// DefaultPlaybackConfig returns config for mono playback at 48kHz.
// V1 uses the system "default" device (dmix) which auto-resamples and shares;
// V2 uses a /etc/asound.conf-defined plug device that pins hardware to
// 44.1 kHz for the same MCLK/PLL reasons that affect capture.
func DefaultPlaybackConfig() Config {
	return Config{
		Device:     CodecPlaybackDevice(),
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
	}
}

// ExtractChannel extracts a single channel from interleaved stereo PCM.
// ch=0 for left, ch=1 for right.
func ExtractChannel(interleaved []int16, totalChannels, ch int) []int16 {
	if len(interleaved) == 0 || totalChannels <= 0 || ch < 0 || ch >= totalChannels {
		return []int16{}
	}
	frames := len(interleaved) / totalChannels
	out := make([]int16, frames)
	for i := 0; i < frames; i++ {
		out[i] = interleaved[i*totalChannels+ch]
	}
	return out
}

// configureHWParams sets up ALSA hw params on the given PCM handle.
func configureHWParams(handle *C.snd_pcm_t, cfg Config) error {
	var params *C.snd_pcm_hw_params_t
	C.snd_pcm_hw_params_malloc(&params)
	defer C.snd_pcm_hw_params_free(params)

	if rc := C.snd_pcm_hw_params_any(handle, params); rc < 0 {
		return fmt.Errorf("snd_pcm_hw_params_any: %s", C.GoString(C.snd_strerror(rc)))
	}

	if rc := C.snd_pcm_hw_params_set_access(handle, params, C.SND_PCM_ACCESS_RW_INTERLEAVED); rc < 0 {
		return fmt.Errorf("set_access: %s", C.GoString(C.snd_strerror(rc)))
	}

	if rc := C.snd_pcm_hw_params_set_format(handle, params, C.SND_PCM_FORMAT_S16_LE); rc < 0 {
		return fmt.Errorf("set_format: %s", C.GoString(C.snd_strerror(rc)))
	}

	rate := C.uint(cfg.SampleRate)
	if rc := C.snd_pcm_hw_params_set_rate_near(handle, params, &rate, nil); rc < 0 {
		return fmt.Errorf("set_rate_near: %s", C.GoString(C.snd_strerror(rc)))
	}

	if rc := C.snd_pcm_hw_params_set_channels(handle, params, C.uint(cfg.Channels)); rc < 0 {
		return fmt.Errorf("set_channels: %s", C.GoString(C.snd_strerror(rc)))
	}

	periodSize := C.snd_pcm_uframes_t(cfg.FrameSize)
	if rc := C.snd_pcm_hw_params_set_period_size_near(handle, params, &periodSize, nil); rc < 0 {
		return fmt.Errorf("set_period_size_near: %s", C.GoString(C.snd_strerror(rc)))
	}

	bufSize := C.snd_pcm_uframes_t(cfg.FrameSize * 4)
	if rc := C.snd_pcm_hw_params_set_buffer_size_near(handle, params, &bufSize); rc < 0 {
		return fmt.Errorf("set_buffer_size_near: %s", C.GoString(C.snd_strerror(rc)))
	}

	if rc := C.snd_pcm_hw_params(handle, params); rc < 0 {
		return fmt.Errorf("snd_pcm_hw_params: %s", C.GoString(C.snd_strerror(rc)))
	}

	return nil
}

// Capture wraps an ALSA capture handle.
type Capture struct {
	handle *C.snd_pcm_t
	cfg    Config
}

// NewCapture opens an ALSA capture device with the given config.
func NewCapture(cfg Config) (*Capture, error) {
	cdev := C.CString(cfg.Device)
	defer C.free(unsafe.Pointer(cdev))

	var handle *C.snd_pcm_t
	rc := C.snd_pcm_open(&handle, cdev, C.SND_PCM_STREAM_CAPTURE, 0)
	if rc < 0 {
		return nil, fmt.Errorf("snd_pcm_open capture %q: %s", cfg.Device, C.GoString(C.snd_strerror(rc)))
	}

	if err := configureHWParams(handle, cfg); err != nil {
		C.snd_pcm_close(handle)
		return nil, fmt.Errorf("configure capture hw params: %w", err)
	}

	return &Capture{handle: handle, cfg: cfg}, nil
}

// ReadFrame reads one frame of interleaved samples (stereo = 2 * FrameSize samples).
func (c *Capture) ReadFrame() ([]int16, error) {
	total := c.cfg.FrameSize * c.cfg.Channels
	buf := make([]int16, total)

	rc := C.snd_pcm_readi(c.handle, unsafe.Pointer(&buf[0]), C.snd_pcm_uframes_t(c.cfg.FrameSize))
	if rc < 0 {
		C.snd_pcm_recover(c.handle, C.int(rc), 0)
		rc = C.snd_pcm_readi(c.handle, unsafe.Pointer(&buf[0]), C.snd_pcm_uframes_t(c.cfg.FrameSize))
		if rc < 0 {
			return nil, fmt.Errorf("snd_pcm_readi: %s", C.GoString(C.snd_strerror(C.int(rc))))
		}
	}

	frames := int(rc)
	out := make([]int16, frames*c.cfg.Channels)
	copy(out, buf[:frames*c.cfg.Channels])
	return out, nil
}

// Close closes the capture handle.
func (c *Capture) Close() {
	if c.handle != nil {
		C.snd_pcm_close(c.handle)
		c.handle = nil
	}
}

// Playback wraps an ALSA playback handle.
type Playback struct {
	handle *C.snd_pcm_t
	cfg    Config
}

// NewPlayback opens an ALSA playback device with the given config.
func NewPlayback(cfg Config) (*Playback, error) {
	cdev := C.CString(cfg.Device)
	defer C.free(unsafe.Pointer(cdev))

	var handle *C.snd_pcm_t
	rc := C.snd_pcm_open(&handle, cdev, C.SND_PCM_STREAM_PLAYBACK, 0)
	if rc < 0 {
		return nil, fmt.Errorf("snd_pcm_open playback %q: %s", cfg.Device, C.GoString(C.snd_strerror(rc)))
	}

	if err := configureHWParams(handle, cfg); err != nil {
		C.snd_pcm_close(handle)
		return nil, fmt.Errorf("configure playback hw params: %w", err)
	}

	return &Playback{handle: handle, cfg: cfg}, nil
}

// Handle returns the underlying ALSA PCM handle (as unsafe.Pointer for cgo callers).
func (p *Playback) Handle() unsafe.Pointer {
	return unsafe.Pointer(p.handle)
}

// WriteFrame writes one frame of mono samples to the playback device.
// Must only be called from the mixer render goroutine — not thread-safe.
func (p *Playback) WriteFrame(samples []int16) error {
	frames := len(samples) / p.cfg.Channels
	if frames == 0 {
		return nil
	}

	rc := C.snd_pcm_writei(p.handle, unsafe.Pointer(&samples[0]), C.snd_pcm_uframes_t(frames))
	if rc < 0 {
		underrun := rc == -C.EPIPE
		C.snd_pcm_recover(p.handle, C.int(rc), 0)
		rc = C.snd_pcm_writei(p.handle, unsafe.Pointer(&samples[0]), C.snd_pcm_uframes_t(frames))
		if rc < 0 {
			return fmt.Errorf("snd_pcm_writei: %s", C.GoString(C.snd_strerror(C.int(rc))))
		}
		if underrun {
			return ErrUnderrun
		}
		return nil
	}

	return nil
}

// PeriodSize returns the configured period size in frames.
func (p *Playback) PeriodSize() int {
	return p.cfg.FrameSize
}

// Prime writes silence to fill the ALSA buffer, preventing the first real
// snd_pcm_writei from blocking while the hardware buffer drains.
// Call once before switching from idle/keepalive to live audio playback.
func (p *Playback) Prime() error {
	silence := make([]int16, p.cfg.FrameSize*p.cfg.Channels)

	// Recover from any prior underrun state
	state := C.snd_pcm_state(p.handle)
	if state == C.SND_PCM_STATE_XRUN || state == C.SND_PCM_STATE_SETUP {
		C.snd_pcm_prepare(p.handle)
	}

	// Fill the buffer (4 periods = 80ms at 960-sample period, 48kHz)
	for i := 0; i < 4; i++ {
		rc := C.snd_pcm_writei(p.handle, unsafe.Pointer(&silence[0]),
			C.snd_pcm_uframes_t(p.cfg.FrameSize))
		if rc < 0 {
			C.snd_pcm_recover(p.handle, C.int(rc), 0)
			rc = C.snd_pcm_writei(p.handle, unsafe.Pointer(&silence[0]),
				C.snd_pcm_uframes_t(p.cfg.FrameSize))
			if rc < 0 {
				return fmt.Errorf("snd_pcm_writei (prime): %s", C.GoString(C.snd_strerror(C.int(rc))))
			}
		}
	}
	return nil
}

// Close drains and closes the playback handle.
func (p *Playback) Close() {
	if p.handle != nil {
		C.snd_pcm_drain(p.handle)
		C.snd_pcm_close(p.handle)
		p.handle = nil
	}
}

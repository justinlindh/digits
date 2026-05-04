package phonekit

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Phone combines serial communication with the Pico firmware and audio
// playback into a single API.
type Phone struct {
	serial *Serial
	audio  *audioPlayer
}

// Open opens the serial device at baud, starts the read loop, and returns a
// Phone ready for use.
func Open(device string, baud int) (*Phone, error) {
	s, err := openSerial(device, baud)
	if err != nil {
		return nil, err
	}
	return &Phone{serial: s, audio: newAudioPlayer()}, nil
}

// newPhoneFromSerial wraps an existing Serial for use in tests.
func newPhoneFromSerial(s *Serial) *Phone {
	return &Phone{serial: s, audio: newAudioPlayer()}
}

// Close stops the serial read loop and closes the underlying port.
func (p *Phone) Close() error {
	return p.serial.Close()
}

// Ping sends a PING command and expects a PONG response within 2 seconds.
func (p *Phone) Ping() error {
	resp, err := p.serial.SendCommand("PING", 2*time.Second)
	if err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	if resp != "PONG" {
		return fmt.Errorf("ping: unexpected response %q", resp)
	}
	return nil
}

// LED sets the LED to the given mode. Valid modes: ON, OFF, BLINK,
// DOUBLE_PULSE, HEARTBEAT.
func (p *Phone) LED(mode string) error {
	return p.serial.SendFire("LED:" + mode)
}

// SetPhase sends STATE:SET:<phase> and expects STATE:SET:OK in response within
// 5 seconds.
func (p *Phone) SetPhase(phase string) error {
	resp, err := p.serial.SendCommand("STATE:SET:"+phase, 5*time.Second)
	if err != nil {
		return fmt.Errorf("set phase: %w", err)
	}
	if resp != "STATE:SET:OK" {
		return fmt.Errorf("set phase: %s", resp)
	}
	return nil
}

// Events returns the channel of decoded firmware events.
func (p *Phone) Events() <-chan Event {
	return p.serial.Events()
}

// Play plays WAV audio data in the foreground. The context can be used to
// cancel playback.
func (p *Phone) Play(ctx context.Context, wav []byte) error {
	return p.audio.Play(ctx, wav)
}

// PlayFile reads the WAV file at path and plays it via Play.
func (p *Phone) PlayFile(ctx context.Context, path string) error {
	return p.audio.PlayFile(ctx, path)
}

// WaitForEvent blocks until an event matching the predicate arrives or ctx is
// cancelled. It returns the matched event or ctx.Err() on cancellation.
func (p *Phone) WaitForEvent(ctx context.Context, match func(Event) bool) (Event, error) {
	ch := p.serial.Events()
	for {
		select {
		case ev := <-ch:
			if match(ev) {
				return ev, nil
			}
		case <-ctx.Done():
			return Event{}, ctx.Err()
		}
	}
}

// WaitForKey blocks until a KEY event arrives or ctx is cancelled. It returns
// the key value (one of "0"-"9", "*", "#").
func (p *Phone) WaitForKey(ctx context.Context) (string, error) {
	ev, err := p.WaitForEvent(ctx, func(e Event) bool {
		return e.Type == "KEY"
	})
	if err != nil {
		return "", err
	}
	return ev.Value, nil
}

// WaitForHook blocks until a HOOK event with the given state arrives or ctx is
// cancelled. state is compared case-insensitively ("ON" or "OFF").
func (p *Phone) WaitForHook(ctx context.Context, state string) error {
	want := strings.ToUpper(state)
	_, err := p.WaitForEvent(ctx, func(e Event) bool {
		return e.Type == "HOOK" && e.Value == want
	})
	return err
}

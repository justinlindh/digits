package phone

import (
	"testing"
	"time"
)

func TestSerialPortInterface(t *testing.T) {
	// Verify the type implements expected interface at compile time
	var _ interface {
		Events() <-chan string
		SendCommand(string, time.Duration) (string, error)
		SendFire(string)
		Ping() error
		Ring(bool)
		LED(string)
		Close() error
	} = (*SerialPort)(nil)
}

func TestIsUnsolicitedEvent(t *testing.T) {
	unsolicited := []string{
		"HOOK:OFF", "HOOK:ON",
		"STATUS:READY",
		"KEY:5", "KEY:*", "KEY:#",
		"DIAL:5551234",
		"FSM:IDLE", "FSM:DIALING", "FSM:RINGING",
	}
	for _, msg := range unsolicited {
		if !isUnsolicitedEvent(msg) {
			t.Errorf("expected %q to be unsolicited", msg)
		}
	}

	commandResponses := []string{
		"PONG",
		"VERSION:1.0.0:abc1234",
		"RING:ACK", "RING:DONE", "RING:TEST:ACK",
		"HOOK:FORCED:ON_HOOK", "HOOK:FORCED:OFF_HOOK",
		"HOOK:RELEASED",
		"HOOK:INVERT:ON", "HOOK:INVERT:OFF",
		"RST:OK",
		"STATE:IDLE",
		"MODE:KEYTEST", "MODE:NORMAL",
	}
	for _, msg := range commandResponses {
		if isUnsolicitedEvent(msg) {
			t.Errorf("expected %q to be a command response, not unsolicited", msg)
		}
	}
}

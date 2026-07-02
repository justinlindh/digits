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
		StartRing()
		StopRing()
		LED(string)
		AddMonitor(chan string) func()
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
		// Ack for the fire-and-forget DIAL:RESET command. Shares the DIAL:
		// prefix but must not be classified as an unsolicited dialed number.
		"DIAL:RESET:OK",
	}
	for _, msg := range commandResponses {
		if isUnsolicitedEvent(msg) {
			t.Errorf("expected %q to be a command response, not unsolicited", msg)
		}
	}
}

func TestIsFireAndForgetAck(t *testing.T) {
	acks := []string{
		"HOOK:FLASH:ON", "HOOK:FLASH:OFF",
		"CALL:CONNECTED:ACK", "CALL:CONNECTED:IGNORED",
		// DIAL:RESET is sent fire-and-forget, so its ack has no waiting
		// consumer and must be dropped here rather than routed as an event.
		"DIAL:RESET:OK",
	}
	for _, msg := range acks {
		if !isFireAndForgetAck(msg) {
			t.Errorf("expected %q to be a fire-and-forget ack", msg)
		}
	}

	notAcks := []string{
		"HOOK:OFF", "HOOK:ON", "HOOK:FLASH",
		"PONG", "STATUS:READY",
		"RING:ACK", "RING:DONE",
		"VERSION:1.0.0:abc1234",
		"KEY:5", "DIAL:5551234", "FSM:IDLE",
	}
	for _, msg := range notAcks {
		if isFireAndForgetAck(msg) {
			t.Errorf("expected %q to NOT be a fire-and-forget ack", msg)
		}
	}
}

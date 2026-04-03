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

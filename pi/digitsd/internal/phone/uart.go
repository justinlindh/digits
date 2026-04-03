package phone

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

const defaultTimeout = 500 * time.Millisecond

// UARTClient talks to the Pico via Unix socket (legacy bridge, mostly unused).
// Each command opens a new connection (one command per connection, per the socket protocol).
type UARTClient struct {
	socketPath string
}

// NewUARTClient creates a new UARTClient targeting the given Unix socket path.
func NewUARTClient(socketPath string) *UARTClient {
	return &UARTClient{socketPath: socketPath}
}

// SendCommand sends a command string and returns the trimmed response.
// It opens a new connection for each command, writes cmd + "\n", reads one response line, then closes.
func (c *UARTClient) SendCommand(cmd string, timeout time.Duration) (string, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, timeout)
	if err != nil {
		return "", fmt.Errorf("uart: dial %s: %w", c.socketPath, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", fmt.Errorf("uart: set deadline: %w", err)
	}

	if _, err := fmt.Fprintf(conn, "%s\n", cmd); err != nil {
		return "", fmt.Errorf("uart: write command %q: %w", cmd, err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("uart: read response: %w", err)
		}
		return "", fmt.Errorf("uart: no response from server")
	}

	return strings.TrimSpace(scanner.Text()), nil
}

// Ring sends RING:START or RING:STOP to the Pico via the UART service.
func (c *UARTClient) Ring(start bool) error {
	cmd := "RING:STOP"
	if start {
		cmd = "RING:START"
	}
	_, err := c.SendCommand(cmd, defaultTimeout)
	return err
}

// Tone sends TONE:<name> (e.g. DIAL, RINGBACK, STOP).
// Handled locally by the tone player, returns "OK".
func (c *UARTClient) Tone(name string) error {
	_, err := c.SendCommand("TONE:"+name, defaultTimeout)
	return err
}

// LED sends LED:<mode> (e.g. ON, OFF, BLINK).
func (c *UARTClient) LED(mode string) error {
	_, err := c.SendCommand("LED:"+mode, defaultTimeout)
	return err
}

// Ping sends PING and expects PONG back. Returns an error if the response is wrong.
func (c *UARTClient) Ping() error {
	resp, err := c.SendCommand("PING", defaultTimeout)
	if err != nil {
		return fmt.Errorf("uart: ping failed: %w", err)
	}
	if resp != "PONG" {
		return fmt.Errorf("uart: ping expected PONG, got %q", resp)
	}
	return nil
}

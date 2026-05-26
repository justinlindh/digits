package signal

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// pingTimeout is how long the client waits between server pings
	// before considering the connection dead. Must be greater than the
	// server's ping interval (30s).
	pingTimeout = 45 * time.Second
)

// Client connects to a signald WebSocket server and manages message I/O.
type Client struct {
	url         string
	number      string
	hardwareID  string
	deviceToken string
	conn        *websocket.Conn
	inbox       chan *Message
	done        chan struct{}
	mu          sync.Mutex
}

// NewClient creates a new Client. Call Connect() to establish the connection.
func NewClient(url, number, hardwareID, deviceToken string) *Client {
	return &Client{
		url:         url,
		number:      number,
		hardwareID:  hardwareID,
		deviceToken: deviceToken,
		inbox:       make(chan *Message, 32),
		done:        make(chan struct{}),
	}
}

// Connect dials the WebSocket URL, sends a register message, and starts
// the readPump goroutine. On failure, the done channel is closed so that
// callers selecting on Done() can detect the failure and retry.
func (c *Client) Connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(c.url, http.Header{})
	if err != nil {
		close(c.done)
		return fmt.Errorf("signal: dial %s: %w", c.url, err)
	}
	c.conn = conn

	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
			close(c.done)
		}
	}()

	// Send registration
	reg := &Message{Type: TypeRegister, Number: c.number, HardwareID: c.hardwareID, DeviceToken: c.deviceToken}
	data, err := reg.Marshal()
	if err != nil {
		return fmt.Errorf("signal: marshal register: %w", err)
	}
	c.mu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, data)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("signal: send register: %w", err)
	}

	// Reset read deadline on each server ping so Cloudflare/proxy
	// idle timeouts don't kill the connection.
	if err := c.conn.SetReadDeadline(time.Now().Add(pingTimeout)); err != nil {
		return fmt.Errorf("signal: set read deadline: %w", err)
	}
	c.conn.SetPingHandler(func(appData string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(pingTimeout)); err != nil {
			slog.Warn("signal: set read deadline on ping", "error", err)
		}
		return c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	go c.readPump()
	ok = true
	return nil
}

// readPump reads messages from the WebSocket until the connection closes.
// It closes the done channel when it exits.
func (c *Client) readPump() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Error("signal: read error", "error", err)
			}
			return
		}
		msg, err := ParseMessage(data)
		if err != nil {
			slog.Error("signal: parse message", "error", err)
			continue
		}
		select {
		case c.inbox <- msg:
		default:
			slog.Error("signal: inbox full, dropping message", "type", msg.Type)
		}
	}
}

// Send writes a message to the signald server. Thread-safe.
func (c *Client) Send(msg *Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("signal: marshal: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("signal: not connected")
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("signal: write: %w", err)
	}
	return nil
}

// Inbox returns the channel of incoming messages.
func (c *Client) Inbox() <-chan *Message {
	return c.inbox
}

// Done returns a channel that is closed when the connection is lost.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Close shuts down the WebSocket connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	// Send a clean close frame
	_ = c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	return c.conn.Close()
}

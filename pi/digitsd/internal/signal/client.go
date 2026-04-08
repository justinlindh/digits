package signal

import (
	"crypto/tls"
	"fmt"
	"log"
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
	url             string
	number          string
	hardwareID      string
	deviceToken     string
	conn            *websocket.Conn
	inbox           chan *Message
	done            chan struct{}
	mu              sync.Mutex
	insecureSkipTLS bool
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

// SetInsecureSkipTLS disables TLS certificate verification (for dev/self-signed certs).
func (c *Client) SetInsecureSkipTLS(skip bool) {
	c.insecureSkipTLS = skip
}

// Connect dials the WebSocket URL, sends a register message, and starts
// the readPump goroutine.
func (c *Client) Connect() error {
	dialer := websocket.DefaultDialer
	if c.insecureSkipTLS {
		dialer = &websocket.Dialer{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	conn, _, err := dialer.Dial(c.url, http.Header{})
	if err != nil {
		return fmt.Errorf("signal: dial %s: %w", c.url, err)
	}
	c.conn = conn

	// Send registration
	reg := &Message{Type: TypeRegister, Number: c.number, HardwareID: c.hardwareID, DeviceToken: c.deviceToken}
	data, err := reg.Marshal()
	if err != nil {
		conn.Close()
		return fmt.Errorf("signal: marshal register: %w", err)
	}
	c.mu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, data)
	c.mu.Unlock()
	if err != nil {
		conn.Close()
		return fmt.Errorf("signal: send register: %w", err)
	}

	// Reset read deadline on each server ping so Cloudflare/proxy
	// idle timeouts don't kill the connection.
	c.conn.SetReadDeadline(time.Now().Add(pingTimeout))
	c.conn.SetPingHandler(func(appData string) error {
		c.conn.SetReadDeadline(time.Now().Add(pingTimeout))
		return c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	go c.readPump()
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
				log.Printf("signal: read: %v", err)
			}
			return
		}
		msg, err := ParseMessage(data)
		if err != nil {
			log.Printf("signal: parse message: %v", err)
			continue
		}
		select {
		case c.inbox <- msg:
		default:
			log.Printf("signal: inbox full, dropping message type=%s", msg.Type)
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

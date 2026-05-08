package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
)

type message struct {
	Type        string `json:"type"`
	Number      string `json:"number,omitempty"`
	To          string `json:"to,omitempty"`
	From        string `json:"from,omitempty"`
	HardwareID  string `json:"hardware_id,omitempty"`
	DeviceToken string `json:"device_token,omitempty"`
	SDP         string `json:"sdp,omitempty"`
	Error       string `json:"error,omitempty"`
}

type device struct {
	number     string
	hardwareID string
	token      string
}

type safeConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (c *safeConn) WriteMessage(msgType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteMessage(msgType, data)
}

func (c *safeConn) ReadMessage() (int, []byte, error) {
	return c.ws.ReadMessage()
}

func (c *safeConn) Close() error {
	return c.ws.Close()
}

func (c *safeConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

type stats struct {
	connected     atomic.Int64
	failed        atomic.Int64
	rejected      atomic.Int64
	callsStarted  atomic.Int64
	callsAnswered atomic.Int64
	callsFailed   atomic.Int64
	msgsRecv      atomic.Int64
}

func main() {
	serverURL := flag.String("server", "ws://localhost:8443/ws", "signald WebSocket URL")
	hostHeader := flag.String("host", "", "Host header for WebSocket upgrade (e.g. app.digits.family)")
	dbURL := flag.String("db", "", "DATABASE_URL for seeding (required)")
	numDevices := flag.Int("devices", 1000, "number of simulated devices")
	rampRate := flag.Int("ramp", 100, "devices per second during ramp-up")
	callRate := flag.Float64("call-rate", 10, "calls per second to initiate")
	callDuration := flag.Duration("call-duration", 3*time.Second, "average call hold time before hangup")
	duration := flag.Duration("duration", 60*time.Second, "how long to run after ramp-up")
	seedOnly := flag.Bool("seed-only", false, "seed test data and exit")
	cleanOnly := flag.Bool("clean-only", false, "delete test data and exit")
	flag.Parse()

	if *dbURL == "" {
		log.Fatal("--db is required (DATABASE_URL for seeding test data)")
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	db, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	if *cleanOnly {
		clean(db)
		return
	}

	devices := seed(db, *numDevices)
	if *seedOnly {
		log.Printf("seeded %d devices, exiting", len(devices))
		return
	}

	u, err := url.Parse(*serverURL)
	if err != nil {
		log.Fatal(err)
	}

	wsHost := *hostHeader
	log.Printf("loadtest: %d devices, ramp %d/s, call-rate %.1f/s, call-duration %s, run %s",
		*numDevices, *rampRate, *callRate, *callDuration, *duration)
	log.Printf("target: %s (host: %s)", *serverURL, wsHost)

	var s stats
	var wg sync.WaitGroup
	conns := make([]*safeConn, len(devices))

	rampStart := time.Now()
	batch := 0
	for i, dev := range devices {
		wg.Add(1)
		go func(idx int, d device) {
			defer wg.Done()
			ws, err := connect(u, wsHost, d)
			if err != nil {
				s.failed.Add(1)
				if s.failed.Load()%100 == 1 {
					log.Printf("connect failed (sample): %v", err)
				}
				return
			}
			sc := &safeConn{ws: ws}
			conns[idx] = sc
			s.connected.Add(1)

			go readPump(sc, &s)
		}(i, dev)

		batch++
		if batch >= *rampRate {
			time.Sleep(time.Second)
			batch = 0
			log.Printf("ramp: %d/%d connected, %d failed, %d rejected (%.1fs)",
				s.connected.Load(), len(devices), s.failed.Load(), s.rejected.Load(),
				time.Since(rampStart).Seconds())
		}
	}
	wg.Wait()

	rampDur := time.Since(rampStart)
	log.Printf("ramp complete: %d connected, %d failed, %d rejected in %.1fs (%.0f conn/s)",
		s.connected.Load(), s.failed.Load(), s.rejected.Load(), rampDur.Seconds(),
		float64(s.connected.Load())/rampDur.Seconds())

	callInterval := time.Duration(float64(time.Second) / *callRate)
	callStop := time.After(*duration)
	ticker := time.NewTicker(callInterval)
	defer ticker.Stop()

	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			log.Printf("live: conn=%d calls_started=%d answered=%d failed=%d msgs=%d",
				s.connected.Load(), s.callsStarted.Load(), s.callsAnswered.Load(),
				s.callsFailed.Load(), s.msgsRecv.Load())
		}
	}()

	log.Printf("starting call generation: %.1f calls/s", *callRate)
callLoop:
	for {
		select {
		case <-callStop:
			break callLoop
		case <-ticker.C:
			a, b := pickPair(conns, devices)
			if a == -1 {
				continue
			}
			go simulateCall(conns[a], conns[b], devices[a], devices[b], *callDuration, &s)
		}
	}

	log.Printf("=== RESULTS ===")
	log.Printf("  connections: %d succeeded, %d failed, %d rejected",
		s.connected.Load(), s.failed.Load(), s.rejected.Load())
	log.Printf("  calls: %d started, %d answered, %d failed",
		s.callsStarted.Load(), s.callsAnswered.Load(), s.callsFailed.Load())
	log.Printf("  messages received: %d", s.msgsRecv.Load())

	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range splitLines(data) {
			if len(line) > 4 && (line[:4] == "VmRS" || line[:4] == "VmPe" || line[:5] == "Threa") {
				log.Printf("  %s", line)
			}
		}
	}

	log.Printf("cleaning up test data...")
	clean(db)

	for _, conn := range conns {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func seed(db *sql.DB, n int) []device {
	ctx := context.Background()
	log.Printf("seeding %d test devices...", n)

	userID := uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (email) DO UPDATE SET name = $3 RETURNING id`,
		userID, "loadtest@digits.local", "Load Test")
	if err != nil {
		log.Fatalf("create user: %v", err)
	}
	_ = db.QueryRowContext(ctx, `SELECT id FROM users WHERE email = 'loadtest@digits.local'`).Scan(&userID)

	hhID := uuid.New().String()
	_, err = db.ExecContext(ctx,
		`INSERT INTO households (id, name, created_at) VALUES ($1, $2, NOW())
		 ON CONFLICT DO NOTHING`,
		hhID, "loadtest-household")
	if err != nil {
		log.Fatalf("create household: %v", err)
	}
	_ = db.QueryRowContext(ctx,
		`SELECT id FROM households WHERE name = 'loadtest-household'`).Scan(&hhID)

	_, _ = db.ExecContext(ctx,
		`INSERT INTO household_members (user_id, household_id, role, created_at)
		 VALUES ($1, $2, 'admin', NOW()) ON CONFLICT DO NOTHING`,
		userID, hhID)

	devices := make([]device, n)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("begin tx: %v", err)
	}

	for i := range n {
		number := fmt.Sprintf("LT%07d", i)
		hwID := fmt.Sprintf("lt-hw-%07d", i)
		token := fmt.Sprintf("lt-token-%07d", i)

		devices[i] = device{
			number:     number,
			hardwareID: hwID,
			token:      token,
		}

		var lineID int64
		err := tx.QueryRowContext(ctx,
			`INSERT INTO lines (number, name, household_id, created_at, updated_at)
			 VALUES ($1, $2, $3, NOW(), NOW())
			 ON CONFLICT (number) DO UPDATE SET name = $2
			 RETURNING id`,
			number, fmt.Sprintf("Load Test %d", i), hhID).Scan(&lineID)
		if err != nil {
			tx.Rollback()
			log.Fatalf("create line %d: %v", i, err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO devices (line_id, hardware_id, device_token, paired_at, created_at)
			 VALUES ($1, $2, $3, NOW(), NOW())
			 ON CONFLICT (hardware_id) DO UPDATE SET device_token = $3, paired_at = NOW(), line_id = $1`,
			lineID, hwID, hashToken(token))
		if err != nil {
			tx.Rollback()
			log.Fatalf("create device %d: %v", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}

	log.Printf("seeded: user=%s household=%s lines=%d devices=%d", userID, hhID, n, n)
	return devices
}

func clean(db *sql.DB) {
	ctx := context.Background()
	log.Printf("cleaning loadtest data...")

	res, _ := db.ExecContext(ctx, `DELETE FROM devices WHERE hardware_id LIKE 'lt-hw-%'`)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("  deleted %d devices", n)
	}
	res, _ = db.ExecContext(ctx, `DELETE FROM lines WHERE number LIKE 'LT%'`)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("  deleted %d lines", n)
	}
	res, _ = db.ExecContext(ctx, `DELETE FROM household_members WHERE user_id IN (SELECT id FROM users WHERE email = 'loadtest@digits.local')`)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("  deleted %d memberships", n)
	}
	db.ExecContext(ctx, `DELETE FROM households WHERE name = 'loadtest-household'`)
	db.ExecContext(ctx, `DELETE FROM users WHERE email = 'loadtest@digits.local'`)
	log.Printf("clean done")
}

func connect(u *url.URL, hostHeader string, d device) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	var header map[string][]string
	if hostHeader != "" {
		header = map[string][]string{"Host": {hostHeader}}
	}
	conn, _, err := dialer.Dial(u.String(), header)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	reg := message{
		Type:        "register",
		Number:      d.number,
		HardwareID:  d.hardwareID,
		DeviceToken: d.token,
	}
	data, _ := json.Marshal(reg)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		conn.Close()
		return nil, fmt.Errorf("register write: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, resp, err := conn.ReadMessage()
	if err == nil {
		var m message
		if json.Unmarshal(resp, &m) == nil && m.Type == "error" {
			conn.Close()
			return nil, fmt.Errorf("register rejected: %s", m.Error)
		}
	}
	conn.SetReadDeadline(time.Time{})

	return conn, nil
}

func readPump(conn *safeConn, s *stats) {
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.msgsRecv.Add(1)
	}
}

func pickPair(conns []*safeConn, devices []device) (int, int) {
	for range 100 {
		a := rand.IntN(len(conns))
		b := rand.IntN(len(conns))
		if a != b && conns[a] != nil && conns[b] != nil {
			return a, b
		}
	}
	return -1, -1
}

func simulateCall(caller, callee *safeConn, from, to device, holdTime time.Duration, s *stats) {
	s.callsStarted.Add(1)

	callMsg := message{
		Type: "call",
		To:   to.number,
		SDP:  fakeSDP("offer"),
	}
	data, _ := json.Marshal(callMsg)
	if err := caller.WriteMessage(websocket.TextMessage, data); err != nil {
		s.callsFailed.Add(1)
		return
	}

	time.Sleep(100 * time.Millisecond)
	answerMsg := message{
		Type: "answer",
		To:   from.number,
		SDP:  fakeSDP("answer"),
	}
	data, _ = json.Marshal(answerMsg)
	if err := callee.WriteMessage(websocket.TextMessage, data); err != nil {
		s.callsFailed.Add(1)
		return
	}
	s.callsAnswered.Add(1)

	for range 3 {
		ice := message{Type: "ice", To: to.number, SDP: `{"candidate":"candidate:0 1 UDP 2122252543 0.0.0.0 50000 typ host"}`}
		data, _ = json.Marshal(ice)
		_ = caller.WriteMessage(websocket.TextMessage, data)
		time.Sleep(50 * time.Millisecond)
	}

	jitter := time.Duration(rand.Int64N(int64(holdTime)))
	time.Sleep(holdTime/2 + jitter)

	hangup := message{Type: "hangup", To: to.number}
	data, _ = json.Marshal(hangup)
	_ = caller.WriteMessage(websocket.TextMessage, data)
}

func fakeSDP(sdpType string) string {
	return fmt.Sprintf(`{"type":"%s","sdp":"v=0\r\no=- %d 0 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\na=group:BUNDLE audio\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\nc=IN IP4 0.0.0.0\r\na=rtpmap:111 opus/48000/2\r\n"}`,
		sdpType, time.Now().UnixNano())
}

func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

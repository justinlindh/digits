package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
)

// Latency measurement client: connects to signald as 3140002,
// calls 3140001, sends audio with precise timestamps.
// Compare with Pi-side logs to measure one-way latency.

func main() {
	signaldURL := flag.String("signald", "wss://localhost:8443/ws", "signald WebSocket URL")
	number := flag.String("number", "3140002", "our extension")
	target := flag.String("target", "3140001", "target extension (Pi)")
	duration := flag.Duration("duration", 10*time.Second, "how long to send audio")
	flag.Parse()

	log.SetFlags(log.Lmicroseconds)

	// Connect to signald
	u, _ := url.Parse(*signaldURL)
	q := u.Query()
	q.Set("number", *number)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{}
	ws, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("dial signald: %v", err)
	}
	defer func() { _ = ws.Close() }()

	// Register with signald (required before any signaling)
	regMsg, _ := json.Marshal(sigclient.Message{Type: sigclient.TypeRegister, Number: *number})
	if err := ws.WriteMessage(websocket.TextMessage, regMsg); err != nil {
		log.Fatalf("register: %v", err)
	}
	slog.Info("registered with signald", "number", *number)

	// Create WebRTC peer connection
	config := webrtc.Configuration{}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Fatalf("create peer connection: %v", err)
	}
	defer func() { _ = pc.Close() }()

	// Create audio track with known starting sequence number (1) for latency correlation
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "latclient",
		webrtc.WithRTPSequenceNumber(1),
	)
	if err != nil {
		log.Fatalf("create track: %v", err)
	}
	rtpSender, err := pc.AddTrack(track)
	if err != nil {
		log.Fatalf("add track: %v", err)
	}
	// Drain RTCP — required by pion to prevent buffer backpressure
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	// Opus encoder
	enc, err := codec.NewEncoder(48000, 1, 24000)
	if err != nil {
		log.Fatalf("encoder: %v", err)
	}

	var mu sync.Mutex

	// ICE candidate handling
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		j := c.ToJSON()
		b, _ := json.Marshal(j)
		msg := sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        *target,
			Candidate: string(b),
		}
		data, _ := json.Marshal(msg)
		mu.Lock()
		if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
			slog.Warn("send ICE candidate failed", "error", err)
		}
		mu.Unlock()
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		slog.Info("connection state", "state", s)
	})

	// Create offer
	gatherDone := webrtc.GatheringCompletePromise(pc)
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Fatalf("create offer: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		log.Fatalf("set local desc: %v", err)
	}

	// Wait for ICE gathering
	select {
	case <-gatherDone:
	case <-time.After(5 * time.Second):
		log.Fatal("ICE gathering timeout")
	}

	// Send call + SDP (same as browser test client)
	callMsg := sigclient.Message{
		Type: sigclient.TypeCall,
		To:   *target,
	}
	callData, _ := json.Marshal(callMsg)
	mu.Lock()
	if err := ws.WriteMessage(websocket.TextMessage, callData); err != nil {
		mu.Unlock()
		log.Fatalf("send call: %v", err)
	}
	mu.Unlock()

	offerMsg := sigclient.Message{
		Type: sigclient.TypeSDP,
		To:   *target,
		SDP:  pc.LocalDescription().SDP,
	}
	offerData, _ := json.Marshal(offerMsg)
	mu.Lock()
	if err := ws.WriteMessage(websocket.TextMessage, offerData); err != nil {
		mu.Unlock()
		log.Fatalf("send offer: %v", err)
	}
	mu.Unlock()
	slog.Info("sent call + offer", "target", *target)

	// Read signaling messages
	go func() {
		for {
			_, msgData, err := ws.ReadMessage()
			if err != nil {
				slog.Warn("ws read failed", "error", err)
				return
			}
			var msg sigclient.Message
			if err := json.Unmarshal(msgData, &msg); err != nil {
				continue
			}

			switch msg.Type {
			case sigclient.TypeAnswer:
				slog.Info("received answer SDP")
				answer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  msg.SDP,
				}
				if err := pc.SetRemoteDescription(answer); err != nil {
					slog.Warn("set remote desc failed", "error", err)
				}

			case sigclient.TypeICE:
				var candidate webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(msg.Candidate), &candidate); err != nil {
					slog.Warn("parse ICE failed", "error", err)
					continue
				}
				pc.AddICECandidate(candidate) //nolint:errcheck
			}
		}
	}()

	// Wait briefly for ring+SDP to arrive on the Pi, then inject HOOK:OFF to answer.
	// The Pi can't create a PeerManager until we answer, so we must inject before waiting for connection.
	slog.Info("waiting 2s for ring to arrive on Pi")
	time.Sleep(2 * time.Second)

	slog.Info("Injecting HOOK:OFF on Pi via socket")
	injectCmd := `ssh digits@<pi-ip> 'python3 -c "import socket; s=socket.socket(socket.AF_UNIX); s.connect(\"/home/digits/digits/pi/uart.sock\"); s.send(b\"TEST:EVENT:HOOK:OFF\n\"); print(s.recv(64)); s.close()"'`
	out, err := exec.Command("bash", "-c", injectCmd).CombinedOutput()
	slog.Info("HOOK:OFF inject result", "output", strings.TrimSpace(string(out)), "error", err)

	// Now wait for WebRTC connection (Pi creates PeerManager on answer, sends back SDP)
	slog.Info("waiting for WebRTC connection")
	deadline := time.After(10 * time.Second)
	for pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		select {
		case <-deadline:
			log.Fatal("connection timeout")
		case <-time.After(100 * time.Millisecond):
		}
	}

	slog.Info("=== CONNECTED ===")

	// Send audio frames with precise timestamps
	const (
		sampleRate = 48000
		frameMs    = 20
		frameSize  = sampleRate * frameMs / 1000 // 960
		freq       = 1000.0                       // 1kHz
	)

	// Wait 3 seconds for call to be fully established
	slog.Info("Warming up for 3 seconds")
	ticker := time.NewTicker(time.Duration(frameMs) * time.Millisecond)
	defer ticker.Stop()

	warmupEnd := time.Now().Add(3 * time.Second)
	warmupFrames := 0
	for range ticker.C {
		if time.Now().After(warmupEnd) {
			break
		}
		pcm := make([]int16, frameSize)
		for i := range pcm {
			t := float64(warmupFrames*frameSize+i) / float64(sampleRate)
			pcm[i] = int16(16000 * math.Sin(2*math.Pi*freq*t))
		}
		pkt, err := enc.Encode(pcm)
		if err != nil {
			continue
		}
		track.WriteSample(media.Sample{Data: pkt, Duration: time.Duration(frameMs) * time.Millisecond}) //nolint:errcheck
		warmupFrames++
		if warmupFrames == 1 {
			slog.Info("WARMUP[1]", "seq", 1, "wallclock", time.Now().Format("15:04:05.000000"))
		}
	}
	slog.Info("WARMUP last", "frame", warmupFrames, "seq", warmupFrames, "wallclock", time.Now().Format("15:04:05.000000"))
	slog.Info("Warmup done, starting measurement phase", "frames", warmupFrames)
	fmt.Println()

	// Now measure: send frames and log precise wallclock times.
	// The Pi side logs when it receives/plays each frame.
	// By comparing wallclocks (NTP-synced), we get one-way streaming latency.
	startTime := time.Now()
	frameNum := 0

	for range ticker.C {
		if time.Since(startTime) > *duration {
			break
		}

		pcm := make([]int16, frameSize)
		for i := range pcm {
			t := float64((warmupFrames+frameNum)*frameSize+i) / float64(sampleRate)
			pcm[i] = int16(16000 * math.Sin(2*math.Pi*freq*t))
		}

		pkt, err := enc.Encode(pcm)
		if err != nil {
			slog.Warn("encode failed", "error", err)
			continue
		}

		sendTime := time.Now()
		err = track.WriteSample(media.Sample{
			Data:     pkt,
			Duration: time.Duration(frameMs) * time.Millisecond,
		})
		if err != nil {
			slog.Warn("write sample failed", "error", err)
			continue
		}

		frameNum++
		elapsed := sendTime.Sub(startTime)

		totalFrame := warmupFrames + frameNum
		if frameNum <= 5 || frameNum%50 == 0 {
			slog.Info("SEND", "frame", frameNum, "seq", totalFrame, "elapsed", elapsed.Round(time.Millisecond), "wallclock", sendTime.Format("15:04:05.000000"))
		}
	}

	slog.Info("=== DONE ===", "frames_sent", frameNum, "warmup_frames", warmupFrames, "duration", time.Since(startTime).Round(time.Millisecond))
	slog.Info("Now check Pi logs: ssh digits@<pi-ip> 'cat /tmp/digitsd.log'")

	// Graceful close
	_ = pc.Close()
	os.Exit(0)
}

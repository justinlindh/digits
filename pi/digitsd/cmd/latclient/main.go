package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
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

	// Connect to signald
	u, _ := url.Parse(*signaldURL)
	q := u.Query()
	q.Set("number", *number)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	ws, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		slog.Error(fmt.Sprintf("dial signald: %v", err))
		os.Exit(1)
	}
	defer ws.Close()

	// Register with signald (required before any signaling)
	regMsg, _ := json.Marshal(sigclient.Message{Type: sigclient.TypeRegister, Number: *number})
	ws.WriteMessage(websocket.TextMessage, regMsg)
	slog.Info(fmt.Sprintf("registered with signald as %s", *number))

	// Create WebRTC peer connection
	config := webrtc.Configuration{}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		slog.Error(fmt.Sprintf("create peer connection: %v", err))
		os.Exit(1)
	}
	defer pc.Close()

	// Create audio track with known starting sequence number (1) for latency correlation
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "latclient",
		webrtc.WithRTPSequenceNumber(1),
	)
	if err != nil {
		slog.Error(fmt.Sprintf("create track: %v", err))
		os.Exit(1)
	}
	rtpSender, err := pc.AddTrack(track)
	if err != nil {
		slog.Error(fmt.Sprintf("add track: %v", err))
		os.Exit(1)
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
		slog.Error(fmt.Sprintf("encoder: %v", err))
		os.Exit(1)
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
		ws.WriteMessage(websocket.TextMessage, data)
		mu.Unlock()
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		slog.Info(fmt.Sprintf("connection state: %s", s))
	})

	// Create offer
	gatherDone := webrtc.GatheringCompletePromise(pc)
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		slog.Error(fmt.Sprintf("create offer: %v", err))
		os.Exit(1)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		slog.Error(fmt.Sprintf("set local desc: %v", err))
		os.Exit(1)
	}

	// Wait for ICE gathering
	select {
	case <-gatherDone:
	case <-time.After(5 * time.Second):
		slog.Error("ICE gathering timeout")
		os.Exit(1)
	}

	// Send call + SDP (same as browser test client)
	callMsg := sigclient.Message{
		Type: sigclient.TypeCall,
		To:   *target,
	}
	callData, _ := json.Marshal(callMsg)
	mu.Lock()
	ws.WriteMessage(websocket.TextMessage, callData)
	mu.Unlock()

	offerMsg := sigclient.Message{
		Type: sigclient.TypeSDP,
		To:   *target,
		SDP:  pc.LocalDescription().SDP,
	}
	offerData, _ := json.Marshal(offerMsg)
	mu.Lock()
	ws.WriteMessage(websocket.TextMessage, offerData)
	mu.Unlock()
	slog.Info(fmt.Sprintf("sent call + offer to %s", *target))

	// Read signaling messages
	go func() {
		for {
			_, msgData, err := ws.ReadMessage()
			if err != nil {
				slog.Error(fmt.Sprintf("ws read: %v", err))
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
					slog.Error(fmt.Sprintf("set remote desc: %v", err))
				}

			case sigclient.TypeICE:
				var candidate webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(msg.Candidate), &candidate); err != nil {
					slog.Error(fmt.Sprintf("parse ICE: %v", err))
					continue
				}
				pc.AddICECandidate(candidate) //nolint:errcheck
			}
		}
	}()

	// Wait briefly for ring+SDP to arrive on the Pi, then inject HOOK:OFF to answer.
	// The Pi can't create a PeerManager until we answer, so we must inject before waiting for connection.
	slog.Info("waiting 2s for ring to arrive on Pi...")
	time.Sleep(2 * time.Second)

	slog.Info("Injecting HOOK:OFF on Pi via socket...")
	injectCmd := `ssh digits@<pi-ip> 'python3 -c "import socket; s=socket.socket(socket.AF_UNIX); s.connect(\"/home/digits/digits/pi/uart.sock\"); s.send(b\"TEST:EVENT:HOOK:OFF\n\"); print(s.recv(64)); s.close()"'`
	out, err := exec.Command("bash", "-c", injectCmd).CombinedOutput()
	slog.Info(fmt.Sprintf("HOOK:OFF inject result: %s (err: %v)", strings.TrimSpace(string(out)), err))

	// Now wait for WebRTC connection (Pi creates PeerManager on answer, sends back SDP)
	slog.Info("waiting for WebRTC connection...")
	deadline := time.After(10 * time.Second)
	for {
		if pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
			break
		}
		select {
		case <-deadline:
			slog.Error("connection timeout")
			os.Exit(1)
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
	slog.Info("Warming up for 3 seconds...")
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
			slog.Info(fmt.Sprintf("WARMUP[1]: seq=1 wallclock=%s", time.Now().Format("15:04:05.000000")))
		}
	}
	slog.Info(fmt.Sprintf("WARMUP[%d]: seq=%d wallclock=%s (last warmup)", warmupFrames, warmupFrames, time.Now().Format("15:04:05.000000")))
	slog.Info(fmt.Sprintf("Warmup done (%d frames). Starting measurement phase.", warmupFrames))
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
			slog.Error(fmt.Sprintf("encode: %v", err))
			continue
		}

		sendTime := time.Now()
		err = track.WriteSample(media.Sample{
			Data:     pkt,
			Duration: time.Duration(frameMs) * time.Millisecond,
		})
		if err != nil {
			slog.Error(fmt.Sprintf("write sample: %v", err))
			continue
		}

		frameNum++
		elapsed := sendTime.Sub(startTime)

		totalFrame := warmupFrames + frameNum
		if frameNum <= 5 || frameNum%50 == 0 {
			slog.Info(fmt.Sprintf("SEND[%d]: seq=%d elapsed=%s wallclock=%s",
				frameNum, totalFrame, elapsed.Round(time.Millisecond),
				sendTime.Format("15:04:05.000000")))
		}
	}

	slog.Info(fmt.Sprintf("=== DONE — sent %d frames (after %d warmup) in %v ===",
		frameNum, warmupFrames, time.Since(startTime).Round(time.Millisecond)))
	slog.Info("Now check Pi logs: ssh digits@<pi-ip> 'cat /tmp/digitsd.log'")

	// Graceful close
	pc.Close()
	os.Exit(0)
}

package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func memStats() (heapMB, sysMB float64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / 1024 / 1024, float64(m.Sys) / 1024 / 1024
}

func report(stage string) {
	heap, sys := memStats()
	fmt.Printf("%-40s heap=%.1fMB  sys=%.1fMB\n", stage, heap, sys)
}

func main() {
	runtime.GC()
	report("baseline")

	// Create two peer connections (simulates a call)
	config := webrtc.Configuration{}

	pc1, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	report("after NewPeerConnection (caller)")

	pc2, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	report("after NewPeerConnection (callee)")

	// Add audio tracks
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "digits",
	)
	if err != nil {
		panic(err)
	}

	_, err = pc1.AddTrack(track)
	if err != nil {
		panic(err)
	}
	report("after AddTrack")

	// ICE candidate exchange
	pc1.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			if err := pc2.AddICECandidate(c.ToJSON()); err != nil {
				fmt.Printf("pc2 add ICE candidate: %v\n", err)
			}
		}
	})
	pc2.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			if err := pc1.AddICECandidate(c.ToJSON()); err != nil {
				fmt.Printf("pc1 add ICE candidate: %v\n", err)
			}
		}
	})

	// Create offer
	offer, err := pc1.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	gatherComplete1 := webrtc.GatheringCompletePromise(pc1)
	err = pc1.SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}
	<-gatherComplete1
	report("after SetLocalDescription (offer)")

	// Set remote description on callee
	err = pc2.SetRemoteDescription(*pc1.LocalDescription())
	if err != nil {
		panic(err)
	}
	report("after SetRemoteDescription (callee)")

	// Create answer
	answer, err := pc2.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}
	gatherComplete2 := webrtc.GatheringCompletePromise(pc2)
	err = pc2.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}
	<-gatherComplete2
	report("after SetLocalDescription (answer)")

	// Complete handshake
	err = pc1.SetRemoteDescription(*pc2.LocalDescription())
	if err != nil {
		panic(err)
	}
	report("after handshake complete")

	// Wait for connection
	connected := make(chan struct{})
	pc1.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			close(connected)
		}
	})

	select {
	case <-connected:
		report("CONNECTED (DTLS+SRTP up)")
	case <-time.After(10 * time.Second):
		report("timeout waiting for connection")
	}

	// Let it settle
	time.Sleep(2 * time.Second)
	runtime.GC()
	report("after GC settle")

	// Simulate sending audio for 5 seconds
	fmt.Println("\nSimulating 5s of audio send (20ms Opus frames)...")
	buf := make([]byte, 160) // small Opus frame
	start := time.Now()
	frames := 0
	for time.Since(start) < 5*time.Second {
		if err := track.WriteSample(media.Sample{
			Data:     buf,
			Duration: 20 * time.Millisecond,
		}); err != nil {
			fmt.Printf("write sample: %v\n", err)
		}
		frames++
		time.Sleep(20 * time.Millisecond)
	}
	report(fmt.Sprintf("after %d frames sent", frames))

	runtime.GC()
	report("final after GC")

	// RSS from OS perspective
	fmt.Println("\n--- OS-level memory ---")
	fmt.Printf("Check RSS with: ps -o rss= -p %d\n", 0) // placeholder

	if err := pc1.Close(); err != nil {
		fmt.Printf("pc1 close: %v\n", err)
	}
	if err := pc2.Close(); err != nil {
		fmt.Printf("pc2 close: %v\n", err)
	}
	report("after cleanup")
}

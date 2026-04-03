// clocksync measures one-way clock offset between two hosts using UDP timestamps.
//
// Server mode (run on Pi):   clocksync -server :9999
// Client mode (run on VM):   clocksync -client <pi-ip>:9999
//
// Sends 20 UDP packets with the sender's unix microsecond timestamp.
// Server echoes back with its own timestamp appended.
// Client computes: offset = (server_time - client_send - rtt/2)
// Reports min/avg/max offset. Positive = server ahead.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"time"
)

var (
	server = flag.String("server", "", "run as server on this address (e.g., :9999)")
	client = flag.String("client", "", "run as client to this address (e.g., <pi-ip>:9999)")
	count  = flag.Int("count", 20, "number of probes")
)

func main() {
	flag.Parse()

	if *server != "" {
		runServer(*server)
	} else if *client != "" {
		runClient(*client, *count)
	} else {
		fmt.Println("usage: clocksync -server :9999  OR  clocksync -client host:9999")
	}
}

func runServer(addr string) {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	log.Printf("clocksync server listening on %s", addr)

	buf := make([]byte, 64)
	for {
		n, remote, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}
		if n < 8 {
			continue
		}
		// Append server timestamp
		now := time.Now().UnixMicro()
		resp := make([]byte, n+8)
		copy(resp, buf[:n])
		binary.LittleEndian.PutUint64(resp[n:], uint64(now))
		conn.WriteTo(resp, remote)
	}
}

func runClient(addr string, count int) {
	conn, err := net.Dial("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	var offsets []float64

	for i := 0; i < count; i++ {
		// Send client timestamp
		sendTime := time.Now()
		sendUs := sendTime.UnixMicro()
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(sendUs))
		conn.Write(buf)

		// Read response
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		resp := make([]byte, 64)
		n, err := conn.Read(resp)
		if err != nil {
			fmt.Printf("  probe %d: timeout\n", i+1)
			continue
		}
		recvTime := time.Now()

		if n < 16 {
			continue
		}

		clientSendUs := int64(binary.LittleEndian.Uint64(resp[0:8]))
		serverUs := int64(binary.LittleEndian.Uint64(resp[8:16]))

		rtt := recvTime.Sub(sendTime)
		halfRtt := rtt / 2

		// Server time at send + halfRtt should equal server timestamp
		// offset = server_time - (client_send + halfRtt)
		clientMidUs := clientSendUs + halfRtt.Microseconds()
		offsetUs := serverUs - clientMidUs
		offsetMs := float64(offsetUs) / 1000.0

		offsets = append(offsets, offsetMs)
		fmt.Printf("  probe %d: rtt=%.1fms offset=%.2fms\n", i+1, float64(rtt.Microseconds())/1000.0, offsetMs)

		time.Sleep(50 * time.Millisecond)
	}

	if len(offsets) == 0 {
		fmt.Println("no successful probes")
		return
	}

	minO, maxO, sum := offsets[0], offsets[0], 0.0
	for _, o := range offsets {
		sum += o
		minO = math.Min(minO, o)
		maxO = math.Max(maxO, o)
	}
	avg := sum / float64(len(offsets))
	fmt.Printf("\nClock offset: min=%.2fms avg=%.2fms max=%.2fms (positive = server ahead)\n", minO, avg, maxO)
}

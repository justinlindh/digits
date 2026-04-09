package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

//go:embed static/*
var staticFS embed.FS

type statusResponse struct {
	BootCount int    `json:"boot_count"`
	Hostname  string `json:"hostname"`
}

type recoveryServer struct {
	counterPath string
	recoveryDir string
	rootfsDev   string
	dataDev     string
	hostname    string
	rebootFunc  func() error
}

func (s *recoveryServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	count := s.readCounter()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statusResponse{
		BootCount: count,
		Hostname:  s.hostname,
	})
}

func (s *recoveryServer) handleTryAgain(w http.ResponseWriter, _ *http.Request) {
	if err := os.WriteFile(s.counterPath, []byte("0"), 0644); err != nil {
		log.Printf("recovery: failed to clear boot counter: %v", err)
		http.Error(w, "failed to clear boot counter", http.StatusInternalServerError)
		return
	}
	log.Println("recovery: try again requested, counter cleared")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Rebooting...")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if s.rebootFunc != nil {
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.rebootFunc()
		}()
	}
}

func (s *recoveryServer) handleFactoryReset(w http.ResponseWriter, _ *http.Request) {
	log.Println("recovery: factory reset requested")

	if err := os.WriteFile(s.counterPath, []byte("0"), 0644); err != nil {
		log.Printf("recovery: failed to clear boot counter: %v", err)
		http.Error(w, "failed to clear boot counter", http.StatusInternalServerError)
		return
	}

	rootfsImg := s.recoveryDir + "/rootfs.img.zst"
	log.Printf("recovery: restoring rootfs from %s to %s", rootfsImg, s.rootfsDev)
	if err := pipeCommands(
		exec.Command("zstd", "-d", "-c", rootfsImg),
		exec.Command("dd", "of="+s.rootfsDev, "bs=4M", "status=progress"),
	); err != nil {
		log.Printf("recovery: rootfs restore failed: %v", err)
		http.Error(w, "rootfs restore failed", http.StatusInternalServerError)
		return
	}

	log.Printf("recovery: formatting %s", s.dataDev)
	if err := exec.Command("mkfs.ext4", "-L", "data", "-F", s.dataDev).Run(); err != nil {
		log.Printf("recovery: data format failed: %v", err)
		http.Error(w, "data format failed", http.StatusInternalServerError)
		return
	}

	dataMnt := "/tmp/data-restore"
	if err := os.MkdirAll(dataMnt, 0755); err != nil {
		log.Printf("recovery: failed to create mount point: %v", err)
		http.Error(w, "failed to create mount point", http.StatusInternalServerError)
		return
	}
	if err := exec.Command("mount", s.dataDev, dataMnt).Run(); err != nil {
		log.Printf("recovery: data mount failed: %v", err)
		http.Error(w, "data mount failed", http.StatusInternalServerError)
		return
	}

	skelArchive := s.recoveryDir + "/data-skeleton.tar.zst"
	if err := pipeCommands(
		exec.Command("zstd", "-d", "-c", skelArchive),
		exec.Command("tar", "xf", "-", "-C", dataMnt),
	); err != nil {
		log.Printf("recovery: data skeleton restore failed: %v", err)
		exec.Command("umount", dataMnt).Run()
		http.Error(w, "data restore failed", http.StatusInternalServerError)
		return
	}
	exec.Command("umount", dataMnt).Run()

	log.Println("recovery: factory reset complete, rebooting")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Factory reset complete. Rebooting...")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	if s.rebootFunc != nil {
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.rebootFunc()
		}()
	}
}

func (s *recoveryServer) readCounter() int {
	data, err := os.ReadFile(s.counterPath)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

// pipeCommands connects two commands via a pipe (cmd1 stdout -> cmd2 stdin)
// and runs them, avoiding sh -c and shell injection risks.
func pipeCommands(cmd1, cmd2 *exec.Cmd) error {
	pipe, err := cmd1.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	cmd2.Stdin = pipe
	cmd2.Stdout = os.Stdout
	cmd2.Stderr = os.Stderr
	cmd1.Stderr = os.Stderr

	if err := cmd1.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd1.Path, err)
	}
	if err := cmd2.Start(); err != nil {
		cmd1.Process.Kill()
		return fmt.Errorf("start %s: %w", cmd2.Path, err)
	}
	if err := cmd1.Wait(); err != nil {
		cmd2.Process.Kill()
		return fmt.Errorf("%s: %w", cmd1.Path, err)
	}
	if err := cmd2.Wait(); err != nil {
		return fmt.Errorf("%s: %w", cmd2.Path, err)
	}
	return nil
}

// startAP brings up wlan0 in AP mode with hostapd and dnsmasq.
// Uses tools from the recovery partition's bin/ directory.
func startAP(recoveryDir string) error {
	// Bring up wlan0
	if err := exec.Command("ip", "link", "set", "wlan0", "up").Run(); err != nil {
		return fmt.Errorf("ip link set wlan0 up: %w", err)
	}
	// Flush and assign static IP
	exec.Command("ip", "addr", "flush", "dev", "wlan0").Run()
	if err := exec.Command("ip", "addr", "add", "192.168.4.1/24", "dev", "wlan0").Run(); err != nil {
		return fmt.Errorf("ip addr add: %w", err)
	}

	// Write hostapd config (minimal, open network)
	hostapdConf := "/tmp/recovery-hostapd.conf"
	os.WriteFile(hostapdConf, []byte(`interface=wlan0
driver=nl80211
ssid=Digits-Recovery
hw_mode=g
channel=6
auth_algs=1
wpa=0
country_code=US
ieee80211d=1
`), 0644)

	// Write dnsmasq config (DHCP + captive portal DNS)
	dnsmasqConf := "/tmp/recovery-dnsmasq.conf"
	os.WriteFile(dnsmasqConf, []byte(`interface=wlan0
bind-interfaces
dhcp-range=192.168.4.10,192.168.4.50,255.255.255.0,5m
address=/#/192.168.4.1
no-resolv
domain-needed
`), 0644)

	// Start hostapd
	hostapd := exec.Command("hostapd", "-B", hostapdConf)
	hostapd.Stdout = os.Stdout
	hostapd.Stderr = os.Stderr
	if err := hostapd.Run(); err != nil {
		return fmt.Errorf("hostapd: %w", err)
	}

	// Start dnsmasq
	dnsmasq := exec.Command("dnsmasq", "-C", dnsmasqConf)
	dnsmasq.Stdout = os.Stdout
	dnsmasq.Stderr = os.Stderr
	if err := dnsmasq.Run(); err != nil {
		return fmt.Errorf("dnsmasq: %w", err)
	}

	log.Println("recovery: AP mode started (SSID: Digits-Recovery)")
	return nil
}

func main() {
	hostname, _ := os.Hostname()

	recoveryDir := envOr("RECOVERY_DIR", "/mnt/recovery")

	// Start AP mode so users can connect
	if err := startAP(recoveryDir); err != nil {
		log.Printf("recovery: WARNING: AP setup failed: %v", err)
		log.Println("recovery: continuing anyway (HTTP server may not be reachable)")
	}

	srv := &recoveryServer{
		counterPath: envOr("BOOT_COUNTER_PATH", "/data/digits/boot-counter"),
		recoveryDir: recoveryDir,
		rootfsDev:   envOr("ROOTFS_DEV", "/dev/mmcblk0p2"),
		dataDev:     envOr("DATA_DEV", "/dev/mmcblk0p4"),
		hostname:    hostname,
		rebootFunc: func() error {
			return exec.Command("reboot").Run()
		},
	}

	staticSub, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", srv.handleStatus)
	mux.HandleFunc("POST /try-again", srv.handleTryAgain)
	mux.HandleFunc("POST /factory-reset", srv.handleFactoryReset)
	mux.Handle("GET /style.css", http.FileServer(http.FS(staticSub)))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		data, _ := fs.ReadFile(staticSub, "index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	log.Printf("digits-recovery: serving on :80 (hostname=%s)", hostname)
	if err := http.ListenAndServe(":80", mux); err != nil {
		log.Fatalf("http: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

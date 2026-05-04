package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/justinlindh/digits/pi/phonekit"
)

//go:embed static/*
var staticFS embed.FS

//go:embed recovery_audio/*
var recoveryAudioFS embed.FS

type statusResponse struct {
	BootCount int    `json:"boot_count"`
	Hostname  string `json:"hostname"`
}

type resetState struct {
	mu         sync.Mutex
	inProgress bool
	status     string
}

type resetStatusResponse struct {
	InProgress bool   `json:"in_progress"`
	Status     string `json:"status"`
}

type recoveryServer struct {
	counterPath string
	recoveryDir string
	rootfsDev   string
	dataDev     string
	hostname    string
	rebootFunc  func() error
	reset       resetState
}

func (s *recoveryServer) setResetStatus(status string) {
	s.reset.mu.Lock()
	s.reset.inProgress = true
	s.reset.status = status
	s.reset.mu.Unlock()
}

func (s *recoveryServer) handleFactoryResetStatus(w http.ResponseWriter, _ *http.Request) {
	s.reset.mu.Lock()
	resp := resetStatusResponse{
		InProgress: s.reset.inProgress,
		Status:     s.reset.status,
	}
	s.reset.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
	// Also clear the persistent recovery-mode flag on the data partition
	os.Remove(filepath.Join(filepath.Dir(s.counterPath), "recovery-mode"))
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

// doFactoryReset runs the wipe + restore sequence and returns nil on success.
// Caller is responsible for the inProgress lock and for triggering the reboot.
func (s *recoveryServer) doFactoryReset() error {
	// Non-fatal: data partition may be corrupt (that's why we're here).
	// mkfs.ext4 below will wipe it anyway.
	if err := os.WriteFile(s.counterPath, []byte("0"), 0644); err != nil {
		log.Printf("recovery: failed to clear boot counter (non-fatal): %v", err)
	}

	bin := filepath.Join(s.recoveryDir, "bin")
	rootfsImg := filepath.Join(s.recoveryDir, "rootfs.img.zst")
	log.Printf("recovery: restoring rootfs from %s to %s", rootfsImg, s.rootfsDev)
	if err := pipeCommands(
		exec.Command(filepath.Join(bin, "zstd"), "-d", "-c", rootfsImg),
		exec.Command(filepath.Join(bin, "dd"), "of="+s.rootfsDev, "bs=4M", "conv=fsync"),
	); err != nil {
		return fmt.Errorf("rootfs restore: %w", err)
	}
	log.Println("recovery: rootfs restore complete, syncing")
	syscall.Sync()

	s.setResetStatus("Formatting data partition...")

	// Close log file and unmount data partition before formatting.
	// The log file holds /data busy, preventing unmount.
	closeDataLog()
	if out, err := exec.Command(filepath.Join(bin, "umount"), "/data").CombinedOutput(); err != nil {
		log.Printf("recovery: umount /data failed (trying lazy): %v: %s", err, string(out))
		exec.Command(filepath.Join(bin, "umount"), "-l", "/data").Run()
	}

	log.Printf("recovery: formatting %s", s.dataDev)
	if err := exec.Command(filepath.Join(bin, "mkfs.ext4"), "-L", "data", "-F", s.dataDev).Run(); err != nil {
		return fmt.Errorf("data format: %w", err)
	}

	dataMnt := "/tmp/data-restore"
	if err := os.MkdirAll(dataMnt, 0755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}
	if err := exec.Command(filepath.Join(bin, "mount"), s.dataDev, dataMnt).Run(); err != nil {
		return fmt.Errorf("data mount: %w", err)
	}

	s.setResetStatus("Restoring data...")

	skelArchive := filepath.Join(s.recoveryDir, "data-skeleton.tar.zst")
	if err := pipeCommands(
		exec.Command(filepath.Join(bin, "zstd"), "-d", "-c", skelArchive),
		exec.Command(filepath.Join(bin, "tar"), "xf", "-", "-C", dataMnt),
	); err != nil {
		exec.Command(filepath.Join(bin, "umount"), dataMnt).Run()
		return fmt.Errorf("data skeleton restore: %w", err)
	}
	exec.Command(filepath.Join(bin, "umount"), dataMnt).Run()

	s.setResetStatus("Rebooting...")
	log.Println("recovery: factory reset complete")
	return nil
}

func (s *recoveryServer) handleFactoryReset(w http.ResponseWriter, _ *http.Request) {
	s.reset.mu.Lock()
	if s.reset.inProgress {
		s.reset.mu.Unlock()
		http.Error(w, "factory reset already in progress", http.StatusConflict)
		return
	}
	s.reset.inProgress = true
	s.reset.status = "Restoring rootfs..."
	s.reset.mu.Unlock()

	log.Println("recovery: factory reset requested via web UI")

	if err := s.doFactoryReset(); err != nil {
		log.Printf("recovery: factory reset failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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
// Uses tools from the recovery partition's bin/ directory to avoid
// depending on rootfs, which may be overwritten during factory reset.
func startAP(recoveryDir string) error {
	bin := filepath.Join(recoveryDir, "bin")

	// Bring up wlan0
	if err := exec.Command(filepath.Join(bin, "ip"), "link", "set", "wlan0", "up").Run(); err != nil {
		return fmt.Errorf("ip link set wlan0 up: %w", err)
	}
	// Flush and assign static IP
	exec.Command(filepath.Join(bin, "ip"), "addr", "flush", "dev", "wlan0").Run()
	if err := exec.Command(filepath.Join(bin, "ip"), "addr", "add", "192.168.4.1/24", "dev", "wlan0").Run(); err != nil {
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
user=root
pid-file=/tmp/dnsmasq.pid
dhcp-range=192.168.4.10,192.168.4.50,255.255.255.0,5m
address=/#/192.168.4.1
no-resolv
domain-needed
dhcp-leasefile=/tmp/dnsmasq-recovery.leases
`), 0644)

	// Start hostapd (forks to background with -B)
	hostapd := exec.Command(filepath.Join(bin, "hostapd"), "-B", hostapdConf)
	hostapd.Stdout = os.Stdout
	hostapd.Stderr = os.Stderr
	if err := hostapd.Run(); err != nil {
		return fmt.Errorf("hostapd: %w", err)
	}

	// Start dnsmasq (daemonizes by default)
	dnsmasq := exec.Command(filepath.Join(bin, "dnsmasq"), "-C", dnsmasqConf)
	dnsmasq.Stdout = os.Stdout
	dnsmasq.Stderr = os.Stderr
	if err := dnsmasq.Run(); err != nil {
		// Capture error details on retry with combined output
		retryOut, retryErr := exec.Command(filepath.Join(bin, "dnsmasq"), "--no-daemon", "--test", "-C", dnsmasqConf).CombinedOutput()
		return fmt.Errorf("dnsmasq: %w (test: %v: %s)", err, retryErr, string(retryOut))
	}

	log.Println("recovery: AP mode started (SSID: Digits-Recovery)")
	return nil
}

func isInitMode() bool {
	return os.Getpid() == 1
}

// waitForInterface waits for a network interface to appear.
func waitForInterface(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := net.InterfaceByName(name); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for %s", name)
}

// unblockWifi unblocks all WiFi rfkill devices via sysfs, avoiding the need
// for the rfkill binary on the recovery partition.
func unblockWifi() {
	entries, err := os.ReadDir("/sys/class/rfkill")
	if err != nil {
		return
	}
	for _, entry := range entries {
		typePath := filepath.Join("/sys/class/rfkill", entry.Name(), "type")
		data, err := os.ReadFile(typePath)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "wlan" {
			softPath := filepath.Join("/sys/class/rfkill", entry.Name(), "soft")
			os.WriteFile(softPath, []byte("0"), 0644)
		}
	}
}

func loadAudio(name string) []byte {
	data, err := recoveryAudioFS.ReadFile("recovery_audio/" + name)
	if err != nil {
		return nil
	}
	return data
}

func runVoiceMenu(phone *phonekit.Phone, srv *recoveryServer) {
	ctx := context.Background()
	for {
		log.Println("recovery: voice menu waiting for handset pickup")
		if err := phone.WaitForHook(ctx, "OFF"); err != nil {
			log.Printf("recovery: hook wait error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		menuLoop(ctx, phone, srv)
	}
}

func menuLoop(ctx context.Context, phone *phonekit.Phone, srv *recoveryServer) {
	for {
		if clip := loadAudio("recovery_menu.wav"); clip != nil {
			if err := phone.Play(ctx, clip); err != nil {
				log.Printf("recovery: play menu failed: %v", err)
			}
		}

		keyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ev, err := phone.WaitForEvent(keyCtx, func(e phonekit.Event) bool {
			return e.Type == "KEY" || (e.Type == "HOOK" && e.Value == "ON")
		})
		cancel()

		if err != nil {
			continue
		}

		if ev.Type == "HOOK" && ev.Value == "ON" {
			return
		}

		switch ev.Value {
		case "1":
			handleVoiceTryAgain(ctx, phone, srv)
			return
		case "2":
			if handleVoiceFactoryReset(ctx, phone, srv) {
				return
			}
		}
	}
}

func handleVoiceTryAgain(ctx context.Context, phone *phonekit.Phone, srv *recoveryServer) {
	if clip := loadAudio("restarting.wav"); clip != nil {
		phone.Play(ctx, clip)
	}
	os.WriteFile(srv.counterPath, []byte("0"), 0644)
	os.Remove(filepath.Join(filepath.Dir(srv.counterPath), "recovery-mode"))
	time.Sleep(500 * time.Millisecond)
	if srv.rebootFunc != nil {
		srv.rebootFunc()
	}
}

func handleVoiceFactoryReset(ctx context.Context, phone *phonekit.Phone, srv *recoveryServer) bool {
	if clip := loadAudio("confirm_factory_reset.wav"); clip != nil {
		phone.Play(ctx, clip)
	}

	confirmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ev, err := phone.WaitForEvent(confirmCtx, func(e phonekit.Event) bool {
		return e.Type == "KEY" || (e.Type == "HOOK" && e.Value == "ON")
	})
	cancel()

	if err != nil || ev.Type == "HOOK" || ev.Value != "2" {
		if clip := loadAudio("factory_reset_cancelled.wav"); clip != nil {
			phone.Play(ctx, clip)
		}
		return ev.Type == "HOOK" && ev.Value == "ON"
	}

	if clip := loadAudio("factory_reset_in_progress.wav"); clip != nil {
		phone.Play(ctx, clip)
	}

	srv.reset.mu.Lock()
	srv.reset.inProgress = true
	srv.reset.status = "Restoring rootfs..."
	srv.reset.mu.Unlock()

	if err := doFactoryResetWithVoice(ctx, phone, srv); err != nil {
		log.Printf("recovery: voice factory reset failed: %v", err)
		return true
	}

	time.Sleep(500 * time.Millisecond)
	if srv.rebootFunc != nil {
		srv.rebootFunc()
	}
	return true
}

func doFactoryResetWithVoice(ctx context.Context, phone *phonekit.Phone, srv *recoveryServer) error {
	if clip := loadAudio("restoring_system.wav"); clip != nil {
		phone.Play(ctx, clip)
	}

	if err := os.WriteFile(srv.counterPath, []byte("0"), 0644); err != nil {
		log.Printf("recovery: failed to clear boot counter (non-fatal): %v", err)
	}

	bin := filepath.Join(srv.recoveryDir, "bin")
	rootfsImg := filepath.Join(srv.recoveryDir, "rootfs.img.zst")
	log.Printf("recovery: restoring rootfs from %s to %s", rootfsImg, srv.rootfsDev)
	if err := pipeCommands(
		exec.Command(filepath.Join(bin, "zstd"), "-d", "-c", rootfsImg),
		exec.Command(filepath.Join(bin, "dd"), "of="+srv.rootfsDev, "bs=4M", "conv=fsync"),
	); err != nil {
		return fmt.Errorf("rootfs restore: %w", err)
	}
	log.Println("recovery: rootfs restore complete, syncing")
	syscall.Sync()

	if clip := loadAudio("formatting_data.wav"); clip != nil {
		phone.Play(ctx, clip)
	}

	srv.setResetStatus("Formatting data partition...")
	closeDataLog()
	if out, err := exec.Command(filepath.Join(bin, "umount"), "/data").CombinedOutput(); err != nil {
		log.Printf("recovery: umount /data failed (trying lazy): %v: %s", err, string(out))
		exec.Command(filepath.Join(bin, "umount"), "-l", "/data").Run()
	}

	log.Printf("recovery: formatting %s", srv.dataDev)
	if err := exec.Command(filepath.Join(bin, "mkfs.ext4"), "-L", "data", "-F", srv.dataDev).Run(); err != nil {
		return fmt.Errorf("data format: %w", err)
	}

	dataMnt := "/tmp/data-restore"
	if err := os.MkdirAll(dataMnt, 0755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}
	if err := exec.Command(filepath.Join(bin, "mount"), srv.dataDev, dataMnt).Run(); err != nil {
		return fmt.Errorf("data mount: %w", err)
	}

	srv.setResetStatus("Restoring data...")
	skelArchive := filepath.Join(srv.recoveryDir, "data-skeleton.tar.zst")
	if err := pipeCommands(
		exec.Command(filepath.Join(bin, "zstd"), "-d", "-c", skelArchive),
		exec.Command(filepath.Join(bin, "tar"), "xf", "-", "-C", dataMnt),
	); err != nil {
		exec.Command(filepath.Join(bin, "umount"), dataMnt).Run()
		return fmt.Errorf("data skeleton restore: %w", err)
	}
	exec.Command(filepath.Join(bin, "umount"), dataMnt).Run()

	if clip := loadAudio("reset_complete.wav"); clip != nil {
		phone.Play(ctx, clip)
	}

	srv.setResetStatus("Rebooting...")
	log.Println("recovery: factory reset complete")
	return nil
}

func main() {
	initMode := isInitMode()

	if initMode {
		log.Println("digits-recovery: running as init (PID 1)")
		initSetup()
	}

	hostname, _ := os.Hostname()

	// When running as init from the recovery partition, the recovery files
	// (rootfs.img.zst, bin/, etc.) are at the filesystem root.
	recoveryDir := envOr("RECOVERY_DIR", "/mnt/recovery")
	if initMode && os.Getenv("RECOVERY_DIR") == "" {
		recoveryDir = "/"
	}

	if initMode {
		// Wait for WiFi hardware (kernel module + firmware load)
		log.Println("recovery: waiting for wlan0...")
		if err := waitForInterface("wlan0", 15*time.Second); err != nil {
			log.Printf("recovery: WARNING: %v", err)
		}

		// Unblock WiFi radio (rfkill may have soft-blocked it)
		unblockWifi()
	}

	// Start AP mode so users can connect
	if err := startAP(recoveryDir); err != nil {
		log.Printf("recovery: WARNING: AP setup failed: %v", err)
		log.Println("recovery: continuing anyway (HTTP server may not be reachable)")
	}

	var phone *phonekit.Phone
	if initMode {
		p, err := phonekit.Open("/dev/serial0", 115200)
		if err != nil {
			log.Printf("recovery: phonekit open failed: %v (voice menu disabled)", err)
		} else {
			var pingOK bool
			for attempt := 1; attempt <= 10; attempt++ {
				time.Sleep(500 * time.Millisecond)
				if err := p.Ping(); err == nil {
					pingOK = true
					break
				}
				log.Printf("recovery: pico ping attempt %d/10 failed", attempt)
			}
			if pingOK {
				phone = p
				phone.LED("HEARTBEAT")
				log.Println("recovery: phonekit connected, LED set to HEARTBEAT")
			} else {
				log.Println("recovery: pico ping failed after 10 attempts, continuing without voice menu")
				phone = p
			}
		}
	}

	rebootFn := func() error {
		return exec.Command("systemctl", "reboot").Run()
	}
	if initMode {
		rebootFn = func() error {
			return rebootDirect()
		}
	}

	srv := &recoveryServer{
		counterPath: envOr("BOOT_COUNTER_PATH", "/data/digits/boot-counter"),
		recoveryDir: recoveryDir,
		rootfsDev:   envOr("ROOTFS_DEV", "/dev/mmcblk0p2"),
		dataDev:     envOr("DATA_DEV", "/dev/mmcblk0p4"),
		hostname:    hostname,
		rebootFunc:  rebootFn,
	}

	// autoFactoryResetFlag duplicates pi/digitsd/internal/bootcount.AutoFactoryResetFlag
	// (separate Go modules; Go's internal-package rule prevents direct import).
	// Both must agree on the path. When digitsd's confirmed *#00000# path
	// writes this sentinel before reboot, recovery skips its Try Again /
	// Factory Reset menu and runs the wipe directly. The wipe runs in a
	// goroutine so the HTTP server can start immediately and serve the
	// existing in-progress UI (status polling already supports it). Doing
	// the wipe synchronously here would block ListenAndServe and leave the
	// AP up with nothing on :80, indistinguishable from a wedged recovery.
	autoFactoryResetFlag := envOr("AUTO_FACTORY_RESET_FLAG", "/data/digits/auto-factory-reset")
	if _, err := os.Stat(autoFactoryResetFlag); err == nil {
		log.Printf("recovery: %s present, auto-running factory reset (skipping menu)", autoFactoryResetFlag)
		srv.reset.mu.Lock()
		srv.reset.inProgress = true
		srv.reset.status = "Auto-confirmed factory reset, restoring rootfs..."
		srv.reset.mu.Unlock()
		go func() {
			if phone != nil {
				if clip := loadAudio("factory_reset_in_progress.wav"); clip != nil {
					phone.Play(context.Background(), clip)
				}
			}
			var resetErr error
			if phone != nil {
				resetErr = doFactoryResetWithVoice(context.Background(), phone, srv)
			} else {
				resetErr = srv.doFactoryReset()
			}
			if resetErr != nil {
				log.Printf("recovery: auto factory reset failed: %v", resetErr)
				srv.reset.mu.Lock()
				srv.reset.inProgress = false
				srv.reset.status = ""
				srv.reset.mu.Unlock()
				return
			}
			log.Println("recovery: auto factory reset complete, rebooting")
			// Brief settle so the final "Rebooting..." status reaches any
			// poller still on the recovery web UI before the kernel goes down.
			time.Sleep(500 * time.Millisecond)
			if rebootFn == nil {
				return
			}
			if err := rebootFn(); err != nil {
				// rebootDirect doesn't return on success; systemctl reboot
				// usually doesn't either. A returning error means the reboot
				// path itself failed and the device is wedged. Surface it on
				// the status endpoint so the user has SOMETHING to read.
				log.Printf("recovery: reboot failed after auto-reset: %v", err)
				srv.reset.mu.Lock()
				srv.reset.status = fmt.Sprintf("Reboot failed: %v", err)
				srv.reset.mu.Unlock()
			}
		}()
	}

	if phone != nil {
		go runVoiceMenu(phone, srv)
	}

	staticSub, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", srv.handleStatus)
	mux.HandleFunc("GET /factory-reset/status", srv.handleFactoryResetStatus)
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

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
	if s.rebootFunc != nil {
		s.rebootFunc()
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
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("zstd -d -c %s | dd of=%s bs=4M status=progress", rootfsImg, s.rootfsDev))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
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
	cmd = exec.Command("sh", "-c",
		fmt.Sprintf("zstd -d -c %s | tar xf - -C %s", skelArchive, dataMnt))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("recovery: data skeleton restore failed: %v", err)
		exec.Command("umount", dataMnt).Run()
		http.Error(w, "data restore failed", http.StatusInternalServerError)
		return
	}
	exec.Command("umount", dataMnt).Run()

	log.Println("recovery: factory reset complete, rebooting")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Factory reset complete. Rebooting...")

	if s.rebootFunc != nil {
		s.rebootFunc()
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

func main() {
	hostname, _ := os.Hostname()

	srv := &recoveryServer{
		counterPath: envOr("BOOT_COUNTER_PATH", "/boot/firmware/boot-counter"),
		recoveryDir: envOr("RECOVERY_DIR", "/mnt/recovery"),
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

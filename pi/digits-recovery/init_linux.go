//go:build linux

package main

import (
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func initSetup() {
	// /dev, /proc, /sys, /run are already mounted (moved from initramfs by
	// move_virtual_filesystems before switch_root).

	// Mount tmpfs on /tmp for hostapd/dnsmasq config files.
	if err := os.MkdirAll("/tmp", 0755); err != nil {
		log.Printf("init: mkdir /tmp: %v", err)
	}
	if err := syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "size=64M"); err != nil {
		log.Printf("init: mount /tmp: %v", err)
	}

	// Mount data partition for boot counter access.
	// May fail if the data partition is corrupt -- that's OK, factory reset
	// will reformat it.
	if err := os.MkdirAll("/data", 0755); err != nil {
		log.Printf("init: mkdir /data: %v", err)
	}
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		log.Printf("init: mount /data (non-fatal): %v", err)
	}

	// Tee log output to /data/digits/recovery.log for post-mortem debugging.
	if f, err := os.OpenFile("/data/digits/recovery.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
		recoveryLogFile = f
		log.SetOutput(io.MultiWriter(os.Stderr, f))
		log.Println("init: log file opened at /data/digits/recovery.log")
	}

	// Set LD_LIBRARY_PATH so dynamically linked recovery tools (hostapd,
	// dnsmasq, zstd, dd, etc.) can find their shared libraries on the
	// recovery partition.
	os.Setenv("LD_LIBRARY_PATH", "/lib")

	// Dump diagnostic info for WiFi debugging
	if data, err := os.ReadFile("/proc/modules"); err == nil {
		log.Printf("init: loaded modules:\n%s", string(data))
	}
	if entries, err := os.ReadDir("/sys/bus/sdio/devices"); err == nil {
		for _, e := range entries {
			log.Printf("init: sdio device: %s", e.Name())
		}
	} else {
		log.Printf("init: /sys/bus/sdio/devices: %v", err)
	}
	if entries, err := os.ReadDir("/sys/bus/sdio/drivers"); err == nil {
		for _, e := range entries {
			log.Printf("init: sdio driver: %s", e.Name())
		}
	} else {
		log.Printf("init: /sys/bus/sdio/drivers: %v", err)
	}

	// Load WiFi kernel modules via modprobe (busybox). This handles the
	// dependency chain: rfkill -> cfg80211 -> brcmutil -> brcmfmac.
	// When brcmfmac probes the SDIO device, it calls request_module("brcmfmac-wcc")
	// which the kernel routes to /sbin/modprobe, also handled by busybox.
	log.Println("init: loading brcmfmac via modprobe")
	if out, err := exec.Command("/sbin/modprobe", "brcmfmac").CombinedOutput(); err != nil {
		log.Printf("init: modprobe brcmfmac: %v: %s", err, string(out))
	} else {
		log.Println("init: modprobe brcmfmac: ok")
	}

	// Give firmware load time to complete (async SDIO probe + firmware upload)
	time.Sleep(3 * time.Second)

	// Post-module-load diagnostics
	if entries, err := os.ReadDir("/sys/class/net"); err == nil {
		var nets []string
		for _, e := range entries {
			nets = append(nets, e.Name())
		}
		log.Printf("init: network interfaces after module load: %v", nets)
	}
	if entries, err := os.ReadDir("/sys/bus/sdio/drivers"); err == nil {
		for _, e := range entries {
			log.Printf("init: sdio driver (post-load): %s", e.Name())
			// Check if driver has bound devices
			subs, _ := os.ReadDir("/sys/bus/sdio/drivers/" + e.Name())
			for _, s := range subs {
				if s.Name() != "bind" && s.Name() != "unbind" && s.Name() != "module" && s.Name() != "uevent" {
					log.Printf("init:   bound device: %s", s.Name())
				}
			}
		}
	}
	// Dump kernel log for firmware load messages.
	// /dev/kmsg is a streaming interface, read /proc/kmsg would block.
	// Use /sys/fs/pstore or just open /dev/kmsg with O_NONBLOCK.
	if f, err := syscall.Open("/dev/kmsg", syscall.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		buf := make([]byte, 65536)
		var dmesg []byte
		for {
			n, readErr := syscall.Read(f, buf)
			if n <= 0 || readErr != nil {
				break
			}
			dmesg = append(dmesg, buf[:n]...)
		}
		syscall.Close(f)
		// Extract last 50 lines
		lines := strings.Split(string(dmesg), "\n")
		start := len(lines) - 50
		if start < 0 {
			start = 0
		}
		log.Printf("init: dmesg (last 50 lines):\n%s", strings.Join(lines[start:], "\n"))
	} else {
		log.Printf("init: open /dev/kmsg: %v (errno %d)", err, err)
	}
	// Flush log
	syncLog()

	// PID 1 must reap zombie children. Use WNOHANG so we don't steal
	// waits from exec.Command -- the reaper only picks up orphans.
	go reapChildren()
}

// recoveryLogFile holds the open log file on /data so it can be closed
// before factory reset unmounts the data partition.
var recoveryLogFile *os.File

// closeDataLog closes the recovery log file and resets log output to stderr
// so /data can be unmounted for factory reset.
func closeDataLog() {
	if recoveryLogFile != nil {
		log.SetOutput(os.Stderr)
		recoveryLogFile.Close()
		recoveryLogFile = nil
	}
}

func reapChildren() {
	var ws syscall.WaitStatus
	for {
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

// syncLog flushes the log file to disk.
func syncLog() {
	syscall.Sync()
}

func rebootDirect() error {
	syscall.Sync()
	return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
}

//go:build linux

package main

import (
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
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
		log.Println("init: log file opened")
	}

	// Set LD_LIBRARY_PATH so dynamically linked recovery tools (hostapd,
	// dnsmasq, zstd, dd, etc.) can find their shared libraries.
	os.Setenv("LD_LIBRARY_PATH", "/lib")

	// Load WiFi kernel modules via modprobe (busybox). Handles the
	// dependency chain: rfkill -> cfg80211 -> brcmutil -> brcmfmac.
	// brcmfmac calls request_module("brcmfmac-wcc") during probe,
	// which the kernel routes to /sbin/modprobe (busybox).
	log.Println("init: loading brcmfmac via modprobe")
	if out, err := exec.Command("/sbin/modprobe", "brcmfmac").CombinedOutput(); err != nil {
		log.Printf("init: modprobe brcmfmac: %v: %s", err, string(out))
	}

	// PID 1 must reap zombie children. Wait for SIGCHLD rather than
	// polling to avoid unnecessary wakeups.
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
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, syscall.SIGCHLD)
	var ws syscall.WaitStatus
	for range sigchld {
		for {
			pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
		}
	}
}

func rebootDirect() error {
	syscall.Sync()
	return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
}

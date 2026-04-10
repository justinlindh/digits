//go:build linux

package main

import (
	"log"
	"os"
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

	// Set LD_LIBRARY_PATH so dynamically linked recovery tools (hostapd,
	// dnsmasq, zstd, dd, etc.) can find their shared libraries on the
	// recovery partition.
	os.Setenv("LD_LIBRARY_PATH", "/lib")

	// PID 1 must reap zombie children.
	go reapChildren()
}

func reapChildren() {
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(-1, &ws, 0, nil)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func rebootDirect() error {
	syscall.Sync()
	return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
}

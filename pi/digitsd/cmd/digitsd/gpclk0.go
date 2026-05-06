package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	clkBase  = 0x3F101000
	gpioBase = 0x3F200000
	passwd   = 0x5A000000

	ctlOff = 0x70 // CM_GP0CTL
	divOff = 0x74 // CM_GP0DIV

	gpfsel0Off = 0x00
)

// enableGPCLK0 configures BCM2835 GPCLK0 on GPIO4 at 12.288 MHz for the
// TLV320AIC3104 MCLK. Must run as root (/dev/mem access).
func enableGPCLK0() error {
	fd, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return fmt.Errorf("open /dev/mem: %w", err)
	}
	defer fd.Close()

	clkMem, err := syscall.Mmap(int(fd.Fd()), clkBase, 0x1000,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap clk: %w", err)
	}
	defer syscall.Munmap(clkMem)

	gpioMem, err := syscall.Mmap(int(fd.Fd()), gpioBase, 0x1000,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap gpio: %w", err)
	}
	defer syscall.Munmap(gpioMem)

	clk := (*[256]uint32)(unsafe.Pointer(&clkMem[0]))
	gpio := (*[256]uint32)(unsafe.Pointer(&gpioMem[0]))

	ctlIdx := ctlOff / 4
	divIdx := divOff / 4
	fselIdx := gpfsel0Off / 4

	// 1. Disable GPCLK0
	atomic.StoreUint32(&clk[ctlIdx], passwd|0)
	time.Sleep(time.Millisecond)

	// Wait for BUSY clear (bit 7)
	for i := 0; i < 100; i++ {
		if atomic.LoadUint32(&clk[ctlIdx])&(1<<7) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// 2. Program divider: 19.2 MHz / 12.288 MHz = 1.5625 -> DIVI=1, DIVF=2304
	atomic.StoreUint32(&clk[divIdx], passwd|(1<<12)|2304)

	// 3. Set source = OSC (1), MASH = 1
	atomic.StoreUint32(&clk[ctlIdx], passwd|(1<<9)|1)
	time.Sleep(time.Millisecond)

	// 4. Enable
	atomic.StoreUint32(&clk[ctlIdx], passwd|(1<<9)|1|(1<<4))
	time.Sleep(time.Millisecond)

	// 5. Set GPIO4 to ALT0 (fn=4, bits 14:12 of GPFSEL0)
	const shift = 12 // GPIO4: bits 14:12
	v := atomic.LoadUint32(&gpio[fselIdx])
	v &^= 0b111 << shift
	v |= 4 << shift // ALT0
	atomic.StoreUint32(&gpio[fselIdx], v)

	// Verify
	ctl := atomic.LoadUint32(&clk[ctlIdx])
	div := atomic.LoadUint32(&clk[divIdx])
	fsel := atomic.LoadUint32(&gpio[fselIdx])
	gpio4fn := (fsel >> shift) & 0b111
	divi := (div >> 12) & 0xFFF
	divf := div & 0xFFF
	freq := 19200000.0 * 4096.0 / float64(divi*4096+divf)

	fmt.Printf("gpclk0: configured %.4f MHz (DIVI=%d DIVF=%d ENAB=%d GPIO4=ALT%d)\n",
		freq/1e6, divi, divf, (ctl>>4)&1, gpio4fn-4)

	return nil
}

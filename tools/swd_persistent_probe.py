"""
Persistent SWD probe in Python via /dev/mem GPIO bitbang.

Bypasses OpenOCD's MAX_DAP_WAIT=3 limit by retrying DPIDR reads indefinitely
until either the chip ACKs (returning DPIDR) or our patience runs out.

V2 wiring: Pi GPIO 25 = SWCLK, Pi GPIO 24 = SWDIO.
"""
import mmap, os, struct, time, sys

GPIO_BASE = 0x3F200000
fd = os.open("/dev/mem", os.O_RDWR | os.O_SYNC)
mm = mmap.mmap(fd, 0x1000, offset=GPIO_BASE)

SWCLK = 25
SWDIO = 24

def fsel(pin, fn):
    reg_idx = pin // 10
    shift = (pin % 10) * 3
    off = reg_idx * 4
    v = struct.unpack("<I", mm[off:off+4])[0]
    v &= ~(0b111 << shift)
    v |= (fn & 0b111) << shift
    mm[off:off+4] = struct.pack("<I", v)

def gpio_set(pin, val):
    if val:
        mm[0x1c:0x20] = struct.pack("<I", 1 << pin)
    else:
        mm[0x28:0x2c] = struct.pack("<I", 1 << pin)

def gpio_read(pin):
    return (struct.unpack("<I", mm[0x34:0x38])[0] >> pin) & 1

def swdio_out():
    fsel(SWDIO, 1)

def swdio_in():
    fsel(SWDIO, 0)

def clk_pulse_write(bit):
    gpio_set(SWDIO, bit)
    gpio_set(SWCLK, 0)
    gpio_set(SWCLK, 1)

def line_reset():
    swdio_out()
    for _ in range(60):
        clk_pulse_write(1)

def jtag_to_swd():
    swdio_out()
    seq = 0x79E7
    for i in range(16):
        clk_pulse_write((seq >> i) & 1)

def turn_around(n=1):
    swdio_in()
    for _ in range(n):
        gpio_set(SWCLK, 0)
        gpio_set(SWCLK, 1)

def send_request(req):
    swdio_out()
    for i in range(8):
        clk_pulse_write((req >> i) & 1)

DPIDR_REQ = 0xA5

def read_ack():
    swdio_in()
    a = 0
    for i in range(3):
        gpio_set(SWCLK, 0)
        b = gpio_read(SWDIO)
        gpio_set(SWCLK, 1)
        a |= (b << i)
    return a

def read_data32():
    swdio_in()
    data = 0
    p = 0
    for i in range(32):
        gpio_set(SWCLK, 0)
        b = gpio_read(SWDIO)
        gpio_set(SWCLK, 1)
        data |= (b << i)
        p ^= b
    gpio_set(SWCLK, 0)
    pb = gpio_read(SWDIO)
    gpio_set(SWCLK, 1)
    return data, (p == pb)

fsel(SWCLK, 1)
gpio_set(SWCLK, 1)
swdio_out()
gpio_set(SWDIO, 1)

line_reset()
jtag_to_swd()
line_reset()

print("Sending DPIDR read request, retrying WAIT indefinitely (30 second budget)...")
deadline = time.time() + 30
attempts = 0
last_summary_t = time.time()
counts = {0: 0, 1: 0, 2: 0, 4: 0, 5: 0, 7: 0}

while time.time() < deadline:
    attempts += 1
    swdio_out()
    send_request(DPIDR_REQ)
    turn_around(1)
    ack = read_ack()
    counts[ack] = counts.get(ack, 0) + 1
    if ack == 1:
        data, p_ok = read_data32()
        turn_around(1)
        swdio_out()
        gpio_set(SWDIO, 0)
        for _ in range(8):
            gpio_set(SWCLK, 0)
            gpio_set(SWCLK, 1)
        print(f"\n*** SUCCESS at attempt {attempts}: DPIDR = 0x{data:08x}, parity_ok={p_ok}")
        if data == 0x0bc12477:
            print("    matches RP2040 official DPIDR signature")
        else:
            print(f"    unexpected DPIDR (RP2040 expects 0x0bc12477)")
        break
    elif ack == 2:
        swdio_out()
        gpio_set(SWDIO, 0)
        gpio_set(SWCLK, 0)
        gpio_set(SWCLK, 1)
        if time.time() - last_summary_t > 2:
            print(f"  attempts={attempts}, OK={counts[1]} WAIT={counts[2]} FAULT={counts[4]} other={counts[0]+counts[5]+counts[7]}")
            last_summary_t = time.time()
        continue
    elif ack == 4:
        line_reset()
        continue
    else:
        if attempts % 200 == 0:
            print(f"  attempts={attempts}, weird ack={ack}, OK={counts[1]} WAIT={counts[2]} FAULT={counts[4]} other={counts[0]+counts[5]+counts[7]}")
        line_reset()
        continue

print(f"\nFinal: attempts={attempts}, OK={counts[1]} WAIT={counts[2]} FAULT={counts[4]} other={counts[0]+counts[5]+counts[7]}")

fsel(SWCLK, 0)
fsel(SWDIO, 0)
mm.close()
os.close(fd)

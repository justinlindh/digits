package phonekit

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type serialPort struct {
	file *os.File
}

func (s *serialPort) Read(p []byte) (int, error)  { return s.file.Read(p) }
func (s *serialPort) Write(p []byte) (int, error) { return s.file.Write(p) }
func (s *serialPort) Close() error                { return s.file.Close() }

// openSerialPort opens the given device at the specified baud rate with 8N1,
// raw mode, and a 200ms read timeout (VTIME=2).
func openSerialPort(device string, baud int) (io.ReadWriteCloser, error) {
	f, err := os.OpenFile(device, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", device, err)
	}

	fd := int(f.Fd())
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("tcgets: %w", err)
	}

	speed, ok := baudToSpeed(baud)
	if !ok {
		_ = f.Close()
		return nil, fmt.Errorf("unsupported baud rate: %d", baud)
	}

	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8 | unix.CLOCAL | unix.CREAD

	t.Ispeed = speed
	t.Ospeed = speed

	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 2

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("tcsets: %w", err)
	}

	return &serialPort{file: f}, nil
}

func baudToSpeed(baud int) (uint32, bool) {
	switch baud {
	case 9600:
		return unix.B9600, true
	case 115200:
		return unix.B115200, true
	default:
		return 0, false
	}
}

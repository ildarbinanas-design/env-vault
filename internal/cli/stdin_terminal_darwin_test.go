//go:build darwin

package cli

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPseudoTerminal returns both sides of a new pseudo-terminal without
// changing the test process's controlling terminal.
func openPseudoTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	fd := int(controller.Fd())
	for _, request := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if err := unix.IoctlSetInt(fd, request, 0); err != nil {
			t.Fatalf("prepare the pseudo-terminal: %v", err)
		}
	}
	// TIOCPTYGNAME writes a NUL-terminated device path into a 128-byte buffer.
	var name [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, controller.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		t.Fatalf("read the pseudo-terminal name: %v", errno)
	}
	end := bytes.IndexByte(name[:], 0)
	if end <= 0 {
		t.Fatal("invalid pseudo-terminal name")
	}
	terminal, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open the pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return controller, terminal
}

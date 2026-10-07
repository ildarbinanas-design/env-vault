//go:build darwin

package e2e_test

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const e2ePromptReadTermios = unix.TIOCGETA

func openPromptTerminal(sc *scenario) (*os.File, *os.File) {
	sc.t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		sc.t.Fatal("native hidden-input E2E requires a pseudo-terminal")
	}
	sc.t.Cleanup(func() { _ = controller.Close() })
	for _, request := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if err := unix.IoctlSetInt(int(controller.Fd()), request, 0); err != nil {
			sc.t.Fatal("unable to prepare the pseudo-terminal")
		}
	}
	var name [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, controller.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	end := bytes.IndexByte(name[:], 0)
	if errno != 0 || end <= 0 {
		sc.t.Fatal("unable to locate the pseudo-terminal")
	}
	terminal, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		sc.t.Fatal("unable to open the pseudo-terminal")
	}
	sc.t.Cleanup(func() { _ = terminal.Close() })
	return controller, terminal
}

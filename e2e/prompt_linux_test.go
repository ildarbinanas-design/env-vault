//go:build linux

package e2e_test

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const e2ePromptReadTermios = unix.TCGETS

func openPromptTerminal(sc *scenario) (*os.File, *os.File) {
	sc.t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		sc.t.Fatal("native hidden-input E2E requires a pseudo-terminal")
	}
	sc.t.Cleanup(func() { _ = controller.Close() })
	fd := int(controller.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		sc.t.Fatal("unable to unlock the pseudo-terminal")
	}
	number, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		sc.t.Fatal("unable to locate the pseudo-terminal")
	}
	terminal, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		sc.t.Fatal("unable to open the pseudo-terminal")
	}
	sc.t.Cleanup(func() { _ = terminal.Close() })
	return controller, terminal
}

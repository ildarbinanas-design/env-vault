//go:build darwin || linux

package cli

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ildarbinanas-design/env-vault/internal/runner"
)

// readHiddenPassword keeps the terminal in canonical mode, with echo disabled.
// Register handlers before changing its state and start the handler only after
// that change, so a signal cannot restore echo just before we disable it. No
// secret or passphrase is sent to a backend until this function returns.
func readHiddenPassword(fd int) ([]byte, error) {
	state, err := unix.IoctlGetTermios(fd, promptReadTermios)
	if err != nil {
		return nil, err
	}
	notifications := make(chan os.Signal, 4)
	signals := []os.Signal{syscall.SIGTERM, syscall.SIGQUIT}
	// Background jobs/nohup may inherit an ignored interrupt or hangup.
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			signals = append(signals, sig)
		}
	}
	signal.Notify(notifications, signals...)
	hidden := *state
	hidden.Lflag &^= unix.ECHO
	hidden.Lflag |= unix.ICANON | unix.ISIG
	hidden.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, promptWriteTermios, &hidden); err != nil {
		signal.Stop(notifications)
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sig := range notifications {
			// Discard a partially typed line before restoring echo. Otherwise
			// the calling shell could read the unfinished secret as its input.
			_ = unix.IoctlSetTermios(fd, promptFlushTermios, state)
			signal.Stop(notifications)
			runner.ExitBySignal(sig)
			os.Exit(128 + int(sig.(syscall.Signal)))
		}
	}()
	defer func() {
		signal.Stop(notifications)
		close(notifications)
		<-done
		_ = unix.IoctlSetTermios(fd, promptWriteTermios, state)
	}()
	// The terminal handles line editing in canonical mode. Read one byte at a
	// time to avoid consuming input intended for a confirmation prompt.
	var value []byte
	var b [1]byte
	for {
		n, err := unix.Read(fd, b[:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return value, err
		}
		if n == 0 {
			if len(value) == 0 {
				return nil, io.EOF
			}
			return value, nil
		}
		if b[0] == '\n' {
			return value, nil
		}
		value = append(value, b[0])
	}
}

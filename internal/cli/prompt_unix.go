//go:build darwin || linux

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/runner"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

func readHiddenPassword(fd int) ([]byte, error) { return readHiddenInput(fd, 0) }

func readHiddenSecret(fd int) ([]byte, error) {
	return readHiddenInput(fd, secretstore.MaxValueBytes)
}

func readHiddenInput(fd, limit int) ([]byte, error) {
	return withHiddenPrompt(fd, func() ([]byte, error) {
		return readHiddenLine(func(p []byte) (int, error) { return unix.Read(fd, p) }, limit)
	})
}

func (a *App) readTerminalPassphrase(command string, confirm bool) ([]byte, error) {
	file, ok := a.stdin.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(file.Fd())) {
		// Preserve the existing structured error for non-terminal input.
		return a.collectPassphrase(command, confirm, a.terminalPassphrase(command))
	}
	value, err := withHiddenPrompt(int(file.Fd()), func() ([]byte, error) {
		read := a.terminalPassphraseWithReader(command, func(fd int) ([]byte, error) {
			return readHiddenLine(func(p []byte) (int, error) { return unix.Read(fd, p) }, 0)
		})
		return a.collectPassphrase(command, confirm, read)
	})
	if err != nil {
		if _, ok := apperrors.From(err); !ok {
			return nil, apperrors.Wrap(command, apperrors.CodeRuntimeError, "Unable to read hidden passphrase prompt", "Retry from an interactive terminal", apperrors.ExitRuntimeError, err)
		}
	}
	return value, err
}

// withHiddenPrompt disables canonical buffering and echo but retains signals.
// Register handlers before changing its state and start the handler only after
// that change, so a signal cannot restore echo just before we disable it. No
// secret or passphrase is sent to a backend until this function returns.
// A confirmation pair shares this session: restoring canonical mode between
// lines can corrupt a second line that is already queued on a macOS terminal.
func withHiddenPrompt(fd int, read func() ([]byte, error)) ([]byte, error) {
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
	hidden.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN
	hidden.Lflag |= unix.ISIG
	hidden.Iflag |= unix.ICRNL
	hidden.Cc[unix.VMIN] = 1
	hidden.Cc[unix.VTIME] = 0
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
			restoreInterruptedPrompt(fd, state)
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
	return read()
}

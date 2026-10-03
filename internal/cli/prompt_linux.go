package cli

import "golang.org/x/sys/unix"

const promptReadTermios = unix.TCGETS
const promptWriteTermios = unix.TCSETS

const promptFlushTermios = unix.TCSETSF

func restoreInterruptedPrompt(fd int, state *unix.Termios) {
	// TCSETSF flushes the line discipline, but input may still be queued
	// before it in the TTY flip buffer. TCIFLUSH discards that input before
	// restoring echo. Attempt restoration even if this flush fails.
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
	_ = unix.IoctlSetTermios(fd, promptFlushTermios, state)
}

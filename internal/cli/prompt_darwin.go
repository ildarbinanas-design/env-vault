package cli

import "golang.org/x/sys/unix"

const promptReadTermios = unix.TIOCGETA
const promptWriteTermios = unix.TIOCSETA

const promptFlushTermios = unix.TIOCSETAF

func restoreInterruptedPrompt(fd int, state *unix.Termios) {
	_ = unix.IoctlSetTermios(fd, promptFlushTermios, state)
}

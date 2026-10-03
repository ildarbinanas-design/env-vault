package cli

import "golang.org/x/sys/unix"

const promptReadTermios = unix.TCGETS
const promptWriteTermios = unix.TCSETS

const promptFlushTermios = unix.TCSETSF

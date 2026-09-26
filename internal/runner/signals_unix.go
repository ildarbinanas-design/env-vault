//go:build !windows

package runner

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ignoredAtStart records the signals env-vault inherited as ignored, as under
// nohup. signal.Notify clears that state, so it is read at package start.
var ignoredAtStart = map[syscall.Signal]bool{
	syscall.SIGHUP:  signal.Ignored(syscall.SIGHUP),
	syscall.SIGINT:  signal.Ignored(syscall.SIGINT),
	syscall.SIGTERM: signal.Ignored(syscall.SIGTERM),
}

// terminalForegroundGroup is a variable so tests can stand in for a terminal.
var terminalForegroundGroup = foregroundGroupOfTerminal

// foregroundGroupOfTerminal returns the foreground process group of
// env-vault's controlling terminal, if it has one.
func foregroundGroupOfTerminal() (int, bool) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return 0, false
	}
	defer tty.Close()
	group, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	return group, err == nil
}

// terminalDelivered reports whether the terminal already sent Ctrl+C or
// Ctrl+\ to the child. A terminal signals its whole foreground process group,
// so that holds only when env-vault and the child are both in that group.
func terminalDelivered(child int) bool {
	group, ok := terminalForegroundGroup()
	if !ok || group != syscall.Getpgrp() {
		return false
	}
	childGroup, err := syscall.Getpgid(child)
	return err == nil && childGroup == group
}

func signalNotifications() chan os.Signal {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	return ch
}

func stopSignalNotifications(ch chan os.Signal) {
	signal.Stop(ch)
	close(ch)
}

func forwardSignals(process *os.Process, ch chan os.Signal) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sig := range ch {
			// When the terminal already delivered SIGINT or SIGQUIT to the
			// child, forwarding would deliver it twice. Sent any other way,
			// for example by a service manager or a script, or when the child
			// left env-vault's process group, they are forwarded like the rest.
			if (sig == os.Interrupt || sig == syscall.SIGQUIT) && terminalDelivered(process.Pid) {
				continue
			}
			_ = process.Signal(sig)
		}
	}()
	return func() {
		stopSignalNotifications(ch)
		<-done
	}
}

// terminatingSignal reports the signal that killed the child, if any.
func terminatingSignal(state *os.ProcessState) (syscall.Signal, bool) {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0, false
	}
	return status.Signal(), true
}

// ExitBySignal ends env-vault with the signal that killed its child, so a
// calling shell sees the same kind of death it would see without env-vault
// (for example, an interrupted loop stops). It returns only when the signal
// cannot end the process that way; the caller then exits with 128+n.
func ExitBySignal(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	switch s {
	case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL:
	default:
		// Go turns other signals into a stack dump or ignores them.
		return
	}
	// An ignored signal cannot end the process, and PID 1 (a container
	// without an init) does not receive default-action signals from itself.
	if ignoredAtStart[s] || os.Getpid() == 1 {
		return
	}
	signal.Reset(s)
	if syscall.Kill(os.Getpid(), s) != nil {
		return
	}
	time.Sleep(time.Second)
}

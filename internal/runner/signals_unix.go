//go:build !windows

package runner

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

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
			// A terminal delivers SIGINT and SIGQUIT to the whole foreground
			// process group, so the child already has them. Forwarding would
			// deliver each one twice. They are still caught here so that
			// env-vault waits for the child instead of dying first.
			if sig == os.Interrupt || sig == syscall.SIGQUIT {
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
	if signal.Ignored(s) {
		return
	}
	signal.Reset(s)
	if syscall.Kill(os.Getpid(), s) != nil {
		return
	}
	time.Sleep(time.Second)
}

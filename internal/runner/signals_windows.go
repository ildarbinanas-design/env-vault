//go:build windows

package runner

import (
	"os"
	"os/signal"
	"syscall"
)

func signalNotifications() chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
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
			_ = process.Signal(sig)
		}
	}()
	return func() {
		stopSignalNotifications(ch)
		<-done
	}
}

// terminatingSignal reports the signal that killed the child. Windows
// processes do not die by signal.
func terminatingSignal(*os.ProcessState) (syscall.Signal, bool) {
	return 0, false
}

// ExitBySignal has nothing to re-raise on Windows.
func ExitBySignal(os.Signal) {}

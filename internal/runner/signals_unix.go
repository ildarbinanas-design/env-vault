//go:build !windows

package runner

import (
	"os"
	"os/signal"
	"syscall"
)

func forwardSignals(process *os.Process) func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sig := range ch {
			_ = process.Signal(sig)
		}
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
		<-done
	}
}

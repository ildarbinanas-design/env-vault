//go:build windows

package runner

import (
	"os"
	"os/signal"
)

func forwardSignals(process *os.Process) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
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

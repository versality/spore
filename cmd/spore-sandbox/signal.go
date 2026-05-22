package main

import (
	"os"
	"os/signal"
	"syscall"
)

// forwardSignals relays SIGINT, SIGTERM, SIGHUP, and SIGQUIT to proc
// until the returned stop function is called. The goroutine exits
// cleanly on stop; safe to defer.
func forwardSignals(proc *os.Process) func() {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigCh:
				if proc != nil {
					_ = proc.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

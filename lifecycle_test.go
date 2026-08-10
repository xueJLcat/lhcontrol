package main

import (
	"context"
	"testing"
	"time"
)

func TestShutdownBoundsStuckAPIServerLoop(t *testing.T) {
	app := NewApp()
	app.api = nil
	app.apiShutdownWait = 25 * time.Millisecond
	app.apiWG.Add(1)

	done := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(done)
	}()

	select {
	case <-done:
		app.apiWG.Done()
	case <-time.After(500 * time.Millisecond):
		// Release the simulated listener before failing so the shutdown goroutine
		// and the waiter's goroutine cannot leak into later tests.
		app.apiWG.Done()
		<-done
		t.Fatal("shutdown remained blocked on the API listener after its timeout")
	}
}

package main

import (
	"context"
	"errors"
	"log"
	"net"
	"time"
)

// apiShutdownTimeout bounds the whole API shutdown, including Fiber's
// graceful shutdown and the listener loop. An in-flight handler stuck in a
// Bluetooth call that ignores cancellation must not hang the exit sequence.
const apiShutdownTimeout = 10 * time.Second

func (a *App) waitForAPIShutdown() {
	limit := a.apiShutdownWait
	if limit <= 0 {
		limit = apiShutdownTimeout
	}

	var fiberDone <-chan error
	if a.api != nil {
		log.Println("Shutting down API server...")
		result := make(chan error, 1)
		fiberDone = result
		go func() {
			// Bound the drain itself, not just the wait: Fiber holds its app
			// lock across Shutdown, and an uncancellable background drain
			// outlasting this budget would keep a goroutine (and the lock)
			// alive past the point the caller has moved on.
			result <- a.api.ShutdownWithTimeout(limit)
		}()
	}

	listenerResult := make(chan struct{})
	var listenerDone <-chan struct{} = listenerResult
	go func() {
		a.apiWG.Wait()
		close(listenerResult)
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()
	for fiberDone != nil || listenerDone != nil {
		select {
		case err := <-fiberDone:
			fiberDone = nil
			// Fiber's Shutdown closes every listener it ever served, including
			// stale listeners from earlier address changes that the loop already
			// closed itself. Closing an already-closed listener reports
			// net.ErrClosed; that is a benign duplicate, not a shutdown failure,
			// and logging it would mask a real error on every restart. A
			// deadline error is the bounded drain reporting this same budget;
			// it may win the select race against the timer, so log it here.
			switch {
			case err == nil || errors.Is(err, net.ErrClosed):
			case errors.Is(err, context.DeadlineExceeded):
				log.Printf("API server drain exceeded %s; continuing", limit)
			default:
				log.Printf("Error shutting down API server: %v", err)
			}
		case <-listenerDone:
			listenerDone = nil
		case <-timer.C:
			log.Printf("API server shutdown timed out after %s; continuing", limit)
			return
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	// Wails can run OnShutdown and still return a run error, which makes the
	// fatal path call shutdown a second time. Everything here is idempotent
	// except the bounded waits, so run the whole sequence once.
	a.shutdownOnce.Do(func() {
		a.runShutdown(ctx)
	})
}

func (a *App) runShutdown(ctx context.Context) {

	a.shuttingDown.Store(true)

	log.Println("App shutdown requested. Cleaning up...")

	a.stationManager.BeginShutdown()

	a.stopAutoSleep()

	// The gate interlocks with restartAPIServer: a restart either completed
	// before this point (its listener is torn down below) or it observes
	// shuttingDown and does not start a new loop. Holding the gate across
	// waiting for apiWG also keeps WaitGroup Add calls from racing the Wait.

	a.apiLifecycleGate.Lock()

	a.apiLifecycleMutex.Lock()

	cancelAPI := a.apiCancel

	a.apiCancel = nil

	a.apiLifecycleMutex.Unlock()

	if cancelAPI != nil {

		cancelAPI()

	}

	a.waitForAPIShutdown()

	a.apiLifecycleGate.Unlock()

	log.Println("Requesting disconnect for all stations...")

	a.stationManager.Shutdown()

	log.Println("App shutdown sequence complete.")

}

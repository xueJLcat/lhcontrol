package main

import (
	"context"
	"log"
	"time"
)

// apiShutdownTimeout bounds fiber's graceful shutdown. An in-flight handler
// stuck in a Bluetooth call that ignores cancellation would otherwise block
// Shutdown forever and hang the whole exit sequence. The API listener loop
// exits on its own once its context is cancelled, so giving up here is safe.
const apiShutdownTimeout = 10 * time.Second

func (a *App) shutdown(ctx context.Context) {

	a.shuttingDown.Store(true)

	log.Println("App shutdown requested. Cleaning up...")

	a.stationManager.BeginShutdown()

	a.stopAutoSleep()

	// The gate interlocks with restartAPIServer: a restart either completed
	// before this point (its listener is torn down below) or it observes
	// shuttingDown and does not start a new loop. Holding the gate across
	// apiWG.Wait also keeps WaitGroup Add calls from racing the Wait here.

	a.apiLifecycleGate.Lock()

	a.apiLifecycleMutex.Lock()

	cancelAPI := a.apiCancel

	a.apiCancel = nil

	a.apiLifecycleMutex.Unlock()

	if cancelAPI != nil {

		cancelAPI()

	}

	if a.api != nil {

		log.Println("Shutting down API server...")

		shutdownErr := make(chan error, 1)

		go func() {

			shutdownErr <- a.api.Shutdown()

		}()

		select {

		case err := <-shutdownErr:

			if err != nil {

				log.Printf("Error shutting down API server: %v", err)

			}

		case <-time.After(apiShutdownTimeout):

			log.Printf("API server shutdown timed out after %s; continuing", apiShutdownTimeout)

		}

	}

	a.apiWG.Wait()

	a.apiLifecycleGate.Unlock()

	log.Println("Requesting disconnect for all stations...")

	a.stationManager.Shutdown()

	log.Println("App shutdown sequence complete.")

}

package main

import (
	"context"
	"log"
)

func (a *App) shutdown(ctx context.Context) {

	a.shuttingDown.Store(true)

	log.Println("App shutdown requested. Cleaning up...")

	a.stationManager.BeginShutdown()

	a.stopAutoSleep()

	a.apiLifecycleMutex.Lock()

	cancelAPI := a.apiCancel

	a.apiCancel = nil

	a.apiLifecycleMutex.Unlock()

	if cancelAPI != nil {

		cancelAPI()

	}

	if a.api != nil {

		log.Println("Shutting down API server...")

		if err := a.api.Shutdown(); err != nil {

			log.Printf("Error shutting down API server: %v", err)

		}

	}

	a.apiWG.Wait()

	log.Println("Requesting disconnect for all stations...")

	a.stationManager.Shutdown()

	log.Println("App shutdown sequence complete.")

}

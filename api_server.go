package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"time"
)

func (a *App) startAPIServer() {

	a.apiLifecycleMutex.Lock()

	if a.apiCancel != nil {

		a.apiLifecycleMutex.Unlock()

		return

	}

	apiContext, cancel := context.WithCancel(context.Background())

	a.apiCancel = cancel

	a.apiGeneration++

	generation := a.apiGeneration

	a.apiWG.Add(1)

	a.apiLifecycleMutex.Unlock()

	go func() {

		defer func() {

			a.apiLifecycleMutex.Lock()

			if a.apiGeneration == generation {

				a.apiCancel = nil

			}

			a.apiLifecycleMutex.Unlock()

			a.apiWG.Done()

		}()

		a.runAPIServer(apiContext)

	}()

}

// restartAPIServer stops the active listener loop and starts a new one so a
// changed listen address applies without restarting the application. The
// generation bump detaches the dying goroutine's cleanup so it cannot clear
// the freshly registered cancel function.
//
// The lifecycle gate serializes restart against shutdown and against other
// restarts: apiWG.Wait followed by apiWG.Add inside startAPIServer would
// otherwise violate the WaitGroup contract when two waiters wake up
// concurrently, and a restart racing shutdown could resurrect a listener
// after the app started shutting down.
func (a *App) restartAPIServer() {

	a.apiLifecycleGate.Lock()

	defer a.apiLifecycleGate.Unlock()

	if a.shuttingDown.Load() {

		return

	}

	a.apiLifecycleMutex.Lock()

	cancelAPI := a.apiCancel

	a.apiCancel = nil

	a.apiGeneration++

	a.apiLifecycleMutex.Unlock()

	if cancelAPI != nil {

		cancelAPI()

	}

	a.apiWG.Wait()

	if a.shuttingDown.Load() {

		return

	}

	a.startAPIServer()

}

func (a *App) runAPIServer(ctx context.Context) {

	for {

		select {

		case <-ctx.Done():

			a.setAPIStatus(false, nil)

			return

		default:

		}

		listener, err := a.listen("tcp", a.GetAPIStatus().Address)

		if err != nil {

			a.setAPIStatus(false, err)

			log.Printf("Error starting API server; retrying: %v", err)

			timer := time.NewTimer(a.apiRetryDelay)

			select {

			case <-ctx.Done():

				if !timer.Stop() {

					<-timer.C

				}

				a.setAPIStatus(false, nil)

				return

			case <-timer.C:

				continue

			}

		}

		a.setAPIStatus(true, nil)

		listenerDone := make(chan struct{})

		go func() {

			select {

			case <-ctx.Done():

				_ = listener.Close()

			case <-listenerDone:

			}

		}()

		err = a.serveAPIListener(listener)

		_ = listener.Close()

		close(listenerDone)

		if ctx.Err() != nil {

			a.setAPIStatus(false, nil)

			return

		}

		if err == nil {

			err = errors.New("API listener stopped unexpectedly")

		}

		a.setAPIStatus(false, err)

		log.Printf("API server stopped; retrying: %v", err)

		timer := time.NewTimer(a.apiRetryDelay)

		select {

		case <-ctx.Done():

			if !timer.Stop() {

				<-timer.C

			}

			a.setAPIStatus(false, nil)

			return

		case <-timer.C:

		}

	}

}

func (a *App) serveAPIListener(listener net.Listener) (returnErr error) {

	defer func() {

		if recovered := recover(); recovered != nil {

			returnErr = fmt.Errorf("API server panic: %v", recovered)

			log.Printf("Recovered API server panic: %v\n%s", recovered, debug.Stack())

		}

	}()

	return a.serveListener(listener)

}

// --- Bluetooth Methods exposed to Wails --- //

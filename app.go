package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const apiBodyLimit = 16 * 1024

func apiStatusForError(err error) int {
	status := fiber.StatusInternalServerError
	switch {
	case errors.Is(err, station.ErrInvalidArgument):
		status = fiber.StatusBadRequest
	case errors.Is(err, station.ErrNotFound):
		status = fiber.StatusNotFound
	case errors.Is(err, station.ErrOperationInProgress),
		errors.Is(err, station.ErrChannelConflict),
		errors.Is(err, station.ErrScanRequired),
		errors.Is(err, bluetooth.ErrScanCancelled):
		status = fiber.StatusConflict
	case errors.Is(err, station.ErrStationTransitioning):
		status = fiber.StatusLocked
	case errors.Is(err, station.ErrUnsupported):
		status = fiber.StatusUnprocessableEntity
	case errors.Is(err, station.ErrShuttingDown):
		status = fiber.StatusServiceUnavailable
	}
	return status
}

func sendAPIError(c *fiber.Ctx, err error) error {
	status := apiStatusForError(err)
	return c.Status(status).JSON(fiber.Map{"error": err.Error()})
}

func apiErrorHandler(c *fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	message := "internal server error"
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		status = fiberErr.Code
		message = fiberErr.Message
	}
	return c.Status(status).JSON(fiber.Map{"error": message})
}

func sendPowerActionResponse(c *fiber.Ctx, result station.PowerActionResult, err error) error {
	if err == nil {
		return c.JSON(result)
	}
	var confirmationErr *bluetooth.PowerConfirmationError
	if errors.As(err, &confirmationErr) && result.CommandSent {
		return c.Status(fiber.StatusOK).JSON(result)
	}
	return sendAPIError(c, err)
}

func sendChannelActionResponse(c *fiber.Ctx, result station.ChannelChangeResult, expectedChannel int, err error) error {
	if err == nil || (result.CommandSent && !result.Confirmed) {
		return c.Status(fiber.StatusOK).JSON(result)
	}
	return c.Status(apiStatusForError(err)).JSON(fiber.Map{
		"error":             err.Error(),
		"address":           result.Address,
		"previousChannel":   result.PreviousChannel,
		"expectedChannel":   expectedChannel,
		"actualChannel":     result.Channel,
		"commandSent":       result.CommandSent,
		"confirmed":         result.Confirmed,
		"confirmationError": result.ConfirmationError,
		"warnings":          result.Warnings,
		"station":           result.Station,
	})
}

// App struct
type App struct {
	ctx               context.Context
	config            *config.Config
	stationManager    *station.Manager
	api               *fiber.App
	apiStatusMutex    sync.RWMutex
	apiStatus         APIStatus
	configLoadWarning string
	configSaveWarning string
	apiLifecycleMutex sync.Mutex
	apiCancel         context.CancelFunc
	apiWG             sync.WaitGroup
	listen            func(string, string) (net.Listener, error)
	serveListener     func(net.Listener) error
	apiRetryDelay     time.Duration
	apiGeneration     uint64
	externalScanID    atomic.Uint64
	shuttingDown      atomic.Bool
}

type APIStatus struct {
	Running        bool     `json:"running"`
	Address        string   `json:"address"`
	Error          string   `json:"error"`
	Warnings       []string `json:"warnings"`
	ConfigWritable bool     `json:"configWritable"`
}

type apiStationManager interface {
	GetStationInfo() []station.StationInfo
	StartScan(station.ScanCallbacks) error
	StopScan() error
	GetScanStatus() station.ScanStatus
	SetAllStationsPowerDetailed(string) (station.BulkPowerResult, error)
	SetStationPower(string, string) (station.PowerActionResult, error)
	IdentifyStation(string) error
	RefreshStationCapabilities(string) (station.StationInfo, error)
	SetStationChannel(string, int, bool) (station.ChannelChangeResult, error)
}

type scanEventCallbacks struct {
	nextID    func() uint64
	started   func(scanEvent)
	completed func(scanEvent)
	failed    func(scanEvent)
	cancelled func(scanEvent)
}

// scanEvent ties every external lifecycle notification to one scan request so
// a delayed terminal event cannot replace a newer scan in the desktop UI.
type scanEvent struct {
	ID       uint64                `json:"id"`
	Stations []station.StationInfo `json:"stations,omitempty"`
	Error    string                `json:"error,omitempty"`
}

func registerAPIRoutes(api *fiber.App, manager apiStationManager, events scanEventCallbacks, status func() APIStatus) {
	api.Use(func(c *fiber.Ctx) error {
		if len(c.Body()) > apiBodyLimit {
			return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body exceeds the allowed limit")
		}
		return c.Next()
	})
	api.Post("/allon", func(c *fiber.Ctx) error {
		result, err := manager.SetAllStationsPowerDetailed("on")
		if err != nil {
			return sendAPIError(c, err)
		}
		return c.JSON(result)
	})
	api.Post("/alloff", func(c *fiber.Ctx) error {
		result, err := manager.SetAllStationsPowerDetailed("sleep")
		if err != nil {
			return sendAPIError(c, err)
		}
		return c.JSON(result)
	})
	api.Get("/status", func(c *fiber.Ctx) error {
		return c.JSON(manager.GetStationInfo())
	})
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(status())
	})
	api.Post("/scan", func(c *fiber.Ctx) error {
		id := uint64(0)
		if events.nextID != nil {
			id = events.nextID()
		}
		event := scanEvent{ID: id}
		err := manager.StartScan(station.ScanCallbacks{
			Started: func() {
				if events.started != nil {
					events.started(event)
				}
			},
			Completed: func(stations []station.StationInfo) {
				if events.completed != nil {
					event.Stations = stations
					events.completed(event)
				}
			},
			Failed: func(err error) {
				if events.failed != nil {
					event.Error = err.Error()
					events.failed(event)
				}
			},
			Cancelled: func() {
				if events.cancelled != nil {
					events.cancelled(event)
				}
			},
		})
		if err != nil {
			return sendAPIError(c, err)
		}
		return c.SendStatus(fiber.StatusAccepted)
	})
	api.Get("/scan/status", func(c *fiber.Ctx) error {
		return c.JSON(manager.GetScanStatus())
	})
	api.Post("/scan/stop", func(c *fiber.Ctx) error {
		if err := manager.StopScan(); err != nil {
			return sendAPIError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	api.Post("/stations/power", func(c *fiber.Ctx) error {
		var request struct {
			State string `json:"state"`
		}
		if err := c.BodyParser(&request); err != nil {
			return sendAPIError(c, fmt.Errorf("%w: invalid JSON body", station.ErrInvalidArgument))
		}
		result, err := manager.SetAllStationsPowerDetailed(request.State)
		if err != nil {
			return sendAPIError(c, err)
		}
		return c.JSON(result)
	})
	api.Post("/stations/:address/power", func(c *fiber.Ctx) error {
		var request struct {
			State string `json:"state"`
		}
		if err := c.BodyParser(&request); err != nil {
			return sendAPIError(c, fmt.Errorf("%w: invalid JSON body", station.ErrInvalidArgument))
		}
		result, err := manager.SetStationPower(c.Params("address"), request.State)
		return sendPowerActionResponse(c, result, err)
	})
	api.Post("/stations/:address/identify", func(c *fiber.Ctx) error {
		if err := manager.IdentifyStation(c.Params("address")); err != nil {
			return sendAPIError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	api.Post("/stations/:address/refresh", func(c *fiber.Ctx) error {
		result, err := manager.RefreshStationCapabilities(c.Params("address"))
		if err != nil {
			return sendAPIError(c, err)
		}
		return c.JSON(result)
	})
	api.Put("/stations/:address/channel", func(c *fiber.Ctx) error {
		var request struct {
			Channel                  int  `json:"channel"`
			AllowUnknownConflictRisk bool `json:"allowUnknownConflictRisk"`
		}
		if err := c.BodyParser(&request); err != nil {
			return sendAPIError(c, fmt.Errorf("%w: invalid JSON body", station.ErrInvalidArgument))
		}
		result, err := manager.SetStationChannel(c.Params("address"), request.Channel, request.AllowUnknownConflictRisk)
		return sendChannelActionResponse(c, result, request.Channel, err)
	})
}

// NewApp creates a new App application struct
func NewApp() *App {
	cfg := config.NewConfig()
	mgr := station.NewManager(cfg)
	api := fiber.New(fiber.Config{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  30 * time.Second,
		ErrorHandler: apiErrorHandler,
	})
	app := &App{
		config:         cfg,
		stationManager: mgr,
		api:            api,
		apiStatus: APIStatus{
			Address:        "127.0.0.1:7575",
			Warnings:       []string{},
			ConfigWritable: true,
		},
		listen:        net.Listen,
		apiRetryDelay: 2 * time.Second,
	}
	app.serveListener = api.Listener
	return app
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.shuttingDown.Store(false)

	// Use standard logger (already configured in main)
	log.Println("-----------------------------------------")
	log.Println("Application startup initiated.")
	log.Println("-----------------------------------------")

	if err := a.stationManager.Initialize(); err != nil {
		log.Printf("Error initializing Bluetooth: %v", err)
	}

	configLoadErr := a.config.Load()
	a.setConfigLoadStatus(configLoadErr)
	if configLoadErr != nil {
		log.Printf("Error loading config: %v", configLoadErr)
	}

	registerAPIRoutes(a.api, a.stationManager, scanEventCallbacks{
		nextID: func() uint64 {
			return a.externalScanID.Add(1)
		},
		started: func(event scanEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-started", event)
			}
		},
		completed: func(event scanEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-completed", event)
			}
		},
		failed: func(event scanEvent) {
			log.Printf("API background scan failed: %s", event.Error)
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-failed", event)
			}
		},
		cancelled: func(event scanEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-cancelled", event)
			}
		},
	}, a.GetAPIStatus)
	a.startAPIServer()

	log.Println("Startup sequence complete.")
}

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

func (a *App) ScanAndFetchStations() ([]station.StationInfo, error) {
	return a.stationManager.ScanAndFetchStations()
}

func (a *App) IsScanning() bool {
	return a.stationManager.IsScanning()
}

func (a *App) GetScanStatus() station.ScanStatus {
	return a.stationManager.GetScanStatus()
}

func (a *App) StopScan() error {
	return a.stationManager.StopScan()
}

func (a *App) CheckAllStationStatuses() ([]station.StationInfo, error) {
	return a.stationManager.CheckAllStationStatuses()
}

func (a *App) GetCurrentStationInfo() []station.StationInfo {
	return a.stationManager.GetStationInfo()
}

func (a *App) setAPIStatus(running bool, err error) {
	a.apiStatusMutex.Lock()
	a.apiStatus.Running = running
	if err != nil {
		a.apiStatus.Error = err.Error()
	} else {
		a.apiStatus.Error = ""
	}
	a.apiStatusMutex.Unlock()
}

func (a *App) setConfigLoadStatus(err error) {
	a.apiStatusMutex.Lock()
	defer a.apiStatusMutex.Unlock()
	if err == nil {
		a.configLoadWarning = ""
	} else {
		a.configLoadWarning = fmt.Sprintf("Configuration could not be loaded: %v", err)
	}
	a.refreshConfigStatusLocked()
}

func (a *App) setConfigPersistenceStatus() {
	a.apiStatusMutex.Lock()
	defer a.apiStatusMutex.Unlock()
	if err := a.config.PersistenceError(); err != nil {
		a.configSaveWarning = fmt.Sprintf("Configuration changes could not be saved: %v", err)
	} else {
		a.configSaveWarning = ""
	}
	a.refreshConfigStatusLocked()
}

func (a *App) refreshConfigStatusLocked() {
	a.apiStatus.ConfigWritable = a.config.PersistenceError() == nil
	a.apiStatus.Warnings = make([]string, 0, 2)
	if a.configLoadWarning != "" {
		a.apiStatus.Warnings = append(a.apiStatus.Warnings, a.configLoadWarning)
	}
	if a.configSaveWarning != "" && a.configSaveWarning != a.configLoadWarning {
		a.apiStatus.Warnings = append(a.apiStatus.Warnings, a.configSaveWarning)
	}
}

func (a *App) GetAPIStatus() APIStatus {
	a.apiStatusMutex.RLock()
	defer a.apiStatusMutex.RUnlock()
	status := a.apiStatus
	status.Warnings = append([]string{}, a.apiStatus.Warnings...)
	return status
}

func (a *App) PowerOnStation(address string) error {
	log.Printf("Requesting Power ON for address %s", address)
	result, err := a.stationManager.SetStationPower(address, "on")
	return legacyPowerActionError("on", address, result, err)
}

func (a *App) PowerOffStation(address string) error {
	log.Printf("Requesting Power OFF for address %s", address)
	result, err := a.stationManager.SetStationPower(address, "sleep")
	return legacyPowerActionError("sleep", address, result, err)
}

func legacyPowerActionError(state, address string, result station.PowerActionResult, err error) error {
	if err != nil && result.CommandSent {
		log.Printf(
			"Legacy %s request for %s was accepted but could not be confirmed: %v",
			state,
			address,
			err,
		)
		return nil
	}
	return err
}

func (a *App) SetStationPower(address, state string) (station.PowerActionResult, error) {
	log.Printf("Requesting power state %s for address %s", state, address)
	result, err := a.stationManager.SetStationPower(address, state)
	var confirmationErr *bluetooth.PowerConfirmationError
	if errors.As(err, &confirmationErr) {
		// Wails discards return values when a Go error is returned. Preserve the
		// structured command-sent/readback-failed result for the desktop UI.
		return result, nil
	}
	return result, err
}

func (a *App) IdentifyStation(address string) error {
	log.Printf("Requesting identify for address %s", address)
	return a.stationManager.IdentifyStation(address)
}

func (a *App) RefreshStationCapabilities(address string) (station.StationInfo, error) {
	log.Printf("Refreshing capabilities for address %s", address)
	return a.stationManager.RefreshStationCapabilities(address)
}

func (a *App) SetStationChannel(address string, channel int, allowUnknownConflictRisk bool) (station.ChannelChangeResult, error) {
	log.Printf("Requesting channel %d for address %s", channel, address)
	result, err := a.stationManager.SetStationChannel(address, channel, allowUnknownConflictRisk)
	return channelResultForWails(result, err)
}

func channelResultForWails(result station.ChannelChangeResult, err error) (station.ChannelChangeResult, error) {
	if err != nil && result.CommandSent && !result.Confirmed {
		// Wails discards structured return values when a Go error is returned.
		return result, nil
	}
	return result, err
}

func (a *App) PowerOnAllStations() error {
	return a.stationManager.PowerOnAllStations()
}

func (a *App) PowerOffAllStations() error {
	return a.stationManager.PowerOffAllStations()
}

func (a *App) SetAllStationsPower(state string) error {
	log.Printf("Requesting power state %s for all known stations", state)
	return a.stationManager.SetAllStationsPower(state)
}

func (a *App) SetAllStationsPowerDetailed(state string) (station.BulkPowerResult, error) {
	log.Printf("Requesting detailed power state %s for all known stations", state)
	return a.stationManager.SetAllStationsPowerDetailed(state)
}

// RenameStation is the legacy name-based rename kept for compatibility: it
// renames every station sharing the same original factory name. New
// integrations should call RenameStationByAddress instead.
func (a *App) RenameStation(originalName string, newName string) error {
	log.Printf("Renaming %s to %s", originalName, newName)
	err := a.stationManager.RenameStation(originalName, newName)
	a.setConfigPersistenceStatus()
	return err
}

func (a *App) RenameStationByAddress(address string, newName string) error {
	log.Printf("Renaming station at %s to %s", address, newName)
	err := a.stationManager.RenameStationByAddress(address, newName)
	a.setConfigPersistenceStatus()
	return err
}

func (a *App) SaveConfig() error {
	err := a.stationManager.SaveConfig()
	a.setConfigPersistenceStatus()
	return err
}

// shutdown is called when the app terminates.
func (a *App) shutdown(ctx context.Context) {
	a.shuttingDown.Store(true)
	log.Println("App shutdown requested. Cleaning up...")
	a.stationManager.BeginShutdown()
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

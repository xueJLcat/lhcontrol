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

func apiStatusForError(err error) int {
	status := fiber.StatusInternalServerError
	switch {
	case errors.Is(err, station.ErrInvalidArgument):
		status = fiber.StatusBadRequest
	case errors.Is(err, station.ErrNotFound):
		status = fiber.StatusNotFound
	case errors.Is(err, station.ErrOperationInProgress),
		errors.Is(err, station.ErrChannelConflict),
		errors.Is(err, station.ErrScanRequired):
		status = fiber.StatusConflict
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

// App struct
type App struct {
	ctx            context.Context
	config         *config.Config
	stationManager *station.Manager
	api            *fiber.App
	apiStatusMutex sync.RWMutex
	apiStatus      APIStatus
	shuttingDown   atomic.Bool
}

type APIStatus struct {
	Running bool   `json:"running"`
	Address string `json:"address"`
	Error   string `json:"error"`
}

type apiStationManager interface {
	PowerOnAllStations() error
	PowerOffAllStations() error
	GetStationInfo() []station.StationInfo
	StartScan(func([]station.StationInfo, error)) error
	GetScanStatus() station.ScanStatus
	SetAllStationsPowerDetailed(string) (station.BulkPowerResult, error)
	SetStationPower(string, string) (station.PowerActionResult, error)
	IdentifyStation(string) error
	RefreshStationCapabilities(string) (station.StationInfo, error)
	SetStationChannel(string, int, bool) (station.ChannelChangeResult, error)
}

type scanEventCallbacks struct {
	started   func()
	completed func([]station.StationInfo)
	failed    func(error)
}

func registerAPIRoutes(api *fiber.App, manager apiStationManager, events scanEventCallbacks, status func() APIStatus) {
	api.Post("/allon", func(c *fiber.Ctx) error {
		if err := manager.PowerOnAllStations(); err != nil {
			return sendAPIError(c, err)
		}
		return c.SendStatus(fiber.StatusOK)
	})
	api.Post("/alloff", func(c *fiber.Ctx) error {
		if err := manager.PowerOffAllStations(); err != nil {
			return sendAPIError(c, err)
		}
		return c.SendStatus(fiber.StatusOK)
	})
	api.Get("/status", func(c *fiber.Ctx) error {
		return c.JSON(manager.GetStationInfo())
	})
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(status())
	})
	api.Post("/scan", func(c *fiber.Ctx) error {
		err := manager.StartScan(func(stations []station.StationInfo, err error) {
			if err != nil {
				if events.failed != nil {
					events.failed(err)
				}
				return
			}
			if events.completed != nil {
				events.completed(stations)
			}
		})
		if err != nil {
			return sendAPIError(c, err)
		}
		if events.started != nil {
			events.started()
		}
		return c.SendStatus(fiber.StatusAccepted)
	})
	api.Get("/scan/status", func(c *fiber.Ctx) error {
		return c.JSON(manager.GetScanStatus())
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
		if err != nil {
			return c.Status(apiStatusForError(err)).JSON(fiber.Map{
				"error":           err.Error(),
				"address":         result.Address,
				"previousChannel": result.PreviousChannel,
				"expectedChannel": request.Channel,
				"actualChannel":   result.Channel,
				"warnings":        result.Warnings,
			})
		}
		return c.JSON(result)
	})
}

// NewApp creates a new App application struct
func NewApp() *App {
	cfg := config.NewConfig()
	mgr := station.NewManager(cfg)
	return &App{
		config:         cfg,
		stationManager: mgr,
		api: fiber.New(fiber.Config{
			BodyLimit:    16 * 1024,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 45 * time.Second,
			IdleTimeout:  30 * time.Second,
		}),
		apiStatus: APIStatus{Address: "127.0.0.1:7575"},
	}
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

	if err := a.config.Load(); err != nil {
		log.Printf("Error loading config: %v", err)
	}

	registerAPIRoutes(a.api, a.stationManager, scanEventCallbacks{
		started: func() {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-started")
			}
		},
		completed: func(stations []station.StationInfo) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-completed", stations)
			}
		},
		failed: func(err error) {
			log.Printf("API background scan failed: %v", err)
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-failed", err.Error())
			}
		},
	}, a.GetAPIStatus)
	listener, listenErr := net.Listen("tcp", a.GetAPIStatus().Address)
	if listenErr != nil {
		a.setAPIStatus(false, listenErr)
		log.Printf("Error starting API server: %v", listenErr)
	} else {
		a.setAPIStatus(true, nil)
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					a.setAPIStatus(false, fmt.Errorf("API server panic: %v", recovered))
					log.Printf("Recovered API server panic: %v\n%s", recovered, debug.Stack())
				}
			}()
			if err := a.api.Listener(listener); err != nil && !errors.Is(err, net.ErrClosed) {
				a.setAPIStatus(false, err)
				log.Printf("API server stopped with error: %v", err)
				return
			}
			a.setAPIStatus(false, nil)
		}()
	}

	log.Println("Startup sequence complete.")
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

func (a *App) GetAPIStatus() APIStatus {
	a.apiStatusMutex.RLock()
	defer a.apiStatusMutex.RUnlock()
	return a.apiStatus
}

func (a *App) PowerOnStation(address string) error {
	log.Printf("Requesting Power ON for address %s", address)
	return a.stationManager.PowerOnStation(address)
}

func (a *App) PowerOffStation(address string) error {
	log.Printf("Requesting Power OFF for address %s", address)
	return a.stationManager.PowerOffStation(address)
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
	return a.stationManager.SetStationChannel(address, channel, allowUnknownConflictRisk)
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

func (a *App) RenameStation(originalName string, newName string) error {
	log.Printf("Renaming %s to %s", originalName, newName)
	return a.stationManager.RenameStation(originalName, newName)
}

func (a *App) RenameStationByAddress(address string, newName string) error {
	log.Printf("Renaming station at %s to %s", address, newName)
	return a.stationManager.RenameStationByAddress(address, newName)
}

func (a *App) SaveConfig() error {
	return a.config.Save()
}

// shutdown is called when the app terminates.
func (a *App) shutdown(ctx context.Context) {
	a.shuttingDown.Store(true)
	log.Println("App shutdown requested. Cleaning up...")
	if a.api != nil {
		log.Println("Shutting down API server...")
		if err := a.api.Shutdown(); err != nil {
			log.Printf("Error shutting down API server: %v", err)
		}
	}
	log.Println("Requesting disconnect for all stations...")
	a.stationManager.Shutdown()
	log.Println("App shutdown sequence complete.")
}

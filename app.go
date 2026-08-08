package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx                       context.Context
	config                    *config.Config
	stationManager            *station.Manager
	api                       *fiber.App
	apiStatusMutex            sync.RWMutex
	apiStatus                 APIStatus
	configLoadWarning         string
	configSaveWarning         string
	apiLifecycleMutex         sync.Mutex
	apiCancel                 context.CancelFunc
	apiWG                     sync.WaitGroup
	apiLifecycleGate          sync.Mutex
	listen                    func(string, string) (net.Listener, error)
	serveListener             func(net.Listener) error
	apiRetryDelay             time.Duration
	apiGeneration             uint64
	externalScanID            atomic.Uint64
	externalUpdateID          atomic.Uint64
	externalOperationID       atomic.Uint64
	externalOperationMutex    sync.RWMutex
	activeExternalOperations  map[uint64]string
	externalOperationRevision uint64
	shuttingDown              atomic.Bool
	autoSleepMutex            sync.Mutex
	autoSleepCancel           context.CancelFunc
	autoSleepWG               sync.WaitGroup
	scanForAutoSleep          func(context.Context) ([]station.StationInfo, error)
	setPowerForAutoSleep      func(context.Context, string) (station.BulkPowerResult, error)
	autoSleepEventSink        func(autoSleepEvent)
}

func NewApp() *App {
	cfg := config.NewConfig()
	mgr := station.NewManager(cfg)
	api := fiber.New(fiber.Config{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  30 * time.Second,
		BodyLimit:    apiBodyLimit,
		ErrorHandler: apiErrorHandler,
	})
	app := &App{
		config:                   cfg,
		stationManager:           mgr,
		api:                      api,
		activeExternalOperations: make(map[uint64]string),
		apiStatus: APIStatus{
			Address:        "127.0.0.1:7575",
			Warnings:       []string{},
			ConfigWritable: true,
		},
		listen:        net.Listen,
		apiRetryDelay: 2 * time.Second,
	}
	app.serveListener = api.Listener
	app.scanForAutoSleep = mgr.ScanAndFetchStationsContext
	app.setPowerForAutoSleep = mgr.SetAllStationsPowerDetailedContext
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

	// Load configuration before initializing Bluetooth so the very first
	// adapter-initialization retry cooldown honors the persisted
	// bluetoothInitRetrySeconds instead of the built-in default.
	configLoadErr := a.config.Load()
	a.setConfigLoadStatus(configLoadErr)
	if configLoadErr != nil {
		log.Printf("Error loading config: %v", configLoadErr)
	}

	a.applyBluetoothTiming()

	if err := a.stationManager.Initialize(); err != nil {
		log.Printf("Error initializing Bluetooth: %v", err)
	}

	a.setAPIAddress(a.config.GetAPIListenAddress())
	a.applyAutoSleep(a.config.GetAutoSleep())

	registerAPIRoutes(a.api, a.stationManager, scanEventCallbacks{
		nextID: func() uint64 {
			return a.externalScanID.Add(1)
		},
		nextUpdateID: func() uint64 {
			return a.externalUpdateID.Add(1)
		},
		nextOperationID: func() uint64 {
			return a.externalOperationID.Add(1)
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
		updated: func(event stationUpdateEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-stations-updated", event)
			}
		},
		operation: func(event externalOperationEvent) {
			event = a.recordExternalOperation(event)
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-operation", event)
			}
		},
	}, a.GetAPIStatus)
	a.startAPIServer()

	log.Println("Startup sequence complete.")
}

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

// setAPIAddress updates the advertised listen address. The server loop reads
// it on every bind attempt, so a hot restart picks the new address up.
func (a *App) setAPIAddress(address string) {
	a.apiStatusMutex.Lock()
	a.apiStatus.Address = address
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

// GetAPIStatus assembles the server state and the external-operation snapshot
// from two independent locks. The two halves can reflect slightly different
// moments under concurrent requests; each half is internally consistent, and
// consumers gate merges with OperationRevision rather than assuming a single
// atomic instant.
func (a *App) GetAPIStatus() APIStatus {
	a.apiStatusMutex.RLock()
	status := a.apiStatus
	status.Warnings = append([]string{}, a.apiStatus.Warnings...)
	a.apiStatusMutex.RUnlock()
	status.ActiveOperations, status.OperationRevision = a.externalOperationSnapshot()
	return status
}

func (a *App) recordExternalOperation(event externalOperationEvent) externalOperationEvent {
	a.externalOperationMutex.Lock()
	defer a.externalOperationMutex.Unlock()
	if a.activeExternalOperations == nil {
		a.activeExternalOperations = make(map[uint64]string)
	}
	a.externalOperationRevision++
	event.Revision = a.externalOperationRevision
	if event.Phase == "started" {
		a.activeExternalOperations[event.ID] = event.Kind
	} else if event.Phase == "finished" {
		delete(a.activeExternalOperations, event.ID)
	}
	return event
}

func (a *App) externalOperationSnapshot() ([]OperationStatus, uint64) {
	a.externalOperationMutex.RLock()
	defer a.externalOperationMutex.RUnlock()
	operations := make([]OperationStatus, 0, len(a.activeExternalOperations))
	for id, kind := range a.activeExternalOperations {
		operations = append(operations, OperationStatus{ID: id, Kind: kind})
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })
	return operations, a.externalOperationRevision
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

func (a *App) CancelBulkPower() error {
	log.Printf("Requesting cancellation of the active bulk power operation")
	return a.stationManager.CancelBulkPower()
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

// ListBluetoothAdapters returns the Bluetooth radios known to the operating
// system, in enumeration order.
func (a *App) ListBluetoothAdapters() ([]bluetooth.AdapterInfo, error) {
	return bluetooth.ListAdapters()
}

// autoSleepEvent is the frontend payload for the "auto-sleep" event. Phase is
// "started", "completed", "cancelled" (the watched session restarted),
// "skipped" (user was operating Bluetooth) or "failed".

package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
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
	case errors.Is(err, station.ErrBulkOperationTimeout),
		errors.Is(err, station.ErrStationOperationTimeout),
		errors.Is(err, context.DeadlineExceeded):
		status = fiber.StatusRequestTimeout
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

type OperationStatus struct {
	ID   uint64 `json:"id"`
	Kind string `json:"kind"`
}

type APIStatus struct {
	Running           bool              `json:"running"`
	Address           string            `json:"address"`
	Error             string            `json:"error"`
	Warnings          []string          `json:"warnings"`
	ConfigWritable    bool              `json:"configWritable"`
	ActiveOperations  []OperationStatus `json:"activeOperations"`
	OperationRevision uint64            `json:"operationRevision"`
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
	nextID          func() uint64
	snapshotUpdate  func(func() []station.StationInfo) (uint64, []station.StationInfo)
	nextOperationID func() uint64
	started         func(scanEvent)
	completed       func(scanEvent)
	failed          func(scanEvent)
	cancelled       func(scanEvent)
	updated         func(stationUpdateEvent)
	operation       func(externalOperationEvent)
}

// scanEvent ties every external lifecycle notification to one scan request so
// a delayed terminal event cannot replace a newer scan in the desktop UI.
type scanEvent struct {
	ID       uint64                `json:"id"`
	Stations []station.StationInfo `json:"stations,omitempty"`
	Error    string                `json:"error,omitempty"`
}

type stationUpdateEvent struct {
	ID       uint64                `json:"id"`
	Source   string                `json:"source"`
	Stations []station.StationInfo `json:"stations"`
}

type externalOperationEvent struct {
	ID       uint64 `json:"id"`
	Phase    string `json:"phase"`
	Kind     string `json:"kind"`
	Revision uint64 `json:"revision"`
}

// fallbackExternalOperationID keeps concurrent operations distinguishable even
// when a caller registers an operation sink without an ID allocator; a shared
// zero ID would make started/finished events overwrite each other.
var fallbackExternalOperationID atomic.Uint64

func beginExternalOperation(events scanEventCallbacks, kind string) func() {
	var id uint64
	if events.nextOperationID != nil {
		id = events.nextOperationID()
	} else {
		id = fallbackExternalOperationID.Add(1)
	}
	if events.operation != nil {
		events.operation(externalOperationEvent{ID: id, Phase: "started", Kind: kind})
	}
	return func() {
		if events.operation != nil {
			events.operation(externalOperationEvent{ID: id, Phase: "finished", Kind: kind})
		}
	}
}

func emitStationUpdate(events scanEventCallbacks, source string, snapshot func() []station.StationInfo) {
	if events.updated == nil {
		return
	}
	id := uint64(0)
	stations := []station.StationInfo{}
	if events.snapshotUpdate != nil {
		id, stations = events.snapshotUpdate(snapshot)
	} else if snapshot != nil {
		stations = snapshot()
	}
	events.updated(stationUpdateEvent{ID: id, Source: source, Stations: stations})
}

func shouldEmitExternalStationSnapshot(err error) bool {
	// These outcomes are decided before Bluetooth work starts. Besides being a
	// redundant snapshot, an event for a request rejected as busy could arrive
	// after the accepted local scan and overwrite its newer result.
	return err == nil || !(errors.Is(err, station.ErrInvalidArgument) ||
		errors.Is(err, station.ErrNotFound) ||
		errors.Is(err, station.ErrOperationInProgress) ||
		errors.Is(err, station.ErrStationTransitioning))
}

// beginExternalStationOperation publishes executed work's final cached state
// before the matching finished event. Failed Bluetooth work can still change
// connection/error metadata, so its snapshot is just as important as a
// successful operation's snapshot.
func beginExternalStationOperation(
	events scanEventCallbacks,
	kind, source string,
	snapshot func() []station.StationInfo,
	operationErr *error,
) func() {
	finish := beginExternalOperation(events, kind)
	return func() {
		defer finish()
		if operationErr == nil || shouldEmitExternalStationSnapshot(*operationErr) {
			emitStationUpdate(events, source, snapshot)
		}
	}
}

func registerAPIRoutes(api *fiber.App, manager apiStationManager, events scanEventCallbacks, status func() APIStatus) {
	api.Use(func(c *fiber.Ctx) error {
		if len(c.Body()) > apiBodyLimit {
			return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body exceeds the allowed limit")
		}
		return c.Next()
	})
	api.Post("/allon", func(c *fiber.Ctx) error {
		var operationErr error
		finish := beginExternalStationOperation(events, "bulk-power", "http-power", manager.GetStationInfo, &operationErr)
		defer finish()
		result, err := manager.SetAllStationsPowerDetailed("on")
		operationErr = err
		if err != nil {
			return sendAPIError(c, err)
		}
		return c.JSON(result)
	})
	api.Post("/alloff", func(c *fiber.Ctx) error {
		var operationErr error
		finish := beginExternalStationOperation(events, "bulk-power", "http-power", manager.GetStationInfo, &operationErr)
		defer finish()
		result, err := manager.SetAllStationsPowerDetailed("sleep")
		operationErr = err
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
		// Each callback builds its own event value: Started runs on the HTTP
		// goroutine while terminal callbacks run later on the scan goroutine,
		// so a shared mutable event would race between the two.
		err := manager.StartScan(station.ScanCallbacks{
			Started: func() {
				if events.started != nil {
					events.started(scanEvent{ID: id})
				}
			},
			Completed: func(stations []station.StationInfo) {
				if events.completed != nil {
					events.completed(scanEvent{ID: id, Stations: stations})
				}
			},
			Failed: func(err error) {
				if events.failed != nil {
					events.failed(scanEvent{ID: id, Error: err.Error()})
				}
			},
			Cancelled: func() {
				if events.cancelled != nil {
					events.cancelled(scanEvent{ID: id})
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
		var operationErr error
		finish := beginExternalStationOperation(events, "bulk-power", "http-power", manager.GetStationInfo, &operationErr)
		defer finish()
		result, err := manager.SetAllStationsPowerDetailed(request.State)
		operationErr = err
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
		var operationErr error
		finish := beginExternalStationOperation(events, "power", "http-power", manager.GetStationInfo, &operationErr)
		defer finish()
		result, err := manager.SetStationPower(c.Params("address"), request.State)
		operationErr = err
		return sendPowerActionResponse(c, result, err)
	})
	api.Post("/stations/:address/identify", func(c *fiber.Ctx) error {
		var operationErr error
		finish := beginExternalStationOperation(events, "identify", "http-identify", manager.GetStationInfo, &operationErr)
		defer finish()
		operationErr = manager.IdentifyStation(c.Params("address"))
		if operationErr != nil {
			return sendAPIError(c, operationErr)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	api.Post("/stations/:address/refresh", func(c *fiber.Ctx) error {
		var operationErr error
		finish := beginExternalStationOperation(events, "refresh", "http-refresh", manager.GetStationInfo, &operationErr)
		defer finish()
		result, err := manager.RefreshStationCapabilities(c.Params("address"))
		operationErr = err
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
		var operationErr error
		finish := beginExternalStationOperation(events, "channel", "http-channel", manager.GetStationInfo, &operationErr)
		defer finish()
		result, err := manager.SetStationChannel(c.Params("address"), request.Channel, request.AllowUnknownConflictRisk)
		operationErr = err
		return sendChannelActionResponse(c, result, request.Channel, err)
	})
}

// NewApp creates a new App application struct

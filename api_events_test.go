package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

type fakeAPIStationManager struct {
	powerResult station.PowerActionResult

	powerErr error

	bulkResult station.BulkPowerResult

	bulkErr error

	channelResult station.ChannelChangeResult

	channelErr error

	legacyErr error

	scanErr error

	stopScanErr error

	stopCalls int
}

func (f *fakeAPIStationManager) PowerOnAllStations() error { return f.legacyErr }

func (f *fakeAPIStationManager) PowerOffAllStations() error { return f.legacyErr }

func (f *fakeAPIStationManager) GetStationInfo() []station.StationInfo {

	return []station.StationInfo{}

}

func (f *fakeAPIStationManager) StartScan(callbacks station.ScanCallbacks) error {

	if f.legacyErr != nil {

		return f.legacyErr

	}

	if callbacks.Started != nil {

		callbacks.Started()

	}

	if f.scanErr != nil {

		if callbacks.Failed != nil {

			callbacks.Failed(f.scanErr)

		}

		return nil

	}

	if callbacks.Completed != nil {

		callbacks.Completed([]station.StationInfo{})

	}

	return nil

}

func (f *fakeAPIStationManager) StopScan() error {

	f.stopCalls++

	return f.stopScanErr

}

func TestScanEventsAreAlwaysStartedBeforeCompletion(t *testing.T) {

	for _, test := range []struct {
		name string

		scanErr error

		wantSecond string
	}{

		{name: "completed", wantSecond: "completed"},

		{name: "failed", scanErr: errors.New("scan failed immediately"), wantSecond: "failed"},
	} {

		t.Run(test.name, func(t *testing.T) {

			manager := &fakeAPIStationManager{scanErr: test.scanErr}

			order := []string{}

			api := fiber.New()

			registerAPIRoutes(api, manager, scanEventCallbacks{

				nextID: func() uint64 { return 42 },

				started: func(event scanEvent) {

					if event.ID != 42 {

						t.Errorf("started event ID = %d, want 42", event.ID)

					}

					order = append(order, "started")

				},

				completed: func(event scanEvent) {

					if event.ID != 42 {

						t.Errorf("completed event ID = %d, want 42", event.ID)

					}

					order = append(order, "completed")

				},

				failed: func(event scanEvent) {

					if event.ID != 42 || event.Error == "" {

						t.Errorf("failed event = %+v, want ID 42 and error", event)

					}

					order = append(order, "failed")

				},
			}, func() APIStatus { return APIStatus{Running: true} })

			response, err := api.Test(httptest.NewRequest(http.MethodPost, "/scan", nil))

			if err != nil {

				t.Fatal(err)

			}

			_ = response.Body.Close()

			if response.StatusCode != fiber.StatusAccepted {

				t.Fatalf("status = %d, want 202", response.StatusCode)

			}

			if len(order) != 2 || order[0] != "started" || order[1] != test.wantSecond {

				t.Fatalf("event order = %v, want [started %s]", order, test.wantSecond)

			}

		})

	}

}

func TestStopScanAPI(t *testing.T) {

	manager := &fakeAPIStationManager{}

	response, err := testAPI(manager).Test(httptest.NewRequest(http.MethodPost, "/scan/stop", nil))

	if err != nil {

		t.Fatal(err)

	}

	_ = response.Body.Close()

	if response.StatusCode != fiber.StatusNoContent || manager.stopCalls != 1 {

		t.Fatalf("stop response status=%d calls=%d, want 204 and 1", response.StatusCode, manager.stopCalls)

	}

}

func TestHTTPMutationsEmitMonotonicStationUpdates(t *testing.T) {

	manager := &fakeAPIStationManager{}

	api := fiber.New(fiber.Config{ErrorHandler: apiErrorHandler})

	var nextID atomic.Uint64

	updates := []stationUpdateEvent{}

	registerAPIRoutes(api, manager, scanEventCallbacks{

		nextUpdateID: func() uint64 { return nextID.Add(1) },

		updated: func(event stationUpdateEvent) {

			updates = append(updates, event)

		},
	}, func() APIStatus { return APIStatus{Running: true} })

	for _, request := range []*http.Request{

		httptest.NewRequest(http.MethodPost, "/allon", nil),

		httptest.NewRequest(http.MethodPost, "/stations/AA/refresh", nil),
	} {

		response, err := api.Test(request)

		if err != nil {

			t.Fatal(err)

		}

		_ = response.Body.Close()

		if response.StatusCode != fiber.StatusOK {

			t.Fatalf("mutation status = %d, want 200", response.StatusCode)

		}

	}

	if len(updates) != 2 || updates[0].ID != 1 || updates[1].ID != 2 ||

		updates[0].Source != "http-power" || updates[1].Source != "http-refresh" {

		t.Fatalf("station update events = %+v", updates)

	}

}

func TestStationUpdateAllocatesIDBeforeTakingSnapshot(t *testing.T) {

	order := []string{}

	emitStationUpdate(scanEventCallbacks{

		nextUpdateID: func() uint64 {

			order = append(order, "id")

			return 7

		},

		updated: func(event stationUpdateEvent) {

			order = append(order, "event")

			if event.ID != 7 || len(event.Stations) != 1 || event.Stations[0].Address != "AA" {

				t.Fatalf("event = %+v", event)

			}

		},
	}, "http-power", func() []station.StationInfo {

		order = append(order, "snapshot")

		return []station.StationInfo{{Address: "AA"}}

	})

	if strings.Join(order, ",") != "id,snapshot,event" {

		t.Fatalf("update construction order = %v", order)

	}

}

func TestAutoSleepSummaryDoesNotCountSkippedStationsAsFailed(t *testing.T) {

	success, unconfirmed, failed, skipped := summarizeAutoSleepResults([]station.BulkPowerStationResult{

		{Success: true, Confirmed: true},

		{Skipped: true, Reason: "station is booting"},

		{Skipped: true, Success: true, Confirmed: true, Reason: "already at target state"},

		{Success: true, CommandSent: true, Confirmed: false},

		{Error: "connection failed"},
	})

	if success != 2 || unconfirmed != 1 || failed != 1 || skipped != 1 {

		t.Fatalf("summary = success %d, unconfirmed %d, failed %d, skipped %d", success, unconfirmed, failed, skipped)

	}

}

func TestExternalOperationEventsBracketHTTPDeviceWork(t *testing.T) {
	manager := &fakeAPIStationManager{}
	events := make([]externalOperationEvent, 0, 2)
	api := fiber.New()
	registerAPIRoutes(api, manager, scanEventCallbacks{
		nextOperationID: func() uint64 { return 73 },
		operation: func(event externalOperationEvent) {
			events = append(events, event)
		},
	}, func() APIStatus { return APIStatus{} })

	response, err := api.Test(httptest.NewRequest(http.MethodPost, "/allon", nil))
	if err != nil {
		t.Fatalf("allon request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("allon status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	if len(events) != 2 || events[0].ID != 73 || events[1].ID != 73 ||
		events[0].Phase != "started" || events[1].Phase != "finished" ||
		events[0].Kind != "bulk-power" || events[1].Kind != "bulk-power" {
		t.Fatalf("operation events = %+v", events)
	}
}

func TestCancelledAutoSleepEventPreservesPartialResults(t *testing.T) {

	event := cancelledAutoSleepEvent([]station.BulkPowerStationResult{

		{Success: true, Confirmed: true},

		{Success: true, CommandSent: true, Confirmed: false},

		{Skipped: true, Reason: "operation cancelled"},

		{Error: "connection failed"},
	}, "watched process restarted")

	if event.Phase != "cancelled" || event.Success != 1 || event.Unconfirmed != 1 || event.Failed != 1 || event.Skipped != 1 {

		t.Fatalf("cancelled event = %+v", event)

	}

	if event.Error != "watched process restarted" {

		t.Fatalf("cancelled event error = %q", event.Error)

	}

}

func TestScanCancellationAPIIsConflict(t *testing.T) {

	manager := &fakeAPIStationManager{legacyErr: bluetooth.ErrScanCancelled}

	response, err := testAPI(manager).Test(httptest.NewRequest(http.MethodPost, "/scan", nil))

	if err != nil {

		t.Fatal(err)

	}

	defer response.Body.Close()

	if response.StatusCode != fiber.StatusConflict {

		t.Fatalf("scan cancellation status = %d, want %d", response.StatusCode, fiber.StatusConflict)

	}

}

func (f *fakeAPIStationManager) GetScanStatus() station.ScanStatus {

	return station.ScanStatus{State: "idle", Warnings: []string{}}

}

func (f *fakeAPIStationManager) SetAllStationsPowerDetailed(string) (station.BulkPowerResult, error) {

	return f.bulkResult, f.bulkErr

}

func (f *fakeAPIStationManager) SetStationPower(string, string) (station.PowerActionResult, error) {

	if f.powerErr != nil || f.powerResult.Station.Address != "" {

		return f.powerResult, f.powerErr

	}

	return station.PowerActionResult{}, f.legacyErr

}

func (f *fakeAPIStationManager) IdentifyStation(string) error { return f.legacyErr }

func (f *fakeAPIStationManager) RefreshStationCapabilities(string) (station.StationInfo, error) {

	return station.StationInfo{}, f.legacyErr

}

func (f *fakeAPIStationManager) SetStationChannel(string, int, bool) (station.ChannelChangeResult, error) {

	if f.channelErr != nil || f.channelResult.Address != "" {

		return f.channelResult, f.channelErr

	}

	return station.ChannelChangeResult{}, f.legacyErr

}

func testAPI(manager apiStationManager) *fiber.App {

	api := fiber.New(fiber.Config{ErrorHandler: apiErrorHandler})

	registerAPIRoutes(api, manager, scanEventCallbacks{}, func() APIStatus {

		return APIStatus{Running: true, Address: "127.0.0.1:7575"}

	})

	return api

}

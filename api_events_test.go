package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

type fakeAPIStationManager struct {
	stations []station.StationInfo

	powerResult station.PowerActionResult

	powerErr error

	bulkResult station.BulkPowerResult

	bulkErr error

	channelResult station.ChannelChangeResult

	channelErr error

	legacyErr error

	scanErr error

	scanStatusID uint64

	stopScanErr error

	stopCalls int
}

func (f *fakeAPIStationManager) PowerOnAllStations() error { return f.legacyErr }

func (f *fakeAPIStationManager) PowerOffAllStations() error { return f.legacyErr }

func (f *fakeAPIStationManager) GetStationInfo() []station.StationInfo {

	return append([]station.StationInfo(nil), f.stations...)

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

			callbacks.Failed(f.scanStatusID, f.scanErr)

		}

		return nil

	}

	if callbacks.Completed != nil {

		callbacks.Completed(f.scanStatusID, []station.StationInfo{})

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

// TestScanTerminalEventsCarryStatusIdentity pins the scan-status identity in
// terminal events: a recovery read of GetScanStatus can only be matched to
// the finished scan when the event carries the manager's status ID.
func TestScanTerminalEventsCarryStatusIdentity(t *testing.T) {

	for _, test := range []struct {
		name string

		scanErr error
	}{

		{name: "completed"},

		{name: "failed", scanErr: errors.New("scan failed immediately")},
	} {

		t.Run(test.name, func(t *testing.T) {

			manager := &fakeAPIStationManager{scanErr: test.scanErr, scanStatusID: 7}

			var terminalEvent scanEvent

			terminalSeen := false

			api := fiber.New()

			registerAPIRoutes(api, manager, scanEventCallbacks{

				nextID: func() uint64 { return 42 },

				completed: func(event scanEvent) {

					terminalEvent, terminalSeen = event, true

				},

				failed: func(event scanEvent) {

					terminalEvent, terminalSeen = event, true

				},
			}, func() APIStatus { return APIStatus{Running: true} })

			response, err := api.Test(httptest.NewRequest(http.MethodPost, "/scan", nil))

			if err != nil {

				t.Fatal(err)

			}

			_ = response.Body.Close()

			if !terminalSeen {

				t.Fatal("no terminal scan event was emitted")

			}

			if terminalEvent.ID != 42 || terminalEvent.StatusID != 7 {

				t.Fatalf("terminal event = %+v, want ID 42 and StatusID 7", terminalEvent)

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

	sequencer := &App{}

	updates := []stationUpdateEvent{}

	registerAPIRoutes(api, manager, scanEventCallbacks{

		snapshotUpdate: sequencer.snapshotExternalStationUpdate,

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

func TestStationUpdateUsesSnapshotSequencer(t *testing.T) {

	order := []string{}

	emitStationUpdate(scanEventCallbacks{

		snapshotUpdate: func(snapshot func() []station.StationInfo) (uint64, []station.StationInfo) {

			order = append(order, "sequence")

			return 7, snapshot()

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

	if strings.Join(order, ",") != "sequence,snapshot,event" {

		t.Fatalf("update construction order = %v", order)

	}

}

func TestConcurrentStationUpdateIDsFollowSnapshotCompletionOrder(t *testing.T) {
	var snapshotOrder atomic.Uint64
	sequencer := &App{}
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	secondInvoked := make(chan struct{})
	releaseFirst := make(chan struct{})
	updates := make(chan stationUpdateEvent, 2)
	events := scanEventCallbacks{
		snapshotUpdate: sequencer.snapshotExternalStationUpdate,
		updated:        func(event stationUpdateEvent) { updates <- event },
	}

	go emitStationUpdate(events, "first", func() []station.StationInfo {
		close(firstStarted)
		<-releaseFirst
		sequence := snapshotOrder.Add(1)
		return []station.StationInfo{{Address: fmt.Sprintf("snapshot-%d", sequence)}}
	})
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first station snapshot did not start")
	}

	go func() {
		close(secondInvoked)
		emitStationUpdate(events, "second", func() []station.StationInfo {
			close(secondStarted)
			sequence := snapshotOrder.Add(1)
			return []station.StationInfo{{Address: fmt.Sprintf("snapshot-%d", sequence)}}
		})
	}()
	<-secondInvoked

	collected := make([]stationUpdateEvent, 0, 2)
	select {
	case <-secondStarted:
		// The old implementation allows the later call to finish its snapshot
		// while the lower-ID call is still blocked.
		select {
		case event := <-updates:
			collected = append(collected, event)
		case <-time.After(time.Second):
			t.Fatal("second station update did not finish")
		}
		close(releaseFirst)
	case <-time.After(100 * time.Millisecond):
		// A sequenced implementation keeps the second snapshot outside the
		// transaction until the first snapshot and ID are committed together.
		close(releaseFirst)
	}

	deadline := time.After(time.Second)
	for len(collected) < 2 {
		select {
		case event := <-updates:
			collected = append(collected, event)
		case <-deadline:
			t.Fatalf("received %d station update(s), want 2", len(collected))
		}
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].ID < collected[j].ID })
	if collected[0].Stations[0].Address != "snapshot-1" || collected[1].Stations[0].Address != "snapshot-2" {
		t.Fatalf("updates ordered by ID = %+v, want snapshot completion order", collected)
	}
}

func TestConcurrentExternalOperationEventsFollowRevisionOrder(t *testing.T) {
	app := NewApp()
	firstEmitStarted := make(chan struct{})
	secondInvoked := make(chan struct{})
	releaseFirstEmit := make(chan struct{})
	emitted := make(chan externalOperationEvent, 2)
	dispatch := func(event externalOperationEvent, block bool) {
		app.dispatchExternalOperation(event, func(sequenced externalOperationEvent) {
			if block {
				close(firstEmitStarted)
				<-releaseFirstEmit
			}
			emitted <- sequenced
		})
	}

	go dispatch(externalOperationEvent{ID: 1, Phase: "started", Kind: "power"}, true)
	select {
	case <-firstEmitStarted:
	case <-time.After(time.Second):
		t.Fatal("first external-operation event did not reach emission")
	}
	go func() {
		close(secondInvoked)
		dispatch(externalOperationEvent{ID: 2, Phase: "started", Kind: "identify"}, false)
	}()
	<-secondInvoked

	collected := make([]externalOperationEvent, 0, 2)
	select {
	case event := <-emitted:
		collected = append(collected, event)
		close(releaseFirstEmit)
	case <-time.After(100 * time.Millisecond):
		close(releaseFirstEmit)
	}
	deadline := time.After(time.Second)
	for len(collected) < 2 {
		select {
		case event := <-emitted:
			collected = append(collected, event)
		case <-deadline:
			t.Fatalf("received %d external-operation event(s), want 2", len(collected))
		}
	}
	if collected[0].Revision != 1 || collected[1].Revision != 2 {
		t.Fatalf("external-operation revision delivery = %d then %d, want 1 then 2", collected[0].Revision, collected[1].Revision)
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

func TestHTTPDeviceOperationsAlwaysEmitFinalSnapshotBeforeFinish(t *testing.T) {
	failure := errors.New("device operation failed")
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		source     string
		wantStatus int
		configure  func(*fakeAPIStationManager)
	}{
		{
			name:       "bulk power alias failure",
			method:     http.MethodPost,
			path:       "/allon",
			source:     "http-power",
			wantStatus: fiber.StatusInternalServerError,
			configure:  func(manager *fakeAPIStationManager) { manager.bulkErr = failure },
		},
		{
			name:       "bulk power failure",
			method:     http.MethodPost,
			path:       "/stations/power",
			body:       `{"state":"sleep"}`,
			source:     "http-power",
			wantStatus: fiber.StatusInternalServerError,
			configure:  func(manager *fakeAPIStationManager) { manager.bulkErr = failure },
		},
		{
			name:       "single power pre-write failure",
			method:     http.MethodPost,
			path:       "/stations/AA/power",
			body:       `{"state":"on"}`,
			source:     "http-power",
			wantStatus: fiber.StatusInternalServerError,
			configure:  func(manager *fakeAPIStationManager) { manager.legacyErr = failure },
		},
		{
			name:       "identify success",
			method:     http.MethodPost,
			path:       "/stations/AA/identify",
			source:     "http-identify",
			wantStatus: fiber.StatusNoContent,
		},
		{
			name:       "identify failure",
			method:     http.MethodPost,
			path:       "/stations/AA/identify",
			source:     "http-identify",
			wantStatus: fiber.StatusInternalServerError,
			configure:  func(manager *fakeAPIStationManager) { manager.legacyErr = failure },
		},
		{
			name:       "refresh failure",
			method:     http.MethodPost,
			path:       "/stations/AA/refresh",
			source:     "http-refresh",
			wantStatus: fiber.StatusInternalServerError,
			configure:  func(manager *fakeAPIStationManager) { manager.legacyErr = failure },
		},
		{
			name:       "channel pre-write failure",
			method:     http.MethodPut,
			path:       "/stations/AA/channel",
			body:       `{"channel":5}`,
			source:     "http-channel",
			wantStatus: fiber.StatusInternalServerError,
			configure:  func(manager *fakeAPIStationManager) { manager.legacyErr = failure },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &fakeAPIStationManager{stations: []station.StationInfo{{Address: "AA"}}}
			if test.configure != nil {
				test.configure(manager)
			}
			order := []string{}
			api := fiber.New(fiber.Config{ErrorHandler: apiErrorHandler})
			registerAPIRoutes(api, manager, scanEventCallbacks{
				operation: func(event externalOperationEvent) {
					order = append(order, "operation:"+event.Phase)
				},
				updated: func(event stationUpdateEvent) {
					if event.Source != test.source || len(event.Stations) != 1 || event.Stations[0].Address != "AA" {
						t.Fatalf("station update = %+v", event)
					}
					order = append(order, "update")
				},
			}, func() APIStatus { return APIStatus{} })

			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := api.Test(request)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			_ = response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			if got := strings.Join(order, ","); got != "operation:started,update,operation:finished" {
				t.Fatalf("event order = %q", got)
			}
		})
	}
}

func TestHTTPDevicePreflightRejectionsDoNotPublishSnapshots(t *testing.T) {
	for _, rejection := range []error{
		station.ErrInvalidArgument,
		station.ErrNotFound,
		station.ErrOperationInProgress,
		station.ErrStationTransitioning,
	} {
		t.Run(rejection.Error(), func(t *testing.T) {
			manager := &fakeAPIStationManager{
				stations: []station.StationInfo{{Address: "AA"}},
				bulkErr:  rejection,
			}
			order := []string{}
			api := fiber.New(fiber.Config{ErrorHandler: apiErrorHandler})
			registerAPIRoutes(api, manager, scanEventCallbacks{
				operation: func(event externalOperationEvent) { order = append(order, event.Phase) },
				updated:   func(stationUpdateEvent) { order = append(order, "update") },
			}, func() APIStatus { return APIStatus{} })

			response, err := api.Test(httptest.NewRequest(http.MethodPost, "/allon", nil))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			_ = response.Body.Close()
			if response.StatusCode != apiStatusForError(rejection) {
				t.Fatalf("status = %d, want %d", response.StatusCode, apiStatusForError(rejection))
			}
			if got := strings.Join(order, ","); got != "started,finished" {
				t.Fatalf("event order = %q", got)
			}
		})
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

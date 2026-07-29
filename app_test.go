package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

type fakeAPIStationManager struct {
	powerResult   station.PowerActionResult
	powerErr      error
	bulkResult    station.BulkPowerResult
	bulkErr       error
	channelResult station.ChannelChangeResult
	channelErr    error
	legacyErr     error
	scanErr       error
	stopScanErr   error
	stopCalls     int
}

func (f *fakeAPIStationManager) PowerOnAllStations() error  { return f.legacyErr }
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
		name       string
		scanErr    error
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
	return f.powerResult, f.powerErr
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

func TestAPIStatusForError(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{station.ErrInvalidArgument, fiber.StatusBadRequest},
		{station.ErrNotFound, fiber.StatusNotFound},
		{station.ErrOperationInProgress, fiber.StatusConflict},
		{station.ErrChannelConflict, fiber.StatusConflict},
		{station.ErrScanRequired, fiber.StatusConflict},
		{bluetooth.ErrScanCancelled, fiber.StatusConflict},
		{station.ErrUnsupported, fiber.StatusUnprocessableEntity},
		{station.ErrShuttingDown, fiber.StatusServiceUnavailable},
		{fmt.Errorf("BLE failure"), fiber.StatusInternalServerError},
	}
	for _, test := range tests {
		if got := apiStatusForError(fmt.Errorf("wrapped: %w", test.err)); got != test.want {
			t.Errorf("apiStatusForError(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestNewAppReportsConfiguredAPIAddress(t *testing.T) {
	app := NewApp()
	status := app.GetAPIStatus()
	if status.Running || status.Address != "127.0.0.1:7575" || status.Error != "" ||
		!status.ConfigWritable || len(status.Warnings) != 0 {
		t.Fatalf("initial API status = %+v", status)
	}
	if timeout := app.api.Config().WriteTimeout; timeout != 0 {
		t.Fatalf("WriteTimeout = %v, want no premature timeout for Bluetooth operations", timeout)
	}
}

func TestConfigLoadWarningIsExposedAndDefensivelyCopied(t *testing.T) {
	app := NewApp()
	app.setConfigLoadStatus(errors.New("invalid JSON"))

	status := app.GetAPIStatus()
	if !status.ConfigWritable || len(status.Warnings) != 1 ||
		!strings.Contains(status.Warnings[0], "invalid JSON") {
		t.Fatalf("config load status = %+v", status)
	}
	status.Warnings[0] = "changed"
	if current := app.GetAPIStatus(); current.Warnings[0] == "changed" {
		t.Fatal("GetAPIStatus returned mutable warning storage")
	}
}

func TestAPIServerRetriesFixedAddressAfterInitialBindFailure(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.apiStatus.Address = blocker.Addr().String()
	app.apiRetryDelay = 10 * time.Millisecond
	app.startAPIServer()
	t.Cleanup(func() {
		app.apiLifecycleMutex.Lock()
		cancel := app.apiCancel
		app.apiCancel = nil
		app.apiLifecycleMutex.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = app.api.Shutdown()
		app.apiWG.Wait()
	})

	deadline := time.Now().Add(time.Second)
	for app.GetAPIStatus().Error == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := app.GetAPIStatus(); status.Running || status.Error == "" {
		t.Fatalf("status while address occupied = %+v", status)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for !app.GetAPIStatus().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := app.GetAPIStatus(); !status.Running || status.Error != "" {
		t.Fatalf("status after address became available = %+v", status)
	}
}

func TestAPIServerRecoversFromUnexpectedServeExit(t *testing.T) {
	for _, test := range []struct {
		name string
		fail func() error
	}{
		{name: "closed listener", fail: func() error { return net.ErrClosed }},
		{name: "panic", fail: func() error { panic("serve failed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := reservation.Addr().String()
			if err := reservation.Close(); err != nil {
				t.Fatal(err)
			}

			app := NewApp()
			app.apiStatus.Address = address
			app.apiRetryDelay = 5 * time.Millisecond
			var serveCalls atomic.Int32
			app.serveListener = func(listener net.Listener) error {
				if serveCalls.Add(1) == 1 {
					return test.fail()
				}
				return app.api.Listener(listener)
			}
			app.startAPIServer()
			t.Cleanup(func() {
				app.apiLifecycleMutex.Lock()
				cancel := app.apiCancel
				app.apiCancel = nil
				app.apiLifecycleMutex.Unlock()
				if cancel != nil {
					cancel()
				}
				_ = app.api.Shutdown()
				app.apiWG.Wait()
			})

			deadline := time.Now().Add(time.Second)
			for (serveCalls.Load() < 2 || !app.GetAPIStatus().Running) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if status := app.GetAPIStatus(); serveCalls.Load() < 2 || !status.Running || status.Error != "" {
				t.Fatalf("API did not recover after %s: calls=%d status=%+v", test.name, serveCalls.Load(), status)
			}
		})
	}
}

func TestAPIServerRetryStopsDuringShutdown(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	app := NewApp()
	app.apiStatus.Address = blocker.Addr().String()
	app.apiRetryDelay = time.Hour
	app.startAPIServer()
	deadline := time.Now().Add(time.Second)
	for app.GetAPIStatus().Error == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	app.apiLifecycleMutex.Lock()
	cancel := app.apiCancel
	app.apiCancel = nil
	app.apiLifecycleMutex.Unlock()
	cancel()
	done := make(chan struct{})
	go func() {
		app.apiWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("API retry loop did not stop after cancellation")
	}
}

func TestAPIServerCancellationAtSuccessfulBindCannotStrandListener(t *testing.T) {
	app := NewApp()
	app.listen = func(string, string) (net.Listener, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		app.apiLifecycleMutex.Lock()
		cancel := app.apiCancel
		app.apiLifecycleMutex.Unlock()
		cancel()
		return listener, nil
	}
	app.startAPIServer()
	done := make(chan struct{})
	go func() {
		app.apiWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = app.api.Shutdown()
		t.Fatal("API listener remained active when cancellation raced with bind")
	}
	if status := app.GetAPIStatus(); status.Running {
		t.Fatalf("API status after cancellation = %+v", status)
	}
}

func TestAPIServerServesRegisteredRoutesOverLoopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.apiStatus.Address = listener.Addr().String()
	registerAPIRoutes(app.api, &fakeAPIStationManager{}, scanEventCallbacks{}, app.GetAPIStatus)
	app.listen = func(string, string) (net.Listener, error) {
		return listener, nil
	}
	app.startAPIServer()
	t.Cleanup(func() {
		app.apiLifecycleMutex.Lock()
		cancel := app.apiCancel
		app.apiCancel = nil
		app.apiLifecycleMutex.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = app.api.Shutdown()
		app.apiWG.Wait()
	})

	deadline := time.Now().Add(time.Second)
	for !app.GetAPIStatus().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := app.GetAPIStatus(); !status.Running {
		t.Fatalf("API status = %+v, want running", status)
	}

	response, err := http.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("loopback health request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.StatusCode)
	}
	var status APIStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if !status.Running || status.Address != listener.Addr().String() || status.Error != "" {
		t.Fatalf("health response = %+v, want running loopback status", status)
	}
}

func TestPowerActionConfirmationFailureReturnsStructuredSuccess(t *testing.T) {
	confirmationErr := &bluetooth.PowerConfirmationError{
		Target: bluetooth.PowerStateOn,
		Actual: bluetooth.PowerStateBooting,
		Raw:    0x01,
		Err:    errors.New("readback timed out"),
	}
	expected := station.PowerActionResult{
		CommandSent:       true,
		Confirmed:         false,
		ConfirmationError: confirmationErr.Error(),
	}
	api := fiber.New()
	api.Post("/power", func(c *fiber.Ctx) error {
		return sendPowerActionResponse(c, expected, confirmationErr)
	})

	response, err := api.Test(httptest.NewRequest("POST", "/power", nil))
	if err != nil {
		t.Fatalf("power response request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("power response status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var actual station.PowerActionResult
	if err := json.NewDecoder(response.Body).Decode(&actual); err != nil {
		t.Fatalf("decode power response: %v", err)
	}
	if !actual.CommandSent || actual.Confirmed || actual.ConfirmationError != expected.ConfirmationError {
		t.Fatalf("power response = %+v, want %+v", actual, expected)
	}
}

func TestPowerActionFailureBeforeWriteReturnsErrorStatus(t *testing.T) {
	api := fiber.New()
	api.Post("/power", func(c *fiber.Ctx) error {
		return sendPowerActionResponse(c, station.PowerActionResult{}, station.ErrUnsupported)
	})

	response, err := api.Test(httptest.NewRequest("POST", "/power", nil))
	if err != nil {
		t.Fatalf("power response request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusUnprocessableEntity {
		t.Fatalf("power response status = %d, want %d", response.StatusCode, fiber.StatusUnprocessableEntity)
	}
}

func TestLegacyPowerActionAcceptsSentButUnconfirmedCommand(t *testing.T) {
	confirmationErr := &bluetooth.PowerConfirmationError{
		Target: bluetooth.PowerStateOn,
		Actual: bluetooth.PowerStateBooting,
		Raw:    0x01,
		Err:    errors.New("readback timed out"),
	}
	if err := legacyPowerActionError(
		"on",
		"AA",
		station.PowerActionResult{CommandSent: true, Confirmed: false},
		confirmationErr,
	); err != nil {
		t.Fatalf("sent legacy command error = %v, want nil", err)
	}
	preWriteErr := errors.New("write failed")
	if err := legacyPowerActionError("on", "AA", station.PowerActionResult{}, preWriteErr); !errors.Is(err, preWriteErr) {
		t.Fatalf("pre-write legacy error = %v, want %v", err, preWriteErr)
	}
}

func TestRegisteredPowerRoutePreservesConfirmationResult(t *testing.T) {
	manager := &fakeAPIStationManager{
		powerResult: station.PowerActionResult{
			CommandSent:       true,
			Confirmed:         false,
			ConfirmationError: "readback timed out",
		},
		powerErr: &bluetooth.PowerConfirmationError{
			Target: bluetooth.PowerStateOn,
			Actual: bluetooth.PowerStateBooting,
			Raw:    1,
			Err:    errors.New("readback timed out"),
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/stations/AA/power", strings.NewReader(`{"state":"on"}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := testAPI(manager).Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var result station.PowerActionResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.CommandSent || result.Confirmed || result.ConfirmationError == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestChannelResultForWailsPreservesSentUnconfirmedResult(t *testing.T) {
	expected := station.ChannelChangeResult{
		Address:           "AA",
		PreviousChannel:   3,
		Channel:           0,
		CommandSent:       true,
		Confirmed:         false,
		ConfirmationError: "readback timed out",
	}
	result, err := channelResultForWails(expected, errors.New("readback timed out"))
	if err != nil || result.Address != expected.Address || !result.CommandSent || result.Confirmed ||
		result.ConfirmationError != expected.ConfirmationError {
		t.Fatalf("channelResultForWails() = %+v, %v; want %+v, nil", result, err, expected)
	}

	preWriteErr := errors.New("write failed")
	if _, err := channelResultForWails(station.ChannelChangeResult{}, preWriteErr); !errors.Is(err, preWriteErr) {
		t.Fatalf("pre-write error = %v, want %v", err, preWriteErr)
	}
}

func TestChannelAPIReturnsStructuredSuccessWhenCommandWasSentButUnconfirmed(t *testing.T) {
	manager := &fakeAPIStationManager{
		channelResult: station.ChannelChangeResult{
			Address:           "AA",
			PreviousChannel:   3,
			Channel:           0,
			CommandSent:       true,
			Confirmed:         false,
			ConfirmationError: "readback timed out",
			Warnings:          []string{"command was sent"},
		},
		channelErr: errors.New("readback timed out"),
	}
	request := httptest.NewRequest(http.MethodPut, "/stations/AA/channel", strings.NewReader(`{"channel":5}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := testAPI(manager).Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var body struct {
		CommandSent       bool     `json:"commandSent"`
		Confirmed         bool     `json:"confirmed"`
		ConfirmationError string   `json:"confirmationError"`
		Warnings          []string `json:"warnings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.CommandSent || body.Confirmed || body.ConfirmationError != "readback timed out" || len(body.Warnings) != 1 {
		t.Fatalf("structured channel error = %+v", body)
	}
}

func TestRegisteredRoutesKeepLegacyAliases(t *testing.T) {
	for _, test := range []struct {
		path   string
		target string
	}{
		{path: "/allon", target: "on"},
		{path: "/alloff", target: "sleep"},
	} {
		manager := &fakeAPIStationManager{bulkResult: station.BulkPowerResult{
			Target: test.target,
			Results: []station.BulkPowerStationResult{{
				Address: "AA", Skipped: true, Reason: "station is booting",
			}},
		}}
		response, err := testAPI(manager).Test(httptest.NewRequest(http.MethodPost, test.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusOK {
			t.Fatalf("%s status = %d, want 200", test.path, response.StatusCode)
		}
		var result station.BulkPowerResult
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Target != test.target || len(result.Results) != 1 ||
			!result.Results[0].Skipped || result.Results[0].Reason != "station is booting" {
			t.Fatalf("%s result = %+v", test.path, result)
		}
		_ = response.Body.Close()
	}
}

func TestRegisteredRoutesMapFunctionalErrors(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		err    error
		want   int
	}{
		{"duplicate scan", http.MethodPost, "/scan", "", station.ErrOperationInProgress, fiber.StatusConflict},
		{"identify unsupported", http.MethodPost, "/stations/AA/identify", "", station.ErrUnsupported, fiber.StatusUnprocessableEntity},
		{"refresh missing", http.MethodPost, "/stations/AA/refresh", "", station.ErrNotFound, fiber.StatusNotFound},
		{"channel conflict", http.MethodPut, "/stations/AA/channel", `{"channel":4}`, station.ErrChannelConflict, fiber.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &fakeAPIStationManager{legacyErr: test.err}
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			}
			response, err := testAPI(manager).Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.want)
			}
		})
	}
}

func TestBulkRoutePreservesPartialResults(t *testing.T) {
	manager := &fakeAPIStationManager{bulkResult: station.BulkPowerResult{
		Target: "on",
		Results: []station.BulkPowerStationResult{
			{Address: "AA", Success: true, CommandSent: true, Confirmed: true},
			{Address: "BB", Error: "connection failed"},
		},
	}}
	request := httptest.NewRequest(http.MethodPost, "/stations/power", strings.NewReader(`{"state":"on"}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := testAPI(manager).Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var result station.BulkPowerResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || !result.Results[0].Success || result.Results[1].Error == "" {
		t.Fatalf("partial bulk result = %+v", result)
	}
}

func TestAPIBodyLimit(t *testing.T) {
	body := `{"state":"` + strings.Repeat("x", 17*1024) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/stations/power", strings.NewReader(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := testAPI(&fakeAPIStationManager{}).Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusRequestEntityTooLarge)
	}
	if !strings.HasPrefix(response.Header.Get(fiber.HeaderContentType), fiber.MIMEApplicationJSON) {
		t.Fatalf("Content-Type = %q, want JSON", response.Header.Get(fiber.HeaderContentType))
	}
	var responseBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&responseBody); err != nil {
		t.Fatalf("decode body-limit response: %v", err)
	}
	if responseBody.Error == "" {
		t.Fatal("body-limit response omitted error message")
	}
}

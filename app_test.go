package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

type fakeAPIStationManager struct {
	powerResult station.PowerActionResult
	powerErr    error
	bulkResult  station.BulkPowerResult
	bulkErr     error
	legacyErr   error
}

func (f *fakeAPIStationManager) PowerOnAllStations() error  { return f.legacyErr }
func (f *fakeAPIStationManager) PowerOffAllStations() error { return f.legacyErr }
func (f *fakeAPIStationManager) GetStationInfo() []station.StationInfo {
	return []station.StationInfo{}
}
func (f *fakeAPIStationManager) StartScan(callback func([]station.StationInfo, error)) error {
	if f.legacyErr == nil {
		callback([]station.StationInfo{}, nil)
	}
	return f.legacyErr
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
	return station.ChannelChangeResult{}, f.legacyErr
}

func testAPI(manager apiStationManager) *fiber.App {
	api := fiber.New(fiber.Config{BodyLimit: 16 * 1024})
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
	if status.Running || status.Address != "127.0.0.1:7575" || status.Error != "" {
		t.Fatalf("initial API status = %+v", status)
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

func TestRegisteredRoutesKeepLegacyAliases(t *testing.T) {
	for _, path := range []string{"/allon", "/alloff"} {
		response, err := testAPI(&fakeAPIStationManager{}).Test(httptest.NewRequest(http.MethodPost, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}
}

func TestAPIBodyLimit(t *testing.T) {
	body := `{"state":"` + strings.Repeat("x", 17*1024) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/stations/power", strings.NewReader(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := testAPI(&fakeAPIStationManager{}).Test(request)
	if err != nil {
		if strings.Contains(err.Error(), "body size exceeds") {
			return
		}
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusRequestEntityTooLarge)
	}
}

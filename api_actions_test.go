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

func TestPowerActionConfirmationFailureReturnsStructuredSuccess(t *testing.T) {

	confirmationErr := &bluetooth.PowerConfirmationError{

		Target: bluetooth.PowerStateOn,

		Actual: bluetooth.PowerStateBooting,

		Raw: 0x01,

		Err: errors.New("readback timed out"),
	}

	expected := station.PowerActionResult{

		CommandSent: true,

		Confirmed: false,

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

		Raw: 0x01,

		Err: errors.New("readback timed out"),
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

			CommandSent: true,

			Confirmed: false,

			ConfirmationError: "readback timed out",
		},

		powerErr: &bluetooth.PowerConfirmationError{

			Target: bluetooth.PowerStateOn,

			Actual: bluetooth.PowerStateBooting,

			Raw: 1,

			Err: errors.New("readback timed out"),
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

func TestPowerResultForWailsPreservesSentUnconfirmedResult(t *testing.T) {

	confirmationErr := &bluetooth.PowerConfirmationError{

		Target: bluetooth.PowerStateOn,

		Actual: bluetooth.PowerStateBooting,

		Raw: 0x01,

		Err: errors.New("readback timed out"),
	}

	expected := station.PowerActionResult{

		Station: station.StationInfo{
			Address: "AA:BB:CC:DD:EE:FF",
		},

		CommandSent: true,

		Confirmed: false,

		ConfirmationError: confirmationErr.Error(),
	}

	result, err := powerResultForWails(expected, confirmationErr)

	if err != nil || !result.CommandSent || result.Confirmed ||

		result.ConfirmationError != expected.ConfirmationError ||

		result.Station.Address != expected.Station.Address {

		t.Fatalf("powerResultForWails() = %+v, %v; want %+v, nil", result, err, expected)

	}

}

func TestPowerResultForWailsKeepsErrorWhenCommandWasNotSent(t *testing.T) {

	confirmationErr := &bluetooth.PowerConfirmationError{

		Target: bluetooth.PowerStateOn,

		Actual: bluetooth.PowerStateUnknown,

		Raw: -1,

		Err: errors.New("readback timed out"),
	}

	result, err := powerResultForWails(station.PowerActionResult{}, confirmationErr)

	if !errors.Is(err, confirmationErr) {

		t.Fatalf("powerResultForWails() error = %v, want %v", err, confirmationErr)

	}

	if result.CommandSent || result.Confirmed || result.Station.Address != "" {

		t.Fatalf("powerResultForWails() result = %+v, want an empty unstructured result", result)

	}

	preWriteErr := errors.New("write failed")

	if _, err := powerResultForWails(station.PowerActionResult{}, preWriteErr); !errors.Is(err, preWriteErr) {

		t.Fatalf("pre-write error = %v, want %v", err, preWriteErr)

	}

}

func TestChannelResultForWailsPreservesSentUnconfirmedResult(t *testing.T) {

	expected := station.ChannelChangeResult{

		Address: "AA",

		PreviousChannel: 3,

		Channel: 0,

		CommandSent: true,

		Confirmed: false,

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

			Address: "AA",

			PreviousChannel: 3,

			Channel: 0,

			CommandSent: true,

			Confirmed: false,

			ConfirmationError: "readback timed out",

			Warnings: []string{"command was sent"},
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
		CommandSent bool `json:"commandSent"`

		Confirmed bool `json:"confirmed"`

		ConfirmationError string `json:"confirmationError"`

		Warnings []string `json:"warnings"`
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
		path string

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
		name string

		method string

		path string

		body string

		err error

		want int
	}{

		{"duplicate scan", http.MethodPost, "/scan", "", station.ErrOperationInProgress, fiber.StatusConflict},

		{"identify unsupported", http.MethodPost, "/stations/AA/identify", "", station.ErrUnsupported, fiber.StatusUnprocessableEntity},

		{"refresh missing", http.MethodPost, "/stations/AA/refresh", "", station.ErrNotFound, fiber.StatusNotFound},

		{"channel conflict", http.MethodPut, "/stations/AA/channel", `{"channel":4}`, station.ErrChannelConflict, fiber.StatusConflict},

		{"channel booting", http.MethodPut, "/stations/AA/channel", `{"channel":4}`,

			fmt.Errorf("station is booting: %w", station.ErrStationTransitioning), fiber.StatusLocked},

		{"power transitioning", http.MethodPost, "/stations/AA/power", `{"state":"on"}`,

			fmt.Errorf("station is booting: %w", station.ErrStationTransitioning), fiber.StatusLocked},
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

	if got := NewApp().api.Config().BodyLimit; got != apiBodyLimit {

		t.Fatalf("Fiber BodyLimit = %d, want %d", got, apiBodyLimit)

	}

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

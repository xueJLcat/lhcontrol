package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	// A post-send failure that is not a confirmation error (for example a
	// cleanup failure after the write) must not be reported as success.
	postSendErr := errors.New("post-send cleanup failed")

	if err := legacyPowerActionError("on", "AA", station.PowerActionResult{CommandSent: true}, postSendErr); !errors.Is(err, postSendErr) {

		t.Fatalf("post-send legacy error = %v, want %v", err, postSendErr)

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

// TestStationRoutesDecodePercentEncodedAddress guards the RFC 3986 equivalent
// spelling of station addresses: many HTTP clients percent-encode ':' when
// quoting a path segment, and Fiber extracts route params from the raw path.
// The handlers must decode the segment before the station lookup instead of
// rejecting a known station with 404.
func TestStationRoutesDecodePercentEncodedAddress(t *testing.T) {

	for _, test := range []struct {
		name string

		method string

		path string

		body string

		send func(manager *fakeAPIStationManager)
	}{

		{
			name: "power", method: http.MethodPost, path: "/stations/AA%3ABB%3ACC%3ADD%3AEE%3AFF/power", body: `{"state":"on"}`,
			send: func(manager *fakeAPIStationManager) { manager.powerResult = station.PowerActionResult{CommandSent: true, Confirmed: true} },
		},
		{name: "identify", method: http.MethodPost, path: "/stations/AA%3ABB%3ACC%3ADD%3AEE%3AFF/identify", body: ""},
		{name: "refresh", method: http.MethodPost, path: "/stations/AA%3ABB%3ACC%3ADD%3AEE%3AFF/refresh", body: ""},
		{
			name: "channel", method: http.MethodPut, path: "/stations/AA%3ABB%3ACC%3ADD%3AEE%3AFF/channel", body: `{"channel":5}`,
			send: func(manager *fakeAPIStationManager) { manager.channelResult = station.ChannelChangeResult{Address: "AA:BB:CC:DD:EE:FF", Channel: 5, Confirmed: true} },
		},
	} {
		t.Run(test.name, func(t *testing.T) {

			manager := &fakeAPIStationManager{}
			if test.send != nil {
				test.send(manager)
			}
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := testAPI(manager).Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if manager.lastAddress != "AA:BB:CC:DD:EE:FF" {
				t.Fatalf("station lookup address = %q, want the decoded percent-encoded segment", manager.lastAddress)
			}
		})
	}

	// A raw colon address keeps working unchanged.
	manager := &fakeAPIStationManager{powerResult: station.PowerActionResult{CommandSent: true, Confirmed: true}}
	request := httptest.NewRequest(http.MethodPost, "/stations/AA:BB:CC:DD:EE:FF/power", strings.NewReader(`{"state":"on"}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := testAPI(manager).Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if manager.lastAddress != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("station lookup address = %q, want the raw segment", manager.lastAddress)
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

// TestChannelErrorResponseKeepsResultFieldNames locks the failure shape of the
// channel endpoint to the success shape: every ChannelChangeResult field keeps
// its exact JSON name (including "channel"), with error and expectedChannel as
// additive diagnostics. A divergent name (a previous revision used
// "actualChannel" only in errors) breaks clients that parse both shapes.
func TestChannelErrorResponseKeepsResultFieldNames(t *testing.T) {

	manager := &fakeAPIStationManager{

		channelErr:    station.ErrChannelConflict,
		channelResult: station.ChannelChangeResult{Address: "AA:BB:CC:DD:EE:FF", PreviousChannel: 3, Channel: 3, Warnings: []string{"conflict risk"}},
	}

	request := httptest.NewRequest(http.MethodPut, "/stations/AA:BB:CC:DD:EE:FF/channel", strings.NewReader(`{"channel":4}`))

	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	response, err := testAPI(manager).Test(request)

	if err != nil {

		t.Fatal(err)

	}

	defer response.Body.Close()

	if response.StatusCode != fiber.StatusConflict {

		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusConflict)

	}

	var fields map[string]json.RawMessage

	if err := json.NewDecoder(response.Body).Decode(&fields); err != nil {

		t.Fatal(err)

	}

	for _, required := range []string{"error", "expectedChannel", "address", "previousChannel", "channel", "commandSent", "confirmed", "confirmationError", "warnings", "station"} {

		if _, ok := fields[required]; !ok {

			t.Fatalf("channel error response missing field %q: %v", required, fields)

		}

	}

	if _, ok := fields["actualChannel"]; ok {

		t.Fatalf("channel error response still carries the divergent actualChannel field: %v", fields)

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

	// Serve on a real listener: fiber's Test() surfaces an oversized-body
	// rejection as a client-side error instead of the 413 response a live
	// server writes, so only a live server exercises the production path
	// (fasthttp BodyLimit -> serverErrorHandler -> the app's JSON error shape).
	app := testAPI(&fakeAPIStationManager{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {

		t.Fatal(err)

	}

	serveDone := make(chan struct{})

	go func() {

		defer close(serveDone)

		_ = app.Listener(listener)

	}()

	defer func() {

		_ = app.Shutdown()

		<-serveDone

	}()

	address := listener.Addr().String()

	body := `{"state":"` + strings.Repeat("x", 17*1024) + `"}`

	var response *http.Response

	deadline := time.Now().Add(5 * time.Second)

	for {

		response, err = http.Post("http://"+address+"/stations/power", fiber.MIMEApplicationJSON, strings.NewReader(body))

		if err == nil {

			break

		}

		if time.Now().After(deadline) {

			t.Fatalf("body-limit request never succeeded: %v", err)

		}

		time.Sleep(10 * time.Millisecond)

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

// TestAPIPowerRoutesParseJSONRegardlessOfContentType verifies the JSON body is
// decoded by content rather than by the request Content-Type. External callers
// commonly POST valid JSON with a form content type (curl -d default) or
// text/plain; those requests must decode the body instead of silently reading
// zero values or being rejected.
func TestAPIPowerRoutesParseJSONRegardlessOfContentType(t *testing.T) {
	for _, contentType := range []string{
		fiber.MIMEApplicationForm,
		fiber.MIMETextPlain,
		"",
	} {
		manager := &fakeAPIStationManager{}
		request := httptest.NewRequest(http.MethodPost, "/stations/power", strings.NewReader(`{"state":"standby"}`))
		if contentType != "" {
			request.Header.Set(fiber.HeaderContentType, contentType)
		}
		response, err := testAPI(manager).Test(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if manager.lastBulkState != "standby" {
			t.Fatalf("content-type %q: bulk state = %q, want standby (JSON body must be decoded)", contentType, manager.lastBulkState)
		}

		singleManager := &fakeAPIStationManager{}
		singleRequest := httptest.NewRequest(http.MethodPost, "/stations/AA/power", strings.NewReader(`{"state":"sleep"}`))
		if contentType != "" {
			singleRequest.Header.Set(fiber.HeaderContentType, contentType)
		}
		singleResponse, err := testAPI(singleManager).Test(singleRequest)
		if err != nil {
			t.Fatal(err)
		}
		_ = singleResponse.Body.Close()
		if singleManager.lastPowerState != "sleep" {
			t.Fatalf("content-type %q: single power state = %q, want sleep (JSON body must be decoded)", contentType, singleManager.lastPowerState)
		}

		channelManager := &fakeAPIStationManager{}
		channelRequest := httptest.NewRequest(http.MethodPut, "/stations/AA/channel", strings.NewReader(`{"channel":7,"allowUnknownConflictRisk":true}`))
		if contentType != "" {
			channelRequest.Header.Set(fiber.HeaderContentType, contentType)
		}
		channelResponse, err := testAPI(channelManager).Test(channelRequest)
		if err != nil {
			t.Fatal(err)
		}
		_ = channelResponse.Body.Close()
		if channelManager.lastChannelRequest != 7 || !channelManager.lastAllowUnknownConflictRisk {
			t.Fatalf("content-type %q: channel = %d allowUnknown = %v, want 7/true", contentType, channelManager.lastChannelRequest, channelManager.lastAllowUnknownConflictRisk)
		}
	}
}

func TestAPIStatusCanceledMapsToConflict(t *testing.T) {
	if status := apiStatusForError(context.Canceled); status != fiber.StatusConflict {
		t.Fatalf("apiStatusForError(context.Canceled) = %d, want %d", status, fiber.StatusConflict)
	}
	wrapped := fmt.Errorf("scan interrupted: %w", context.Canceled)
	if status := apiStatusForError(wrapped); status != fiber.StatusConflict {
		t.Fatalf("apiStatusForError(wrapped canceled) = %d, want %d", status, fiber.StatusConflict)
	}
}

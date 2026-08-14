package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

// TestAPINeverPanicsOrReturns5xxOnMalformedInput drives every registered
// route with malformed, oversized, and adversarial inputs. Handlers must never
// panic and must answer with client-level statuses (never 5xx) for bad input.
func TestAPINeverPanicsOrReturns5xxOnMalformedInput(t *testing.T) {
	api := fiber.New(fiber.Config{ErrorHandler: apiErrorHandler})
	registerAPIRoutes(api, &fakeAPIStationManager{}, scanEventCallbacks{}, func() APIStatus {
		return APIStatus{Running: true, Address: "127.0.0.1:7575"}
	})

	bodies := []string{
		"",
		"{",
		"}",
		"null",
		"[]",
		`{"state": 5}`,
		`{"state": ""}`,
		`{"state": "ON"}`,
		`{"state": "bogus"}`,
		`{"channel": 0}`,
		`{"channel": -1}`,
		`{"channel": 17}`,
		`{"channel": "x"}`,
		`{"channel": 5, "allowUnknownConflictRisk": "yes"}`,
		strings.Repeat("A", 32*1024),
	}
	addresses := []string{
		"AA:BB:CC:DD:EE:FF",
		"",
		"%2e%2e%2f",
		strings.Repeat("Z", 512),
		"AA:BB:CC:DD:EE:FF/../../power",
	}

	for method, paths := range map[string][]string{
		http.MethodPost: {"/allon", "/alloff", "/scan", "/scan/stop", "/stations/power"},
		http.MethodGet:  {"/status", "/health", "/scan/status"},
	} {
		for _, path := range paths {
			for _, body := range bodies {
				request := httptest.NewRequest(method, path, strings.NewReader(body))
				response, err := api.Test(request, -1)
				if err != nil {
					t.Fatalf("%s %s body=%d bytes: request error: %v", method, path, len(body), err)
				}
				_ = response.Body.Close()
				if response.StatusCode >= 500 {
					t.Fatalf("%s %s body=%.32q => %d, want a client-level status", method, path, body, response.StatusCode)
				}
			}
		}
	}
	for _, address := range addresses {
		for pathTemplate, method := range map[string]string{
			"/stations/%s/power":   http.MethodPost,
			"/stations/%s/identify": http.MethodPost,
			"/stations/%s/refresh": http.MethodPost,
			"/stations/%s/channel": http.MethodPut,
		} {
			path := strings.Replace(pathTemplate, "%s", address, 1)
			body := `{"state":"on","channel":5}`
			request := httptest.NewRequest(method, path, strings.NewReader(body))
			response, err := api.Test(request, -1)
			if err != nil {
				t.Fatalf("%s %s: request error: %v", method, path, err)
			}
			bodyBytes := make([]byte, 512)
			n, _ := response.Body.Read(bodyBytes)
			_ = response.Body.Close()
			if response.StatusCode >= 500 {
				t.Fatalf("%s %s => %d body=%q, want a client-level status", method, path, response.StatusCode, string(bodyBytes[:n]))
			}
		}
	}
}

// TestAPIErrorResponsesCarryErrorShape verifies every non-2xx response uses
// the documented {"error": "..."} envelope.
func TestAPIErrorResponsesCarryErrorShape(t *testing.T) {
	api := fiber.New(fiber.Config{ErrorHandler: apiErrorHandler})
	registerAPIRoutes(api, &fakeAPIStationManager{legacyErr: station.ErrScanRequired}, scanEventCallbacks{}, func() APIStatus {
		return APIStatus{Running: true}
	})
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/stations/power", strings.NewReader("not json")),
		httptest.NewRequest(http.MethodPost, "/stations/AA/power", strings.NewReader("not json")),
		httptest.NewRequest(http.MethodPut, "/stations/AA/channel", strings.NewReader("not json")),
		httptest.NewRequest(http.MethodPost, "/scan", nil),
	}
	for _, request := range requests {
		response, err := api.Test(request, -1)
		if err != nil {
			t.Fatalf("%s %s: request error: %v", request.Method, request.URL.Path, err)
		}
		if response.StatusCode < 400 {
			_ = response.Body.Close()
			continue
		}
		var payload struct {
			Error string `json:"error"`
		}
		if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr != nil {
			_ = response.Body.Close()
			t.Fatalf("%s %s status=%d: error body is not JSON: %v", request.Method, request.URL.Path, response.StatusCode, decodeErr)
		}
		_ = response.Body.Close()
		if payload.Error == "" {
			t.Fatalf("%s %s status=%d: empty error field", request.Method, request.URL.Path, response.StatusCode)
		}
	}
}


package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
)

func TestAPIStatusForError(t *testing.T) {

	tests := []struct {
		err error

		want int
	}{

		{station.ErrInvalidArgument, fiber.StatusBadRequest},

		{station.ErrNotFound, fiber.StatusNotFound},

		{station.ErrOperationInProgress, fiber.StatusConflict},

		{station.ErrChannelConflict, fiber.StatusConflict},

		{station.ErrScanRequired, fiber.StatusConflict},

		{bluetooth.ErrScanCancelled, fiber.StatusConflict},

		{station.ErrStationTransitioning, fiber.StatusLocked},

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

func TestGetAPIStatusIncludesRecoverableExternalOperations(t *testing.T) {

	app := NewApp()

	started := app.recordExternalOperation(externalOperationEvent{ID: 42, Phase: "started", Kind: "bulk-power"})

	status := app.GetAPIStatus()

	if started.Revision == 0 || status.OperationRevision != started.Revision || len(status.ActiveOperations) != 1 ||

		status.ActiveOperations[0].ID != 42 || status.ActiveOperations[0].Kind != "bulk-power" {

		t.Fatalf("API status operation snapshot = %+v", status)

	}

	finished := app.recordExternalOperation(externalOperationEvent{ID: 42, Phase: "finished", Kind: "bulk-power"})

	status = app.GetAPIStatus()

	if finished.Revision <= started.Revision || status.OperationRevision != finished.Revision || len(status.ActiveOperations) != 0 {

		t.Fatalf("API status after finish = %+v, event = %+v", status, finished)

	}

}

func TestConfigPersistenceFailureUpdatesStatusAndEmptyWarningsStayArrays(t *testing.T) {

	app := NewApp()

	app.setConfigPersistenceStatus()

	status := app.GetAPIStatus()

	encoded, err := json.Marshal(status)

	if err != nil {

		t.Fatal(err)

	}

	if !strings.Contains(string(encoded), `"warnings":[]`) {

		t.Fatalf("empty warnings encoded as %s, want JSON array", encoded)

	}

	blockedRoot := filepath.Join(t.TempDir(), "not-a-directory")

	if err := os.WriteFile(blockedRoot, []byte("occupied"), 0o644); err != nil {

		t.Fatal(err)

	}

	t.Setenv("AppData", blockedRoot)

	if err := app.SaveConfig(); err == nil {

		t.Fatal("App.SaveConfig() unexpectedly succeeded")

	}

	if current := app.GetAPIStatus(); current.ConfigWritable || len(current.Warnings) != 1 {

		t.Fatalf("failed persistence status = %+v", current)

	}

	t.Setenv("AppData", t.TempDir())

	if err := app.SaveConfig(); err != nil {

		t.Fatalf("recovery App.SaveConfig() error = %v", err)

	}

	if current := app.GetAPIStatus(); !current.ConfigWritable || len(current.Warnings) != 0 {

		t.Fatalf("successful persistence status = %+v", current)

	}

}

func TestConfigPathLoadFailureMarksStatusReadOnly(t *testing.T) {

	blockedRoot := filepath.Join(t.TempDir(), "not-a-directory")

	if err := os.WriteFile(blockedRoot, []byte("occupied"), 0o644); err != nil {

		t.Fatal(err)

	}

	t.Setenv("AppData", blockedRoot)

	app := NewApp()

	loadErr := app.config.Load()

	if loadErr == nil {

		t.Fatal("config Load() unexpectedly succeeded")

	}

	app.setConfigLoadStatus(loadErr)

	status := app.GetAPIStatus()

	if status.ConfigWritable || len(status.Warnings) != 1 ||

		!strings.Contains(status.Warnings[0], "failed to resolve config path") {

		t.Fatalf("config path failure status = %+v", status)

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

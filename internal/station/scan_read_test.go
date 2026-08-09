package station

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"

	tinybluetooth "tinygo.org/x/bluetooth"
)

func TestScanInitialReadHasPerStationTotalTimeout(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.initialReadTimeout = 25 * time.Millisecond
	address := "11:22:33:44:55:68"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{{
			Name: "LHB-TIMEOUT", Address: mustAddress(t, address),
		}}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		<-ctx.Done()
		return ctx.Err()
	}

	started := time.Now()
	stations, err := manager.ScanAndFetchStations()
	if err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("initial read timeout took %v, want under 1s", elapsed)
	}
	if len(stations) != 1 {
		t.Fatalf("stations = %+v, want one discovered station", stations)
	}
	status := manager.GetScanStatus()
	if status.State != "completed" || len(status.Warnings) != 1 {
		t.Fatalf("scan status = %+v, want completed with one warning", status)
	}
	manager.Shutdown()
}

func TestScanInitialReadPhaseTimeoutBoundsWholeFleet(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	manager.initialReadTimeout = time.Hour
	manager.initialReadPhaseTimeout = 40 * time.Millisecond
	discovered := make([]internalbluetooth.DiscoveredStation, 0, 6)
	for index := 1; index <= 6; index++ {
		address := fmt.Sprintf("11:22:33:44:66:%02X", index)
		discovered = append(discovered, internalbluetooth.DiscoveredStation{
			Name: fmt.Sprintf("LHB-PHASE-%d", index), Address: mustAddress(t, address),
		})
	}
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return discovered, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		<-ctx.Done()
		return ctx.Err()
	}

	started := time.Now()
	stations, err := manager.ScanAndFetchStations()
	if err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("fleet initial-read phase took %v, want a fleet-wide bound", elapsed)
	}
	if len(stations) != len(discovered) {
		t.Fatalf("stations = %d, want %d discovered stations", len(stations), len(discovered))
	}
	if status := manager.GetScanStatus(); status.State != "completed" || len(status.Warnings) == 0 {
		t.Fatalf("scan status = %+v, want completed with phase warning", status)
	}
	manager.statusRetryMutex.Lock()
	for _, item := range discovered {
		address := item.Address.String()
		retry, tracked := manager.statusRetries[address]
		if !tracked || effectiveStatusRetryKinds(retry)&statusRetryRefresh == 0 {
			t.Fatalf("phase-limited station %s retry = %+v, tracked=%v; want refresh retry", address, retry, tracked)
		}
	}
	manager.statusRetryMutex.Unlock()
	for _, station := range stations {
		if station.LastError != "" {
			t.Fatalf("phase budget marked %s as a device failure: %q", station.Address, station.LastError)
		}
	}
	manager.Shutdown()
}

func TestScanInitialReadClassifiesPartialFailures(t *testing.T) {
	for _, test := range []struct {
		name            string
		readErr         error
		wantDisconnect  bool
		wantRetry       bool
		wantUnavailable bool
	}{
		{
			name:      "channel only",
			readErr:   &internalbluetooth.InitialReadError{Channel: errors.New("channel unavailable")},
			wantRetry: true,
		},
		{
			name:           "power",
			readErr:        &internalbluetooth.InitialReadError{Power: errors.New("power unavailable")},
			wantDisconnect: true,
			wantRetry:      true,
		},
		{
			name: "channel transport failure",
			readErr: &internalbluetooth.InitialReadError{
				Channel: &internalbluetooth.DeviceTransportError{
					Operation: "read channel characteristic",
					Err:       tinybluetooth.ErrGATTCommunication,
				},
			},
			wantDisconnect: true,
			wantRetry:      true,
		},
		{
			name: "channel unsupported read",
			readErr: &internalbluetooth.InitialReadError{
				Channel: &internalbluetooth.UnsupportedCapabilityError{
					Capability: "channel read",
					Err:        tinybluetooth.ErrAttReadNotPermitted,
				},
			},
			wantRetry: false,
		},
		{
			name:            "adapter",
			readErr:         tinybluetooth.ErrRadioNotAvailable,
			wantDisconnect:  true,
			wantRetry:       true,
			wantUnavailable: true,
		},
		{
			// A per-station read budget deadline (phase budget still alive) is
			// not evidence the link is broken: no disconnect, only backoff.
			name:           "per-station read budget deadline",
			readErr:        context.DeadlineExceeded,
			wantDisconnect: false,
			wantRetry:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			address := "11:22:33:44:55:66"
			manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
				return []internalbluetooth.DiscoveredStation{{
					Name: "LHB-TEST", Address: mustAddress(t, address),
				}}, nil
			}
			manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
				return test.readErr
			}
			disconnects := 0
			manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
				disconnects++
				return nil
			}

			if _, err := manager.ScanAndFetchStations(); err != nil {
				t.Fatalf("ScanAndFetchStations() error = %v", err)
			}
			if got := disconnects > 0; got != test.wantDisconnect {
				t.Fatalf("disconnect = %v, want %v", got, test.wantDisconnect)
			}
			manager.statusRetryMutex.Lock()
			_, retryTracked := manager.statusRetries[address]
			manager.statusRetryMutex.Unlock()
			if retryTracked != test.wantRetry {
				t.Fatalf("retry tracked = %v, want %v", retryTracked, test.wantRetry)
			}
			manager.initializeMutex.Lock()
			unavailable := manager.initializeErr != nil
			manager.initializeMutex.Unlock()
			if unavailable != test.wantUnavailable {
				t.Fatalf("adapter unavailable = %v, want %v", unavailable, test.wantUnavailable)
			}
		})
	}
}

func TestScanContinuesAndReportsConnectionReleaseWarnings(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:83"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-RELEASE", Address: mustAddress(t, address), Present: true,
	}
	releaseErr := errors.New("session is still in use")
	manager.bluetoothOps.releaseStationForScan = func(*internalbluetooth.BaseStation) error {
		return releaseErr
	}
	var scans atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scans.Add(1)
		return nil, nil
	}

	if _, err := manager.ScanAndFetchStations(); err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	status := manager.GetScanStatus()
	if scans.Load() != 1 {
		t.Fatalf("scan calls = %d, want 1", scans.Load())
	}
	if len(status.Warnings) != 1 || !strings.Contains(status.Warnings[0], releaseErr.Error()) {
		t.Fatalf("scan warnings = %v", status.Warnings)
	}
	snapshot := manager.stations[address].Snapshot()
	if !snapshot.Present || snapshot.MissedScans != 0 || !snapshot.PresenceUncertain {
		t.Fatalf("unreliable scan changed station presence: %+v", snapshot)
	}
	info := manager.GetStationInfo()
	if len(info) != 1 || info[0].SeenInLatestScan || info[0].ScanFresh {
		t.Fatalf("unreliable scan was reported as a current discovery: %+v", info)
	}
}

func TestStopScanPreventsPlatformScanAfterConnectionRelease(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:86"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-RELEASE-CANCEL", Address: mustAddress(t, address), Present: true,
	}
	releaseStarted := make(chan struct{})
	releaseDone := make(chan struct{})
	manager.bluetoothOps.releaseStationForScan = func(*internalbluetooth.BaseStation) error {
		close(releaseStarted)
		<-releaseDone
		return nil
	}
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scanCalls.Add(1)
		return nil, nil
	}

	if err := manager.StartScan(ScanCallbacks{}); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	<-releaseStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopScan() }()
	deadline := time.Now().Add(time.Second)
	for {
		manager.scanLifecycleMutex.Lock()
		lifecycle := manager.scanLifecycle
		manager.scanLifecycleMutex.Unlock()
		if lifecycle != nil && lifecycle.ctx.Err() != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("StopScan() did not cancel the scan lifecycle")
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseDone)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopScan() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StopScan() did not finish")
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("platform scan calls = %d, want 0", scanCalls.Load())
	}
}

func TestStopScanDoesNotReportScanFailure(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return nil, errors.New("radio exploded")
	}
	failedEntered := make(chan struct{})
	releaseFailed := make(chan struct{})
	if err := manager.StartScan(ScanCallbacks{Failed: func(error) {
		close(failedEntered)
		<-releaseFailed
	}}); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	// The scan has failed and its lifecycle is still published while the
	// terminal callback is blocked. Stopping in this window must succeed:
	// the scan's own failure is reported via status and callbacks, not as a
	// stop failure.
	<-failedEntered
	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopScan() }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopScan() error = %v, want nil even though the scan failed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StopScan() did not finish")
	}
	close(releaseFailed)
	manager.scanCallbackWg.Wait()
	if status := manager.GetScanStatus(); status.State != "failed" {
		t.Fatalf("scan status = %+v, want failed", status)
	}
	manager.Shutdown()
}

func TestScanResumesPresenceTrackingAfterConnectionReleaseRecovers(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:84"
	station := &internalbluetooth.BaseStation{
		Name: "LHB-RELEASE-RECOVERY", Address: mustAddress(t, address), Present: true,
	}
	manager.stations[address] = station
	releaseFails := true
	manager.bluetoothOps.releaseStationForScan = func(*internalbluetooth.BaseStation) error {
		if releaseFails {
			return errors.New("session is still in use")
		}
		return nil
	}
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return nil, nil
	}

	if _, err := manager.ScanAndFetchStations(); err != nil {
		t.Fatalf("unreliable ScanAndFetchStations() error = %v", err)
	}
	if snapshot := station.Snapshot(); snapshot.MissedScans != 0 || !snapshot.Present || !snapshot.PresenceUncertain {
		t.Fatalf("failed release changed presence: %+v", snapshot)
	}

	releaseFails = false
	if _, err := manager.ScanAndFetchStations(); err != nil {
		t.Fatalf("recovered ScanAndFetchStations() error = %v", err)
	}
	if snapshot := station.Snapshot(); snapshot.MissedScans != 1 || !snapshot.Present || snapshot.PresenceUncertain {
		t.Fatalf("reliable scan did not resume presence tracking: %+v", snapshot)
	}
}

func TestDiscoveryClearsUncertainPresenceFromReleaseFailure(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:85"
	parsedAddress := mustAddress(t, address)
	station := &internalbluetooth.BaseStation{
		Name: "LHB-RELEASE-DISCOVERED", Address: parsedAddress, Present: true,
	}
	station.MarkSeen(time.Now().Add(-time.Minute))
	manager.stations[address] = station
	manager.bluetoothOps.releaseStationForScan = func(*internalbluetooth.BaseStation) error {
		return errors.New("session is still in use")
	}
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{{
			Name:    "LHB-RELEASE-DISCOVERED",
			Address: parsedAddress,
		}}, nil
	}

	info, err := manager.ScanAndFetchStations()
	if err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	if len(info) != 1 || !info[0].SeenInLatestScan || !info[0].ScanFresh {
		t.Fatalf("discovered station retained uncertain presence: %+v", info)
	}
	snapshot := station.Snapshot()
	if snapshot.PresenceUncertain || snapshot.MissedScans != 0 {
		t.Fatalf("discovery did not clear uncertain presence: %+v", snapshot)
	}
}

func TestScanStatusLifecycleAndDefensiveCopy(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.markScanStarted()
	manager.addScanWarning("partial read")

	starting := manager.GetScanStatus()
	if starting.State != "starting" || starting.StartedAt == "" {
		t.Fatalf("starting scan status = %+v", starting)
	}
	manager.markScanRunning()
	running := manager.GetScanStatus()
	if running.State != "running" || running.StartedAt != starting.StartedAt {
		t.Fatalf("running scan status = %+v", running)
	}

	manager.markScanFinished(2, nil)
	completed := manager.GetScanStatus()
	if completed.State != "completed" || completed.CompletedAt == "" ||
		completed.Found != 2 || len(completed.Warnings) != 1 {
		t.Fatalf("completed scan status = %+v", completed)
	}

	completed.Warnings[0] = "modified"
	if got := manager.GetScanStatus().Warnings[0]; got != "partial read" {
		t.Fatalf("GetScanStatus leaked mutable warnings slice: %q", got)
	}
}

func TestFallbackStationNameUsesAddressSuffix(t *testing.T) {
	if got, want := fallbackStationName("AA:BB:CC:DD:EE:FF"), "LHB-CCDDEEFF"; got != want {
		t.Fatalf("fallbackStationName() = %q, want %q", got, want)
	}
}

func TestStalePowerStateIsNotConfirmed(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["stale"] = &internalbluetooth.BaseStation{
		Name:          "LHB-STALE",
		PowerState:    internalbluetooth.PowerStateOn,
		RawPowerState: 0x0B,
	}

	infos := manager.GetStationInfo()
	if len(infos) != 1 || infos[0].PowerFresh || infos[0].PowerStateConfirmed {
		t.Fatalf("stale cached power was reported as confirmed: %+v", infos)
	}
}

func TestGetStationInfoCanRunWhileStationMapChanges(t *testing.T) {
	manager := NewManager(config.NewConfig())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for index := 0; index < 500; index++ {
			key := "dynamic"
			manager.stationsMutex.Lock()
			manager.stations[key] = &internalbluetooth.BaseStation{Name: "LHB-DYNAMIC"}
			if index%2 == 0 {
				delete(manager.stations, key)
			}
			manager.stationsMutex.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for index := 0; index < 500; index++ {
			_ = manager.GetStationInfo()
		}
	}()
	wg.Wait()
}

func TestScanAndFetchStationsContextCancelsAndReleasesScan(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	started := make(chan struct{})
	var calls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(ctx context.Context, _ time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []internalbluetooth.DiscoveredStation{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStationsContext(ctx)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, internalbluetooth.ErrScanCancelled) {
			t.Fatalf("cancelled scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled scan did not return promptly")
	}
	if manager.IsScanning() {
		t.Fatal("cancelled scan left the manager scanning")
	}
	if _, err := manager.ScanAndFetchStationsContext(context.Background()); err != nil {
		t.Fatalf("scan lock was not released after cancellation: %v", err)
	}
}

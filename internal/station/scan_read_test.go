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

// TestScanInitialReadMixedDeadlineKeepsFailureBookkeeping guards the phase
// deadline classification: a genuine transport failure joined with the phase
// deadline is not a clean phase interruption. It must keep the structured
// failure handling (disconnect/backoff) instead of being folded into the
// phase timeout's refresh-only marker, matching the isPureContextError rule
// the cancellation path already applies.
func TestScanInitialReadMixedDeadlineKeepsFailureBookkeeping(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	manager.initialReadTimeout = time.Hour
	manager.initialReadPhaseTimeout = 40 * time.Millisecond
	address := "11:22:33:44:66:20"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{{
			Name: "LHB-MIXED-DEADLINE", Address: mustAddress(t, address),
		}}, nil
	}
	transportErr := &internalbluetooth.DeviceTransportError{
		Operation: "read power characteristic",
		Err:       tinybluetooth.ErrGATTUnreachable,
	}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		// The transport fault lands just as the phase budget expires; the
		// bluetooth layer joins the two into one error.
		<-ctx.Done()
		return &internalbluetooth.InitialReadError{
			Power:   transportErr,
			Channel: ctx.Err(),
		}
	}

	if _, err := manager.ScanAndFetchStations(); err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	if disconnects.Load() != 1 {
		t.Fatalf("disconnects = %d, want the transport failure to disconnect once", disconnects.Load())
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry)&statusRetryConnection == 0 || retry.failures == 0 {
		t.Fatalf("mixed-deadline retry = %+v, tracked=%v; want a connection failure backoff", retry, tracked)
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
			// The Bluetooth layer reports a power read stopped by its own
			// budget as a structured InitialReadError wrapping the deadline.
			// The deadline rule must apply to that shape too: no disconnect,
			// only backoff.
			name: "power structured read budget deadline",
			readErr: &internalbluetooth.InitialReadError{
				Power: &internalbluetooth.DeviceTransportError{
					Operation: "read power characteristic",
					Err:       context.DeadlineExceeded,
				},
			},
			wantDisconnect: false,
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

func TestScanInitialReadPerStationBudgetNotMisclassifiedAsPhase(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	manager.initialReadTimeout = 25 * time.Millisecond
	manager.initialReadPhaseTimeout = time.Hour
	address := "11:22:33:44:55:77"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{{
			Name: "LHB-OWNBUDGET", Address: mustAddress(t, address),
		}}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		<-ctx.Done()
		return ctx.Err()
	}

	if _, err := manager.ScanAndFetchStations(); err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	failures := retry.failures
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatalf("per-station budget expiry for %s was not tracked", address)
	}
	if failures == 0 {
		t.Fatalf("per-station budget expiry was folded into the phase deadline (no failure recorded): %+v", retry)
	}
	if effectiveStatusRetryKinds(retry)&statusRetryConnection == 0 {
		t.Fatalf("per-station budget expiry did not record a connection retry: %+v", retry)
	}
	manager.Shutdown()
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
	if err := manager.StartScan(ScanCallbacks{Failed: func(uint64, error) {
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

// TestMalformedReadValueDoesNotDisconnectOrScheduleRecovery guards the value
// classification: a device reporting an invalid value length must not discard
// the healthy connection (reconnecting cannot change the reported value) and
// must not schedule failure retries for the same reason.
func TestMalformedReadValueDoesNotDisconnectOrScheduleRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "11:22:33:44:55:6E"
	station := &internalbluetooth.BaseStation{
		Name: "LHB-VALUE", Address: mustAddress(t, address), Present: true,
	}
	manager.stations[address] = station

	valueErr := &internalbluetooth.DeviceValueError{
		Operation: "read channel characteristic",
		Err:       errors.New("unexpected value length 5"),
	}
	manager.recordStructuredReadResult(station, address, nil, valueErr)
	if disconnects.Load() != 0 {
		t.Fatalf("disconnects after value error = %d, want 0", disconnects.Load())
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatalf("value error scheduled recovery retries: %+v", retry)
	}

	manager.recordUnstructuredStationFailure(station, address, valueErr)
	if disconnects.Load() != 0 {
		t.Fatalf("disconnects after unstructured value error = %d, want 0", disconnects.Load())
	}
}

// TestCancelledScanStillBooksRealInitialReadFailures guards the cancellation
// bookkeeping: when a scan's initial-read phase is cancelled, a station whose
// read hit a genuine transport fault (joined with the cancellation) must keep
// its disconnect/backoff handling, and a read that only saw the cancellation
// must stay clean. Dropping the outcomes together left a failed station
// disconnected from its recovery path.
func TestCancelledScanStillBooksRealInitialReadFailures(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})

	failedAddress := "11:22:33:44:55:A1"
	pendingAddress := "11:22:33:44:55:A2"
	transportErr := errors.New("gatt read failed")
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{
			{Name: "LHB-FAILED", Address: mustAddress(t, failedAddress)},
			{Name: "LHB-PENDING", Address: mustAddress(t, pendingAddress)},
		}, nil
	}
	readStarted := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, station *internalbluetooth.BaseStation) error {
		if station.Address.String() == failedAddress {
			close(readStarted)
			return errors.Join(transportErr, context.Canceled)
		}
		<-readStarted
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStationsContext(ctx)
		done <- err
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("initial read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, internalbluetooth.ErrScanCancelled) {
			t.Fatalf("cancelled scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled scan did not return promptly")
	}

	if disconnects.Load() == 0 {
		t.Fatal("real transport failure during a cancelled scan was not disconnected")
	}
	manager.statusRetryMutex.Lock()
	_, failedTracked := manager.statusRetries[failedAddress]
	_, pendingTracked := manager.statusRetries[pendingAddress]
	manager.statusRetryMutex.Unlock()
	if !failedTracked {
		t.Fatal("real transport failure during a cancelled scan was not scheduled for recovery")
	}
	if pendingTracked {
		t.Fatal("purely cancelled read scheduled recovery instead of staying clean")
	}
}

// TestCancelledScanBooksExpiredReadBudget guards the cancellation bookkeeping
// for a per-station read budget that expired on its own: even when the scan's
// cancellation lands in the same window, the deadline is a real read failure
// and must keep its backoff and recovery entry instead of being folded into
// the clean cancellation bucket and left untracked.
func TestCancelledScanBooksExpiredReadBudget(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})

	deadlineAddress := "11:22:33:44:55:A3"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{
			{Name: "LHB-BUDGET", Address: mustAddress(t, deadlineAddress)},
		}, nil
	}
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, station *internalbluetooth.BaseStation) error {
		close(readStarted)
		// Hold the read until the cancellation lands, then fail with the
		// station's own budget deadline: a bare deadline error with no
		// transport cause, shaped like the read's own context running out.
		<-releaseRead
		return fmt.Errorf("initial station read: %w", context.DeadlineExceeded)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStationsContext(ctx)
		done <- err
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("initial read did not start")
	}
	cancel()
	close(releaseRead)
	select {
	case err := <-done:
		if !errors.Is(err, internalbluetooth.ErrScanCancelled) {
			t.Fatalf("cancelled scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled scan did not return promptly")
	}

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[deadlineAddress]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("expired per-station read budget during a cancelled scan was not scheduled for recovery")
	}
}

// TestInitialScanReadsAbandonWedgedStationLock guards the initial-read worker
// join: a reader wedged on a station lock inside a transport call that ignores
// cancellation must not hang the scan. After the phase and per-read budgets
// plus a grace the wedged reader is abandoned, and its station is booked like
// a phase deadline (tracked for a background refresh) instead of blocking the
// whole scan behind the lock.
func TestInitialScanReadsAbandonWedgedStationLock(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.initialReadPhaseTimeout = 20 * time.Millisecond
	manager.initialReadTimeout = 20 * time.Millisecond
	originalGrace := initialReadJoinGrace
	initialReadJoinGrace = 20 * time.Millisecond
	t.Cleanup(func() { initialReadJoinGrace = originalGrace })

	address := "11:22:33:44:55:A4"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{
			{Name: "LHB-WEDGED", Address: mustAddress(t, address)},
		}, nil
	}
	release := make(chan struct{})
	defer close(release)
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, station *internalbluetooth.BaseStation) error {
		// Mirrors FetchInitialPowerStateContext: the transport call wedges
		// while holding the station's write lock, ignoring the read context.
		station.HoldLockWhile(func() { <-release })
		return nil
	}

	started := time.Now()
	_, err := manager.ScanAndFetchStationsContext(context.Background())
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("scan took %v, want the initial-read join bounded", elapsed)
	}
	if err != nil {
		t.Fatalf("ScanAndFetchStationsContext() error = %v, want the abandoned read booked without a scan error", err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("abandoned initial read retry = %+v, tracked=%v; want a refresh retry", retry, tracked)
	}
}

package station

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

func TestStopScanCancelsPendingInitializationBeforeScanStarts(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.initializeErr = errors.New("radio unavailable")
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	initializeStarted := make(chan struct{})
	initializeRelease := make(chan struct{})
	manager.initializeBluetooth = func() error {
		close(initializeStarted)
		<-initializeRelease
		return nil
	}
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scanCalls.Add(1)
		return nil, nil
	}

	startDone := make(chan error, 1)
	go func() { startDone <- manager.StartScan(ScanCallbacks{}) }()
	<-initializeStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopScan() }()
	cancelDeadline := time.Now().Add(time.Second)
	for manager.currentScanContext().Err() == nil {
		if time.Now().After(cancelDeadline) {
			t.Fatal("StopScan did not cancel the published scan lifecycle")
		}
		time.Sleep(time.Millisecond)
	}
	close(initializeRelease)
	if err := <-startDone; err != nil && !errors.Is(err, internalbluetooth.ErrScanCancelled) {
		t.Fatalf("StartScan() error = %v, want nil or ErrScanCancelled", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("StopScan() error = %v", err)
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("scan started %d time(s) after pending cancellation", scanCalls.Load())
	}
	if status := manager.GetScanStatus(); status.State != "cancelled" {
		t.Fatalf("scan status = %+v, want cancelled", status)
	}
}

func TestStopScanWaitsForScanLifecyclePublication(t *testing.T) {
	manager := NewManager(config.NewConfig())
	publishingStarted := make(chan struct{})
	releasePublishing := make(chan struct{})
	manager.scanLifecycleStartHook = func() {
		close(publishingStarted)
		<-releasePublishing
	}
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scanCalls.Add(1)
		return nil, nil
	}

	startDone := make(chan error, 1)
	go func() { startDone <- manager.StartScan(ScanCallbacks{}) }()
	<-publishingStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopScan() }()
	select {
	case err := <-stopDone:
		t.Fatalf("StopScan() returned before lifecycle publication: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releasePublishing)
	if err := <-startDone; err != nil && !errors.Is(err, internalbluetooth.ErrScanCancelled) {
		t.Fatalf("StartScan() error = %v, want nil or ErrScanCancelled", err)
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopScan() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StopScan did not finish after lifecycle publication")
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("platform scan started %d time(s) after cancellation", scanCalls.Load())
	}
}

func TestAsyncScanEventsCannotOvertakePreviousCompletion(t *testing.T) {
	manager := NewManager(config.NewConfig())
	firstScanRelease := make(chan struct{})
	firstCompletionEntered := make(chan struct{})
	firstCompletionRelease := make(chan struct{})
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		if scanCalls.Add(1) == 1 {
			<-firstScanRelease
		}
		return nil, nil
	}

	var eventsMutex sync.Mutex
	events := make([]string, 0, 4)
	callbacks := ScanCallbacks{
		Started: func() {
			eventsMutex.Lock()
			events = append(events, "started")
			eventsMutex.Unlock()
		},
		Completed: func([]StationInfo) {
			eventsMutex.Lock()
			events = append(events, "completed")
			completionNumber := 0
			for _, event := range events {
				if event == "completed" {
					completionNumber++
				}
			}
			eventsMutex.Unlock()
			if completionNumber == 1 {
				close(firstCompletionEntered)
				<-firstCompletionRelease
			}
		},
	}
	if err := manager.StartScan(callbacks); err != nil {
		t.Fatalf("first StartScan() error = %v", err)
	}
	close(firstScanRelease)
	<-firstCompletionEntered
	if status := manager.GetScanStatus(); status.State != "completed" {
		t.Fatalf("status while completion callback is running = %+v", status)
	}
	if manager.IsBusy() {
		t.Fatal("terminal scan status was published before releasing the operation lock")
	}

	secondReturned := make(chan error, 1)
	go func() { secondReturned <- manager.StartScan(callbacks) }()
	if err := <-secondReturned; !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("second StartScan() error = %v, want ErrOperationInProgress", err)
	}
	close(firstCompletionRelease)
	manager.scanCallbackWg.Wait()
	if err := manager.StartScan(callbacks); err != nil {
		t.Fatalf("StartScan() after completion callback error = %v", err)
	}
	manager.scanCallbackWg.Wait()

	eventsMutex.Lock()
	defer eventsMutex.Unlock()
	wantStarted := 2
	wantCompleted := 2
	var startedCount, completedCount int
	for _, event := range events {
		if event == "started" {
			startedCount++
		}
		if event == "completed" {
			completedCount++
		}
	}
	if startedCount != wantStarted || completedCount != wantCompleted {
		t.Fatalf("events = %v, want %d started and %d completed", events, wantStarted, wantCompleted)
	}
}

func TestAsyncScanRejectsForegroundOperationsBeforeStarted(t *testing.T) {
	tests := []struct {
		name    string
		acquire func(*Manager) error
		release func(*Manager)
	}{
		{
			name: "device",
			acquire: func(manager *Manager) error {
				return manager.beginForegroundStationOperation("11:22:33:44:55:90")
			},
			release: func(manager *Manager) {
				manager.endStationOperation("11:22:33:44:55:90")
			},
		},
		{
			name:    "configuration",
			acquire: func(manager *Manager) error { return manager.beginForegroundSharedOperation() },
			release: func(manager *Manager) { manager.endForegroundSharedOperation() },
		},
		{
			name:    "global",
			acquire: func(manager *Manager) error { return manager.beginBulkGlobalOperation() },
			release: func(manager *Manager) { manager.endForegroundGlobalOperation() },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			if err := test.acquire(manager); err != nil {
				t.Fatalf("acquire foreground operation: %v", err)
			}
			defer test.release(manager)
			var started atomic.Int32

			err := manager.StartScan(ScanCallbacks{Started: func() { started.Add(1) }})
			if !errors.Is(err, ErrOperationInProgress) {
				t.Fatalf("StartScan() error = %v, want ErrOperationInProgress", err)
			}
			if got := started.Load(); got != 0 {
				t.Fatalf("Started callbacks = %d, want 0", got)
			}
			if manager.IsScanning() {
				t.Fatal("rejected scan remained active")
			}
		})
	}
}

func TestStopScanFinishesBeforeCancelledCallbackAndIsIdempotent(t *testing.T) {
	manager := NewManager(config.NewConfig())
	scanStarted := make(chan struct{})
	callbackEntered := make(chan struct{})
	callbackRelease := make(chan struct{})
	manager.bluetoothOps.scanForDurationContext = func(ctx context.Context, _ time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		close(scanStarted)
		<-ctx.Done()
		return nil, internalbluetooth.ErrScanCancelled
	}
	if err := manager.StartScan(ScanCallbacks{Cancelled: func() {
		close(callbackEntered)
		<-callbackRelease
	}}); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	<-scanStarted

	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopScan() }()
	<-callbackEntered
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopScan() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StopScan did not return after scan processing finished")
	}
	close(callbackRelease)
	manager.scanCallbackWg.Wait()
	if err := manager.StopScan(); err != nil {
		t.Fatalf("second StopScan() error = %v", err)
	}
	status := manager.GetScanStatus()
	if status.State != "cancelled" || status.CompletedAt == "" || status.Error != "" {
		t.Fatalf("cancelled scan status = %+v", status)
	}
}

func TestStopScanFromScanCallbackDoesNotDeadlock(t *testing.T) {
	for _, test := range []struct {
		name      string
		scanErr   error
		callbacks func(*Manager, chan<- error) ScanCallbacks
	}{
		{
			name: "started",
			callbacks: func(manager *Manager, stopped chan<- error) ScanCallbacks {
				return ScanCallbacks{Started: func() { stopped <- manager.StopScan() }}
			},
		},
		{
			name: "completed",
			callbacks: func(manager *Manager, stopped chan<- error) ScanCallbacks {
				return ScanCallbacks{Completed: func([]StationInfo) { stopped <- manager.StopScan() }}
			},
		},
		{
			name:    "failed",
			scanErr: errors.New("scan failed"),
			callbacks: func(manager *Manager, stopped chan<- error) ScanCallbacks {
				return ScanCallbacks{Failed: func(error) { stopped <- manager.StopScan() }}
			},
		},
		{
			name: "cancelled",
			callbacks: func(manager *Manager, stopped chan<- error) ScanCallbacks {
				return ScanCallbacks{Cancelled: func() { stopped <- manager.StopScan() }}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			defer manager.Shutdown()
			stopped := make(chan error, 1)
			if test.name == "cancelled" {
				manager.bluetoothOps.scanForDurationContext = func(ctx context.Context, _ time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
					<-ctx.Done()
					return nil, internalbluetooth.ErrScanCancelled
				}
			} else {
				manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
					return nil, test.scanErr
				}
			}

			if err := manager.StartScan(test.callbacks(manager, stopped)); err != nil {
				t.Fatalf("StartScan() error = %v", err)
			}
			if test.name == "cancelled" {
				go func() { _ = manager.StopScan() }()
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("StopScan() from lifecycle callback did not return")
			}
			manager.scanCallbackWg.Wait()
		})
	}
}

func TestShutdownFromScanCallbackDoesNotDeadlock(t *testing.T) {
	for _, test := range []struct {
		name      string
		scanErr   error
		callbacks func(*Manager, chan<- struct{}) ScanCallbacks
	}{
		{
			name: "started",
			callbacks: func(manager *Manager, shutdownDone chan<- struct{}) ScanCallbacks {
				return ScanCallbacks{Started: func() { manager.Shutdown(); close(shutdownDone) }}
			},
		},
		{
			name: "completed",
			callbacks: func(manager *Manager, shutdownDone chan<- struct{}) ScanCallbacks {
				return ScanCallbacks{Completed: func([]StationInfo) { manager.Shutdown(); close(shutdownDone) }}
			},
		},
		{
			name:    "failed",
			scanErr: errors.New("scan failed"),
			callbacks: func(manager *Manager, shutdownDone chan<- struct{}) ScanCallbacks {
				return ScanCallbacks{Failed: func(error) { manager.Shutdown(); close(shutdownDone) }}
			},
		},
		{
			name: "cancelled",
			callbacks: func(manager *Manager, shutdownDone chan<- struct{}) ScanCallbacks {
				return ScanCallbacks{Cancelled: func() { manager.Shutdown(); close(shutdownDone) }}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			shutdownDone := make(chan struct{})
			if test.name == "cancelled" {
				manager.bluetoothOps.scanForDurationContext = func(ctx context.Context, _ time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
					<-ctx.Done()
					return nil, internalbluetooth.ErrScanCancelled
				}
			} else {
				manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
					return nil, test.scanErr
				}
			}

			if err := manager.StartScan(test.callbacks(manager, shutdownDone)); err != nil {
				t.Fatalf("StartScan() error = %v", err)
			}
			if test.name == "cancelled" {
				go func() { _ = manager.StopScan() }()
			}
			select {
			case <-shutdownDone:
			case <-time.After(time.Second):
				t.Fatal("Shutdown() from lifecycle callback did not return")
			}
		})
	}
}

func TestScanCompletionCallbackUsesLatestAuthoritativeSnapshot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:65"
	if err := manager.beginScan(ScanCallbacks{}); err != nil {
		t.Fatalf("beginScan() error = %v", err)
	}
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:            "LHB-LATEST",
		Address:         mustAddress(t, address),
		Present:         true,
		PowerState:      internalbluetooth.PowerStateOn,
		RawPowerState:   0x0B,
		LastPowerReadAt: time.Now(),
	}
	manager.scanLifecycleMutex.Lock()
	lifecycle := manager.scanLifecycle
	manager.scanLifecycleMutex.Unlock()
	close(lifecycle.startedDone)
	var completed []StationInfo
	deliver := manager.finishScan(
		1,
		nil,
		ScanCallbacks{Completed: func(stations []StationInfo) { completed = stations }},
	)
	deliver()

	if len(completed) != 1 || completed[0].Name != "LHB-LATEST" ||
		completed[0].PowerState != int(internalbluetooth.PowerStateOn) {
		t.Fatalf("completed stations = %+v, want latest manager snapshot", completed)
	}
	manager.Shutdown()
}

func TestExternalStopScanWaitsForTerminalScanState(t *testing.T) {
	manager := NewManager(config.NewConfig())
	started := make(chan struct{})
	releaseStarted := make(chan struct{})
	manager.bluetoothOps.scanForDurationContext = func(ctx context.Context, _ time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		<-ctx.Done()
		return nil, internalbluetooth.ErrScanCancelled
	}
	startReturned := make(chan error, 1)
	go func() {
		startReturned <- manager.StartScan(ScanCallbacks{Started: func() {
			close(started)
			<-releaseStarted
		}})
	}()
	<-started
	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopScan() }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopScan() error = %v", err)
		}
		if status := manager.GetScanStatus(); status.State != "cancelled" {
			t.Fatalf("StopScan returned before terminal scan status: %+v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("StopScan did not return after terminal scan state")
	}
	close(releaseStarted)
	if err := <-startReturned; err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	manager.scanCallbackWg.Wait()
}

func TestScanCancellationSkipsPostScanInitialization(t *testing.T) {
	manager := NewManager(config.NewConfig())
	scanReturned := make(chan struct{})
	var initialReads atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(ctx context.Context, _ time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		close(scanReturned)
		<-ctx.Done()
		// A full-duration scan that was cancelled during the stop handshake
		// reports its results with a nil error; the discovery must survive.
		return []internalbluetooth.DiscoveredStation{{
			Name: "LHB-TEST", Address: mustAddress(t, "11:22:33:44:55:66"),
		}}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		initialReads.Add(1)
		return nil
	}
	go func() {
		<-scanReturned
		_ = manager.StopScan()
	}()

	stations, err := manager.ScanAndFetchStations()
	if err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v, want the completed scan results", err)
	}
	if initialReads.Load() != 0 {
		t.Fatalf("initial station reads after cancellation = %d, want 0", initialReads.Load())
	}
	if len(stations) != 1 || stations[0].Name != "LHB-TEST" || !stations[0].IsPresent {
		t.Fatalf("completed scan stations = %+v, want the merged discovery", stations)
	}
}

func TestStopScanCancelsBlockingInitialReadWithoutWarningsOrRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:66"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{{
			Name: "LHB-TEST", Address: mustAddress(t, address),
		}}, nil
	}
	readStarted := make(chan struct{})
	readCancelled := make(chan struct{})
	var readCalls atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		if readCalls.Add(1) == 1 {
			close(readStarted)
			<-ctx.Done()
			close(readCancelled)
			return ctx.Err()
		}
		return nil
	}

	if err := manager.StartScan(ScanCallbacks{}); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("initial GATT read did not start")
	}

	stopStarted := time.Now()
	if err := manager.StopScan(); err != nil {
		t.Fatalf("StopScan() error = %v", err)
	}
	if elapsed := time.Since(stopStarted); elapsed > 3*time.Second {
		t.Fatalf("StopScan() took %v, want no more than 3s", elapsed)
	}
	select {
	case <-readCancelled:
	default:
		t.Fatal("StopScan() returned before the initial read observed cancellation")
	}
	manager.scanCallbackWg.Wait()

	status := manager.GetScanStatus()
	if status.State != "cancelled" || status.Error != "" || len(status.Warnings) != 0 {
		t.Fatalf("cancelled scan status = %+v, want cancelled without errors or warnings", status)
	}
	manager.statusRetryMutex.Lock()
	retryCount := len(manager.statusRetries)
	manager.statusRetryMutex.Unlock()
	if retryCount != 0 {
		t.Fatalf("status recovery tasks after cancellation = %d, want 0", retryCount)
	}

	completed := make(chan struct{})
	if err := manager.StartScan(ScanCallbacks{Completed: func([]StationInfo) { close(completed) }}); err != nil {
		t.Fatalf("second StartScan() error = %v", err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("second scan did not complete")
	}
	if status := manager.GetScanStatus(); status.State != "completed" {
		t.Fatalf("second scan status = %+v, want completed", status)
	}
}

func TestStopScanCancelsWhileWaitingForBackgroundRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scanCalls.Add(1)
		return nil, nil
	}

	scanResult := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStations()
		scanResult <- err
	}()
	deadline := time.Now().Add(time.Second)
	for !manager.IsScanning() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !manager.IsScanning() {
		t.Fatal("scan lifecycle was not published while waiting for recovery")
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- manager.StopScan() }()
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("StopScan() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StopScan blocked behind background recovery")
	}
	select {
	case err := <-scanResult:
		if !errors.Is(err, internalbluetooth.ErrScanCancelled) {
			t.Fatalf("ScanAndFetchStations() error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan start waiter did not observe cancellation")
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("platform scan calls = %d, want 0", scanCalls.Load())
	}
	if status := manager.GetScanStatus(); status.State != "cancelled" {
		t.Fatalf("scan status = %+v, want cancelled", status)
	}

	manager.endRecoveryStationOperation("RECOVERY")
	manager.Shutdown()
}

func TestAsyncScanReturnsWhileWaitingAndDeliversPreStartCancellation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- manager.StartScan(ScanCallbacks{
			Started:   func() { close(started) },
			Cancelled: func() { close(cancelled) },
		})
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("StartScan() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("StartScan blocked while waiting for background recovery")
	}
	<-started
	if status := manager.GetScanStatus(); status.State != "starting" {
		t.Fatalf("scan status = %+v, want starting while queued", status)
	}
	if err := manager.StopScan(); err != nil {
		t.Fatalf("StopScan() error = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("pre-start cancellation callback was not delivered")
	}
	manager.endRecoveryStationOperation("RECOVERY")
	manager.Shutdown()
}

func TestPreStartTerminalCallbackFinishesBeforeNextScanCanStart(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	cancelledEntered := make(chan struct{})
	releaseCancelled := make(chan struct{})
	if err := manager.StartScan(ScanCallbacks{Cancelled: func() {
		close(cancelledEntered)
		<-releaseCancelled
	}}); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	if err := manager.StopScan(); err != nil {
		t.Fatalf("StopScan() error = %v", err)
	}
	<-cancelledEntered
	if err := manager.StartScan(ScanCallbacks{}); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("overlapping StartScan() error = %v, want ErrOperationInProgress", err)
	}
	close(releaseCancelled)
	manager.scanCallbackWg.Wait()
	manager.endRecoveryStationOperation("RECOVERY")
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return nil, nil
	}
	completed := make(chan struct{})
	if err := manager.StartScan(ScanCallbacks{Completed: func([]StationInfo) { close(completed) }}); err != nil {
		t.Fatalf("StartScan() after terminal callback error = %v", err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("next scan did not complete")
	}
	manager.Shutdown()
}

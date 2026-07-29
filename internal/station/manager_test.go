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

func mustAddress(t *testing.T, value string) tinybluetooth.Address {
	t.Helper()
	mac, err := tinybluetooth.ParseMAC(value)
	if err != nil {
		t.Fatalf("ParseMAC(%q) error = %v", value, err)
	}
	return tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}}
}

func TestBluetoothInitializationRecoversAfterRetry(t *testing.T) {
	manager := NewManager(config.NewConfig())
	attempts := 0
	manager.initializeBluetooth = func() error {
		attempts++
		if attempts == 1 {
			return errors.New("radio unavailable")
		}
		return nil
	}
	if err := manager.Initialize(); err == nil {
		t.Fatal("first initialization unexpectedly succeeded")
	}
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	if err := manager.ensureReady(); err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("initialization attempts = %d, want 2", attempts)
	}
}

func TestUnsupportedPowerReadDoesNotHideChannelRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:FF"

	manager.recordStructuredReadResult(
		&internalbluetooth.BaseStation{Address: mustAddress(t, address)},
		address,
		tinybluetooth.ErrAttReadNotPermitted,
		errors.New("temporary channel read failure"),
	)

	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("retry = %+v tracked=%v, want channel-only retry", retry, tracked)
	}
	if disconnects.Load() != 0 {
		t.Fatalf("healthy connection was disconnected %d time(s)", disconnects.Load())
	}
}

func TestPermanentUnsupportedDiscoveryDoesNotRemainInBackgroundRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F9"
	station := &internalbluetooth.BaseStation{Address: mustAddress(t, address)}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		failures: 2,
		kinds:    statusRetryConnection,
		nextAt:   time.Now().Add(time.Hour),
	}
	manager.statusRetryMutex.Unlock()

	manager.recordUnstructuredStationFailure(
		station,
		address,
		tinybluetooth.ErrAttRequestNotSupported,
	)

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked || disconnects.Load() != 0 {
		t.Fatalf("unsupported station recovery tracked=%v disconnects=%d, want false and 0", tracked, disconnects.Load())
	}
}

func TestUnsupportedFailureWithCleanupErrorStillSchedulesRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F8"
	station := &internalbluetooth.BaseStation{Address: mustAddress(t, address)}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	err := errors.Join(
		tinybluetooth.ErrAttRequestNotSupported,
		&internalbluetooth.DeviceTransportError{
			Operation: "cleanup",
			Err:       tinybluetooth.ErrGATTCommunication,
		},
	)

	manager.recordUnstructuredStationFailure(station, address, err)

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || disconnects.Load() != 1 {
		t.Fatalf("mixed failure recovery tracked=%v disconnects=%d, want true and 1", tracked, disconnects.Load())
	}
}

func TestConnectionAndChannelRetrySchedulesAreIndependent(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	manager.statusRetryMax = 4 * time.Hour
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:FA"

	manager.noteStatusFailure(address)
	manager.noteStatusFailure(address)
	manager.noteChannelFailure(address)

	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.failures != 2 || retry.channelFailures != 1 {
		t.Fatalf("retry counters = connection:%d channel:%d, want 2 and 1", retry.failures, retry.channelFailures)
	}
	if retry.nextAt.Sub(retry.lastAttempt) != 2*time.Hour {
		t.Fatalf("connection delay = %v, want 2h", retry.nextAt.Sub(retry.lastAttempt))
	}
	if retry.channelNextAt.Sub(retry.channelLastAttempt) != time.Hour {
		t.Fatalf("channel delay = %v, want 1h", retry.channelNextAt.Sub(retry.channelLastAttempt))
	}

	manager.clearStatusFailureKind(address, statusRetryConnection)
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.channelFailures != 1 || retry.channelNextAt.IsZero() ||
		effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("clearing connection retry damaged channel schedule: %+v tracked=%v", retry, tracked)
	}
}

func TestDisconnectedStationAddsImmediateConnectionRecoveryToChannelBackoff(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F7"
	channelNextAt := time.Now().Add(5 * time.Minute)
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:              statusRetryChannel,
		channelFailures:    3,
		channelLastAttempt: time.Now(),
		channelNextAt:      channelNextAt,
	}
	manager.statusRetryMutex.Unlock()

	startedAt := time.Now()
	manager.ensureStatusRecoveryTracked(address)

	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if effectiveStatusRetryKinds(retry) != statusRetryConnection|statusRetryChannel {
		t.Fatalf("retry kinds = %v, want connection and channel", effectiveStatusRetryKinds(retry))
	}
	if retry.nextAt.Before(startedAt) || retry.nextAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("connection retry time = %v, want immediate schedule", retry.nextAt)
	}
	if retry.channelFailures != 3 || !retry.channelNextAt.Equal(channelNextAt) {
		t.Fatalf("channel retry was changed: %+v", retry)
	}
}

func TestDisconnectedStationDoesNotResetExistingConnectionBackoff(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F6"
	connectionNextAt := time.Now().Add(5 * time.Minute)
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:    statusRetryConnection,
		failures: 3,
		nextAt:   connectionNextAt,
	}
	manager.statusRetryMutex.Unlock()

	manager.ensureStatusRecoveryTracked(address)

	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.failures != 3 || !retry.nextAt.Equal(connectionNextAt) {
		t.Fatalf("existing connection backoff was reset: %+v", retry)
	}
}

func TestShutdownRejectsNewOperations(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.Shutdown()
	if err := manager.beginOperation(); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("beginOperation() = %v, want ErrShuttingDown", err)
	}
	if err := manager.beginStationOperation("AA"); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("beginStationOperation() = %v, want ErrShuttingDown", err)
	}
}

func TestShutdownWaitsForActiveOperation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginOperation(); err != nil {
		t.Fatalf("beginOperation() error = %v", err)
	}
	shutdownDone := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while an operation was active")
	case <-time.After(25 * time.Millisecond):
	}
	manager.endOperation()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after the active operation ended")
	}
}

func TestShutdownWaitsForSharedConfigurationOperation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginSharedOperation(); err != nil {
		t.Fatalf("beginSharedOperation() error = %v", err)
	}
	shutdownDone := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while a shared operation was active")
	case <-time.After(25 * time.Millisecond):
	}
	manager.endSharedOperation()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after shared operation ended")
	}
	if err := manager.beginSharedOperation(); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("beginSharedOperation() after shutdown = %v, want ErrShuttingDown", err)
	}
}

func TestShutdownWaitsForInitializationAndPreventsLateScan(t *testing.T) {
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

	scanDone := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStations()
		scanDone <- err
	}()
	<-initializeStarted
	shutdownDone := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while adapter initialization was active")
	case <-time.After(25 * time.Millisecond):
	}
	close(initializeRelease)
	if err := <-scanDone; !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("scan error = %v, want ErrShuttingDown", err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after initialization stopped")
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("scan started %d time(s) after shutdown began", scanCalls.Load())
	}
	if status := manager.GetScanStatus(); status.State != "cancelled" || status.Error != "" {
		t.Fatalf("shutdown scan status = %+v, want cancelled without error", status)
	}
}

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
		[]StationInfo{{Name: "LHB-STALE", Address: address}},
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

	_, err := manager.ScanAndFetchStations()
	if !errors.Is(err, internalbluetooth.ErrScanCancelled) {
		t.Fatalf("ScanAndFetchStations() error = %v, want ErrScanCancelled", err)
	}
	if initialReads.Load() != 0 {
		t.Fatalf("initial station reads after cancellation = %d, want 0", initialReads.Load())
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

func TestAdapterUnavailableForcesInitializationRetry(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.observeBluetoothError(tinybluetooth.ErrRadioNotAvailable)
	attempts := 0
	manager.initializeBluetooth = func() error {
		attempts++
		return nil
	}
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	if err := manager.ensureReady(); err != nil {
		t.Fatalf("ensureReady() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("initialization attempts = %d, want 1", attempts)
	}
}

func TestOperationCoordinator(t *testing.T) {
	manager := NewManager(config.NewConfig())

	if err := manager.beginOperation(); err != nil {
		t.Fatalf("first operation should start: %v", err)
	}
	if !manager.IsBusy() {
		t.Fatal("manager should report busy while an operation owns the coordinator")
	}
	if err := manager.beginOperation(); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("second operation should be rejected with ErrOperationInProgress, got %v", err)
	}

	manager.endOperation()
	if manager.IsBusy() {
		t.Fatal("manager should report idle after the operation ends")
	}
}

func TestStationOperationsRejectDuplicateAndLimitConcurrency(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginStationOperation("AA"); err != nil {
		t.Fatalf("first station operation error = %v", err)
	}
	if err := manager.beginStationOperation("aa"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("duplicate station operation error = %v", err)
	}
	if err := manager.beginStationOperation("BB"); err != nil {
		t.Fatalf("second independent operation error = %v", err)
	}
	if err := manager.beginStationOperation("CC"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("third concurrent operation error = %v", err)
	}
	manager.endStationOperation("BB")
	manager.endStationOperation("AA")
	if manager.IsBusy() {
		t.Fatal("manager remained busy after station operations ended")
	}
}

func TestVisibleStationsOnlyParticipateInChannelConflicts(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["visible-a"] = &internalbluetooth.BaseStation{
		Name: "LHB-A", Channel: 4, Present: true, LastSeenAt: now, LastChannelReadAt: now,
	}
	manager.stations["visible-b"] = &internalbluetooth.BaseStation{
		Name: "LHB-B", Channel: 4, Present: true, LastSeenAt: now, LastChannelReadAt: now,
	}
	manager.stations["not-visible"] = &internalbluetooth.BaseStation{
		Name: "LHB-C", Channel: 5, Present: false, LastSeenAt: now, LastChannelReadAt: now,
	}
	manager.stations["visible-c"] = &internalbluetooth.BaseStation{
		Name: "LHB-D", Channel: 5, Present: true, LastSeenAt: now, LastChannelReadAt: now,
	}

	infos := manager.GetStationInfo()
	conflicts := map[string]bool{}
	for _, info := range infos {
		conflicts[info.OriginalName] = info.ChannelConflict
	}
	if !conflicts["LHB-A"] || !conflicts["LHB-B"] {
		t.Fatalf("visible duplicate channel was not marked: %v", conflicts)
	}
	if conflicts["LHB-C"] || conflicts["LHB-D"] {
		t.Fatalf("station absent from latest scan affected conflict detection: %v", conflicts)
	}
}

func TestStationInfoIsSortedByChannelNameAndAddress(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["unknown"] = &internalbluetooth.BaseStation{Name: "Unknown", Address: mustAddress(t, "11:22:33:44:55:66")}
	manager.stations["channel-two-z"] = &internalbluetooth.BaseStation{Name: "Zulu", Address: mustAddress(t, "22:22:33:44:55:66"), Channel: 2}
	manager.stations["channel-two-a2"] = &internalbluetooth.BaseStation{Name: "alpha", Address: mustAddress(t, "44:22:33:44:55:66"), Channel: 2}
	manager.stations["channel-two-a1"] = &internalbluetooth.BaseStation{Name: "Alpha", Address: mustAddress(t, "33:22:33:44:55:66"), Channel: 2}
	manager.stations["channel-one"] = &internalbluetooth.BaseStation{Name: "Bravo", Address: mustAddress(t, "55:22:33:44:55:66"), Channel: 1}

	infos := manager.GetStationInfo()
	got := make([]string, 0, len(infos))
	for _, info := range infos {
		got = append(got, info.Address)
	}
	want := []string{
		"55:22:33:44:55:66",
		"33:22:33:44:55:66",
		"44:22:33:44:55:66",
		"22:22:33:44:55:66",
		"11:22:33:44:55:66",
	}
	if len(got) != len(want) {
		t.Fatalf("sorted station count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("sorted addresses = %v, want %v", got, want)
		}
	}
}

func TestStationMissedOnceDoesNotCreateHardChannelConflict(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["fresh"] = &internalbluetooth.BaseStation{
		Name: "LHB-FRESH", Channel: 4, Present: true,
	}
	manager.stations["stale"] = &internalbluetooth.BaseStation{
		Name: "LHB-STALE", Channel: 4, Present: true, MissedScans: 1,
	}

	for _, info := range manager.GetStationInfo() {
		if info.ChannelConflict {
			t.Fatalf("stale scan value created a hard conflict: %+v", info)
		}
	}
}

func TestSetStationChannelRejectsVisibleConflictBeforeWrite(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.stations["other"] = &internalbluetooth.BaseStation{
		Name: "LHB-OTHER", Channel: 5, Present: true, LastSeenAt: now, LastChannelReadAt: now,
	}

	_, err := manager.SetStationChannel("target", 5, false)
	if !errors.Is(err, ErrChannelConflict) {
		t.Fatalf("SetStationChannel() error = %v, want ErrChannelConflict", err)
	}
}

func TestSetStationChannelAlreadyAtFreshTargetIsNoOp(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginStationOperation("busy-1"); err != nil {
		t.Fatalf("beginStationOperation(busy-1) error = %v", err)
	}
	defer manager.endStationOperation("busy-1")
	if err := manager.beginStationOperation("busy-2"); err != nil {
		t.Fatalf("beginStationOperation(busy-2) error = %v", err)
	}
	defer manager.endStationOperation("busy-2")
	manager.initializeErr = errors.New("adapter unavailable")
	manager.nextInitializeAt = time.Now().Add(time.Hour)
	manager.initializeBluetooth = func() error {
		t.Fatal("Bluetooth initialization was attempted for a channel no-op")
		return nil
	}
	address := "AA:BB:CC:DD:EE:09"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, address),
		Name:              "LHB-SAME-CHANNEL",
		Channel:           7,
		LastChannelReadAt: time.Now(),
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability refresh was attempted for a channel no-op")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setChannel = func(*internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		t.Fatal("channel read/write operation was attempted for a channel no-op")
		return internalbluetooth.ChannelWriteResult{}, nil
	}

	result, err := manager.SetStationChannel(address, 7, false)
	if err != nil {
		t.Fatalf("SetStationChannel() error = %v", err)
	}
	if result.Address != address || result.PreviousChannel != 7 || result.Channel != 7 ||
		result.CommandSent || !result.Confirmed || len(result.Warnings) != 0 {
		t.Fatalf("channel no-op result = %+v", result)
	}
}

func TestSetStationChannelPreservesPostWriteConfirmationError(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, "AA:BB:CC:DD:EE:01"),
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.setChannel = func(*internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		return internalbluetooth.ChannelWriteResult{
				PreviousChannel: 3,
				Channel:         internalbluetooth.ChannelUnknown,
				CommandSent:     true,
			},
			fmt.Errorf(
				"channel confirmation failed: %w",
				&internalbluetooth.UnsupportedCapabilityError{
					Capability: "channel read",
					Err:        tinybluetooth.ErrAttReadNotPermitted,
				},
			)
	}

	result, err := manager.SetStationChannel("target", 5, false)
	if err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetStationChannel() error = %v, want post-write confirmation error", err)
	}
	if !errors.Is(err, tinybluetooth.ErrAttReadNotPermitted) {
		t.Fatalf("SetStationChannel() error = %v, want ATT read-not-permitted cause", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "command was sent") {
		t.Fatalf("warnings = %v, want unconfirmed command warning", result.Warnings)
	}
	if !result.CommandSent || result.Confirmed || result.ConfirmationError == "" {
		t.Fatalf("result = %+v, want sent but unconfirmed with confirmation error", result)
	}
}

func TestSetStationChannelMapsConfirmedWriteResult(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	address := "AA:BB:CC:DD:EE:04"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, address),
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.setChannel = func(*internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		return internalbluetooth.ChannelWriteResult{
			PreviousChannel: 3,
			Channel:         5,
			CommandSent:     true,
			WriteWarning:    "confirmed after retry",
		}, nil
	}

	result, err := manager.SetStationChannel(address, 5, false)
	if err != nil {
		t.Fatalf("SetStationChannel() error = %v", err)
	}
	if result.PreviousChannel != 3 || result.Channel != 5 || !result.CommandSent || !result.Confirmed ||
		result.ConfirmationError != "" || len(result.Warnings) != 1 {
		t.Fatalf("confirmed channel result = %+v", result)
	}
}

func TestSetStationChannelMapsPreWriteUnsupportedCapability(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, "AA:BB:CC:DD:EE:02"),
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.setChannel = func(*internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		return internalbluetooth.ChannelWriteResult{},
			&internalbluetooth.UnsupportedCapabilityError{
				Capability: "channel write",
				Err:        tinybluetooth.ErrAttWriteNotPermitted,
			}
	}

	result, err := manager.SetStationChannel("target", 5, false)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetStationChannel() error = %v, want ErrUnsupported", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, command was not sent", result.Warnings)
	}
}

func TestStationChannelRequiresRecentScan(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Name:       "LHB-TARGET",
		Channel:    3,
		Present:    true,
		LastSeenAt: time.Now().Add(-channelScanFreshnessWindow - time.Second),
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}

	_, err := manager.SetStationChannel("target", 5, false)
	if !errors.Is(err, ErrScanRequired) {
		t.Fatalf("SetStationChannel() error = %v, want ErrScanRequired", err)
	}
}

func TestStationChannelRejectsUncertainPresence(t *testing.T) {
	now := time.Now()
	newStation := func(address string) *internalbluetooth.BaseStation {
		station := &internalbluetooth.BaseStation{
			Name: "LHB-" + address[len(address)-2:], Address: mustAddress(t, address),
			Channel: 3, Present: true, LastSeenAt: now, LastChannelReadAt: now,
			Capabilities: internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true},
		}
		station.MarkPresenceUncertain()
		return station
	}

	t.Run("target", func(t *testing.T) {
		manager := NewManager(config.NewConfig())
		address := "11:22:33:44:55:91"
		manager.stations[address] = newStation(address)
		if _, err := manager.SetStationChannel(address, 5, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("SetStationChannel() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("other station", func(t *testing.T) {
		manager := NewManager(config.NewConfig())
		targetAddress := "11:22:33:44:55:92"
		manager.stations[targetAddress] = &internalbluetooth.BaseStation{
			Name: "LHB-TARGET", Address: mustAddress(t, targetAddress), Channel: 3, Present: true,
			LastSeenAt: now, LastChannelReadAt: now,
			Capabilities: internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true},
		}
		manager.stations["uncertain"] = newStation("11:22:33:44:55:93")
		if _, err := manager.SetStationChannel(targetAddress, 5, false); !errors.Is(err, ErrScanRequired) {
			t.Fatalf("SetStationChannel() error = %v, want ErrScanRequired", err)
		}
	})
}

func TestStationChannelRequiresUnknownRiskAcknowledgement(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Name: "LHB-TARGET", Channel: 3, Present: true, LastSeenAt: now,
		Capabilities: internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true},
	}
	manager.stations["unknown"] = &internalbluetooth.BaseStation{
		Name: "LHB-UNKNOWN", Present: true, LastSeenAt: now,
	}

	_, err := manager.SetStationChannel("target", 5, false)
	if !errors.Is(err, ErrScanRequired) {
		t.Fatalf("SetStationChannel() error = %v, want ErrScanRequired", err)
	}
}

func TestStaleChannelDoesNotCreateHardConflict(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["fresh"] = &internalbluetooth.BaseStation{
		Name: "LHB-FRESH", Channel: 4, Present: true, LastSeenAt: now, LastChannelReadAt: now,
	}
	manager.stations["stale"] = &internalbluetooth.BaseStation{
		Name: "LHB-STALE", Channel: 4, Present: true, LastSeenAt: now,
		LastChannelReadAt: now.Add(-statusFreshnessWindow - time.Second),
	}

	for _, info := range manager.GetStationInfo() {
		if info.ChannelConflict {
			t.Fatalf("stale channel created a hard conflict: %+v", info)
		}
	}
}

func TestStationLookupIsCaseInsensitive(t *testing.T) {
	manager := NewManager(config.NewConfig())
	expected := &internalbluetooth.BaseStation{Name: "LHB-TEST"}
	manager.stations["AA:BB:CC:DD:EE:FF"] = expected

	actual, err := manager.stationByAddress("aa:bb:cc:dd:ee:ff")
	if err != nil || actual != expected {
		t.Fatalf("stationByAddress() = %p, %v; want %p", actual, err, expected)
	}
}

func TestSetStationChannelValidatesRange(t *testing.T) {
	manager := NewManager(config.NewConfig())
	for _, channel := range []int{0, 17} {
		if _, err := manager.SetStationChannel("missing", channel, false); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("channel %d error = %v, want ErrInvalidArgument", channel, err)
		}
	}
}

func TestSetAllStationsPowerValidatesState(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.SetAllStationsPower("invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetAllStationsPower() error = %v, want ErrInvalidArgument", err)
	}
}

func TestSetAllStationsPowerSkipsIneligibleStations(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["already-on"] = &internalbluetooth.BaseStation{
		Name: "LHB-ON", Present: true, PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x0B,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true, LastPowerReadAt: time.Now(),
	}
	manager.stations["booting"] = &internalbluetooth.BaseStation{
		Name: "LHB-BOOTING", Present: true, PowerState: internalbluetooth.PowerStateBooting,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true, LastPowerReadAt: time.Now(),
	}
	manager.stations["not-visible"] = &internalbluetooth.BaseStation{
		Name: "LHB-OFFLINE", Present: false, PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x0B,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true, Standby: false}, CapabilitiesKnown: true,
		LastPowerReadAt: time.Now(),
	}

	if err := manager.SetAllStationsPower("on"); err != nil {
		t.Fatalf("SetAllStationsPower() should skip all ineligible stations, got %v", err)
	}

	manager.stations["no-standby"] = &internalbluetooth.BaseStation{
		Name: "LHB-NO-STANDBY", Present: true, PowerState: internalbluetooth.PowerStateSleep,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true, Standby: false}, CapabilitiesKnown: true,
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerWrite: true, Standby: false}, nil
	}
	if err := manager.SetAllStationsPower("standby"); err != nil {
		t.Fatalf("SetAllStationsPower() should skip stations without standby capability, got %v", err)
	}
	result, err := manager.SetAllStationsPowerDetailed("standby")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 4 {
		t.Fatalf("detailed result count = %d, want all four known stations", len(result.Results))
	}
	for _, stationResult := range result.Results {
		if !stationResult.Skipped || stationResult.Reason == "" {
			t.Fatalf("expected structured skipped result: %+v", stationResult)
		}
		if stationResult.Station.Name == "" {
			t.Fatalf("skipped result is missing station data: %+v", stationResult)
		}
	}
}

func TestSingleStandbyRefreshesCachedUnsupportedCapability(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:81"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-STANDBY", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true, Standby: false}, CapabilitiesKnown: true,
	}
	var refreshes atomic.Int32
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		refreshes.Add(1)
		return internalbluetooth.Capabilities{PowerWrite: true, Standby: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateStandby, Confirmed: true}, nil
	}

	if _, err := manager.SetStationPower(address, "standby"); err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("capability refreshes = %d, want 1", refreshes.Load())
	}
}

func TestBulkStandbyRefreshesCachedUnsupportedCapability(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:82"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-BULK-STANDBY", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true, Standby: false}, CapabilitiesKnown: true,
	}
	var refreshes atomic.Int32
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		refreshes.Add(1)
		return internalbluetooth.Capabilities{PowerWrite: true, Standby: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateStandby, Confirmed: true}, nil
	}

	result, err := manager.SetAllStationsPowerDetailed("standby")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if refreshes.Load() != 1 || len(result.Results) != 1 || result.Results[0].Skipped || !result.Results[0].Success {
		t.Fatalf("bulk result = %+v, refreshes = %d", result, refreshes.Load())
	}
}

func TestRefreshCapabilitiesReturnsStationAfterChannelOnlyReadFailure(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:83"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CHANNEL-PARTIAL", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerRead: true, ChannelRead: true}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Channel: errors.New("channel unavailable")}
	}

	info, err := manager.RefreshStationCapabilities(address)
	if err != nil {
		t.Fatalf("RefreshStationCapabilities() error = %v", err)
	}
	if info.Address != address || info.Name != "LHB-CHANNEL-PARTIAL" {
		t.Fatalf("RefreshStationCapabilities() returned zero or wrong station: %+v", info)
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

func TestBulkPowerIncludesAbsentKnownStations(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["offline"] = &internalbluetooth.BaseStation{
		Name:              "LHB-OFFLINE",
		Present:           false,
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		LastPowerReadAt:   time.Now(),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Address != "00:00:00:00:00:00" ||
		!result.Results[0].Skipped || !result.Results[0].Success {
		t.Fatalf("absent known station was excluded from bulk result: %+v", result.Results)
	}
}

func TestBulkPowerPureSkipsDoNotRequireBluetoothReadiness(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginStationOperation("busy"); err != nil {
		t.Fatalf("beginStationOperation() error = %v", err)
	}
	defer manager.endStationOperation("busy")
	manager.initializeErr = errors.New("adapter unavailable")
	manager.nextInitializeAt = time.Now().Add(time.Hour)
	manager.initializeBluetooth = func() error {
		t.Fatal("Bluetooth initialization was attempted for a pure-skip batch")
		return nil
	}
	manager.stations["11:22:33:44:55:61"] = &internalbluetooth.BaseStation{
		Name:            "LHB-ALREADY",
		Address:         mustAddress(t, "11:22:33:44:55:61"),
		Present:         true,
		PowerState:      internalbluetooth.PowerStateOn,
		RawPowerState:   0x0B,
		LastPowerReadAt: time.Now(),
	}
	manager.stations["11:22:33:44:55:62"] = &internalbluetooth.BaseStation{
		Name:            "LHB-BOOTING",
		Address:         mustAddress(t, "11:22:33:44:55:62"),
		Present:         true,
		PowerState:      internalbluetooth.PowerStateBooting,
		RawPowerState:   0x01,
		LastPowerReadAt: time.Now(),
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted for a pure-skip batch")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 2 ||
		!result.Results[0].Skipped || !result.Results[0].Success || !result.Results[0].Confirmed ||
		!result.Results[1].Skipped || result.Results[1].Success ||
		result.Results[1].Reason != "station is booting" {
		t.Fatalf("pure-skip batch result = %+v", result.Results)
	}
}

func TestBulkPowerDoesNotStartQueuedWorkAfterShutdown(t *testing.T) {
	manager := NewManager(config.NewConfig())
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var writes atomic.Int32
	for index, address := range []string{
		"11:22:33:44:55:61",
		"11:22:33:44:55:62",
		"11:22:33:44:55:63",
		"11:22:33:44:55:64",
	} {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name:              fmt.Sprintf("LHB-%d", index),
			Address:           mustAddress(t, address),
			Present:           true,
			Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
			CapabilitiesKnown: true,
		}
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		started <- struct{}{}
		<-release
		writes.Add(1)
		return internalbluetooth.PowerControlResult{Confirmed: true}, nil
	}

	type bulkResponse struct {
		result BulkPowerResult
		err    error
	}
	done := make(chan bulkResponse, 1)
	go func() {
		result, err := manager.SetAllStationsPowerDetailed("on")
		done <- bulkResponse{result: result, err: err}
	}()
	<-started
	<-started
	manager.BeginShutdown()
	close(release)

	response := <-done
	if response.err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", response.err)
	}
	if got := writes.Load(); got != 2 {
		t.Fatalf("power writes after shutdown = %d total, want only 2 already-started writes", got)
	}
	cancelled := 0
	for _, stationResult := range response.result.Results {
		if stationResult.Skipped && stationResult.Reason == "application is shutting down" {
			cancelled++
		}
	}
	if cancelled != 2 {
		t.Fatalf("shutdown-skipped results = %d, want 2: %+v", cancelled, response.result.Results)
	}
}

func TestIsRecentRejectsFutureTimestamps(t *testing.T) {
	now := time.Now()
	window := 45 * time.Second
	tests := []struct {
		name  string
		value time.Time
		want  bool
	}{
		{name: "zero", value: time.Time{}, want: false},
		{name: "current", value: now, want: true},
		{name: "within window", value: now.Add(-window), want: true},
		{name: "expired", value: now.Add(-window - time.Nanosecond), want: false},
		{name: "future", value: now.Add(time.Nanosecond), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRecent(test.value, now, window); got != test.want {
				t.Fatalf("isRecent() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAbsentRecoveryStopsExhaustedKindsIndependently(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:65"
	station := &internalbluetooth.BaseStation{Address: mustAddress(t, address), Present: false}
	manager.statusRetries[address] = statusRetry{
		failures:        statusAbsentRetryLimit,
		channelFailures: statusAbsentRetryLimit - 1,
		nextAt:          time.Now(),
		channelNextAt:   time.Now(),
		kinds:           statusRetryConnection | statusRetryChannel,
	}

	manager.stopExhaustedAbsentRecovery(address, station)
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel || retry.channelFailures != statusAbsentRetryLimit-1 {
		t.Fatalf("retry after connection exhaustion = %+v tracked=%v, want channel-only", retry, tracked)
	}

	manager.statusRetryMutex.Lock()
	retry.channelFailures = statusAbsentRetryLimit
	manager.statusRetries[address] = retry
	manager.statusRetryMutex.Unlock()
	manager.stopExhaustedAbsentRecovery(address, station)
	manager.statusRetryMutex.Lock()
	_, tracked = manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("absent station remained scheduled after both retry kinds were exhausted")
	}
}

func TestBulkPowerResultsUseStableStationOrder(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	for key, station := range map[string]*internalbluetooth.BaseStation{
		"unknown": {
			Name: "Unknown", Address: mustAddress(t, "11:22:33:44:55:66"),
			PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x0B, LastPowerReadAt: now,
		},
		"channel-two-z": {
			Name: "Zulu", Address: mustAddress(t, "22:22:33:44:55:66"), Channel: 2,
			PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x0B, LastPowerReadAt: now,
		},
		"channel-two-a": {
			Name: "Alpha", Address: mustAddress(t, "33:22:33:44:55:66"), Channel: 2,
			PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x0B, LastPowerReadAt: now,
		},
		"channel-one": {
			Name: "Bravo", Address: mustAddress(t, "44:22:33:44:55:66"), Channel: 1,
			PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x0B, LastPowerReadAt: now,
		},
	} {
		manager.stations[key] = station
	}

	want := []string{
		"44:22:33:44:55:66",
		"33:22:33:44:55:66",
		"22:22:33:44:55:66",
		"11:22:33:44:55:66",
	}
	for iteration := 0; iteration < 20; iteration++ {
		result, err := manager.SetAllStationsPowerDetailed("on")
		if err != nil {
			t.Fatalf("iteration %d: SetAllStationsPowerDetailed() error = %v", iteration, err)
		}
		got := make([]string, len(result.Results))
		for index, stationResult := range result.Results {
			got[index] = stationResult.Address
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("iteration %d: result order = %v, want %v", iteration, got, want)
			}
		}
	}
}

func TestBulkPowerReportsConfirmedUnsupportedCapabilitiesAsSkipped(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:66"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted for an unsupported station")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped ||
		result.Results[0].Reason != "power control is not supported" ||
		result.Results[0].Success || result.Results[0].CommandSent {
		t.Fatalf("unsupported result = %+v", result.Results)
	}
}

func TestBulkPowerKeepsCapabilityConnectionFailuresAsFailed(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:66"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-FAILED", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, errors.New("connection failed")
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Skipped ||
		result.Results[0].Success || result.Results[0].Error == "" {
		t.Fatalf("connection failure result = %+v", result.Results)
	}
}

func TestBulkPowerLateUnsupportedCapabilityIsSkipped(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:67"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-LATE-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerWrite: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, &internalbluetooth.UnsupportedCapabilityError{
			Capability: "power control",
			Err:        tinybluetooth.ErrAttWriteNotPermitted,
		}
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped ||
		result.Results[0].Success || result.Results[0].Error != "" ||
		!strings.Contains(result.Results[0].Reason, "power control") {
		t.Fatalf("late unsupported result = %+v", result.Results)
	}
}

func TestBulkConfirmationTransportFailureKeepsRecoveryScheduled(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:68"
	station := &internalbluetooth.BaseStation{
		Name: "LHB-CONFIRM", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, &internalbluetooth.PowerConfirmationError{
			Target: internalbluetooth.PowerStateOn,
			Err:    tinybluetooth.ErrGATTUnreachable,
		}
	}
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error { return nil }

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Success ||
		!result.Results[0].CommandSent || result.Results[0].Confirmed || result.Results[0].Error == "" {
		t.Fatalf("confirmation result = %+v", result.Results)
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("confirmation transport failure cleared its recovery record")
	}
}

func TestSinglePowerConfirmationUnsupportedReadPreservesCommandSent(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:76"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CONFIRM-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true}, nil
	}
	confirmationErr := &internalbluetooth.PowerConfirmationError{
		Target: internalbluetooth.PowerStateOn,
		Err: &internalbluetooth.UnsupportedCapabilityError{
			Capability: "power read",
			Err:        tinybluetooth.ErrAttReadNotPermitted,
		},
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateUnknown}, confirmationErr
	}

	result, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, tinybluetooth.ErrAttReadNotPermitted) {
		t.Fatalf("SetStationPower() error = %v, want confirmation read error", err)
	}
	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("confirmation error was incorrectly converted to ErrUnsupported: %v", err)
	}
	if !result.CommandSent || result.Confirmed || result.ConfirmationError == "" {
		t.Fatalf("power result = %+v, want sent but unconfirmed", result)
	}
}

func TestSinglePowerAlreadyAtConfirmedTargetIsNoOp(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginStationOperation("busy-1"); err != nil {
		t.Fatalf("beginStationOperation(busy-1) error = %v", err)
	}
	defer manager.endStationOperation("busy-1")
	if err := manager.beginStationOperation("busy-2"); err != nil {
		t.Fatalf("beginStationOperation(busy-2) error = %v", err)
	}
	defer manager.endStationOperation("busy-2")
	manager.initializeErr = errors.New("adapter unavailable")
	manager.nextInitializeAt = time.Now().Add(time.Hour)
	manager.initializeBluetooth = func() error {
		t.Fatal("Bluetooth initialization was attempted for a confirmed no-op")
		return nil
	}
	address := "11:22:33:44:55:78"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-ALREADY-ON",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		LastPowerReadAt:   time.Now(),
		CapabilitiesKnown: false,
	}
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability discovery was attempted for a confirmed no-op")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability refresh was attempted for a confirmed no-op")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted for a confirmed no-op")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	if result.CommandSent || !result.Confirmed || result.Station.Address != address {
		t.Fatalf("no-op power result = %+v", result)
	}
}

func TestSinglePowerRejectsFreshBootingStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.initializeErr = errors.New("adapter unavailable")
	manager.nextInitializeAt = time.Now().Add(time.Hour)
	manager.initializeBluetooth = func() error {
		t.Fatal("Bluetooth initialization was attempted while station was booting")
		return nil
	}
	address := "11:22:33:44:55:79"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-BOOTING",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateBooting,
		RawPowerState:     0x01,
		LastPowerReadAt:   time.Now(),
		CapabilitiesKnown: false,
	}
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability discovery was attempted while station was booting")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability refresh was attempted while station was booting")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted while station was booting")
		return internalbluetooth.PowerControlResult{}, nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, ErrOperationInProgress) || !strings.Contains(err.Error(), "station is booting") {
		t.Fatalf("SetStationPower() error = %v, want booting ErrOperationInProgress", err)
	}
}

func TestBulkPowerConfirmationUnsupportedReadIsNotSkipped(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:77"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-BULK-CONFIRM-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateUnknown}, &internalbluetooth.PowerConfirmationError{
			Target: internalbluetooth.PowerStateOn,
			Err: &internalbluetooth.UnsupportedCapabilityError{
				Capability: "power read",
				Err:        tinybluetooth.ErrAttReadNotPermitted,
			},
		}
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("bulk results = %+v", result.Results)
	}
	stationResult := result.Results[0]
	if stationResult.Skipped || !stationResult.Success || !stationResult.CommandSent ||
		stationResult.Confirmed || stationResult.Error == "" {
		t.Fatalf("bulk confirmation result = %+v, want sent but unconfirmed", stationResult)
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("unsupported confirmation read incorrectly scheduled connection recovery")
	}
}

func TestRecoverySchedulerIncludesKnownAbsentStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Millisecond
	address := "11:22:33:44:55:69"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-ABSENT", Address: mustAddress(t, address), Present: false,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	manager.statusRetryMutex.Unlock()
	var recovered atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered.Add(1)
		return nil
	}

	manager.runStatusRecoveryRound()
	if recovered.Load() != 1 {
		t.Fatalf("absent known station recovery attempts = %d, want 1", recovered.Load())
	}
}

func TestSuccessfulPowerOperationPreservesPendingChannelRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:70"
	station := &internalbluetooth.BaseStation{
		Name: "LHB-CHANNEL-RETRY", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:  statusRetryConnection | statusRetryChannel,
		nextAt: time.Now().Add(time.Hour),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerWrite: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{Confirmed: true}, nil
	}

	if _, err := manager.SetStationPower(address, "on"); err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("retry after power success = %+v, tracked=%v; want channel-only", retry, tracked)
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

	manager.markScanFinished([]StationInfo{{}, {}}, 2, nil)
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

func TestStaleBootingStationIsNotSkippedByBulkSelection(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["booting"] = &internalbluetooth.BaseStation{
		Name: "LHB-BOOTING", Present: true, PowerState: internalbluetooth.PowerStateBooting,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: false}, CapabilitiesKnown: true,
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Skipped {
		t.Fatalf("stale booting station was skipped: %+v", result.Results)
	}
}

func TestStationGATTFailureInvalidatesConnectionAndRegistersRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:61"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-61",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, tinybluetooth.ErrGATTUnreachable
	}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, tinybluetooth.ErrGATTUnreachable) {
		t.Fatalf("SetStationPower() error = %v, want ErrGATTUnreachable", err)
	}
	if got := disconnects.Load(); got != 1 {
		t.Fatalf("disconnect calls = %d, want 1", got)
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("GATT communication failure did not register status recovery")
	}
}

func TestStatusCheckSchedulesInitialRecoveryForDisconnectedStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["disconnected"] = &internalbluetooth.BaseStation{
		Name:          "LHB-DISCONNECTED",
		Present:       true,
		PowerState:    internalbluetooth.PowerStateSleep,
		RawPowerState: 0x00,
	}

	infos, err := manager.CheckAllStationStatuses()
	if err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	if len(infos) != 1 || infos[0].PowerState != int(internalbluetooth.PowerStateSleep) || infos[0].ConnectionState != "disconnected" {
		t.Fatalf("failed recovery did not preserve stale station state: %+v", infos)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.statusRetryMutex.Lock()
		_, tracked := manager.statusRetries[infos[0].Address]
		manager.statusRetryMutex.Unlock()
		if tracked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background recovery did not create a retry backoff")
}

func TestStatusCheckReadsConnectedAndTracksDisconnectedStationsTogether(t *testing.T) {
	manager := NewManager(config.NewConfig())
	connectedAddress := "11:22:33:44:55:71"
	disconnectedAddress := "11:22:33:44:55:72"
	connected := &internalbluetooth.BaseStation{
		Name: "LHB-CONNECTED", Address: mustAddress(t, connectedAddress), Present: true,
	}
	disconnected := &internalbluetooth.BaseStation{
		Name: "LHB-DISCONNECTED", Address: mustAddress(t, disconnectedAddress), Present: true,
	}
	manager.stations[connectedAddress] = connected
	manager.stations[disconnectedAddress] = disconnected
	manager.bluetoothOps.stationConnected = func(station *internalbluetooth.BaseStation) bool {
		return station == connected
	}
	var connectedReads atomic.Int32
	manager.bluetoothOps.readPowerStateContext = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		if station != connected {
			t.Fatalf("unexpected status read for %s", station.Snapshot().Address)
		}
		connectedReads.Add(1)
		return nil
	}
	recoveryStarted := make(chan struct{})
	releaseRecovery := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		if station == disconnected {
			close(recoveryStarted)
			<-releaseRecovery
		}
		return nil
	}

	if _, err := manager.CheckAllStationStatuses(); err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	if connectedReads.Load() != 1 {
		t.Fatalf("connected reads = %d, want 1", connectedReads.Load())
	}
	select {
	case <-recoveryStarted:
	case <-time.After(time.Second):
		t.Fatal("disconnected station was not scheduled while another station was connected")
	}
	close(releaseRecovery)
	manager.Shutdown()
}

func TestStatusRefreshTimeoutBoundsWholeFleet(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusReadTimeout = time.Hour
	manager.statusRefreshTimeout = 40 * time.Millisecond
	for index := 1; index <= 5; index++ {
		address := fmt.Sprintf("11:22:33:44:77:%02X", index)
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name: "LHB-STATUS-PHASE", Address: mustAddress(t, address), Present: true,
		}
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	manager.bluetoothOps.readPowerStateContext = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		<-ctx.Done()
		return ctx.Err()
	}

	started := time.Now()
	_, err := manager.CheckAllStationStatuses()
	if err == nil {
		t.Fatal("CheckAllStationStatuses() error = nil, want incomplete refresh")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("fleet status refresh took %v, want a fleet-wide bound", elapsed)
	}
	manager.statusRetryMutex.Lock()
	for address, station := range manager.stations {
		retry, tracked := manager.statusRetries[address]
		if !tracked || effectiveStatusRetryKinds(retry)&statusRetryRefresh == 0 {
			t.Fatalf("phase-limited status retry for %s = %+v, tracked=%v", address, retry, tracked)
		}
		if station.Snapshot().LastError != "" {
			t.Fatalf("phase budget marked %s as a device failure: %q", address, station.Snapshot().LastError)
		}
	}
	manager.statusRetryMutex.Unlock()
}

func TestStatusCheckLeavesOneSlotForForegroundWork(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	connectedAddresses := []string{"11:22:33:44:55:73", "11:22:33:44:55:74"}
	connectedStations := make(map[*internalbluetooth.BaseStation]struct{})
	for _, address := range connectedAddresses {
		station := &internalbluetooth.BaseStation{
			Name: "LHB-" + address[len(address)-2:], Address: mustAddress(t, address), Present: true,
		}
		manager.stations[address] = station
		connectedStations[station] = struct{}{}
	}
	disconnectedAddress := "11:22:33:44:55:75"
	disconnected := &internalbluetooth.BaseStation{
		Name: "LHB-DISCONNECTED", Address: mustAddress(t, disconnectedAddress), Present: true,
	}
	manager.stations[disconnectedAddress] = disconnected
	manager.bluetoothOps.stationConnected = func(station *internalbluetooth.BaseStation) bool {
		_, connected := connectedStations[station]
		return connected
	}
	readStarted := make(chan struct{}, len(connectedAddresses))
	releaseReads := make(chan struct{})
	manager.bluetoothOps.readPowerStateContext = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		readStarted <- struct{}{}
		select {
		case <-releaseReads:
		case <-manager.shutdownCh:
		}
		return nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.CheckAllStationStatuses()
		result <- err
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("status read did not acquire a GATT slot")
	}
	if err := manager.beginForegroundStationOperation("11:22:33:44:55:76"); err != nil {
		t.Fatalf("foreground operation could not use the reserved GATT slot: %v", err)
	}
	manager.endStationOperation("11:22:33:44:55:76")
	close(releaseReads)
	if err := <-result; err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
}

func TestShutdownCannotMissScanAfterReadinessCheck(t *testing.T) {
	manager := NewManager(config.NewConfig())
	ready := make(chan struct{})
	release := make(chan struct{})
	manager.scanReadyHook = func() {
		close(ready)
		<-release
	}
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scanCalls.Add(1)
		return nil, nil
	}

	result := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStations()
		result <- err
	}()
	<-ready
	manager.BeginShutdown()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("ScanAndFetchStations() error = %v, want ErrShuttingDown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not leave readiness handoff after shutdown")
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("platform scan started %d time(s) after shutdown", scanCalls.Load())
	}
	manager.Shutdown()
}

func TestStatusCheckReportsBusyStationAndContinuesOtherReads(t *testing.T) {
	manager := NewManager(config.NewConfig())
	addresses := []string{"11:22:33:44:55:61", "11:22:33:44:55:62"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name:    "LHB-" + address[len(address)-2:],
			Address: mustAddress(t, address),
			Present: true,
		}
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool {
		return true
	}
	var readAddressesMutex sync.Mutex
	readAddresses := make([]string, 0, 1)
	manager.bluetoothOps.readPowerStateContext = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		readAddressesMutex.Lock()
		readAddresses = append(readAddresses, station.Snapshot().Address)
		readAddressesMutex.Unlock()
		return nil
	}

	if err := manager.beginStationOperation(addresses[0]); err != nil {
		t.Fatalf("reserve busy station: %v", err)
	}
	defer manager.endStationOperation(addresses[0])

	_, err := manager.CheckAllStationStatuses()
	if !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("CheckAllStationStatuses() error = %v, want ErrOperationInProgress", err)
	}
	readAddressesMutex.Lock()
	defer readAddressesMutex.Unlock()
	if len(readAddresses) != 1 || readAddresses[0] != addresses[1] {
		t.Fatalf("status reads = %v, want only %s", readAddresses, addresses[1])
	}
	manager.statusRetryMutex.Lock()
	_, busyTracked := manager.statusRetries[addresses[0]]
	manager.statusRetryMutex.Unlock()
	if busyTracked {
		t.Fatal("busy station was incorrectly added to connection recovery")
	}
}

func TestForegroundCancelsStatusReadOnSameStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:60"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:    "LHB-60",
		Address: mustAddress(t, address),
		Present: true,
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool {
		return true
	}
	statusStarted := make(chan struct{})
	manager.bluetoothOps.readPowerStateContext = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(statusStarted)
		<-ctx.Done()
		return ctx.Err()
	}

	statusDone := make(chan error, 1)
	go func() {
		_, err := manager.CheckAllStationStatuses()
		statusDone <- err
	}()
	<-statusStarted

	foregroundDone := make(chan error, 1)
	go func() {
		err := manager.beginForegroundStationOperation(address)
		if err == nil {
			manager.endStationOperation(address)
		}
		foregroundDone <- err
	}()
	if err := <-statusDone; err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("preempted status read retry kinds = %v, want refresh retry", retry.kinds)
	}
	select {
	case err := <-foregroundDone:
		if err != nil {
			t.Fatalf("foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground operation did not continue after the status read completed")
	}
}

func TestForegroundGlobalOperationCancelsStatusRefresh(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:64"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-64", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	statusStarted := make(chan struct{})
	manager.bluetoothOps.readPowerStateContext = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(statusStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	statusResult := make(chan error, 1)
	go func() {
		_, err := manager.CheckAllStationStatuses()
		statusResult <- err
	}()
	<-statusStarted

	globalResult := make(chan error, 1)
	go func() {
		err := manager.beginForegroundGlobalOperation()
		if err == nil {
			manager.endForegroundGlobalOperation()
		}
		globalResult <- err
	}()
	if err := <-statusResult; err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("preempted status refresh retry kinds = %v, want refresh retry", retry.kinds)
	}
	select {
	case err := <-globalResult:
		if err != nil {
			t.Fatalf("global foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("global foreground operation did not continue after status refresh")
	}
}

func TestForegroundGlobalOperationWaitsForRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}
	globalResult := make(chan error, 1)
	go func() {
		err := manager.beginForegroundGlobalOperation()
		if err == nil {
			manager.endForegroundGlobalOperation()
		}
		globalResult <- err
	}()
	select {
	case err := <-globalResult:
		t.Fatalf("global foreground operation returned before recovery completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	manager.endRecoveryStationOperation("RECOVERY")
	select {
	case err := <-globalResult:
		if err != nil {
			t.Fatalf("global foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("global foreground operation did not continue after recovery")
	}
}

func TestStatusRecoveryBackfillsBusyCandidatesAndLimitsConcurrency(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	now := time.Now()
	addresses := []string{"11:22:33:44:55:61", "11:22:33:44:55:62", "11:22:33:44:55:63"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name:    "LHB-" + address[len(address)-2:],
			Address: mustAddress(t, address),
			Present: true,
		}
		manager.statusRetries[address] = statusRetry{nextAt: now.Add(-time.Second)}
	}
	if err := manager.beginStationOperation(addresses[0]); err != nil {
		t.Fatalf("reserve busy station: %v", err)
	}
	defer manager.endStationOperation(addresses[0])

	var active atomic.Int32
	var maximum atomic.Int32
	var recoveredMutex sync.Mutex
	recovered := make([]string, 0, 2)
	manager.bluetoothOps.fetchInitialPowerState = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		recoveredMutex.Lock()
		recovered = append(recovered, station.Snapshot().Address)
		recoveredMutex.Unlock()
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		return nil
	}

	manager.scheduleStatusRecovery()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recoveredMutex.Lock()
		complete := len(recovered) == 2
		recoveredMutex.Unlock()
		if complete {
			break
		}
		time.Sleep(time.Millisecond)
	}
	recoveredMutex.Lock()
	defer recoveredMutex.Unlock()
	if len(recovered) != 2 || recovered[0] != addresses[1] || recovered[1] != addresses[2] {
		t.Fatalf("recovered addresses = %v, want busy candidate skipped and remaining candidates recovered in order", recovered)
	}
	if maximum.Load() > 1 {
		t.Fatalf("recovery concurrency = %d, want at most 1", maximum.Load())
	}
}

func TestStatusRecoveryLeavesOneSlotForForegroundWork(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	recoveryAddress := "11:22:33:44:55:61"
	foregroundAddress := "11:22:33:44:55:62"
	manager.stations[recoveryAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY", Address: mustAddress(t, recoveryAddress), Present: true,
	}
	manager.statusRetries[recoveryAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	recoveryStarted := make(chan struct{})
	recoveryRelease := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		close(recoveryStarted)
		<-recoveryRelease
		return nil
	}

	manager.scheduleStatusRecovery()
	<-recoveryStarted
	if err := manager.beginStationOperation(foregroundAddress); err != nil {
		t.Fatalf("foreground operation could not use reserved slot: %v", err)
	}
	manager.endStationOperation(foregroundAddress)
	close(recoveryRelease)
	deadline := time.Now().Add(time.Second)
	for manager.statusRecoveryRunning.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.statusRecoveryRunning.Load() {
		t.Fatal("recovery did not finish")
	}
}

func TestShutdownCancelsBlockingStatusRecoveryRead(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.initialReadTimeout = time.Hour
	address := "11:22:33:44:55:63"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	manager.statusRetryMutex.Unlock()
	readStarted := make(chan struct{})
	readCancelled := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(readStarted)
		<-ctx.Done()
		close(readCancelled)
		return ctx.Err()
	}
	manager.scheduleStatusRecovery()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("status recovery did not start")
	}

	shutdownDone := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not cancel the background recovery read")
	}
	select {
	case <-readCancelled:
	default:
		t.Fatal("Shutdown returned before the recovery read observed cancellation")
	}
}

func TestSecondForegroundOperationWaitsForHiddenRecoverySlot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}
	if err := manager.beginForegroundStationOperation("FIRST"); err != nil {
		t.Fatalf("first foreground operation error = %v", err)
	}

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- manager.beginForegroundStationOperation("SECOND")
	}()
	select {
	case err := <-secondResult:
		t.Fatalf("second foreground operation returned before recovery ended: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	manager.endRecoveryStationOperation("RECOVERY")
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatalf("second foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second foreground operation did not acquire the released recovery slot")
	}
	manager.endStationOperation("SECOND")
	manager.endStationOperation("FIRST")
}

func TestForegroundRetriesWhenRecoveryEndsDuringSlotMiss(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	if err := manager.beginStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve foreground slot: %v", err)
	}
	defer manager.endStationOperation("FIRST")

	var hookOnce sync.Once
	manager.foregroundSlotMissHook = func() {
		hookOnce.Do(func() {
			manager.endRecoveryStationOperation("RECOVERY")
		})
	}
	if err := manager.beginForegroundStationOperation("SECOND"); err != nil {
		t.Fatalf("foreground operation did not retry released recovery slot: %v", err)
	}
	manager.endStationOperation("SECOND")
}

func TestForegroundWaitingForRecoveryReturnsOnShutdown(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	if err := manager.beginStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve foreground slot: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		result <- manager.beginForegroundStationOperation("SECOND")
	}()
	time.Sleep(20 * time.Millisecond)
	manager.BeginShutdown()
	select {
	case err := <-result:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("foreground error = %v, want ErrShuttingDown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wake foreground recovery waiter")
	}
	manager.endRecoveryStationOperation("RECOVERY")
	manager.endStationOperation("FIRST")
	manager.Shutdown()
}

func TestForegroundCancelsReadOnlyRecoveryToAcquireSlot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	manager.recoveryOperationMutex.Lock()
	recoveryContext := manager.recoveryContext
	manager.recoveryOperationMutex.Unlock()
	recoveryCancelled := make(chan struct{})
	go func() {
		<-recoveryContext.Done()
		close(recoveryCancelled)
		manager.endRecoveryStationOperation("RECOVERY")
	}()
	if err := manager.beginForegroundStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve first foreground slot: %v", err)
	}
	defer manager.endStationOperation("FIRST")

	started := time.Now()
	if err := manager.beginForegroundStationOperation("SECOND"); err != nil {
		t.Fatalf("second foreground operation: %v", err)
	}
	defer manager.endStationOperation("SECOND")
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("foreground waited %v for read-only recovery to yield", elapsed)
	}
	select {
	case <-recoveryCancelled:
	default:
		t.Fatal("foreground slot pressure did not cancel read-only recovery")
	}
}

func TestPreemptedRecoveryMovesBehindOtherDueCandidates(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	first := "11:22:33:44:99:01"
	second := "11:22:33:44:99:02"
	due := time.Now().Add(-time.Second)
	manager.statusRetries[first] = statusRetry{kinds: statusRetryConnection, nextAt: due}
	manager.statusRetries[second] = statusRetry{kinds: statusRetryConnection, nextAt: due}

	manager.deferStatusRecovery(first, 250*time.Millisecond)
	manager.statusRetryMutex.Lock()
	_, _, firstNext := statusRetryOrder(manager.statusRetries[first])
	_, _, secondNext := statusRetryOrder(manager.statusRetries[second])
	manager.statusRetryMutex.Unlock()
	if !secondNext.Before(firstNext) {
		t.Fatalf("preempted candidate nextAt=%v, other nextAt=%v; want other candidate first", firstNext, secondNext)
	}
}

func TestShutdownCancelsCapabilityDiscoveryBeforePowerWrite(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:88:01"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CAPABILITY-CANCEL", Address: mustAddress(t, address), Present: true,
	}
	discoveryStarted := make(chan struct{})
	manager.bluetoothOps.ensureCapabilities = func(ctx context.Context, _ *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		close(discoveryStarted)
		<-ctx.Done()
		return internalbluetooth.Capabilities{}, ctx.Err()
	}
	var writes atomic.Int32
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		writes.Add(1)
		return internalbluetooth.PowerControlResult{}, nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.SetStationPower(address, "on")
		result <- err
	}()
	<-discoveryStarted
	manager.BeginShutdown()
	select {
	case err := <-result:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("SetStationPower() error = %v, want ErrShuttingDown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capability discovery did not observe shutdown cancellation")
	}
	if writes.Load() != 0 {
		t.Fatalf("power writes = %d, want zero after cancelled discovery", writes.Load())
	}
	manager.Shutdown()
}

func TestBulkShutdownCancellationReturnsSkippedResults(t *testing.T) {
	manager := NewManager(config.NewConfig())
	addresses := []string{"11:22:33:44:AA:01", "11:22:33:44:AA:02"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name: "LHB-BULK-SHUTDOWN", Address: mustAddress(t, address), Present: true,
		}
	}
	discoveryStarted := make(chan struct{})
	var startOnce sync.Once
	manager.bluetoothOps.ensureCapabilities = func(ctx context.Context, _ *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		startOnce.Do(func() { close(discoveryStarted) })
		<-ctx.Done()
		return internalbluetooth.Capabilities{}, ctx.Err()
	}
	var writes atomic.Int32
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		writes.Add(1)
		return internalbluetooth.PowerControlResult{}, nil
	}
	type bulkResult struct {
		result BulkPowerResult
		err    error
	}
	done := make(chan bulkResult, 1)
	go func() {
		result, err := manager.SetAllStationsPowerDetailed("on")
		done <- bulkResult{result: result, err: err}
	}()
	<-discoveryStarted
	manager.BeginShutdown()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("SetAllStationsPowerDetailed() error = %v", outcome.err)
		}
		if len(outcome.result.Results) != len(addresses) {
			t.Fatalf("bulk results = %+v", outcome.result.Results)
		}
		for _, item := range outcome.result.Results {
			if !item.Skipped || item.Reason != "application is shutting down" ||
				item.CommandSent || item.Error != "" || item.Address == "" || item.Name == "" {
				t.Fatalf("shutdown result = %+v, want complete skipped result", item)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("bulk operation did not finish after shutdown cancellation")
	}
	if writes.Load() != 0 {
		t.Fatalf("power writes = %d, want zero", writes.Load())
	}
	manager.Shutdown()
}

func TestStatusRecoveryProcessesScheduleRequestedDuringActiveRound(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	firstAddress := "11:22:33:44:55:61"
	secondAddress := "11:22:33:44:55:62"
	manager.stations[firstAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY-1", Address: mustAddress(t, firstAddress), Present: true,
	}
	manager.statusRetries[firstAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}

	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	var calls atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-firstRelease
		}
		return nil
	}

	manager.scheduleStatusRecovery()
	<-firstStarted
	manager.stationsMutex.Lock()
	manager.stations[secondAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY-2", Address: mustAddress(t, secondAddress), Present: true,
	}
	manager.stationsMutex.Unlock()
	manager.statusRetryMutex.Lock()
	manager.statusRetries[secondAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	manager.statusRetryMutex.Unlock()
	manager.scheduleStatusRecovery()
	close(firstRelease)

	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("recovery calls = %d, want 2", got)
	}
}

func TestAbsentStationRecoveryStopsAfterBoundedFailures(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:79"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-GONE", Address: mustAddress(t, address), Present: false,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		failures: statusAbsentRetryLimit - 1,
		kinds:    statusRetryConnection,
		nextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return errors.New("station remains unreachable")
	}

	manager.runStatusRecoveryRound()

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("absent station remained scheduled after the bounded failure limit")
	}
}

func TestStatusFailureSchedulesRecoveryWithoutStatusPolling(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRetryBase = 5 * time.Millisecond
	manager.statusRetryMax = 20 * time.Millisecond
	address := "11:22:33:44:55:71"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-AUTOMATIC", Address: mustAddress(t, address), Present: true,
	}
	recovered := make(chan struct{}, 1)
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered <- struct{}{}
		return nil
	}

	manager.noteStatusFailure(address)

	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("status failure did not trigger recovery without a polling request")
	}
}

func TestShutdownWaitsForActiveStatusRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Millisecond
	manager.statusRetryMax = time.Millisecond
	address := "11:22:33:44:55:72"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-SHUTDOWN", Address: mustAddress(t, address), Present: true,
	}
	recoveryStarted := make(chan struct{})
	releaseRecovery := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		close(recoveryStarted)
		<-releaseRecovery
		return nil
	}
	manager.noteStatusFailure(address)
	<-recoveryStarted

	shutdownDone := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while status recovery was inside GATT work")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseRecovery)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after status recovery returned")
	}
}

func TestChannelChangeRevalidatesPresenceAfterCapabilityRefresh(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:81"
	station := &internalbluetooth.BaseStation{
		Name:       "LHB-MOVED",
		Address:    mustAddress(t, address),
		Channel:    3,
		Present:    true,
		LastSeenAt: time.Now(),
	}
	manager.stations[address] = station
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		station.SetPresent(false)
		return internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true}, nil
	}
	var writes atomic.Int32
	manager.bluetoothOps.setChannel = func(*internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		writes.Add(1)
		return internalbluetooth.ChannelWriteResult{}, nil
	}

	_, err := manager.SetStationChannel(address, 4, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStationChannel() error = %v, want ErrNotFound", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("channel writes = %d, want 0", got)
	}
}

func TestMetadataFreshnessExpires(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["fresh"] = &internalbluetooth.BaseStation{
		Name: "fresh", Address: mustAddress(t, "11:22:33:44:55:82"), MetadataReadAt: now,
	}
	manager.stations["stale"] = &internalbluetooth.BaseStation{
		Name: "stale", Address: mustAddress(t, "11:22:33:44:55:83"),
		Metadata:       internalbluetooth.DeviceMetadata{FirmwareRevision: "1.2.3"},
		MetadataReadAt: now.Add(-metadataFreshnessWindow - time.Minute),
	}

	infos := manager.GetStationInfo()
	freshness := make(map[string]bool, len(infos))
	for _, info := range infos {
		freshness[info.Name] = info.MetadataFresh
	}
	if !freshness["fresh"] {
		t.Fatal("recent metadata was marked stale")
	}
	if freshness["stale"] {
		t.Fatal("expired metadata was marked fresh")
	}
}

func TestRenameStationReturnsNotFoundForUnknownOriginalName(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["known"] = &internalbluetooth.BaseStation{
		Name: "LHB-KNOWN", Address: mustAddress(t, "11:22:33:44:55:84"),
	}

	err := manager.RenameStation("LHB-MISSING", "Desk")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("RenameStation() error = %v, want ErrNotFound", err)
	}
}

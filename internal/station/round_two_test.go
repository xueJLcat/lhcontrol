package station

import (
	"context"
	"errors"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

// TestVerificationChannelDeadlineIsNotAChannelFailure covers the read budget
// expiring between the power and channel reads: the interrupted channel read
// must schedule a plain re-read, not a channel failure with backoff.
func TestVerificationChannelDeadlineIsNotAChannelFailure(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:C0"
	station := &internalbluetooth.BaseStation{Name: "LHB-C0", Address: mustAddress(t, address)}
	before := station.Snapshot()

	manager.recordPowerVerificationResult(station, address, before, &internalbluetooth.InitialReadError{
		Channel: context.DeadlineExceeded,
	})

	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("interrupted channel read did not schedule any retry")
	}
	kinds := effectiveStatusRetryKinds(retry)
	if kinds&statusRetryChannel != 0 || retry.channelFailures != 0 {
		t.Fatalf("interrupted channel read recorded a channel failure: %+v", retry)
	}
	if kinds&statusRetryRefresh == 0 {
		t.Fatalf("interrupted channel read did not schedule a refresh: %+v", retry)
	}
}

func TestVerificationChannelFailureStillRecordsChannelRetry(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:C9"
	station := &internalbluetooth.BaseStation{Name: "LHB-C9", Address: mustAddress(t, address)}
	before := station.Snapshot()

	manager.recordPowerVerificationResult(station, address, before, &internalbluetooth.InitialReadError{
		Channel: errors.New("channel read failed"),
	})

	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry)&statusRetryChannel == 0 || retry.channelFailures != 1 {
		t.Fatalf("real channel failure retry = %+v tracked=%v, want one channel failure", retry, tracked)
	}
}

// TestBulkPowerDeadLinkCountsConnectionFailureOnce pins the single-record
// rule across the verification read and the write of one bulk attempt: a
// verification read that proves the link dead disconnects the station and
// books one connection failure; the write step then failing on the same dead
// link must not re-count it. Counting one dead link twice per attempt would
// double the exponential backoff and abandon absent stations early.
func TestBulkPowerDeadLinkCountsConnectionFailureOnce(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:D1"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-DEAD-LINK-BULK",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	var disconnects int
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects++
		return nil
	}
	deadLink := func(operation string) error {
		return &internalbluetooth.DeviceTransportError{
			Operation: operation,
			Err:       errors.New("station unreachable"),
		}
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Power: deadLink("read power characteristic")}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, deadLink("write power characteristic")
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Success || result.Results[0].Skipped || result.Results[0].Error == "" {
		t.Fatalf("dead-link bulk result = %+v, want one failed entry with its error", result.Results)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.failures != 1 {
		t.Fatalf("connection failures = %d (tracked=%v), want exactly 1 for one dead link in one attempt", retry.failures, tracked)
	}
	if disconnects != 1 {
		t.Fatalf("bounded disconnects = %d, want exactly 1", disconnects)
	}
}

// TestSinglePowerDeadLinkCountsConnectionFailureOnce is the SetStationPower
// mirror of the bulk rule above: the verification read's dead-link failure is
// the attempt's single connection-failure record even when the capability or
// write step fails on the same link afterwards.
func TestSinglePowerDeadLinkCountsConnectionFailureOnce(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:D2"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-DEAD-LINK-SINGLE",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	var disconnects int
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects++
		return nil
	}
	deadLink := func(operation string) error {
		return &internalbluetooth.DeviceTransportError{
			Operation: operation,
			Err:       errors.New("station unreachable"),
		}
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Power: deadLink("read power characteristic")}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, deadLink("write power characteristic")
	}

	if _, err := manager.SetStationPower(address, "on"); err == nil {
		t.Fatal("SetStationPower() unexpectedly succeeded against a dead link")
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.failures != 1 {
		t.Fatalf("connection failures = %d (tracked=%v), want exactly 1 for one dead link in one attempt", retry.failures, tracked)
	}
	if disconnects != 1 {
		t.Fatalf("bounded disconnects = %d, want exactly 1", disconnects)
	}
}

// TestForegroundStationOperationWaitDoesNotHoldScanTransitionLock covers a
// foreground device action waiting for background recovery: finishScan and
// every other scan transition need the same lock, so the wait must not hold it.
func TestForegroundStationOperationWaitDoesNotHoldScanTransitionLock(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:c1"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-C1", Address: mustAddress(t, address), Present: true,
	}
	if err := manager.beginRecoveryStationOperation(address); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}

	waitEntered := make(chan struct{})
	go func() {
		close(waitEntered)
		_ = manager.beginForegroundStationOperation(address)
	}()
	<-waitEntered

	// The foreground request cancels the background recovery before waiting
	// for it to finish; observe that cancellation to know the wait started.
	deadline := time.Now().Add(time.Second)
	for {
		manager.recoveryOperationMutex.Lock()
		recoveryContext := manager.recoveryContext
		manager.recoveryOperationMutex.Unlock()
		if recoveryContext != nil && recoveryContext.Err() != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreground operation did not request the background recovery to yield")
		}
		time.Sleep(time.Millisecond)
	}
	// The goroutine is now blocked waiting for the recovery to release the
	// station. The scan transition lock must be free for finishScan.
	if !manager.scanTransitionMutex.TryLock() {
		t.Fatal("foreground station operation wait still holds the scan transition lock")
	}
	manager.scanTransitionMutex.Unlock()

	manager.endRecoveryStationOperation(address)
	deadline = time.Now().Add(time.Second)
	for {
		manager.deviceOperationMutex.Lock()
		_, active := manager.activeDeviceOperations[address]
		manager.deviceOperationMutex.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreground operation did not acquire the station after recovery yielded")
		}
		time.Sleep(time.Millisecond)
	}
	manager.endStationOperation(address)
}

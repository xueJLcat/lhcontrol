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

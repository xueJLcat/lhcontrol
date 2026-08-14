package station

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

// TestStopScanReturnsTimeoutWhenProcessingIgnoresCancellation covers adapter
// calls that ignore cancellation (adapter initialization, stuck platform
// scans): StopScan must report ErrScanStopTimeout instead of hanging forever.
func TestStopScanReturnsTimeoutWhenProcessingIgnoresCancellation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stopScanTimeout = 50 * time.Millisecond
	enteredScan := make(chan struct{})
	releaseScan := make(chan struct{})
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		close(enteredScan)
		<-releaseScan
		return nil, nil
	}

	if err := manager.StartScan(ScanCallbacks{}); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	select {
	case <-enteredScan:
	case <-time.After(2 * time.Second):
		close(releaseScan)
		t.Fatal("platform scan was not reached")
	}

	stopStarted := time.Now()
	err := manager.StopScan()
	if !errors.Is(err, ErrScanStopTimeout) {
		close(releaseScan)
		t.Fatalf("StopScan() error = %v, want ErrScanStopTimeout", err)
	}
	if elapsed := time.Since(stopStarted); elapsed > 2*time.Second {
		close(releaseScan)
		t.Fatalf("StopScan() took %v, want the bounded wait", elapsed)
	}
	close(releaseScan)
	manager.scanCallbackWg.Wait()
}

// TestFailureCleanupAbandonsBlockedDisconnect covers the error paths after a
// failed read: the contextless disconnect must not hold a caller that may
// already be past its operation deadline.
func TestFailureCleanupAbandonsBlockedDisconnect(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.adapterCleanupWait = 20 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	var disconnectCalls atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnectCalls.Add(1)
		<-release
		return nil
	}
	address := "11:22:33:44:55:70"
	station := &internalbluetooth.BaseStation{Name: "LHB-70", Address: mustAddress(t, address)}

	started := time.Now()
	manager.recordStructuredReadResult(station, address, errors.New("read failed"), nil)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("failure cleanup blocked %v behind a stuck disconnect", elapsed)
	}
	if disconnectCalls.Load() != 1 {
		t.Fatalf("disconnect calls = %d, want 1", disconnectCalls.Load())
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("failed read did not register a connection retry")
	}
}

// TestScanReleaseAbandonsBlockedRelease covers the pre-scan connection
// release: one stuck station must not block the whole scan start.
func TestScanReleaseAbandonsBlockedRelease(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.adapterCleanupWait = 20 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	manager.bluetoothOps.releaseStationForScan = func(*internalbluetooth.BaseStation) error {
		<-release
		return nil
	}
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return nil, nil
	}
	address := "11:22:33:44:55:71"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-71", Address: mustAddress(t, address), Present: true,
	}

	started := time.Now()
	_, err := manager.ScanAndFetchStations()
	if err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("scan start blocked %v behind a stuck connection release", elapsed)
	}
	status := manager.GetScanStatus()
	if status.State != "completed" || len(status.Warnings) == 0 {
		t.Fatalf("scan status = %+v, want completed with an unreleased-connection warning", status)
	}
}

// TestRecoverySchedulerRunsConnectionRetryForConnectedStation covers the
// stall where a station records a connection retry without being disconnected
// (deadline-only read failures, disconnect cleanup failures): the scheduler
// must still run the recovery read instead of filtering the station out.
func TestRecoverySchedulerRunsConnectionRetryForConnectedStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	address := "11:22:33:44:55:72"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-72", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:  statusRetryConnection,
		nextAt: time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()

	var recovered atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered.Add(1)
		return nil
	}

	delay, scheduled := manager.nextStatusRecoveryDelay()
	if !scheduled || delay != 0 {
		t.Fatalf("nextStatusRecoveryDelay() = %v, %v; want 0, true", delay, scheduled)
	}
	manager.runStatusRecoveryRound()
	if recovered.Load() != 1 {
		t.Fatalf("recovery attempts = %d, want 1", recovered.Load())
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("successful recovery read did not clear the connection retry")
	}
}

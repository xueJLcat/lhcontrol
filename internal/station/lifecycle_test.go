package station

import (
	"context"
	"errors"
	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"sync/atomic"
	"testing"
	"time"
)

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
func TestShutdownRejectsCachedNoOpOperations(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	address := "11:22:33:44:55:85"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-NOOP",
		Address:           mustAddress(t, address),
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		LastPowerReadAt:   now,
		Channel:           6,
		LastChannelReadAt: now,
	}
	manager.BeginShutdown()
	if _, err := manager.SetStationPower(address, "on"); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("SetStationPower() error = %v, want ErrShuttingDown", err)
	}
	if _, err := manager.SetStationChannel(address, 6, false); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("SetStationChannel() error = %v, want ErrShuttingDown", err)
	}
	if _, err := manager.SetAllStationsPowerDetailed("on"); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v, want ErrShuttingDown", err)
	}
}
func TestShutdownRejectsEmptyBulkNoOp(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.BeginShutdown()
	if _, err := manager.SetAllStationsPowerDetailed("on"); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v, want ErrShuttingDown", err)
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
func TestShutdownBoundedWhenAnOperationCannotDrain(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.shutdownDrainTimeout = 30 * time.Millisecond
	// Simulate an operation stuck in an adapter call that ignores lifecycle
	// cancellation; without the drain limit the exit wait would block on it
	// forever and the window could never close.
	if err := manager.beginOperation(); err != nil {
		t.Fatalf("beginOperation() error = %v", err)
	}

	start := time.Now()
	manager.Shutdown()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown() took %v, want the bounded drain", elapsed)
	}
	manager.endOperation()
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

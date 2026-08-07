package station

import (
	"context"
	"errors"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

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

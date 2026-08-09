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

func TestRenameStationByAddressWorksBeforeAnyScan(t *testing.T) {
	// A station not yet discovered in this session (no scan) must still be
	// renamable by address so API callers can manage aliases for known devices.
	t.Setenv("AppData", t.TempDir())
	manager := NewManager(config.NewConfig())
	// Note: no stations added to manager.stations.

	address := "11:22:33:44:55:90"
	if err := manager.RenameStationByAddress(address, "Corner Base"); err != nil {
		t.Fatalf("RenameStationByAddress() error = %v, want rename to succeed before a scan", err)
	}
	if got, ok := manager.config.GetStationDisplayName(mustAddress(t, address).String(), "LHB-XXXX"); !ok || got != "Corner Base" {
		t.Fatalf("display name = %q ok=%v, want Corner Base", got, ok)
	}
}

func TestRenameStationByAddressRejectsMalformedAddress(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	manager := NewManager(config.NewConfig())

	if err := manager.RenameStationByAddress("not-a-mac", "Desk"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RenameStationByAddress() error = %v, want ErrNotFound for malformed address", err)
	}
}

func TestRenameStationByAddressIsCaseInsensitive(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	manager := NewManager(config.NewConfig())

	if err := manager.RenameStationByAddress("11:22:33:aa:bb:cc", "Lower Case"); err != nil {
		t.Fatalf("RenameStationByAddress() error = %v, want lowercase MAC to normalize", err)
	}
	if got, ok := manager.config.GetStationDisplayName("11:22:33:AA:BB:CC", "LHB-Y"); !ok || got != "Lower Case" {
		t.Fatalf("display name = %q ok=%v, want normalized address entry", got, ok)
	}
}

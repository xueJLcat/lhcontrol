package station

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"

	tinybluetooth "tinygo.org/x/bluetooth"
)

func TestSetAllStationsPowerValidatesState(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.SetAllStationsPower("invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetAllStationsPower() error = %v, want ErrInvalidArgument", err)
	}
}

func TestSinglePowerOperationHasHardTimeoutAndReleasesOperation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.stationOperationTimeout = 25 * time.Millisecond
	address := "11:22:33:44:55:90"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-TIMEOUT", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{}, ctx.Err()
	}

	start := time.Now()
	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrStationOperationTimeout) {
		t.Fatalf("SetStationPower() error = %v, want the station operation timeout sentinel", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("SetStationPower() took %v, want a bounded return", elapsed)
	}

	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{Confirmed: true}, nil
	}
	if _, err := manager.SetStationPower(address, "on"); err != nil {
		t.Fatalf("operation lock was not released after timeout: %v", err)
	}
}

func TestSinglePowerReconcilesMetadataRetryAfterReconnect(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	now := time.Now()
	address := "11:22:33:44:55:8A"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:                 "LHB-POWER-METADATA",
		Address:              mustAddress(t, address),
		Present:              true,
		PowerState:           internalbluetooth.PowerStateSleep,
		RawPowerState:        0x00,
		LastPowerReadAt:      now,
		Capabilities:         internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown:    true,
		MetadataReadRevision: 3,
	}
	manager.noteMetadataFailure(address)
	manager.bluetoothOps.setPowerState = func(
		_ context.Context,
		station *internalbluetooth.BaseStation,
		_ internalbluetooth.PowerState,
	) (internalbluetooth.PowerControlResult, error) {
		station.MetadataReadRevision++
		station.MetadataReadAt = time.Now()
		station.PowerState = internalbluetooth.PowerStateOn
		station.RawPowerState = 0x0B
		station.LastPowerReadAt = time.Now()
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateOn, Confirmed: true}, nil
	}

	if _, err := manager.SetStationPower(address, "on"); err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked && effectiveStatusRetryKinds(retry)&statusRetryMetadata != 0 {
		t.Fatalf("metadata retry survived power reconnect discovery: %+v", retry)
	}
}

func TestStationOperationTimeoutSentinelWhenBudgetExpiresWaitingForSlot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.stationOperationTimeout = 25 * time.Millisecond
	address := "11:22:33:44:55:91"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-SLOT-WAIT", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{Confirmed: true}, nil
	}
	// A background status read holding the station slot makes the foreground
	// operation wait for it; the per-station budget expires during that wait.
	if err := manager.beginStationOperationKindContext(address, deviceOperationStatus, nil); err != nil {
		t.Fatalf("failed to occupy the station slot: %v", err)
	}
	defer manager.endStationOperation(address)

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrStationOperationTimeout) {
		t.Fatalf("SetStationPower() error = %v, want the station operation timeout sentinel", err)
	}

	manager.endStationOperation(address)
	if _, err := manager.SetStationPower(address, "on"); err != nil {
		t.Fatalf("operation lock was not released after timeout: %v", err)
	}
}

func TestSetAllStationsPowerSkipsIneligibleStations(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return nil
	}
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

func TestInferredBootRawStateIsVerifiedAndSkippedByBulkPower(t *testing.T) {
	// Compatibility firmware reports boot-like raw values while awake. The
	// inferred On state counts as verified, so bulk power must skip the
	// station as already at target instead of forcing a real write.
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:84"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-STEADY-BOOT", Address: mustAddress(t, address), Present: true,
		PowerState: internalbluetooth.PowerStateOn, RawPowerState: 0x01,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
		LastPowerReadAt: time.Now(),
	}

	infos := manager.GetStationInfo()
	if len(infos) != 1 || !infos[0].PowerFresh || !infos[0].PowerStateConfirmed {
		t.Fatalf("station info = %+v, want fresh inferred On state reported as confirmed", infos)
	}

	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted for a station already at the target state")
		return internalbluetooth.PowerControlResult{}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return nil
	}
	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped || result.Results[0].CommandSent ||
		!result.Results[0].Success || !result.Results[0].Confirmed ||
		result.Results[0].Reason != "already at target state" {
		t.Fatalf("bulk result = %+v, want a confirmed no-op skip", result.Results)
	}
}

func TestSingleStandbyRefreshesCachedUnsupportedCapability(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:81"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-STANDBY", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true, Standby: false}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	var refreshes atomic.Int32
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		refreshes.Add(1)
		return internalbluetooth.Capabilities{PowerWrite: true, Standby: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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
	stubPowerVerificationRead(manager)
	var refreshes atomic.Int32
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		refreshes.Add(1)
		return internalbluetooth.Capabilities{PowerWrite: true, Standby: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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
	defer manager.Shutdown()
	address := "11:22:33:44:55:83"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CHANNEL-PARTIAL", Address: mustAddress(t, address), Present: true,
		LastError: "operation: previous channel result was unresolved",
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
	if info.Address != address || info.Name != "LHB-CHANNEL-PARTIAL" ||
		info.LastError != "operation: previous channel result was unresolved" {
		t.Fatalf("RefreshStationCapabilities() returned zero or wrong station: %+v", info)
	}
}

func TestRefreshCapabilitiesReturnsStationAfterPowerReadFailure(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:84"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-POWER-PARTIAL", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		station.LastError = "power unavailable"
		return &internalbluetooth.InitialReadError{Power: errors.New("power unavailable")}
	}

	info, err := manager.RefreshStationCapabilities(address)
	if err != nil {
		t.Fatalf("RefreshStationCapabilities() error = %v", err)
	}
	if info.Address != address || info.LastError != "power unavailable" || info.PowerFresh {
		t.Fatalf("RefreshStationCapabilities() partial result = %+v", info)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry)&statusRetryConnection == 0 {
		t.Fatalf("power read recovery was not scheduled: %+v", retry)
	}
}

func TestRefreshCapabilitiesClearsStaleOperationErrorAfterFullRead(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:85"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:      "LHB-REFRESH-RECOVERY",
		Address:   mustAddress(t, address),
		Present:   true,
		LastError: "operation: previous identify failed",
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerRead: true, ChannelRead: true}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return nil
	}

	info, err := manager.RefreshStationCapabilities(address)
	if err != nil {
		t.Fatalf("RefreshStationCapabilities() error = %v", err)
	}
	if info.LastError != "" {
		t.Fatalf("successful capability refresh retained stale operation error %q", info.LastError)
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
	stubPowerVerificationRead(manager)
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
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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

func TestSinglePowerConfirmationPreservesCommandSentWithoutSnapshot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:77"
	// The station is registered by key but its snapshot carries no matching
	// address, so the post-confirmation snapshot lookup fails while the write
	// path itself succeeds.
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CONFIRM-NO-SNAPSHOT", Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true}, nil
	}
	confirmationErr := &internalbluetooth.PowerConfirmationError{
		Target: internalbluetooth.PowerStateOn,
		Err:    errors.New("readback timed out"),
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateUnknown}, confirmationErr
	}

	result, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, confirmationErr) {
		t.Fatalf("SetStationPower() error = %v, want %v", err, confirmationErr)
	}
	// The command landed on the station, so the structured sent/unconfirmed
	// result must survive the failed snapshot lookup instead of degrading to a
	// bare failure that the UI would report as "power change failed".
	if !result.CommandSent || result.Confirmed || result.ConfirmationError == "" {
		t.Fatalf("power result = %+v, want sent but unconfirmed", result)
	}
}

func TestSinglePowerAlreadyAtConfirmedTargetIsNoOp(t *testing.T) {
	manager := NewManager(config.NewConfig())
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
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted for a confirmed no-op")
		return internalbluetooth.PowerControlResult{}, nil
	}
	var reads atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		reads.Add(1)
		return nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	if result.CommandSent || !result.Confirmed || result.Station.Address != address {
		t.Fatalf("no-op power result = %+v", result)
	}
	if !result.Skipped || result.Reason != "already at target state" {
		t.Fatalf("no-op power result = %+v, want skipped with reason", result)
	}
	if reads.Load() != 1 {
		t.Fatalf("fresh verification reads = %d, want 1", reads.Load())
	}
}

func TestSinglePowerDoesNotTrustStaleTargetCacheAfterLiveRead(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:7A"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-EXTERNAL-CHANGE",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		LastPowerReadAt:   time.Now(),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateSleep
		station.RawPowerState = 0x00
		station.LastPowerReadAt = time.Now()
		return nil
	}
	var writes atomic.Int32
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		writes.Add(1)
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateOn, Confirmed: true}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	if writes.Load() != 1 || result.Skipped || !result.CommandSent || !result.Confirmed {
		t.Fatalf("power result = %+v, writes = %d; want a confirmed command after live state changed", result, writes.Load())
	}
}

func TestSinglePowerReadsExpiredStateBeforeWriting(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:7B"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-EXPIRED",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateSleep,
		RawPowerState:     0x00,
		LastPowerReadAt:   time.Now().Add(-operationSafetyFreshnessWindow - time.Second),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateOn
		station.RawPowerState = 0x0B
		station.LastPowerReadAt = time.Now()
		return nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted after an expired snapshot refreshed to the target")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil {
		t.Fatalf("SetStationPower() error = %v", err)
	}
	if !result.Skipped || !result.Confirmed || result.CommandSent || result.Reason != "already at target state" {
		t.Fatalf("power result = %+v, want confirmed no-op", result)
	}
}

func TestSinglePowerUsesConfirmedTargetReadWhenOptionalChannelExhaustsBudget(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.stationOperationTimeout = 25 * time.Millisecond
	address := "11:22:33:44:55:7C"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-TARGET-BEFORE-TIMEOUT",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true, ChannelRead: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateOn
		station.RawPowerState = 0x0B
		station.LastPowerReadAt = time.Now()
		<-ctx.Done()
		return &internalbluetooth.InitialReadError{Channel: ctx.Err()}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted after the target state was confirmed")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil {
		t.Fatalf("SetStationPower() error = %v, want confirmed no-op", err)
	}
	if !result.Skipped || !result.Confirmed || result.CommandSent || result.Reason != ReasonAlreadyAtTarget {
		t.Fatalf("power result = %+v, want confirmed no-op after optional channel timeout", result)
	}
}

func TestSinglePowerSchedulesRecoveryForFailedVerificationChannel(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:7F"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-PARTIAL-VERIFICATION",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true, ChannelRead: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	channelErr := errors.New("channel read unavailable")
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateOn
		station.RawPowerState = 0x0B
		station.LastPowerReadAt = time.Now()
		return &internalbluetooth.InitialReadError{Channel: channelErr}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted after the target state was confirmed")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil || !result.Skipped || !result.Confirmed {
		t.Fatalf("SetStationPower() result = %+v, error = %v; want confirmed no-op", result, err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel || retry.channelFailures != 1 {
		t.Fatalf("verification retry = %+v, tracked=%v; want one channel recovery", retry, tracked)
	}
}

func TestSinglePowerSuccessfulVerificationClearsRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:80"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-COMPLETE-VERIFICATION",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true, ChannelRead: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:           statusRetryConnection | statusRetryChannel,
		failures:        2,
		channelFailures: 3,
		nextAt:          time.Now().Add(time.Hour),
		channelNextAt:   time.Now().Add(time.Hour),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateOn
		station.RawPowerState = 0x0B
		station.LastPowerReadAt = time.Now()
		station.LastChannelReadAt = time.Now()
		return nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted after a complete target-state verification")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil || !result.Skipped || !result.Confirmed {
		t.Fatalf("SetStationPower() result = %+v, error = %v; want confirmed no-op", result, err)
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("complete verification left obsolete connection/channel recovery scheduled")
	}
}

func TestSinglePowerPreservesChannelRecoveryAfterFailedVerificationAndSuccessfulWrite(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:81"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-FAILED-VERIFICATION",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true, ChannelRead: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error { return nil }
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{
			Power:   errors.New("power read unavailable"),
			Channel: errors.New("channel read unavailable"),
		}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateOn, Confirmed: true}, nil
	}

	result, err := manager.SetStationPower(address, "on")
	if err != nil || !result.CommandSent || !result.Confirmed {
		t.Fatalf("SetStationPower() result = %+v, error = %v; want confirmed write", result, err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel || retry.channelFailures != 1 {
		t.Fatalf("verification retry = %+v, tracked=%v; want channel recovery after successful write", retry, tracked)
	}
}

func TestSinglePowerDoesNotWriteNonTargetReadAfterBudgetExpires(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.stationOperationTimeout = 25 * time.Millisecond
	address := "11:22:33:44:55:7D"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-NONTARGET-BEFORE-TIMEOUT",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true, ChannelRead: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateSleep
		station.RawPowerState = 0x00
		station.LastPowerReadAt = time.Now()
		<-ctx.Done()
		return &internalbluetooth.InitialReadError{Channel: ctx.Err()}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted after the operation budget expired")
		return internalbluetooth.PowerControlResult{}, nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrStationOperationTimeout) {
		t.Fatalf("SetStationPower() error = %v, want station operation timeout", err)
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
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted while station was booting")
		return internalbluetooth.PowerControlResult{}, nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, ErrStationTransitioning) || !strings.Contains(err.Error(), "station is booting") {
		t.Fatalf("SetStationPower() error = %v, want booting ErrStationTransitioning", err)
	}
}

func TestSinglePowerRechecksBootingStateAfterCapabilityDiscovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:91"
	station := &internalbluetooth.BaseStation{
		Name:            "LHB-BOOT-DURING-POWER",
		Address:         mustAddress(t, address),
		Present:         true,
		PowerState:      internalbluetooth.PowerStateSleep,
		RawPowerState:   0x00,
		LastPowerReadAt: time.Now(),
	}
	manager.stations[address] = station
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		station.PowerState = internalbluetooth.PowerStateBooting
		station.RawPowerState = 0x01
		station.LastPowerReadAt = time.Now()
		return internalbluetooth.Capabilities{PowerWrite: true}, nil
	}
	var writes atomic.Int32
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		writes.Add(1)
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateOn, Confirmed: true}, nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, ErrStationTransitioning) {
		t.Fatalf("SetStationPower() error = %v, want ErrStationTransitioning", err)
	}
	if writes.Load() != 0 {
		t.Fatalf("power writes after station entered booting during discovery = %d, want 0", writes.Load())
	}
}

func TestSinglePowerRejectsBootingStateReachedWhileWaitingToBegin(t *testing.T) {
	// The pre-begin check sees a healthy snapshot, but a background read that
	// drains during the slot wait can flip the station into a fresh boot. The
	// post-begin classification must still reject the write, matching the bulk
	// worker re-check.
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	if err := manager.beginStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve foreground slot: %v", err)
	}
	defer manager.endStationOperation("FIRST")

	address := "11:22:33:44:55:7E"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-RACE-BOOT",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		LastPowerReadAt:   time.Now(),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station

	var hookOnce sync.Once
	manager.foregroundSlotMissHook = func() {
		hookOnce.Do(func() {
			station.PowerState = internalbluetooth.PowerStateBooting
			station.RawPowerState = 0x01
			station.LastPowerReadAt = time.Now()
			manager.endRecoveryStationOperation("RECOVERY")
		})
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		t.Fatal("cache verification read was attempted for a station that turned booting after begin")
		return nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted for a station that turned booting after begin")
		return internalbluetooth.PowerControlResult{}, nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, ErrStationTransitioning) || !strings.Contains(err.Error(), "station is booting") {
		t.Fatalf("SetStationPower() error = %v, want booting ErrStationTransitioning", err)
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
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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

// TestShutdownDuringPowerCacheVerificationReturnsShuttingDown guards the
// operation-budget checkpoint after the cache verification read: a shutdown
// cancellation observed there must surface as ErrShuttingDown (503) rather
// than leaking a raw context.Canceled (500).
func TestShutdownDuringPowerCacheVerificationReturnsShuttingDown(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:88:02"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CACHE-CANCEL", Address: mustAddress(t, address), Present: true,
	}
	readStarted := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(readStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.SetStationPower(address, "on")
		result <- err
	}()
	<-readStarted
	manager.BeginShutdown()
	select {
	case err := <-result:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("SetStationPower() error = %v, want ErrShuttingDown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cache verification read did not observe shutdown cancellation")
	}
	manager.Shutdown()
}

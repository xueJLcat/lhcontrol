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
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return nil
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

func TestBulkPowerSkipsOnlyAfterFreshVerification(t *testing.T) {
	manager := NewManager(config.NewConfig())
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
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("power write was attempted after a fresh target-state verification")
		return internalbluetooth.PowerControlResult{}, nil
	}
	var reads atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		reads.Add(1)
		return nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 2 ||
		!result.Results[0].Skipped || !result.Results[0].Success || !result.Results[0].Confirmed ||
		!result.Results[1].Skipped || result.Results[1].Success ||
		result.Results[1].Reason != "station is booting" {
		t.Fatalf("verified-skip batch result = %+v", result.Results)
	}
	if reads.Load() != 1 {
		t.Fatalf("fresh verification reads = %d, want 1", reads.Load())
	}
}

// TestBulkPowerSkipsBusyStation guards the bulk against a station whose lock
// is held by a transport call that ignores cancellation. The worker must skip
// such a station (reporting it busy) instead of wedging the whole bulk behind
// its lock. No command has been sent at the skip point, so the station keeps
// its seeded identity in the result and no write is attempted.
func TestBulkPowerSkipsBusyStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:B1"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-BUSY",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateSleep,
		RawPowerState:     0x00,
		LastPowerReadAt:   time.Now(),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	// Prime the snapshot cache so the busy station stays in the candidate set
	// (projected from its last-known state) instead of being dropped.
	station.Snapshot()
	var writes atomic.Int32
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		writes.Add(1)
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateOn, Confirmed: true}, nil
	}

	var result BulkPowerResult
	var bulkErr error
	station.HoldLockWhile(func() {
		result, bulkErr = manager.SetAllStationsPowerDetailed("on")
	})
	if bulkErr != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", bulkErr)
	}
	if writes.Load() != 0 {
		t.Fatalf("power writes = %d, want none for a busy station", writes.Load())
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped || result.Results[0].Reason != ReasonStationBusy {
		t.Fatalf("bulk result = %+v, want the busy station skipped with reason %q", result.Results, ReasonStationBusy)
	}
	if result.Results[0].Address != address {
		t.Fatalf("busy station lost its identity in the result: %+v", result.Results[0])
	}
}

func TestBulkPowerDoesNotTrustStaleTargetCacheAfterLiveRead(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:65"
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

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if writes.Load() != 1 || len(result.Results) != 1 || result.Results[0].Skipped ||
		!result.Results[0].CommandSent || !result.Results[0].Confirmed {
		t.Fatalf("bulk result = %+v, writes = %d; want a confirmed command after live state changed", result, writes.Load())
	}
}

func TestBulkPowerReadsExpiredStateBeforeWriting(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:6A"
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
		t.Fatal("bulk power write was attempted after an expired snapshot refreshed to the target")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped || !result.Results[0].Confirmed || result.Results[0].CommandSent {
		t.Fatalf("bulk result = %+v, want confirmed no-op", result)
	}
}

func TestBulkPowerSchedulesRecoveryForFailedVerificationChannel(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:6B"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-BULK-PARTIAL-VERIFICATION",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true, ChannelRead: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		station.PowerState = internalbluetooth.PowerStateOn
		station.RawPowerState = 0x0B
		station.LastPowerReadAt = time.Now()
		return &internalbluetooth.InitialReadError{Channel: errors.New("channel read unavailable")}
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		t.Fatal("bulk power write was attempted after the target state was confirmed")
		return internalbluetooth.PowerControlResult{}, nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil || len(result.Results) != 1 || !result.Results[0].Skipped || !result.Results[0].Confirmed {
		t.Fatalf("SetAllStationsPowerDetailed() result = %+v, error = %v; want confirmed no-op", result, err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel || retry.channelFailures != 1 {
		t.Fatalf("bulk verification retry = %+v, tracked=%v; want one channel recovery", retry, tracked)
	}
}

func TestBulkPowerPreservesChannelRecoveryAfterFailedVerificationAndSuccessfulWrite(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusRetryBase = time.Hour
	address := "11:22:33:44:55:6C"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-BULK-FAILED-VERIFICATION",
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

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil || len(result.Results) != 1 || !result.Results[0].Success ||
		!result.Results[0].CommandSent || !result.Results[0].Confirmed {
		t.Fatalf("SetAllStationsPowerDetailed() result = %+v, error = %v; want confirmed write", result, err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel || retry.channelFailures != 1 {
		t.Fatalf("bulk verification retry = %+v, tracked=%v; want channel recovery after successful write", retry, tracked)
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
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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
	stubPowerVerificationRead(manager)

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
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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

func TestBulkPowerReconcilesMetadataRetryAfterReconnect(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	now := time.Now()
	address := "11:22:33:44:55:8B"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:                 "LHB-BULK-METADATA",
		Address:              mustAddress(t, address),
		Present:              true,
		PowerState:           internalbluetooth.PowerStateSleep,
		RawPowerState:        0x00,
		LastPowerReadAt:      now,
		Capabilities:         internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown:    true,
		MetadataReadRevision: 2,
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

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Success || !result.Results[0].Confirmed {
		t.Fatalf("bulk power result = %+v", result)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked && effectiveStatusRetryKinds(retry)&statusRetryMetadata != 0 {
		t.Fatalf("metadata retry survived bulk reconnect discovery: %+v", retry)
	}
}

func TestBulkPowerKeepsCapabilityConnectionFailuresAsFailed(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:66"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-FAILED", Address: mustAddress(t, address), Present: true,
	}
	stubPowerVerificationRead(manager)
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

// TestBulkPowerKeepsMixedInterruptionAndTransportFailuresAsFailed guards the
// worker classification against a joined error: the bluetooth layer joins the
// stopping context error with a genuine transport failure hit just before it
// stopped. Only errors made exclusively of context interruptions own the
// entry as a skipped result; the mixed error must run the failure path so its
// disconnect/backoff bookkeeping is not silently dropped.
func TestBulkPowerKeepsMixedInterruptionAndTransportFailuresAsFailed(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:69"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-MIXED-FAILURE", Address: mustAddress(t, address), Present: true,
	}
	stubPowerVerificationRead(manager)
	mixedErr := errors.Join(context.DeadlineExceeded, tinybluetooth.ErrDeviceDisconnected)
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, mixedErr
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if result.Cancelled || result.TimedOut {
		t.Fatalf("batch result = %+v, want a healthy batch with a failed entry", result)
	}
	if len(result.Results) != 1 || result.Results[0].Skipped || result.Results[0].Error == "" {
		t.Fatalf("mixed error result = %+v, want a failed entry with error details", result.Results)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	kinds := effectiveStatusRetryKinds(retry)
	manager.statusRetryMutex.Unlock()
	if !tracked || kinds&statusRetryConnection == 0 {
		t.Fatalf("mixed error bookkeeping = tracked:%v kinds:%v, want a connection recovery entry", tracked, kinds)
	}
}

func TestBulkPowerLateUnsupportedCapabilityIsSkipped(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:67"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-LATE-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.ensureCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{PowerWrite: true}, nil
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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

func TestBulkPowerConfirmationUnsupportedReadIsNotSkipped(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:77"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-BULK-CONFIRM-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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

func TestBulkUnconfirmedSuccessClearsOnlyConnectionRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:78"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-BULK-WRITE-ONLY", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:         statusRetryConnection | statusRetryChannel,
		failures:      3,
		nextAt:        time.Now().Add(time.Hour),
		channelNextAt: time.Now().Add(time.Hour),
	}
	manager.statusRetryMutex.Unlock()
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateOn, Confirmed: false}, nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Success ||
		!result.Results[0].CommandSent || result.Results[0].Confirmed {
		t.Fatalf("bulk write-only result = %+v", result.Results)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("retry after unconfirmed bulk success = %+v, tracked=%v; want channel-only", retry, tracked)
	}
}

func TestBulkConfirmationFailureClearsOnlyConnectionRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:76"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-BULK-UNCONFIRMED", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:         statusRetryConnection | statusRetryChannel,
		failures:      2,
		nextAt:        time.Now().Add(time.Hour),
		channelNextAt: time.Now().Add(time.Hour),
	}
	manager.statusRetryMutex.Unlock()
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{State: internalbluetooth.PowerStateUnknown, Confirmed: false},
			&internalbluetooth.PowerConfirmationError{
				Target: internalbluetooth.PowerStateOn,
				Err:    errors.New("readback timed out"),
			}
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Success ||
		!result.Results[0].CommandSent || result.Results[0].Confirmed || result.Results[0].Error == "" {
		t.Fatalf("bulk confirmation failure result = %+v", result.Results)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("retry after bulk confirmation failure = %+v, tracked=%v; want channel-only", retry, tracked)
	}
}

func TestStaleBootingStationIsNotSkippedByBulkSelection(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["booting"] = &internalbluetooth.BaseStation{
		Name: "LHB-BOOTING", Present: true, PowerState: internalbluetooth.PowerStateBooting,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: false}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, errors.New("connection failed")
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Skipped {
		t.Fatalf("stale booting station was skipped: %+v", result.Results)
	}
}

func TestBulkPowerRechecksBootingStateAfterCapabilityDiscovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:92"
	station := &internalbluetooth.BaseStation{
		Name:            "LHB-BOOT-DURING-BULK",
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

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped ||
		result.Results[0].Reason != ReasonStationBooting || result.Results[0].CommandSent {
		t.Fatalf("boot-after-discovery result = %+v, want a booting skip", result.Results)
	}
	if writes.Load() != 0 {
		t.Fatalf("bulk writes after station entered booting during discovery = %d, want 0", writes.Load())
	}
}

func TestBulkPowerRechecksQueuedStationStateBeforeWrite(t *testing.T) {
	for _, test := range []struct {
		name          string
		state         internalbluetooth.PowerState
		raw           int
		wantReason    string
		wantSuccess   bool
		wantConfirmed bool
	}{
		{
			name:       "booting",
			state:      internalbluetooth.PowerStateBooting,
			raw:        0x01,
			wantReason: "station is booting",
		},
		{
			name:          "already at target",
			state:         internalbluetooth.PowerStateOn,
			raw:           0x0B,
			wantReason:    "already at target state",
			wantSuccess:   true,
			wantConfirmed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			addresses := []string{
				"11:22:33:44:55:81",
				"11:22:33:44:55:82",
				"11:22:33:44:55:83",
				"11:22:33:44:55:84",
			}
			stations := make(map[string]*internalbluetooth.BaseStation, len(addresses))
			for index, address := range addresses {
				station := &internalbluetooth.BaseStation{
					Name:              fmt.Sprintf("LHB-%d", index+1),
					Address:           mustAddress(t, address),
					Present:           true,
					PowerState:        internalbluetooth.PowerStateSleep,
					RawPowerState:     0x00,
					LastPowerReadAt:   time.Now(),
					Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
					CapabilitiesKnown: true,
				}
				manager.stations[address] = station
				stations[address] = station
			}

			entered := make(chan string, 2)
			release := make(chan struct{})
			var writesMutex sync.Mutex
			writes := make(map[string]int)
			manager.bluetoothOps.setPowerState = func(
				_ context.Context,
				station *internalbluetooth.BaseStation,
				_ internalbluetooth.PowerState,
			) (internalbluetooth.PowerControlResult, error) {
				address := station.Snapshot().Address
				writesMutex.Lock()
				writes[address]++
				writesMutex.Unlock()
				select {
				case entered <- address:
					<-release
				default:
				}
				return internalbluetooth.PowerControlResult{Confirmed: true}, nil
			}
			manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
				return nil
			}

			type outcome struct {
				result BulkPowerResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := manager.SetAllStationsPowerDetailed("on")
				done <- outcome{result: result, err: err}
			}()

			started := map[string]bool{
				<-entered: true,
				<-entered: true,
			}
			var queuedAddress string
			for _, address := range addresses {
				if !started[address] {
					queuedAddress = address
					break
				}
			}
			if queuedAddress == "" {
				t.Fatal("no queued station was available for the state transition")
			}
			queued := stations[queuedAddress]
			queued.PowerState = test.state
			queued.RawPowerState = test.raw
			queued.LastPowerReadAt = time.Now()
			manager.statusRetryMutex.Lock()
			manager.statusRetries[queuedAddress] = statusRetry{
				kinds:  statusRetryConnection,
				nextAt: time.Now().Add(time.Hour),
			}
			manager.statusRetryMutex.Unlock()
			close(release)

			actual := <-done
			if actual.err != nil {
				t.Fatalf("SetAllStationsPowerDetailed() error = %v", actual.err)
			}
			var queuedResult *BulkPowerStationResult
			for index := range actual.result.Results {
				if actual.result.Results[index].Address == queuedAddress {
					queuedResult = &actual.result.Results[index]
					break
				}
			}
			if queuedResult == nil {
				t.Fatalf("result for queued station %s was missing: %+v", queuedAddress, actual.result.Results)
			}
			if !queuedResult.Skipped || queuedResult.Reason != test.wantReason ||
				queuedResult.Success != test.wantSuccess || queuedResult.Confirmed != test.wantConfirmed {
				t.Fatalf("queued result = %+v", *queuedResult)
			}
			writesMutex.Lock()
			queuedWrites := writes[queuedAddress]
			writesMutex.Unlock()
			if queuedWrites != 0 {
				t.Fatalf("queued station writes = %d, want 0", queuedWrites)
			}
			manager.statusRetryMutex.Lock()
			_, retryPreserved := manager.statusRetries[queuedAddress]
			manager.statusRetryMutex.Unlock()
			if !retryPreserved {
				t.Fatal("cached queued skip cleared connection recovery without communicating")
			}
		})
	}
}

func TestBulkShutdownCancellationReturnsSkippedResults(t *testing.T) {
	manager := NewManager(config.NewConfig())
	addresses := []string{"11:22:33:44:AA:01", "11:22:33:44:AA:02"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name: "LHB-BULK-SHUTDOWN", Address: mustAddress(t, address), Present: true,
		}
	}
	stubPowerVerificationRead(manager)
	discoveryStarted := make(chan struct{})
	var startOnce sync.Once
	manager.bluetoothOps.ensureCapabilities = func(ctx context.Context, _ *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		startOnce.Do(func() { close(discoveryStarted) })
		<-ctx.Done()
		return internalbluetooth.Capabilities{}, ctx.Err()
	}
	var writes atomic.Int32
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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

// TestBulkPowerShutdownAtReadinessCheckReportsSkippedResults guards the entry
// shape when shutdown lands between the bulk's entry check and ensureReady:
// the batch never started, so every entry must carry the interrupted-batch
// shape (Skipped + shutdown reason, no error text) like every other
// cancellation path, not a failed-with-error record.
func TestBulkPowerShutdownAtReadinessCheckReportsSkippedResults(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:AA:0A"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-READINESS-SHUTDOWN", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.initializeErr = errors.New("radio unavailable")
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	manager.initializeBluetooth = func() error {
		// Shutdown lands between the entry check and the readiness check.
		manager.BeginShutdown()
		return nil
	}

	result, err := manager.SetAllStationsPowerDetailed("on")
	if !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v, want %v", err, ErrShuttingDown)
	}
	if !result.Cancelled {
		t.Fatalf("bulk result = %+v, want a cancelled batch", result)
	}
	if len(result.Results) != 1 {
		t.Fatalf("bulk results = %+v, want one entry", result.Results)
	}
	for _, item := range result.Results {
		if !item.Skipped || item.Reason != ReasonShuttingDown || item.CommandSent || item.Error != "" {
			t.Fatalf("readiness-shutdown result = %+v, want a skipped entry without error text", item)
		}
	}
	manager.Shutdown()
}

func TestBulkPowerContextCancellationPreservesPossiblySentResult(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:03"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CANCELLED-CONFIRMATION", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	started := make(chan struct{})
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, target internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		close(started)
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{State: target}, &internalbluetooth.PowerConfirmationError{
			Target: target, Actual: internalbluetooth.PowerStateUnknown,
			Raw: internalbluetooth.RawPowerStateUnknown, Err: ctx.Err(),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result BulkPowerResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := manager.SetAllStationsPowerDetailedContext(ctx, "on")
		done <- outcome{result: result, err: err}
	}()
	<-started
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("bulk cancellation error = %v, want context.Canceled", result.err)
	}
	if len(result.result.Results) != 1 || !result.result.Results[0].CommandSent ||
		!result.result.Results[0].Success || result.result.Results[0].Confirmed {
		t.Fatalf("possibly-sent cancellation result = %+v", result.result.Results)
	}
}

func TestCancelBulkPowerStopsActiveOperation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:04"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CANCEL", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	started := make(chan struct{})
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		close(started)
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{}, ctx.Err()
	}

	type outcome struct {
		result BulkPowerResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := manager.SetAllStationsPowerDetailed("on")
		done <- outcome{result: result, err: err}
	}()
	<-started
	if err := manager.CancelBulkPower(); err != nil {
		t.Fatalf("CancelBulkPower() error = %v", err)
	}

	actual := <-done
	if actual.err != nil || !actual.result.Cancelled || actual.result.TimedOut {
		t.Fatalf("cancelled bulk result = %+v, error = %v", actual.result, actual.err)
	}
	if len(actual.result.Results) != 1 || !actual.result.Results[0].Skipped ||
		actual.result.Results[0].Reason != "operation cancelled" {
		t.Fatalf("cancelled station result = %+v", actual.result.Results)
	}
}

// TestLegacyBulkPowerReportsCancellationInsteadOfSuccess guards the error-only
// contract: when cancellation skips every station, the legacy API must not
// report success, because the caller has no structured result to inspect.
func TestLegacyBulkPowerReportsCancellationInsteadOfSuccess(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:09"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-LEGACY-CANCEL", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	started := make(chan struct{})
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		close(started)
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{}, ctx.Err()
	}

	done := make(chan error, 1)
	go func() { done <- manager.PowerOnAllStations() }()
	<-started
	if err := manager.CancelBulkPower(); err != nil {
		t.Fatalf("CancelBulkPower() error = %v", err)
	}
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("legacy bulk cancellation error = %v, want context.Canceled", err)
	}
}

func TestBulkPowerReportsCallerDeadline(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:05"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-TIMEOUT", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	result, err := manager.SetAllStationsPowerDetailedContext(ctx, "on")
	if !errors.Is(err, context.DeadlineExceeded) || !result.Cancelled || !result.TimedOut {
		t.Fatalf("timed-out bulk result = %+v, error = %v", result, err)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped ||
		result.Results[0].Reason != "bulk operation timed out" {
		t.Fatalf("timed-out station result = %+v", result.Results)
	}
}

// TestBulkPowerLateDeadlineAfterAllConfirmedNotMislabelled guards the race
// where the bulk deadline lands the instant after the final worker confirms
// its target. Every station reached its goal, so the operation must not be
// flagged cancelled/timed out nor surface an expiry error.
func TestBulkPowerLateDeadlineAfterAllConfirmedNotMislabelled(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:06"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-LATE-DEADLINE", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerRead: true, PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	var enteredOnce sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	manager.bluetoothOps.setPowerState = func(_ context.Context, _ *internalbluetooth.BaseStation, target internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return internalbluetooth.PowerControlResult{State: target, Confirmed: true}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	type outcome struct {
		result BulkPowerResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := manager.SetAllStationsPowerDetailedContext(ctx, "on")
		done <- outcome{result: result, err: err}
	}()
	<-entered
	// Let the caller deadline expire while the worker already holds a confirmed
	// outcome, then release it to finish successfully.
	time.Sleep(70 * time.Millisecond)
	close(release)
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("late deadline after full success returned error = %v", got.err)
		}
		if got.result.Cancelled || got.result.TimedOut {
			t.Fatalf("fully confirmed bulk was mislabelled: %+v", got.result)
		}
		if len(got.result.Results) != 1 || !got.result.Results[0].Success || !got.result.Results[0].Confirmed {
			t.Fatalf("bulk result = %+v", got.result.Results)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bulk operation did not finish after release")
	}
}

// TestBulkPowerLateCancellationAfterTerminalUnconfirmedWriteNotMislabelled
// guards write-only firmware, where a successfully sent command is a complete
// result even though the station cannot confirm the new state. Cancellation
// arriving at that final boundary must not overwrite the terminal outcome.
func TestBulkPowerLateCancellationAfterTerminalUnconfirmedWriteNotMislabelled(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:08"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-WRITE-ONLY",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateSleep,
		RawPowerState:     0x00,
		LastPowerReadAt:   time.Now(),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.bluetoothOps.setPowerState = func(_ context.Context, _ *internalbluetooth.BaseStation, target internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		cancel()
		return internalbluetooth.PowerControlResult{State: target, Confirmed: false}, nil
	}

	result, err := manager.SetAllStationsPowerDetailedContext(ctx, "on")
	if err != nil {
		t.Fatalf("terminal write-only result returned error = %v", err)
	}
	if result.Cancelled || result.TimedOut {
		t.Fatalf("terminal write-only result was mislabelled: %+v", result)
	}
	if len(result.Results) != 1 || !result.Results[0].Success ||
		!result.Results[0].CommandSent || result.Results[0].Confirmed ||
		result.Results[0].Skipped || result.Results[0].Error != "" {
		t.Fatalf("write-only bulk result = %+v", result.Results)
	}
}

// TestBulkCancelOwnsSkipWhenStationBudgetAlsoExpires verifies that a batch
// cancellation is the reported skip reason even when a station's own budget
// happens to expire in the same instant, instead of blaming the station.
func TestBulkCancelOwnsSkipWhenStationBudgetAlsoExpires(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:07"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CANCEL-RACE", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
	stubPowerVerificationRead(manager)
	started := make(chan struct{})
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		close(started)
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{}, context.DeadlineExceeded
	}
	type outcome struct {
		result BulkPowerResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := manager.SetAllStationsPowerDetailed("on")
		done <- outcome{result: result, err: err}
	}()
	<-started
	if err := manager.CancelBulkPower(); err != nil {
		t.Fatalf("CancelBulkPower() error = %v", err)
	}
	select {
	case actual := <-done:
		if len(actual.result.Results) != 1 ||
			actual.result.Results[0].Reason != ReasonOperationCancelled {
			t.Fatalf("cancelled-race station result = %+v, want reason %q", actual.result.Results, ReasonOperationCancelled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bulk operation did not finish after cancellation")
	}
}

// TestBulkStationTimeoutSurfacesInLegacyErrorContract guards the legacy
// error-only contract: a per-station operation timeout that lands while the
// bulk batch is still healthy carries no top-level error and arrives as a
// timeout-skipped entry. The aggregation must report it instead of swallowing
// it as an overall success.
func TestBulkStationTimeoutSurfacesInLegacyErrorContract(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.stationOperationTimeout = 20 * time.Millisecond
	address := "11:22:33:44:AA:09"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-STATION-TIMEOUT", Address: mustAddress(t, address), Present: true,
		PowerState: internalbluetooth.PowerStateSleep, RawPowerState: 0x00,
		LastPowerReadAt:   time.Now(),
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.bluetoothOps.setPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		<-ctx.Done()
		return internalbluetooth.PowerControlResult{}, ctx.Err()
	}

	err := manager.SetAllStationsPower("on")
	if err == nil {
		t.Fatal("SetAllStationsPower() unexpectedly reported success for a timed-out station")
	}
	if !strings.Contains(err.Error(), ReasonStationOperationTimeout) {
		t.Fatalf("SetAllStationsPower() error = %v, want the station timeout surfaced", err)
	}

	// The detailed form keeps its structured shape: skipped with the timeout
	// reason, no batch-level cancellation or timeout flag.
	result, detailedErr := manager.SetAllStationsPowerDetailed("on")
	if detailedErr != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v, want nil for a healthy batch", detailedErr)
	}
	if len(result.Results) != 1 || !result.Results[0].Skipped ||
		result.Results[0].Reason != ReasonStationOperationTimeout {
		t.Fatalf("detailed result = %+v, want a station-timeout skip", result.Results)
	}
	if result.Cancelled || result.TimedOut {
		t.Fatalf("healthy batch was mislabelled as interrupted: %+v", result)
	}
}

// TestBulkPowerInvalidTargetRejectedWhileBusy guards the entry validation
// order: an unparseable target is a permanent argument error and must not be
// masked as a retryable busy rejection just because another batch happens to
// be running.
func TestBulkPowerInvalidTargetRejectedWhileBusy(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.bulkLifecycleMutex.Lock()
	manager.bulkLifecycle = &bulkPowerLifecycle{cancel: func() {}, done: make(chan struct{})}
	manager.bulkLifecycleMutex.Unlock()
	t.Cleanup(func() {
		manager.bulkLifecycleMutex.Lock()
		manager.bulkLifecycle = nil
		manager.bulkLifecycleMutex.Unlock()
	})

	result, err := manager.SetAllStationsPowerDetailed("bogus-state")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v, want ErrInvalidArgument despite the busy batch", err)
	}
	if errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("invalid target masked as a busy rejection: %v", err)
	}
	if result.Results == nil || len(result.Results) != 0 {
		t.Fatalf("invalid-target results = %+v, want an empty non-nil list", result.Results)
	}

	// A valid target must still surface the busy rejection with the target.
	busyResult, busyErr := manager.SetAllStationsPowerDetailed("on")
	if !errors.Is(busyErr, ErrOperationInProgress) {
		t.Fatalf("valid target while busy error = %v, want ErrOperationInProgress", busyErr)
	}
	if busyResult.Target != internalbluetooth.PowerStateOn.String() {
		t.Fatalf("busy result target = %q, want %q", busyResult.Target, internalbluetooth.PowerStateOn.String())
	}
}

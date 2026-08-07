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

	result, err := manager.SetAllStationsPowerDetailed("on")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Skipped {
		t.Fatalf("stale booting station was skipped: %+v", result.Results)
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

func TestBulkPowerContextCancellationPreservesPossiblySentResult(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:03"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CANCELLED-CONFIRMATION", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
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

func TestBulkPowerReportsCallerDeadline(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:AA:05"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-TIMEOUT", Address: mustAddress(t, address), Present: true,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true}, CapabilitiesKnown: true,
	}
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

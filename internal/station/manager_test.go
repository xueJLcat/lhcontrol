package station

import (
	"errors"
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

func TestScanStatusLifecycleAndDefensiveCopy(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.markScanStarted()
	manager.addScanWarning("partial read")

	running := manager.GetScanStatus()
	if running.State != "running" || running.StartedAt == "" {
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

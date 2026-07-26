package station

import (
	"errors"
	"sync"
	"sync/atomic"
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
	manager.bluetoothOps.scanForDuration = func(time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
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
}

func TestAsyncScanEventsCannotOvertakePreviousCompletion(t *testing.T) {
	manager := NewManager(config.NewConfig())
	firstScanRelease := make(chan struct{})
	firstCompletionEntered := make(chan struct{})
	firstCompletionRelease := make(chan struct{})
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDuration = func(time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		if scanCalls.Add(1) == 1 {
			<-firstScanRelease
		}
		return nil, nil
	}

	var eventsMutex sync.Mutex
	events := make([]string, 0, 4)
	callbacks := ScanCallbacks{
		Started: func() {
			eventsMutex.Lock()
			events = append(events, "started")
			eventsMutex.Unlock()
		},
		Completed: func([]StationInfo) {
			eventsMutex.Lock()
			events = append(events, "completed")
			completionNumber := 0
			for _, event := range events {
				if event == "completed" {
					completionNumber++
				}
			}
			eventsMutex.Unlock()
			if completionNumber == 1 {
				close(firstCompletionEntered)
				<-firstCompletionRelease
			}
		},
	}
	if err := manager.StartScan(callbacks); err != nil {
		t.Fatalf("first StartScan() error = %v", err)
	}
	close(firstScanRelease)
	<-firstCompletionEntered
	if status := manager.GetScanStatus(); status.State != "completed" {
		t.Fatalf("status while completion callback is running = %+v", status)
	}
	if manager.IsBusy() {
		t.Fatal("terminal scan status was published before releasing the operation lock")
	}

	secondReturned := make(chan error, 1)
	go func() { secondReturned <- manager.StartScan(callbacks) }()
	select {
	case err := <-secondReturned:
		t.Fatalf("second StartScan returned before prior completion callback ended: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(firstCompletionRelease)
	if err := <-secondReturned; err != nil {
		t.Fatalf("second StartScan() error = %v", err)
	}
	manager.asyncScanWg.Wait()

	eventsMutex.Lock()
	defer eventsMutex.Unlock()
	want := []string{"started", "completed", "started", "completed"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestScanInitialReadClassifiesPartialFailures(t *testing.T) {
	for _, test := range []struct {
		name            string
		readErr         error
		wantDisconnect  bool
		wantRetry       bool
		wantUnavailable bool
	}{
		{
			name:    "channel only",
			readErr: &internalbluetooth.InitialReadError{Channel: errors.New("channel unavailable")},
		},
		{
			name:           "power",
			readErr:        &internalbluetooth.InitialReadError{Power: errors.New("power unavailable")},
			wantDisconnect: true,
			wantRetry:      true,
		},
		{
			name:            "adapter",
			readErr:         tinybluetooth.ErrRadioNotAvailable,
			wantDisconnect:  true,
			wantRetry:       true,
			wantUnavailable: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			address := "11:22:33:44:55:66"
			manager.bluetoothOps.scanForDuration = func(time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
				return []internalbluetooth.DiscoveredStation{{
					Name: "LHB-TEST", Address: mustAddress(t, address),
				}}, nil
			}
			manager.bluetoothOps.fetchInitialPowerState = func(*internalbluetooth.BaseStation) error {
				return test.readErr
			}
			disconnects := 0
			manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) { disconnects++ }

			if _, err := manager.ScanAndFetchStations(); err != nil {
				t.Fatalf("ScanAndFetchStations() error = %v", err)
			}
			if got := disconnects > 0; got != test.wantDisconnect {
				t.Fatalf("disconnect = %v, want %v", got, test.wantDisconnect)
			}
			manager.statusRetryMutex.Lock()
			_, retryTracked := manager.statusRetries[address]
			manager.statusRetryMutex.Unlock()
			if retryTracked != test.wantRetry {
				t.Fatalf("retry tracked = %v, want %v", retryTracked, test.wantRetry)
			}
			manager.initializeMutex.Lock()
			unavailable := manager.initializeErr != nil
			manager.initializeMutex.Unlock()
			if unavailable != test.wantUnavailable {
				t.Fatalf("adapter unavailable = %v, want %v", unavailable, test.wantUnavailable)
			}
		})
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

func TestBulkPowerReportsConfirmedUnsupportedCapabilitiesAsSkipped(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:66"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-UNSUPPORTED", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.ensureCapabilities = func(*internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setPowerState = func(*internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
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
	manager.bluetoothOps.ensureCapabilities = func(*internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
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

func TestStatusRecoveryBackfillsBusyCandidatesAndLimitsConcurrency(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	addresses := []string{"11:22:33:44:55:61", "11:22:33:44:55:62", "11:22:33:44:55:63"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name:    "LHB-" + address[len(address)-2:],
			Address: mustAddress(t, address),
			Present: true,
		}
		manager.statusRetries[address] = statusRetry{nextAt: now.Add(-time.Second)}
	}
	if err := manager.beginStationOperation(addresses[0]); err != nil {
		t.Fatalf("reserve busy station: %v", err)
	}
	defer manager.endStationOperation(addresses[0])

	var active atomic.Int32
	var maximum atomic.Int32
	var recoveredMutex sync.Mutex
	recovered := make([]string, 0, 2)
	manager.bluetoothOps.fetchInitialPowerState = func(station *internalbluetooth.BaseStation) error {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		recoveredMutex.Lock()
		recovered = append(recovered, station.Snapshot().Address)
		recoveredMutex.Unlock()
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		return nil
	}

	manager.scheduleStatusRecovery()
	deadline := time.Now().Add(time.Second)
	for manager.statusRecoveryRunning.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	recoveredMutex.Lock()
	defer recoveredMutex.Unlock()
	if len(recovered) != 1 || recovered[0] != addresses[1] {
		t.Fatalf("recovered addresses = %v, want busy candidate skipped and %s recovered", recovered, addresses[1])
	}
	if maximum.Load() > 1 {
		t.Fatalf("recovery concurrency = %d, want at most 1", maximum.Load())
	}
}

func TestStatusRecoveryLeavesOneSlotForForegroundWork(t *testing.T) {
	manager := NewManager(config.NewConfig())
	recoveryAddress := "11:22:33:44:55:61"
	foregroundAddress := "11:22:33:44:55:62"
	manager.stations[recoveryAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY", Address: mustAddress(t, recoveryAddress), Present: true,
	}
	manager.statusRetries[recoveryAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	recoveryStarted := make(chan struct{})
	recoveryRelease := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(*internalbluetooth.BaseStation) error {
		close(recoveryStarted)
		<-recoveryRelease
		return nil
	}

	manager.scheduleStatusRecovery()
	<-recoveryStarted
	if err := manager.beginStationOperation(foregroundAddress); err != nil {
		t.Fatalf("foreground operation could not use reserved slot: %v", err)
	}
	manager.endStationOperation(foregroundAddress)
	close(recoveryRelease)
	deadline := time.Now().Add(time.Second)
	for manager.statusRecoveryRunning.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.statusRecoveryRunning.Load() {
		t.Fatal("recovery did not finish")
	}
}

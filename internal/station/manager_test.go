package station

import (
	"errors"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

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

	_, err := manager.SetStationChannel("target", 5)
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

	_, err := manager.SetStationChannel("target", 5)
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
		if _, err := manager.SetStationChannel("missing", channel); !errors.Is(err, ErrInvalidArgument) {
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
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true},
	}
	manager.stations["booting"] = &internalbluetooth.BaseStation{
		Name: "LHB-BOOTING", Present: true, PowerState: internalbluetooth.PowerStateBooting,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true},
	}
	manager.stations["not-visible"] = &internalbluetooth.BaseStation{
		Name: "LHB-OFFLINE", Present: false, PowerState: internalbluetooth.PowerStateSleep,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true},
	}

	if err := manager.SetAllStationsPower("on"); err != nil {
		t.Fatalf("SetAllStationsPower() should skip all ineligible stations, got %v", err)
	}

	manager.stations["no-standby"] = &internalbluetooth.BaseStation{
		Name: "LHB-NO-STANDBY", Present: true, PowerState: internalbluetooth.PowerStateSleep,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true, Standby: false},
	}
	if err := manager.SetAllStationsPower("standby"); err != nil {
		t.Fatalf("SetAllStationsPower() should skip stations without standby capability, got %v", err)
	}
	result, err := manager.SetAllStationsPowerDetailed("standby")
	if err != nil {
		t.Fatalf("SetAllStationsPowerDetailed() error = %v", err)
	}
	if len(result.Results) != 3 {
		t.Fatalf("detailed result count = %d, want all three visible stations", len(result.Results))
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

func TestLegacyBulkPowerExcludesAbsentStations(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["offline"] = &internalbluetooth.BaseStation{
		Name:         "LHB-OFFLINE",
		Present:      false,
		PowerState:   internalbluetooth.PowerStateUnknown,
		Capabilities: internalbluetooth.Capabilities{PowerWrite: true},
	}

	if err := manager.PowerOnAllStations(); err != nil {
		t.Fatalf("PowerOnAllStations() attempted an absent station: %v", err)
	}
	if err := manager.PowerOffAllStations(); err != nil {
		t.Fatalf("PowerOffAllStations() attempted an absent station: %v", err)
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

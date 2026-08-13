package station

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"

	tinybluetooth "tinygo.org/x/bluetooth"
)

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

func TestChannelOperationHasHardTimeoutAndReleasesOperation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.stationOperationTimeout = 25 * time.Millisecond
	address := "11:22:33:44:55:91"
	now := time.Now()
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-CHANNEL-TIMEOUT", Address: mustAddress(t, address), Present: true,
		Channel: 3, LastSeenAt: now, LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true}, CapabilitiesKnown: true,
	}
	manager.bluetoothOps.setChannel = func(ctx context.Context, _ *internalbluetooth.BaseStation, _ int) (internalbluetooth.ChannelWriteResult, error) {
		<-ctx.Done()
		return internalbluetooth.ChannelWriteResult{}, ctx.Err()
	}

	if _, err := manager.SetStationChannel(address, 4, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SetStationChannel() error = %v, want context deadline", err)
	}
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		return internalbluetooth.ChannelWriteResult{PreviousChannel: 3, Channel: 4, CommandSent: true}, nil
	}
	if _, err := manager.SetStationChannel(address, 4, false); err != nil {
		t.Fatalf("operation lock was not released after timeout: %v", err)
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

func TestSetStationChannelAlreadyAtFreshTargetIsNoOp(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginStationOperation("busy-1"); err != nil {
		t.Fatalf("beginStationOperation(busy-1) error = %v", err)
	}
	defer manager.endStationOperation("busy-1")
	if err := manager.beginStationOperation("busy-2"); err != nil {
		t.Fatalf("beginStationOperation(busy-2) error = %v", err)
	}
	defer manager.endStationOperation("busy-2")
	manager.initializeErr = errors.New("adapter unavailable")
	manager.nextInitializeAt = time.Now().Add(time.Hour)
	manager.initializeBluetooth = func() error {
		t.Fatal("Bluetooth initialization was attempted for a channel no-op")
		return nil
	}
	address := "AA:BB:CC:DD:EE:09"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, address),
		Name:              "LHB-SAME-CHANNEL",
		Channel:           7,
		LastChannelReadAt: time.Now(),
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability refresh was attempted for a channel no-op")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		t.Fatal("channel read/write operation was attempted for a channel no-op")
		return internalbluetooth.ChannelWriteResult{}, nil
	}

	result, err := manager.SetStationChannel(address, 7, false)
	if err != nil {
		t.Fatalf("SetStationChannel() error = %v", err)
	}
	if result.Address != address || result.PreviousChannel != 7 || result.Channel != 7 ||
		result.CommandSent || !result.Confirmed || len(result.Warnings) != 0 {
		t.Fatalf("channel no-op result = %+v", result)
	}
}

func TestSetStationChannelPreservesPostWriteConfirmationError(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, "AA:BB:CC:DD:EE:01"),
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		return internalbluetooth.ChannelWriteResult{
				PreviousChannel: 3,
				Channel:         internalbluetooth.ChannelUnknown,
				CommandSent:     true,
			},
			fmt.Errorf(
				"channel confirmation failed: %w",
				&internalbluetooth.UnsupportedCapabilityError{
					Capability: "channel read",
					Err:        tinybluetooth.ErrAttReadNotPermitted,
				},
			)
	}

	result, err := manager.SetStationChannel("target", 5, false)
	if err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetStationChannel() error = %v, want post-write confirmation error", err)
	}
	if !errors.Is(err, tinybluetooth.ErrAttReadNotPermitted) {
		t.Fatalf("SetStationChannel() error = %v, want ATT read-not-permitted cause", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "command was sent") {
		t.Fatalf("warnings = %v, want unconfirmed command warning", result.Warnings)
	}
	if !result.CommandSent || result.Confirmed || result.ConfirmationError == "" {
		t.Fatalf("result = %+v, want sent but unconfirmed with confirmation error", result)
	}
}

func TestSetStationChannelMapsConfirmedWriteResult(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	address := "AA:BB:CC:DD:EE:04"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, address),
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.setChannel = func(_ context.Context, station *internalbluetooth.BaseStation, _ int) (internalbluetooth.ChannelWriteResult, error) {
		station.Channel = 5
		station.LastChannelReadAt = time.Now()
		return internalbluetooth.ChannelWriteResult{
			PreviousChannel: 3,
			Channel:         5,
			CommandSent:     true,
			WriteWarning:    "confirmed after retry",
		}, nil
	}

	result, err := manager.SetStationChannel(address, 5, false)
	if err != nil {
		t.Fatalf("SetStationChannel() error = %v", err)
	}
	if result.PreviousChannel != 3 || result.Channel != 5 || !result.CommandSent || !result.Confirmed ||
		result.ConfirmationError != "" || len(result.Warnings) != 1 ||
		result.Station.Address != address || result.Station.Channel != 5 {
		t.Fatalf("confirmed channel result = %+v", result)
	}
}

// TestSetStationChannelReconcilesMetadataRetryAfterReread verifies that the
// metadata re-read performed by a channel change's discovery reconciles the
// background metadata retry, instead of leaving a satisfied retry to trigger
// redundant recovery rounds.
func TestSetStationChannelReconcilesMetadataRetryAfterReread(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	now := time.Now()
	address := "AA:BB:CC:DD:EE:0B"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Address:              mustAddress(t, address),
		Name:                 "LHB-META",
		Channel:              3,
		Present:              true,
		LastSeenAt:           now,
		LastChannelReadAt:    now,
		MetadataReadAt:       now.Add(-time.Minute),
		MetadataReadRevision: 1,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	// Seed a pending metadata retry that a satisfying re-read must clear.
	manager.noteMetadataFailure(address)
	manager.bluetoothOps.setChannel = func(_ context.Context, station *internalbluetooth.BaseStation, _ int) (internalbluetooth.ChannelWriteResult, error) {
		station.Channel = 6
		station.LastChannelReadAt = time.Now()
		// Discovery re-read the device metadata successfully as a side effect.
		station.MetadataReadAt = time.Now()
		station.MetadataReadRevision++
		return internalbluetooth.ChannelWriteResult{PreviousChannel: 3, Channel: 6, CommandSent: true}, nil
	}

	if _, err := manager.SetStationChannel(address, 6, false); err != nil {
		t.Fatalf("SetStationChannel() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked && effectiveStatusRetryKinds(retry)&statusRetryMetadata != 0 {
		t.Fatalf("metadata retry survived a satisfied re-read during channel change: %+v", retry)
	}
}

func TestSetStationChannelRejectsFreshBootingStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "AA:BB:CC:DD:EE:0A"
	now := time.Now()
	manager.stations[address] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, address),
		Name:              "LHB-BOOTING",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		PowerState:        internalbluetooth.PowerStateBooting,
		LastPowerReadAt:   now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability refresh was attempted while station was booting")
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		t.Fatal("channel write was attempted while station was booting")
		return internalbluetooth.ChannelWriteResult{}, nil
	}

	result, err := manager.SetStationChannel(address, 5, false)
	if !errors.Is(err, ErrStationTransitioning) || !strings.Contains(err.Error(), "station is booting") {
		t.Fatalf("SetStationChannel() error = %v, want booting transition conflict", err)
	}
	if result.CommandSent || result.Station.Address != address {
		t.Fatalf("booting channel result = %+v", result)
	}
}

func TestSetStationChannelRechecksBootingStateAfterCapabilityRefresh(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "AA:BB:CC:DD:EE:11"
	now := time.Now()
	station := &internalbluetooth.BaseStation{
		Address:           mustAddress(t, address),
		Name:              "LHB-BOOT-DURING-CHANNEL",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		LastPowerReadAt:   now,
	}
	manager.stations[address] = station
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		// Model a notification landing while capability discovery owns the
		// operation: the earlier power precondition is no longer authoritative.
		station.PowerState = internalbluetooth.PowerStateBooting
		station.RawPowerState = 0x01
		station.LastPowerReadAt = time.Now()
		return internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true}, nil
	}
	var writes atomic.Int32
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		writes.Add(1)
		return internalbluetooth.ChannelWriteResult{PreviousChannel: 3, Channel: 5, CommandSent: true}, nil
	}

	result, err := manager.SetStationChannel(address, 5, false)
	if !errors.Is(err, ErrStationTransitioning) {
		t.Fatalf("SetStationChannel() error = %v, want ErrStationTransitioning", err)
	}
	if writes.Load() != 0 || result.CommandSent {
		t.Fatalf("boot-after-refresh result = %+v, writes = %d; want no command", result, writes.Load())
	}
}

func TestSetStationChannelMapsPreWriteUnsupportedCapability(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Address:           mustAddress(t, "AA:BB:CC:DD:EE:02"),
		Name:              "LHB-TARGET",
		Channel:           3,
		Present:           true,
		LastSeenAt:        now,
		LastChannelReadAt: now,
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		return internalbluetooth.ChannelWriteResult{},
			&internalbluetooth.UnsupportedCapabilityError{
				Capability: "channel write",
				Err:        tinybluetooth.ErrAttWriteNotPermitted,
			}
	}

	result, err := manager.SetStationChannel("target", 5, false)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetStationChannel() error = %v, want ErrUnsupported", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, command was not sent", result.Warnings)
	}
}

func TestStationChannelRequiresRecentScan(t *testing.T) {
	// Drive the freshness window through the config (a non-default value) so a
	// wiring regression that hardcodes the old constant would fail this test.
	cfg := config.NewConfig()
	cfg.ChannelScanFreshnessSeconds = 60
	manager := NewManager(cfg)
	manager.stations["target"] = &internalbluetooth.BaseStation{
		Name:       "LHB-TARGET",
		Channel:    3,
		Present:    true,
		LastSeenAt: time.Now().Add(-60*time.Second - time.Second),
		Capabilities: internalbluetooth.Capabilities{
			ChannelRead: true, ChannelWrite: true,
		},
	}

	_, err := manager.SetStationChannel("target", 5, false)
	if !errors.Is(err, ErrScanRequired) {
		t.Fatalf("SetStationChannel() error = %v, want ErrScanRequired", err)
	}
}

func TestStationChannelRejectsUncertainPresence(t *testing.T) {
	now := time.Now()
	newStation := func(address string) *internalbluetooth.BaseStation {
		station := &internalbluetooth.BaseStation{
			Name: "LHB-" + address[len(address)-2:], Address: mustAddress(t, address),
			Channel: 3, Present: true, LastSeenAt: now, LastChannelReadAt: now,
			Capabilities: internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true},
		}
		station.MarkPresenceUncertain()
		return station
	}

	t.Run("target", func(t *testing.T) {
		manager := NewManager(config.NewConfig())
		address := "11:22:33:44:55:91"
		manager.stations[address] = newStation(address)
		if _, err := manager.SetStationChannel(address, 5, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("SetStationChannel() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("other station", func(t *testing.T) {
		manager := NewManager(config.NewConfig())
		targetAddress := "11:22:33:44:55:92"
		manager.stations[targetAddress] = &internalbluetooth.BaseStation{
			Name: "LHB-TARGET", Address: mustAddress(t, targetAddress), Channel: 3, Present: true,
			LastSeenAt: now, LastChannelReadAt: now,
			Capabilities: internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true},
		}
		manager.stations["uncertain"] = newStation("11:22:33:44:55:93")
		if _, err := manager.SetStationChannel(targetAddress, 5, false); !errors.Is(err, ErrScanRequired) {
			t.Fatalf("SetStationChannel() error = %v, want ErrScanRequired", err)
		}
	})
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
		LastChannelReadAt: now.Add(-operationSafetyFreshnessWindow - time.Second),
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

func TestChannelChangeRevalidatesPresenceAfterCapabilityRefresh(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:81"
	station := &internalbluetooth.BaseStation{
		Name:       "LHB-MOVED",
		Address:    mustAddress(t, address),
		Channel:    3,
		Present:    true,
		LastSeenAt: time.Now(),
	}
	manager.stations[address] = station
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		station.SetPresent(false)
		return internalbluetooth.Capabilities{ChannelRead: true, ChannelWrite: true}, nil
	}
	var writes atomic.Int32
	manager.bluetoothOps.setChannel = func(context.Context, *internalbluetooth.BaseStation, int) (internalbluetooth.ChannelWriteResult, error) {
		writes.Add(1)
		return internalbluetooth.ChannelWriteResult{}, nil
	}

	_, err := manager.SetStationChannel(address, 4, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStationChannel() error = %v, want ErrNotFound", err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("channel writes = %d, want 0", got)
	}
}

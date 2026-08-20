package station

import (
	"fmt"
	"strings"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

func TestDisplayFreshnessTracksLongPollingIntervals(t *testing.T) {
	for _, intervalSeconds := range []int{60, 120, 300} {
		t.Run(fmt.Sprintf("%d_seconds", intervalSeconds), func(t *testing.T) {
			cfg := config.NewConfig()
			cfg.StatusPollIntervalSeconds = intervalSeconds
			manager := NewManager(cfg)
			address := fmt.Sprintf("11:22:33:44:55:%02X", intervalSeconds/60)
			readAt := time.Now().Add(-time.Duration(2*intervalSeconds) * time.Second)
			manager.stations[address] = &internalbluetooth.BaseStation{
				Name:              "LHB-LONG-POLL",
				Address:           mustAddress(t, address),
				Present:           true,
				PowerState:        internalbluetooth.PowerStateOn,
				RawPowerState:     0x0B,
				Channel:           3,
				LastPowerReadAt:   readAt,
				LastChannelReadAt: readAt,
			}

			infos := manager.GetStationInfo()
			if len(infos) != 1 || !infos[0].PowerFresh || !infos[0].PowerStateConfirmed || !infos[0].ChannelFresh {
				t.Fatalf("station info = %+v, want display-fresh confirmed state", infos)
			}
			if infos[0].PowerOperationallyFresh {
				t.Fatalf("station info = %+v, old display power must not pass the write-safety window", infos[0])
			}
			if infos[0].ChannelOperationallyFresh {
				t.Fatalf("station info = %+v, old display channel must not pass the write-safety window", infos[0])
			}
			wantFreshUntil := formatTimestamp(readAt.Add(operationSafetyFreshnessWindow))
			if infos[0].PowerOperationalFreshUntil != wantFreshUntil ||
				infos[0].ChannelOperationalFreshUntil != wantFreshUntil {
				t.Fatalf("operation freshness deadlines = power %q channel %q, want %q",
					infos[0].PowerOperationalFreshUntil, infos[0].ChannelOperationalFreshUntil, wantFreshUntil)
			}
			if isOperationallyFresh(readAt, time.Now()) {
				t.Fatal("long-poll display state unexpectedly passed the fixed operation-safety window")
			}
		})
	}
}

func TestOperationalFreshnessAcceptsRecentReads(t *testing.T) {
	cfg := config.NewConfig()
	cfg.StatusPollIntervalSeconds = 300
	manager := NewManager(cfg)
	address := "11:22:33:44:55:66"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-RECENT",
		Address:           mustAddress(t, address),
		Present:           true,
		PowerState:        internalbluetooth.PowerStateOn,
		RawPowerState:     0x0B,
		Channel:           3,
		LastPowerReadAt:   time.Now().Add(-30 * time.Second),
		LastChannelReadAt: time.Now().Add(-30 * time.Second),
	}

	infos := manager.GetStationInfo()
	if len(infos) != 1 || !infos[0].PowerFresh || !infos[0].PowerOperationallyFresh ||
		!infos[0].ChannelFresh || !infos[0].ChannelOperationallyFresh {
		t.Fatalf("station info = %+v, want recent reads fresh for display and operations", infos)
	}
}

// TestGetStationInfoDoesNotBlockOnWedgedStationLock guards the fleet
// projection against a station wedged inside a transport call that ignores
// cancellation: its lock stays held indefinitely, and every list consumer
// (UI polls, the HTTP status endpoint, scan results) would otherwise queue
// behind it. The projection must return immediately, rendering the wedged
// station from its most recent snapshot.
func TestGetStationInfoDoesNotBlockOnWedgedStationLock(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:7B"
	station := &internalbluetooth.BaseStation{
		Name:          "LHB-WEDGED",
		Address:       mustAddress(t, address),
		Present:       true,
		PowerState:    internalbluetooth.PowerStateOn,
		RawPowerState: 0x0B,
		Channel:       3,
	}
	manager.stations[address] = station
	// Prime the snapshot cache with a successful read before the wedge.
	station.Snapshot()

	acquired := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go station.HoldLockWhile(func() {
		close(acquired)
		<-release
	})
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("wedged lock was not acquired")
	}

	done := make(chan []StationInfo, 1)
	go func() { done <- manager.GetStationInfo() }()
	select {
	case infos := <-done:
		if len(infos) != 1 {
			t.Fatalf("station info count = %d, want the wedged station projected from its cached snapshot", len(infos))
		}
		if infos[0].Address != address || infos[0].PowerState != 1 {
			t.Fatalf("station info = %+v, want the cached last-known state", infos[0])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetStationInfo blocked behind the wedged station lock")
	}
}

// TestRunSafelyConvertsPanicsToErrors guards the panic boundary: a panicking
// operation must surface as a descriptive error instead of killing the caller
// goroutine (and, for the shutdown drain, the whole cleanup sequence).
func TestRunSafelyConvertsPanicsToErrors(t *testing.T) {
	err := runSafely("test scope", func() error {
		panic("driver exploded")
	})
	if err == nil {
		t.Fatal("runSafely() swallowed the panic")
	}
	if !strings.Contains(err.Error(), "test scope") || !strings.Contains(err.Error(), "driver exploded") {
		t.Fatalf("runSafely() error = %q, want it to carry the scope and the panic value", err.Error())
	}
}

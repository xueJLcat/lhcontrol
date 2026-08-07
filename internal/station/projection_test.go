package station

import (
	"fmt"
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
				Name:            "LHB-LONG-POLL",
				Address:         mustAddress(t, address),
				Present:         true,
				PowerState:      internalbluetooth.PowerStateOn,
				RawPowerState:   0x0B,
				LastPowerReadAt: readAt,
			}

			infos := manager.GetStationInfo()
			if len(infos) != 1 || !infos[0].PowerFresh || !infos[0].PowerStateConfirmed {
				t.Fatalf("station info = %+v, want display-fresh confirmed state", infos)
			}
			if isOperationallyFresh(readAt, time.Now()) {
				t.Fatal("long-poll display state unexpectedly passed the fixed operation-safety window")
			}
		})
	}
}

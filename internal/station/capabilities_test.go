package station

import (
	"context"
	"errors"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

func TestCapabilityActionsRejectShutdown(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:A1"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:              "LHB-CAPABILITY-SHUTDOWN",
		Address:           mustAddress(t, address),
		Capabilities:      internalbluetooth.Capabilities{Identify: true},
		CapabilitiesKnown: true,
	}
	manager.bluetoothOps.identify = func(context.Context, *internalbluetooth.BaseStation) error {
		t.Fatal("identify operation started after shutdown")
		return nil
	}
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		t.Fatal("capability refresh started after shutdown")
		return internalbluetooth.Capabilities{}, nil
	}

	manager.BeginShutdown()

	if err := manager.IdentifyStation(address); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("IdentifyStation() error = %v, want ErrShuttingDown", err)
	}
	if _, err := manager.RefreshStationCapabilities(address); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("RefreshStationCapabilities() error = %v, want ErrShuttingDown", err)
	}
}

func TestIdentifyUnsupportedClearsOnlyConnectionRecovery(t *testing.T) {
	tests := []struct {
		name      string
		station   *internalbluetooth.BaseStation
		configure func(*testing.T, *Manager)
	}{
		{
			name: "refreshed capability is unavailable",
			station: &internalbluetooth.BaseStation{
				CapabilitiesKnown: true,
			},
			configure: func(t *testing.T, manager *Manager) {
				manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
					return internalbluetooth.Capabilities{}, nil
				}
				manager.bluetoothOps.identify = func(context.Context, *internalbluetooth.BaseStation) error {
					t.Fatal("identify write was attempted without identify capability")
					return nil
				}
			},
		},
		{
			name: "write reports unsupported capability",
			station: &internalbluetooth.BaseStation{
				Capabilities:      internalbluetooth.Capabilities{Identify: true},
				CapabilitiesKnown: true,
			},
			configure: func(_ *testing.T, manager *Manager) {
				manager.bluetoothOps.identify = func(context.Context, *internalbluetooth.BaseStation) error {
					return &internalbluetooth.UnsupportedCapabilityError{Capability: "identify"}
				}
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(config.NewConfig())
			defer manager.Shutdown()
			manager.statusRecoveryStart.Do(func() {})
			address := []string{"11:22:33:44:55:A2", "11:22:33:44:55:A3"}[index]
			test.station.Name = "LHB-IDENTIFY-UNSUPPORTED"
			test.station.Address = mustAddress(t, address)
			manager.stations[address] = test.station
			manager.statusRetryMutex.Lock()
			manager.statusRetries[address] = statusRetry{
				kinds:         statusRetryConnection | statusRetryChannel,
				failures:      2,
				nextAt:        time.Now().Add(time.Hour),
				channelNextAt: time.Now().Add(time.Hour),
			}
			manager.statusRetryMutex.Unlock()
			test.configure(t, manager)

			err := manager.IdentifyStation(address)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("IdentifyStation() error = %v, want ErrUnsupported", err)
			}
			manager.statusRetryMutex.Lock()
			retry, tracked := manager.statusRetries[address]
			manager.statusRetryMutex.Unlock()
			if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
				t.Fatalf("retry after unsupported identify = %+v, tracked=%v; want channel-only recovery", retry, tracked)
			}
		})
	}
}

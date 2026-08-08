package station

import (
	"context"
	"testing"

	internalbluetooth "lhcontrol/internal/bluetooth"

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

// stubPowerVerificationRead keeps the pre-write cache verification inside tests
// that do not model it explicitly. Without a stub the manager would attempt a
// real GATT read against the host adapter whenever a snapshot is stale or
// already at the target state.
func stubPowerVerificationRead(manager *Manager) {
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return nil
	}
}

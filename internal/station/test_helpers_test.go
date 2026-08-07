package station

import (
	"testing"
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

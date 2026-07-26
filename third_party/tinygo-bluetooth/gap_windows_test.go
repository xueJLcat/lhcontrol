//go:build windows

package bluetooth

import (
	"errors"
	"testing"

	winbluetooth "github.com/saltosystems/winrt-go/windows/devices/bluetooth"
)

func TestScanStoppedErrorMapping(t *testing.T) {
	tests := []struct {
		code winbluetooth.BluetoothError
		want error
	}{
		{winbluetooth.BluetoothErrorSuccess, nil},
		{winbluetooth.BluetoothErrorRadioNotAvailable, ErrRadioNotAvailable},
		{winbluetooth.BluetoothErrorResourceInUse, ErrResourceInUse},
		{winbluetooth.BluetoothErrorDisabledByPolicy, ErrDisabledByPolicy},
	}
	for _, test := range tests {
		err := scanStoppedError(test.code)
		if test.want == nil && err != nil {
			t.Fatalf("code %d returned %v", test.code, err)
		}
		if test.want != nil && !errors.Is(err, test.want) {
			t.Fatalf("code %d returned %v, want %v", test.code, err, test.want)
		}
	}
}

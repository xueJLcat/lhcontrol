//go:build windows

package bluetooth

import (
	"errors"
	"testing"
	"time"

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

func TestDisconnectWaitsForExistingCleanup(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	state := &deviceState{
		cleanupStarted: true,
		cleanupDone:    make(chan struct{}),
	}
	device := Device{state: state}
	returned := make(chan error, 1)
	go func() {
		returned <- device.Disconnect()
	}()

	select {
	case err := <-returned:
		t.Fatalf("Disconnect returned before cleanup completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	state.cleanupMutex.Lock()
	state.cleanupErr = cleanupErr
	state.cleanupComplete = true
	close(state.cleanupDone)
	state.cleanupMutex.Unlock()

	select {
	case err := <-returned:
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("Disconnect error = %v, want %v", err, cleanupErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not return after cleanup completed")
	}
}

func TestCallbackGateWaitsForActiveCallbackAndRejectsNewWork(t *testing.T) {
	gate := newCallbackGate()
	if !gate.begin() {
		t.Fatal("open callback gate rejected work")
	}
	gate.close()

	waited := make(chan struct{})
	go func() {
		gate.wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("callback gate returned while a callback was active")
	case <-time.After(20 * time.Millisecond):
	}

	if gate.begin() {
		t.Fatal("closed callback gate allowed callback work")
	}
	gate.end()
	select {
	case <-waited:
		t.Fatal("callback gate ignored the original active callback")
	case <-time.After(20 * time.Millisecond):
	}

	gate.end()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("callback gate did not finish after callbacks drained")
	}
}

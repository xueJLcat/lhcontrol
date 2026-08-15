package bluetooth

import (
	"errors"
	"sync"
	"testing"
	"time"

	tinybluetooth "tinygo.org/x/bluetooth"
)

// blockingDisconnectDevice is a connected device whose Disconnect blocks until
// released, modeling an unresponsive station whose WinRT cleanup stalls.
type blockingDisconnectDevice struct {
	block   chan struct{}
	started chan struct{}
	failErr error
	once    sync.Once
}

func (d *blockingDisconnectDevice) Disconnect() error {
	d.once.Do(func() { close(d.started) })
	<-d.block
	return d.failErr
}
func (d *blockingDisconnectDevice) Connected() (bool, error) { return true, nil }
func (d *blockingDisconnectDevice) DiscoverServices([]tinybluetooth.UUID) ([]tinybluetooth.DeviceService, error) {
	return nil, errors.New("fake discovery is not configured")
}
func (d *blockingDisconnectDevice) RequestConnectionParams(tinybluetooth.ConnectionParams) error {
	return nil
}

// TestDisconnectReleasesStationLockDuringCleanup guards the fix for a stuck
// WinRT disconnect blocking every snapshot and operation on the station. The
// cleanup must run outside the station lock, and the station must already read
// as disconnected while the cleanup is still in flight.
func TestDisconnectReleasesStationLockDuringCleanup(t *testing.T) {
	device := &blockingDisconnectDevice{block: make(chan struct{}), started: make(chan struct{})}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.device = device

	done := make(chan error, 1)
	go func() {
		station.mutex.Lock()
		done <- disconnectInternal(station)
		station.mutex.Unlock()
	}()

	select {
	case <-device.started:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not reach the WinRT cleanup call")
	}

	// While the WinRT cleanup is blocked the station lock must be free ...
	if !station.mutex.TryLock() {
		close(device.block)
		<-done
		t.Fatal("station lock is still held while the WinRT cleanup is blocked")
	}
	station.mutex.Unlock()
	// ... and the observable state must already describe a disconnected
	// station instead of queuing readers behind the cleanup.
	if snapshot := station.Snapshot(); snapshot.Connected {
		close(device.block)
		<-done
		t.Fatalf("station still reads connected while cleanup is blocked: %+v", snapshot)
	}

	close(device.block)
	if err := <-done; err != nil {
		t.Fatalf("disconnectInternal() error = %v", err)
	}
}

// TestPendingCleanupReleasesLockAndRetainsHandleOnFailure covers the retry path
// for an earlier failed disconnect: the WinRT cleanup runs outside the lock and
// a still-failing handle must be kept for a later retry instead of dropped.
func TestPendingCleanupReleasesLockAndRetainsHandleOnFailure(t *testing.T) {
	failErr := errors.New("cleanup still stuck")
	device := &blockingDisconnectDevice{block: make(chan struct{}), started: make(chan struct{}), failErr: failErr}
	station := connectedFakeStation(nil, nil, nil, Capabilities{})
	station.device = nil
	station.isConnected = false
	station.pendingCleanup = device

	done := make(chan error, 1)
	go func() {
		station.mutex.Lock()
		done <- cleanupPendingInternal(station)
		station.mutex.Unlock()
	}()

	select {
	case <-device.started:
	case <-time.After(2 * time.Second):
		t.Fatal("pending cleanup did not reach the WinRT call")
	}
	if !station.mutex.TryLock() {
		close(device.block)
		<-done
		t.Fatal("station lock is still held while the pending cleanup is blocked")
	}
	station.mutex.Unlock()

	close(device.block)
	if err := <-done; !errors.Is(err, failErr) {
		t.Fatalf("cleanupPendingInternal() error = %v, want %v", err, failErr)
	}
	if station.pendingCleanup != device {
		t.Fatal("failed pending cleanup was not retained for retry")
	}
}

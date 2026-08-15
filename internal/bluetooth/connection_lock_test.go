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

// TestDisconnectDoesNotDropReconnectedTrackingEntry guards against a slow
// disconnect whose cleanup returns after a concurrent operation already
// rebuilt the connection: the late tracking-filter must not remove the entry
// for the live connection, or it leaks from DisconnectAllStations and
// adapter-change invalidation.
func TestDisconnectDoesNotDropReconnectedTrackingEntry(t *testing.T) {
	device := &blockingDisconnectDevice{block: make(chan struct{}), started: make(chan struct{})}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.device = device

	connectedStationsMutex.Lock()
	previous := connectedStations
	connectedStations = []*BaseStation{station}
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		connectedStations = previous
		connectedStationsMutex.Unlock()
	})

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

	// Recreate the connection while the old cleanup is still blocked. The
	// registry entry already exists, so the connect dedup keeps it.
	station.mutex.Lock()
	station.device = fakeConnectedDevice{}
	station.isConnected = true
	station.mutex.Unlock()

	close(device.block)
	if err := <-done; err != nil {
		t.Fatalf("disconnectInternal() error = %v", err)
	}
	connectedStationsMutex.Lock()
	count := len(connectedStations)
	connectedStationsMutex.Unlock()
	if count != 1 {
		t.Fatalf("tracked connection count = %d, want the rebuilt connection retained", count)
	}
	if snapshot := station.Snapshot(); !snapshot.Connected {
		t.Fatalf("rebuilt connection reads disconnected: %+v", snapshot)
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

	// A failed cleanup registers the station for fleet-wide retry; restore the
	// global tracking list so the retained handle does not leak into later
	// tests' DisconnectAllConnections/InvalidateAllConnections runs.
	connectedStationsMutex.Lock()
	previousPending := pendingCleanupStations
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		pendingCleanupStations = previousPending
		connectedStationsMutex.Unlock()
	})

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

package bluetooth

import (
	"errors"
	"testing"
)

func TestReadPowerStatePreservesConnectionOnChannelFailure(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x0B}}
	channelErr := errors.New("channel unavailable")
	channel := &fakeCharacteristic{readErr: channelErr}
	station := connectedFakeStation(power, channel, nil, Capabilities{PowerRead: true, ChannelRead: true})
	err := ReadPowerState(station)
	var readErr *StatusReadError
	if !errors.As(err, &readErr) || readErr.Power != nil || !errors.Is(readErr.Channel, channelErr) {
		t.Fatalf("ReadPowerState() error = %#v, want channel-only StatusReadError", err)
	}
	if !station.IsConnected() || station.PowerState != PowerStateOn {
		t.Fatalf("channel error discarded healthy power connection: %+v", station.Snapshot())
	}
}
func TestInvalidateAllConnectionsDropsCachedHandles(t *testing.T) {
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x0B}},
		&fakeCharacteristic{value: []byte{3}},
		&fakeCharacteristic{},
		Capabilities{PowerRead: true, ChannelRead: true, Identify: true},
	)
	device := &trackingConnectedDevice{}
	station.mutex.Lock()
	station.device = device
	station.mutex.Unlock()
	connectedStationsMutex.Lock()
	previous := connectedStations
	connectedStations = []*BaseStation{station}
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		connectedStations = previous
		connectedStationsMutex.Unlock()
	})
	if err := InvalidateAllConnections(); err != nil {
		t.Fatalf("InvalidateAllConnections() error = %v", err)
	}
	snapshot := station.Snapshot()
	if snapshot.Connected || !snapshot.LastPowerReadAt.IsZero() || !snapshot.LastChannelReadAt.IsZero() {
		t.Fatalf("connection was not invalidated: %+v", snapshot)
	}
	connectedStationsMutex.Lock()
	count := len(connectedStations)
	connectedStationsMutex.Unlock()
	if count != 0 {
		t.Fatalf("tracked connection count = %d, want 0", count)
	}
	if !device.disconnected {
		t.Fatal("detached device was not cleaned up")
	}
}
func TestInvalidateAllConnectionsRetainsFailedCleanupForRetry(t *testing.T) {
	cleanupErr := errors.New("temporary global cleanup failure")
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x0B}},
		nil,
		nil,
		Capabilities{PowerRead: true},
	)
	device := &trackingConnectedDevice{disconnectErr: cleanupErr}
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
	if err := InvalidateAllConnections(); !errors.Is(err, cleanupErr) {
		t.Fatalf("InvalidateAllConnections() error = %v, want %v", err, cleanupErr)
	}
	if station.device != nil || station.pendingCleanup == nil {
		t.Fatal("global invalidation discarded failed cleanup ownership")
	}
	device.disconnectErr = nil
	if err := DisconnectStation(station); err != nil {
		t.Fatalf("DisconnectStation() cleanup retry error = %v", err)
	}
	if station.pendingCleanup != nil || device.disconnects != 2 {
		t.Fatalf("cleanup retry state: pending=%v disconnects=%d", station.pendingCleanup, device.disconnects)
	}
}
func TestInvalidateAllConnectionsRetriesPendingCleanupStations(t *testing.T) {
	station := connectedFakeStation(nil, nil, nil, Capabilities{})
	device := &trackingConnectedDevice{}
	station.device = nil
	station.isConnected = false
	station.pendingCleanup = device
	connectedStationsMutex.Lock()
	previousConnected := connectedStations
	previousPending := pendingCleanupStations
	connectedStations = nil
	pendingCleanupStations = []*BaseStation{station}
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		connectedStations = previousConnected
		pendingCleanupStations = previousPending
		connectedStationsMutex.Unlock()
	})
	if err := InvalidateAllConnections(); err != nil {
		t.Fatalf("InvalidateAllConnections() error = %v", err)
	}
	if device.disconnects != 1 || station.pendingCleanup != nil {
		t.Fatalf("pending cleanup retry state: disconnects=%d pending=%v", device.disconnects, station.pendingCleanup)
	}
}
func TestReleaseStationForScanRemovesCompletedPendingCleanup(t *testing.T) {
	station := connectedFakeStation(nil, nil, nil, Capabilities{})
	device := &trackingConnectedDevice{}
	station.device = nil
	station.isConnected = false
	station.pendingCleanup = device
	connectedStationsMutex.Lock()
	previousPending := pendingCleanupStations
	pendingCleanupStations = []*BaseStation{station}
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		pendingCleanupStations = previousPending
		connectedStationsMutex.Unlock()
	})
	if err := ReleaseStationForScan(station); err != nil {
		t.Fatalf("ReleaseStationForScan() error = %v", err)
	}
	connectedStationsMutex.Lock()
	remaining := len(pendingCleanupStations)
	connectedStationsMutex.Unlock()
	if remaining != 0 || station.pendingCleanup != nil {
		t.Fatalf("completed cleanup remained tracked: count=%d pending=%v", remaining, station.pendingCleanup)
	}
}

type errorReportingConnectedDevice struct {
	trackingConnectedDevice
	queryErr error
}

func (device *errorReportingConnectedDevice) Connected() (bool, error) {
	return false, device.queryErr
}

// TestConnectedQueryFailureKeepsCachedSession guards the fast path: a
// transient status-query failure (COM/RPC errors around GetConnectionStatus)
// is not proof the link dropped, so the cached session must survive instead
// of paying a full reconnect and rediscovery on every query blip.
func TestConnectedQueryFailureKeepsCachedSession(t *testing.T) {
	device := &errorReportingConnectedDevice{queryErr: errors.New("transient COM failure")}
	station := connectedFakeStation(&fakeCharacteristic{value: []byte{0x09}}, nil, nil, Capabilities{PowerRead: true})
	station.device = device

	station.mutex.Lock()
	err := connectAndDiscoverInternal(station)
	station.mutex.Unlock()

	if err != nil {
		t.Fatalf("connectAndDiscoverInternalContext() error = %v, want the cached session kept", err)
	}
	if device.disconnected || device.disconnects != 0 {
		t.Fatalf("status-query failure tore down the session: disconnected=%v disconnects=%d", device.disconnected, device.disconnects)
	}
	if station.device == nil || !station.isConnected {
		t.Fatal("status-query failure cleared the cached connection state")
	}
}

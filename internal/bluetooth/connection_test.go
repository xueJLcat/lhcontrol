package bluetooth

import (
	"context"
	"errors"
	"testing"
	"time"
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
	previousPending := pendingCleanupStations
	connectedStations = []*BaseStation{station}
	// Isolate from cleanup registrations left by unrelated failure-path tests:
	// InvalidateAllConnections walks both tracking lists.
	pendingCleanupStations = nil
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		connectedStations = previous
		pendingCleanupStations = previousPending
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
	previousPending := pendingCleanupStations
	connectedStations = []*BaseStation{station}
	pendingCleanupStations = nil
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		connectedStations = previous
		pendingCleanupStations = previousPending
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

// TestFailedDisconnectRegistersPendingCleanupForFleetRetry guards the tracking
// invariant: a Disconnect that fails while the station has already been pruned
// from the connected list (for example by an interleaved disconnect during the
// unlocked cleanup window) must still be registered for fleet-wide cleanup.
// Otherwise its WinRT handles leak until process exit.
func TestFailedDisconnectRegistersPendingCleanupForFleetRetry(t *testing.T) {
	cleanupErr := errors.New("temporary disconnect failure")
	device := &trackingConnectedDevice{disconnectErr: cleanupErr}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.device = device
	connectedStationsMutex.Lock()
	previousConnected := connectedStations
	previousPending := pendingCleanupStations
	connectedStations = []*BaseStation{station}
	pendingCleanupStations = nil
	connectedStationsMutex.Unlock()
	t.Cleanup(func() {
		connectedStationsMutex.Lock()
		connectedStations = previousConnected
		pendingCleanupStations = previousPending
		connectedStationsMutex.Unlock()
	})

	if err := DisconnectStation(station); !errors.Is(err, cleanupErr) {
		t.Fatalf("DisconnectStation() error = %v, want %v", err, cleanupErr)
	}
	connectedStationsMutex.Lock()
	tracked := false
	for _, pending := range pendingCleanupStations {
		if pending == station {
			tracked = true
		}
	}
	connectedStationsMutex.Unlock()
	if !tracked {
		t.Fatal("failed disconnect was not registered for fleet-wide cleanup retry")
	}

	device.disconnectErr = nil
	if err := DisconnectStation(station); err != nil {
		t.Fatalf("DisconnectStation() retry error = %v", err)
	}
	connectedStationsMutex.Lock()
	pendingCount := len(pendingCleanupStations)
	connectedCount := len(connectedStations)
	connectedStationsMutex.Unlock()
	if pendingCount != 0 || connectedCount != 0 {
		t.Fatalf("completed cleanup left stale tracking entries: pending=%d connected=%d", pendingCount, connectedCount)
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

// reconnectOnDisconnectDevice completes a fake concurrent reconnect for the
// station while a teardown has released the station lock, emulating a second
// operation that finishes connecting inside one of the unlocked windows.
type reconnectOnDisconnectDevice struct {
	trackingConnectedDevice
	station *BaseStation
}

func (device *reconnectOnDisconnectDevice) Disconnect() error {
	device.station.mutex.Lock()
	device.station.device = fakeConnectedDevice{}
	device.station.isConnected = true
	device.station.characteristic = &fakeCharacteristic{value: []byte{0x0B}}
	device.station.Capabilities = Capabilities{PowerRead: true}
	device.station.CapabilitiesKnown = true
	device.station.mutex.Unlock()
	device.trackingConnectedDevice.Disconnect()
	return nil
}

// TestDiscoveryRetryReusesSessionCompletedInsideUnlockedWindow guards the
// discovery-retry path: the retry releases the station lock around the stale
// teardown, the retry delay, and the gate, and a concurrent operation can
// complete a full reconnect inside those windows. The retry must re-check the
// connection state afterwards and reuse that session instead of opening a
// duplicate GATT session that overwrites the fresh one and orphans its WinRT
// objects until process exit.
func TestDiscoveryRetryReusesSessionCompletedInsideUnlockedWindow(t *testing.T) {
	originalAdapter := adapter
	adapter = newFakeBLEAdapter()
	t.Cleanup(func() { adapter = originalAdapter })
	ConfigureTiming(TimingPolicy{DiscoveryRetryDelay: time.Millisecond})
	t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })

	station := connectedFakeStation(nil, nil, nil, Capabilities{})
	station.device = &reconnectOnDisconnectDevice{station: station}
	station.characteristic = nil
	station.CapabilitiesKnown = false

	station.mutex.Lock()
	err := connectAndDiscoverInternalContext(context.Background(), station)
	station.mutex.Unlock()

	if err != nil {
		t.Fatalf("connectAndDiscoverInternalContext() error = %v, want the concurrent session reused", err)
	}
	if station.device == nil || !station.isConnected || station.characteristic == nil {
		t.Fatal("session completed inside the retry window was not adopted")
	}
}

// TestConnectAndDiscoverMarksPreCancelledRequestNotStarted guards the narrow
// window where the context expires after the caller's pre-lock check but
// before discovery starts: the request must be reported as never started so
// callers keep the healthy session instead of tearing it down as cancelled
// discovery would.
func TestConnectAndDiscoverMarksPreCancelledRequestNotStarted(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{value: []byte{0x09}}, nil, nil, Capabilities{PowerRead: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	station.mutex.Lock()
	err := connectAndDiscoverInternalContext(ctx, station)
	station.mutex.Unlock()

	if !isConnectNotStarted(err) {
		t.Fatalf("connectAndDiscoverInternalContext() error = %v, want never-started cancellation", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("never-started error = %v, want it to still match context.Canceled", err)
	}
	if station.device == nil || !station.isConnected {
		t.Fatal("never-started cancellation modified the cached connection")
	}
}

// TestConnectGateCancellationMarkedNotStarted guards the gate wait: the
// connect gate releases the station lock while waiting for an in-flight
// disconnect, so a deadline landing there means the request never touched
// connection state (and a concurrent operation may even have rebuilt the
// session). The error must stay marked never-started so callers keep the
// session instead of tearing it down as cancelled discovery.
func TestConnectGateCancellationMarkedNotStarted(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{value: []byte{0x09}}, nil, nil, Capabilities{PowerRead: true})
	station.mutex.Lock()
	station.disconnectInFlight = make(chan struct{})
	station.mutex.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	station.mutex.Lock()
	err := connectAndDiscoverInternalContext(ctx, station)
	station.mutex.Unlock()

	if !isConnectNotStarted(err) {
		t.Fatalf("connectAndDiscoverInternalContext() error = %v, want never-started deadline", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("never-started error = %v, want it to still match context.DeadlineExceeded", err)
	}
	if station.device == nil || !station.isConnected {
		t.Fatal("gate cancellation modified the cached connection")
	}
}

// TestTrySnapshotSkipsLockedStation pins the best-effort projection primitive:
// a worker wedged inside a transport call holds the station's write lock
// indefinitely, and degraded paths must be able to skip that station instead
// of queueing behind it. Snapshot keeps its blocking contract.
func TestTrySnapshotSkipsLockedStation(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{value: []byte{0x0B}}, nil, nil, Capabilities{PowerRead: true})
	station.mutex.Lock()
	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		defer station.mutex.Unlock()
		close(locked)
		<-release
	}()
	<-locked

	if _, ok := station.TrySnapshot(); ok {
		t.Fatal("TrySnapshot() succeeded while the station lock is held")
	}
	snapshotDone := make(chan struct{})
	go func() {
		defer close(snapshotDone)
		station.Snapshot()
	}()
	select {
	case <-snapshotDone:
		t.Fatal("Snapshot() did not block behind the held station lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-snapshotDone

	snapshot, ok := station.TrySnapshot()
	if !ok {
		t.Fatal("TrySnapshot() failed after the station lock was released")
	}
	if snapshot.Address == "" || !snapshot.Connected {
		t.Fatalf("TrySnapshot() = %+v, want the same projection Snapshot returns", snapshot)
	}
}

// TestConnectFailurePreservesReadTimestamps guards the observation
// preservation contract: a failed connect does not invalidate values read
// from an earlier session, so the read timestamps must survive. Wiping them
// disables the cached-state duplicate-command suppression for a station that
// is merely unreachable right now, and contradicts the interrupted-read and
// discovery-retry paths that deliberately keep them.
func TestConnectFailurePreservesReadTimestamps(t *testing.T) {
	originalAdapter := adapter
	adapter = &reconnectCountingAdapter{connectErr: errors.New("radio unavailable")}
	t.Cleanup(func() { adapter = originalAdapter })

	powerReadAt := time.Now()
	channelReadAt := time.Now()
	station := &BaseStation{
		Name:              "LHB-TIMESTAMPS",
		PowerState:        PowerStateOn,
		RawPowerState:     0x0B,
		Channel:           4,
		LastPowerReadAt:   powerReadAt,
		LastChannelReadAt: channelReadAt,
	}

	station.mutex.Lock()
	err := connectAndDiscoverInternalContext(context.Background(), station)
	station.mutex.Unlock()

	if err == nil {
		t.Fatal("connect unexpectedly succeeded")
	}
	snapshot := station.Snapshot()
	if !snapshot.LastPowerReadAt.Equal(powerReadAt) || !snapshot.LastChannelReadAt.Equal(channelReadAt) {
		t.Fatalf("failed connect wiped read timestamps: power=%v channel=%v", snapshot.LastPowerReadAt, snapshot.LastChannelReadAt)
	}
	if snapshot.PowerState != PowerStateOn || snapshot.Channel != 4 {
		t.Fatalf("failed connect clobbered cached observations: %+v", snapshot)
	}
}

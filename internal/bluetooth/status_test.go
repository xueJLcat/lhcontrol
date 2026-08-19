package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	tinybluetooth "tinygo.org/x/bluetooth"
)

type cancelAfterSuccessfulRead struct {
	*fakeCharacteristic
	cancel context.CancelFunc
}

type observeFirstContextCheck struct {
	context.Context
	checked chan struct{}
	once    atomic.Bool
}

func (c *observeFirstContextCheck) Err() error {
	err := c.Context.Err()
	if err == nil && c.once.CompareAndSwap(false, true) {
		close(c.checked)
	}
	return err
}

func (f *cancelAfterSuccessfulRead) ReadContext(_ context.Context, destination []byte) (int, error) {
	n, err := f.fakeCharacteristic.Read(destination)
	f.cancel()
	return n, err
}

// TestReadPowerStateDisconnectedPreflightIsClassifiable guards the preflight
// contract: a read that never reaches the transport must carry the typed
// not-connected error so scheduling layers can treat it as a disconnect
// observation instead of a transport failure with backoff.
func TestReadPowerStateDisconnectedPreflightIsClassifiable(t *testing.T) {
	station := &BaseStation{Name: "LHB-PREFLIGHT"}
	err := ReadPowerStateContext(context.Background(), station)
	if !IsStationNotConnected(err) {
		t.Fatalf("ReadPowerStateContext() error = %v, want a StationNotConnectedError", err)
	}
	if RequiresReconnect(err) || IsAdapterUnavailable(err) {
		t.Fatalf("preflight error must not classify as transport failure: %v", err)
	}
}

func TestReadMetadataValueFailureIsIsolated(t *testing.T) {
	good := &fakeCharacteristic{value: []byte(" Valve Corp. \x00\r\n")}
	value, err := readMetadataValue(good)
	if err != nil || value != "Valve Corp." {
		t.Fatalf("readMetadataValue() = %q, %v", value, err)
	}
	bad := &fakeCharacteristic{readErr: errors.New("field unavailable")}
	if _, err := readMetadataValue(bad); err == nil {
		t.Fatal("readMetadataValue() unexpectedly succeeded")
	}
}
func TestReleaseStationForScanPreservesLastKnownState(t *testing.T) {
	station := &BaseStation{
		Name:            "LHB-TEST",
		PowerState:      PowerStateOn,
		RawPowerState:   0x09,
		Channel:         4,
		LastStateUpdate: time.Now(),
		characteristic:  &fakeCharacteristic{},
	}
	lastUpdate := station.LastStateUpdate
	ReleaseStationForScan(station)
	if station.PowerState != PowerStateOn || station.RawPowerState != 0x09 || station.Channel != 4 {
		t.Fatalf("last known state was not preserved: %+v", station.Snapshot())
	}
	if !station.LastStateUpdate.Equal(lastUpdate) {
		t.Fatalf("last update changed from %v to %v", lastUpdate, station.LastStateUpdate)
	}
	if station.characteristic != nil {
		t.Fatal("GATT characteristic was not released")
	}
}
func TestScanCompletionErrorRejectsEarlyFailure(t *testing.T) {
	adapterErr := errors.New("adapter stopped unexpectedly")
	if err := scanCompletionError(adapterErr); !errors.Is(err, adapterErr) {
		t.Fatalf("scanCompletionError() = %v, want wrapped adapter error", err)
	}
	if err := scanCompletionError(nil); err != nil {
		t.Fatalf("nil scan error should be successful, got %v", err)
	}
}
func TestScanCompletionErrorExplainsUnavailableRadio(t *testing.T) {
	err := scanCompletionError(tinybluetooth.ErrRadioNotAvailable)
	if !errors.Is(err, tinybluetooth.ErrRadioNotAvailable) {
		t.Fatalf("scanCompletionError() = %v", err)
	}
	if !strings.Contains(err.Error(), "turn on Bluetooth") {
		t.Fatalf("scanCompletionError() is not actionable: %v", err)
	}
}
func TestFetchInitialPowerStateReportsPartialReadErrors(t *testing.T) {
	powerErr := errors.New("power read failed")
	channelErr := errors.New("channel read failed")
	metadataErr := errors.New("metadata read failed")
	station := connectedFakeStation(
		&fakeCharacteristic{readErr: powerErr},
		&fakeCharacteristic{readErr: channelErr},
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)
	station.PowerState = PowerStateOn
	station.RawPowerState = 0x09
	station.Channel = 4
	station.LastPowerReadAt = time.Now()
	station.LastChannelReadAt = time.Now()
	station.setMetadataErrorInternal(metadataErr)
	err := FetchInitialPowerState(station)
	var initialErr *InitialReadError
	if !errors.As(err, &initialErr) {
		t.Fatalf("FetchInitialPowerState() error = %v, want InitialReadError", err)
	}
	if !errors.Is(err, powerErr) || !errors.Is(err, channelErr) || !errors.Is(err, metadataErr) {
		t.Fatalf("InitialReadError does not preserve underlying errors: %v", err)
	}
	if station.PowerState != PowerStateOn || station.RawPowerState != 0x09 || station.Channel != 4 ||
		!station.LastPowerReadAt.IsZero() || !station.LastChannelReadAt.IsZero() {
		t.Fatalf("failed reads did not preserve stale values: %+v", station.Snapshot())
	}
}
func TestFetchInitialPowerStateContextCancelsReadAndCleansUpConnection(t *testing.T) {
	power := &blockingContextCharacteristic{
		fakeCharacteristic: &fakeCharacteristic{},
		started:            make(chan struct{}),
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	device := &trackingConnectedDevice{}
	station.device = device
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- FetchInitialPowerStateContext(ctx, station)
	}()
	select {
	case <-power.started:
	case <-time.After(time.Second):
		t.Fatal("context-aware power read did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FetchInitialPowerStateContext() error = %v, want context.Canceled", err)
		}
		var initialErr *InitialReadError
		if errors.As(err, &initialErr) {
			t.Fatalf("FetchInitialPowerStateContext() error = %#v, want a clean cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled initial read did not return within 3 seconds")
	}
	if device.disconnects != 1 || station.Snapshot().Connected {
		t.Fatalf("cancelled read cleanup: disconnects=%d connected=%v", device.disconnects, station.Snapshot().Connected)
	}
}

// TestFetchInitialPowerStateContextKeepsFailedPowerReadUnderCancellation
// guards the initial-read contract: a power read that ran and failed just
// before the context expired must not fold into a bare cancellation.
// Upstream treats pure context errors as clean interruptions; dropping the
// transport failure would lose its disconnect/backoff bookkeeping and leave
// the station retrying without any recorded failure.
func TestFetchInitialPowerStateContextKeepsFailedPowerReadUnderCancellation(t *testing.T) {
	terminalErr := errors.New("WinRT operation ended with canceled status")
	power := &blockingContextCharacteristic{
		fakeCharacteristic: &fakeCharacteristic{},
		started:            make(chan struct{}),
		terminalErr:        terminalErr,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, ChannelRead: true})
	device := &trackingConnectedDevice{}
	station.device = device
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- FetchInitialPowerStateContext(ctx, station) }()
	select {
	case <-power.started:
	case <-time.After(time.Second):
		t.Fatal("context-aware power read did not start")
	}
	cancel()
	select {
	case err := <-result:
		var initialErr *InitialReadError
		if !errors.As(err, &initialErr) {
			t.Fatalf("FetchInitialPowerStateContext() error = %v, want an InitialReadError", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FetchInitialPowerStateContext() error = %v, want the cancellation preserved", err)
		}
		if !errors.Is(err, terminalErr) {
			t.Fatalf("FetchInitialPowerStateContext() error = %v, want the transport failure preserved", err)
		}
		if initialErr.Power == nil || !errors.Is(initialErr.Channel, context.Canceled) {
			t.Fatalf("InitialReadError fields = power:%v channel:%v, want the power failure and the interrupted channel", initialErr.Power, initialErr.Channel)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled initial read did not return within 3 seconds")
	}
	if device.disconnects != 1 || station.Snapshot().Connected {
		t.Fatalf("interrupted initial read cleanup: disconnects=%d connected=%v", device.disconnects, station.Snapshot().Connected)
	}
}

// TestFetchInitialPowerStateContextKeepsCleanupFailureOutOfChannelField
// guards the interrupted-read cleanup attribution: when the cleanup disconnect
// fails after an interrupted read, the fault must not land in the Channel
// domain (the channel was never read — upstream would note a channel failure
// for an observation that never happened). The top-level error still carries
// the cleanup fault so observers can see it.
func TestFetchInitialPowerStateContextKeepsCleanupFailureOutOfChannelField(t *testing.T) {
	terminalErr := errors.New("WinRT operation ended with canceled status")
	cleanupErr := errors.New("cleanup disconnect failed")
	power := &blockingContextCharacteristic{
		fakeCharacteristic: &fakeCharacteristic{},
		started:            make(chan struct{}),
		terminalErr:        terminalErr,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, ChannelRead: true})
	device := &trackingConnectedDevice{disconnectErr: cleanupErr}
	station.device = device
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- FetchInitialPowerStateContext(ctx, station) }()
	select {
	case <-power.started:
	case <-time.After(time.Second):
		t.Fatal("context-aware power read did not start")
	}
	cancel()
	select {
	case err := <-result:
		var initialErr *InitialReadError
		if !errors.As(err, &initialErr) {
			t.Fatalf("FetchInitialPowerStateContext() error = %v, want an InitialReadError", err)
		}
		if !errors.Is(initialErr.Channel, context.Canceled) || errors.Is(initialErr.Channel, cleanupErr) {
			t.Fatalf("InitialReadError.Channel = %v, want the bare interruption without the cleanup fault", initialErr.Channel)
		}
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("FetchInitialPowerStateContext() error = %v, want the cleanup failure surfaced at the top level", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled initial read did not return within 3 seconds")
	}
	if device.disconnects != 1 || station.Snapshot().Connected {
		t.Fatalf("interrupted initial read cleanup: disconnects=%d connected=%v", device.disconnects, station.Snapshot().Connected)
	}
}

func TestFetchInitialPowerStateContextPreservesPowerReadWhenCancelledBetweenFields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	power := &cancelAfterSuccessfulRead{
		fakeCharacteristic: &fakeCharacteristic{value: []byte{0x0B}},
		cancel:             cancel,
	}
	station := connectedFakeStation(
		power,
		&fakeCharacteristic{value: []byte{3}},
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)
	device := &trackingConnectedDevice{}
	station.device = device

	err := FetchInitialPowerStateContext(ctx, station)
	var initialErr *InitialReadError
	if !errors.As(err, &initialErr) || initialErr.Power != nil || !errors.Is(initialErr.Channel, context.Canceled) {
		t.Fatalf("FetchInitialPowerStateContext() error = %#v, want channel-only cancellation", err)
	}
	snapshot := station.Snapshot()
	if snapshot.PowerState != PowerStateOn || snapshot.RawPowerState != 0x0B || snapshot.LastPowerReadAt.IsZero() {
		t.Fatalf("completed power read was discarded: %+v", snapshot)
	}
	if !snapshot.Connected || device.disconnects != 0 {
		t.Fatalf("between-field cancellation disconnected a completed read: connected=%v disconnects=%d", snapshot.Connected, device.disconnects)
	}
}

func TestFetchInitialPowerStateContextCleansUpCancelledChannelAndPreservesPower(t *testing.T) {
	channel := &blockingContextCharacteristic{
		fakeCharacteristic: &fakeCharacteristic{},
		started:            make(chan struct{}),
	}
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x0B}},
		channel,
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)
	device := &trackingConnectedDevice{}
	station.device = device
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- FetchInitialPowerStateContext(ctx, station) }()
	select {
	case <-channel.started:
	case <-time.After(time.Second):
		t.Fatal("context-aware channel read did not start")
	}
	cancel()
	err := <-result
	var initialErr *InitialReadError
	if !errors.As(err, &initialErr) || initialErr.Power != nil || !errors.Is(initialErr.Channel, context.Canceled) {
		t.Fatalf("FetchInitialPowerStateContext() error = %#v, want channel-only cancellation", err)
	}
	snapshot := station.Snapshot()
	if snapshot.PowerState != PowerStateOn || snapshot.RawPowerState != 0x0B || snapshot.LastPowerReadAt.IsZero() {
		t.Fatalf("completed power read was discarded during cleanup: %+v", snapshot)
	}
	if snapshot.Connected || device.disconnects != 1 {
		t.Fatalf("cancelled channel read cleanup: connected=%v disconnects=%d", snapshot.Connected, device.disconnects)
	}
}

func TestReadPowerStateContextCancelsBlockingRead(t *testing.T) {
	power := &blockingContextCharacteristic{
		fakeCharacteristic: &fakeCharacteristic{},
		started:            make(chan struct{}),
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- ReadPowerStateContext(ctx, station)
	}()
	select {
	case <-power.started:
	case <-time.After(time.Second):
		t.Fatal("context-aware status read did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadPowerStateContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled status read did not return")
	}
}
func TestReadPowerStateContextPreservesCancellationWhenTransportReturnsTerminalError(t *testing.T) {
	power := &blockingContextCharacteristic{
		fakeCharacteristic: &fakeCharacteristic{},
		started:            make(chan struct{}),
		terminalErr:        errors.New("WinRT operation ended with canceled status"),
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	station.mutex.Lock()
	station.setChannelErrorInternal(errors.New("stale channel error"))
	station.mutex.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- ReadPowerStateContext(ctx, station)
	}()
	<-power.started
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadPowerStateContext() error = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), power.terminalErr.Error()) {
		t.Fatalf("ReadPowerStateContext() error = %v, want terminal transport detail", err)
	}
	if snapshot := station.Snapshot(); strings.Contains(snapshot.LastError, "stale channel error") {
		t.Fatalf("cancelled power read retained an unrelated channel error: %q", snapshot.LastError)
	}
}

func TestReadPowerStateContextAcceptsCancellationAfterLastPowerRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	power := &cancelAfterSuccessfulRead{
		fakeCharacteristic: &fakeCharacteristic{value: []byte{0x0B}},
		cancel:             cancel,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	station.mutex.Lock()
	station.setPowerErrorInternal(errors.New("stale power error"))
	station.setChannelErrorInternal(errors.New("stale channel error"))
	station.mutex.Unlock()

	if err := ReadPowerStateContext(ctx, station); err != nil {
		t.Fatalf("ReadPowerStateContext() error = %v, want completed status read", err)
	}
	snapshot := station.Snapshot()
	if snapshot.PowerState != PowerStateOn || snapshot.RawPowerState != 0x0B || snapshot.LastPowerReadAt.IsZero() {
		t.Fatalf("completed power read was discarded: %+v", snapshot)
	}
	if snapshot.LastError != "" {
		t.Fatalf("completed status read retained stale errors: %q", snapshot.LastError)
	}
}

func TestReadPowerStateContextAcceptsCancellationAfterCompletedChannelRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	channel := &cancelAfterSuccessfulRead{
		fakeCharacteristic: &fakeCharacteristic{value: []byte{0x03}},
		cancel:             cancel,
	}
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x0B}},
		channel,
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)

	if err := ReadPowerStateContext(ctx, station); err != nil {
		t.Fatalf("ReadPowerStateContext() error = %v, want completed status read", err)
	}
	snapshot := station.Snapshot()
	if snapshot.PowerState != PowerStateOn || snapshot.Channel != 3 ||
		snapshot.LastPowerReadAt.IsZero() || snapshot.LastChannelReadAt.IsZero() {
		t.Fatalf("completed power/channel read was discarded: %+v", snapshot)
	}
}

func TestReadPowerStateContextReplacesStalePowerErrorWhenCancelledBetweenFields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	power := &cancelAfterSuccessfulRead{
		fakeCharacteristic: &fakeCharacteristic{value: []byte{0x0B}},
		cancel:             cancel,
	}
	station := connectedFakeStation(
		power,
		&fakeCharacteristic{value: []byte{0x03}},
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)
	station.mutex.Lock()
	station.setPowerErrorInternal(errors.New("stale power error"))
	station.mutex.Unlock()

	err := ReadPowerStateContext(ctx, station)
	var readErr *StatusReadError
	if !errors.As(err, &readErr) || readErr.Power != nil || !errors.Is(readErr.Channel, context.Canceled) {
		t.Fatalf("ReadPowerStateContext() error = %#v, want channel-only cancellation", err)
	}
	snapshot := station.Snapshot()
	if strings.Contains(snapshot.LastError, "stale power error") || !strings.Contains(snapshot.LastError, "channel: context canceled") {
		t.Fatalf("status errors after completed power read = %q, want current channel cancellation only", snapshot.LastError)
	}
}

func TestReadPowerStateContextDoesNotMutateWhenCancelledWaitingForStation(t *testing.T) {
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x0B}},
		nil,
		nil,
		Capabilities{PowerRead: true},
	)
	freshRead := time.Now()
	station.PowerState = PowerStateOn
	station.RawPowerState = 0x0B
	station.LastPowerReadAt = freshRead
	station.setPowerErrorInternal(errors.New("previous observation warning"))

	baseContext, cancel := context.WithCancel(context.Background())
	ctx := &observeFirstContextCheck{Context: baseContext, checked: make(chan struct{})}
	station.mutex.Lock()
	locked := true
	defer func() {
		if locked {
			station.mutex.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() { result <- ReadPowerStateContext(ctx, station) }()
	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("status read did not reach its initial context check")
	}
	cancel()
	station.mutex.Unlock()
	locked = false

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadPowerStateContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled status read did not return after the station lock was released")
	}
	snapshot := station.Snapshot()
	if !snapshot.LastPowerReadAt.Equal(freshRead) || snapshot.PowerState != PowerStateOn || snapshot.RawPowerState != 0x0B {
		t.Fatalf("pre-operation cancellation changed the last observation: %+v", snapshot)
	}
	if snapshot.LastError != "power: previous observation warning" {
		t.Fatalf("pre-operation cancellation replaced prior error state: %q", snapshot.LastError)
	}
}

func TestEnsureCapabilitiesUsesConnectedDiscovery(t *testing.T) {
	station := connectedFakeStation(
		&fakeCharacteristic{},
		nil,
		nil,
		Capabilities{PowerWrite: true},
	)
	capabilities, err := EnsureCapabilities(station)
	if err != nil {
		t.Fatalf("EnsureCapabilities() error = %v", err)
	}
	if !capabilities.PowerWrite {
		t.Fatalf("EnsureCapabilities() = %+v, want powerWrite", capabilities)
	}
}
func TestEnsureCapabilitiesContextCleansUpCancelledDiscovery(t *testing.T) {
	device := &blockingDiscoveryDevice{started: make(chan struct{})}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{})
	station.device = device
	station.characteristic = nil
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := EnsureCapabilitiesContext(ctx, station)
		result <- err
	}()
	<-device.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("EnsureCapabilitiesContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled capability discovery did not return")
	}
	if device.disconnects != 1 || station.Snapshot().Connected {
		t.Fatalf("cancelled discovery cleanup: disconnects=%d connected=%v", device.disconnects, station.Snapshot().Connected)
	}
}
func TestRefreshCapabilitiesContextDoesNotDisconnectForPreCancelledContext(t *testing.T) {
	device := &trackingConnectedDevice{}
	station := connectedFakeStation(
		&fakeCharacteristic{},
		nil,
		nil,
		Capabilities{PowerWrite: true},
	)
	station.device = device
	station.CapabilitiesKnown = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RefreshCapabilitiesContext(ctx, station)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshCapabilitiesContext() error = %v, want context.Canceled", err)
	}
	snapshot := station.Snapshot()
	if device.disconnects != 0 || !snapshot.Connected {
		t.Fatalf("pre-cancelled refresh changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, snapshot)
	}
	if !snapshot.CapabilitiesKnown || !snapshot.Capabilities.PowerWrite {
		t.Fatalf("pre-cancelled refresh invalidated known capabilities: %+v", snapshot)
	}
}

func TestEnsureCapabilitiesContextDoesNotDisconnectForPreCancelledContext(t *testing.T) {
	device := &trackingConnectedDevice{}
	station := connectedFakeStation(
		&fakeCharacteristic{},
		nil,
		nil,
		Capabilities{PowerRead: true},
	)
	station.device = device
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := EnsureCapabilitiesContext(ctx, station)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureCapabilitiesContext() error = %v, want context.Canceled", err)
	}
	if device.disconnects != 0 || !station.Snapshot().Connected {
		t.Fatalf("pre-cancelled capability check changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, station.Snapshot())
	}
}

func TestFetchInitialPowerStateContextDoesNotDisconnectForPreCancelledContext(t *testing.T) {
	device := &trackingConnectedDevice{}
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x0B}},
		nil,
		nil,
		Capabilities{PowerRead: true},
	)
	station.device = device
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := FetchInitialPowerStateContext(ctx, station)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchInitialPowerStateContext() error = %v, want context.Canceled", err)
	}
	if device.disconnects != 0 || !station.Snapshot().Connected {
		t.Fatalf("pre-cancelled initial read changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, station.Snapshot())
	}
}

func TestCapabilityReadsDoNotDisconnectWhenCancelledWaitingForStation(t *testing.T) {
	operations := map[string]func(context.Context, *BaseStation) error{
		"ensure capabilities": func(ctx context.Context, station *BaseStation) error {
			_, err := EnsureCapabilitiesContext(ctx, station)
			return err
		},
		"initial state": FetchInitialPowerStateContext,
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			device := &trackingConnectedDevice{}
			station := connectedFakeStation(
				&fakeCharacteristic{value: []byte{0x0B}},
				nil,
				nil,
				Capabilities{PowerRead: true},
			)
			station.device = device
			baseContext, cancel := context.WithCancel(context.Background())
			ctx := &observeFirstContextCheck{Context: baseContext, checked: make(chan struct{})}
			station.mutex.Lock()
			locked := true
			defer func() {
				if locked {
					station.mutex.Unlock()
				}
			}()
			result := make(chan error, 1)
			go func() { result <- operation(ctx, station) }()
			select {
			case <-ctx.checked:
			case <-time.After(time.Second):
				t.Fatal("operation did not reach its initial context check")
			}
			cancel()
			station.mutex.Unlock()
			locked = false

			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("operation error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled operation did not return after the station lock was released")
			}
			if device.disconnects != 0 || !station.Snapshot().Connected {
				t.Fatalf("waiting cancellation changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, station.Snapshot())
			}
		})
	}
}

func TestRefreshCapabilitiesContextDoesNotDisconnectWhenCancelledWaitingForStation(t *testing.T) {
	device := &trackingConnectedDevice{}
	station := connectedFakeStation(
		&fakeCharacteristic{},
		nil,
		nil,
		Capabilities{PowerWrite: true},
	)
	station.device = device
	station.CapabilitiesKnown = true
	baseContext, cancel := context.WithCancel(context.Background())
	ctx := &observeFirstContextCheck{Context: baseContext, checked: make(chan struct{})}
	station.mutex.Lock()
	locked := true
	defer func() {
		if locked {
			station.mutex.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() {
		_, err := RefreshCapabilitiesContext(ctx, station)
		result <- err
	}()
	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("capability refresh did not reach its initial context check")
	}
	cancel()
	station.mutex.Unlock()
	locked = false

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RefreshCapabilitiesContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled capability refresh did not return after the station lock was released")
	}
	snapshot := station.Snapshot()
	if device.disconnects != 0 || !snapshot.Connected || !snapshot.CapabilitiesKnown {
		t.Fatalf("waiting cancellation changed capability state: disconnects=%d snapshot=%+v", device.disconnects, snapshot)
	}
}
func TestStrictPowerConfirmationDoesNotAcceptTransitionalRawState(t *testing.T) {
	if IsPowerStateConfirmed(PowerStateOn, 0x01) {
		t.Fatal("transitional 0x01 must not strictly confirm On")
	}
	for _, raw := range []int{0x09, 0x0B} {
		if !IsPowerStateConfirmed(PowerStateOn, raw) {
			t.Fatalf("raw %#x should strictly confirm On", raw)
		}
	}
	if !IsPowerStateConfirmed(PowerStateStandby, 0x02) {
		t.Fatal("0x02 should strictly confirm Standby")
	}
	if !IsPowerStateConfirmed(PowerStateSleep, 0x00) {
		t.Fatal("0x00 should strictly confirm Sleep")
	}
}
func TestPowerStateVerifiedFollowsCompatibilityDecode(t *testing.T) {
	for _, test := range []struct {
		decoded PowerState
		raw     int
		want    bool
	}{
		{PowerStateOn, 0x09, true},
		{PowerStateOn, 0x0B, true},
		{PowerStateOn, 0x01, true},
		{PowerStateOn, 0x08, true},
		{PowerStateOn, 0x00, false},
		{PowerStateSleep, 0x00, true},
		{PowerStateSleep, 0x01, false},
		{PowerStateStandby, 0x02, true},
		{PowerStateStandby, 0x01, false},
		{PowerStateBooting, 0x01, false},
		{PowerStateUnknown, 0x01, false},
	} {
		if got := IsPowerStateVerified(test.decoded, test.raw); got != test.want {
			t.Errorf("IsPowerStateVerified(%v, %#x) = %v, want %v", test.decoded, test.raw, got, test.want)
		}
	}
}

// setTestBootFallback drives the boot fallback window through the
// configurable timing policy (using a non-default value so a wiring
// regression that read a hard constant would fail) and restores defaults.
func setTestBootFallback(t *testing.T, window time.Duration) {
	t.Helper()
	ConfigureTiming(TimingPolicy{BootFallbackAfter: window})
	t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })
}

func TestDecodePowerStateWithHistory(t *testing.T) {
	const fallback = 10 * time.Second
	setTestBootFallback(t, fallback)
	now := time.Now()
	if got := decodePowerStateWithHistory(0x01, PowerStateOn, time.Time{}, now); got != PowerStateBooting {
		t.Fatalf("fresh booting raw 0x01 after On decoded as %v, want Booting", got)
	}
	if got := decodePowerStateWithHistory(0x08, PowerStateOn, time.Time{}, now); got != PowerStateBooting {
		t.Fatalf("fresh booting raw 0x08 after On decoded as %v, want Booting", got)
	}
	if got := decodePowerStateWithHistory(0x01, PowerStateSleep, time.Time{}, now); got != PowerStateBooting {
		t.Fatalf("booting raw 0x01 after Sleep decoded as %v, want Booting", got)
	}
	if got := decodePowerStateWithHistory(0x08, PowerStateSleep, time.Time{}, now); got != PowerStateBooting {
		t.Fatalf("booting raw 0x08 after Sleep decoded as %v, want Booting", got)
	}
	if got := decodePowerStateWithHistory(0x01, PowerStateBooting, now.Add(-fallback), now); got != PowerStateOn {
		t.Fatalf("persistent 0x01 after the fallback window decoded as %v, want On", got)
	}
	if got := decodePowerStateWithHistory(0x08, PowerStateBooting, now.Add(-fallback), now); got != PowerStateOn {
		t.Fatalf("persistent 0x08 after the fallback window decoded as %v, want On", got)
	}
	if got := decodePowerStateWithHistory(0x01, PowerStateBooting, now.Add(-time.Second), now); got != PowerStateBooting {
		t.Fatalf("booting raw inside the fallback window decoded as %v, want Booting", got)
	}
}
func TestPowerConfirmationRejectsFreshBootRawWhenAlreadyOn(t *testing.T) {
	// A power command re-arms boot observation. A boot-like read immediately
	// afterwards must therefore remain transitional instead of being accepted
	// solely because the cached state before the command was On.
	power := &fakeCharacteristic{value: []byte{0x01}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateOn
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := SetPowerStateContext(ctx, station, PowerStateOn)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want an unconfirmed boot transition", result, err)
	}
	if station.PowerState != PowerStateBooting || station.RawPowerState != 0x01 {
		t.Fatalf("station state = %v raw %#x, want Booting with raw 0x01", station.PowerState, station.RawPowerState)
	}
}
func TestPowerConfirmationRejectsFresh0x08RawWhenAlreadyOn(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x08}, ignoreWrite: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateOn
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := SetPowerStateContext(ctx, station, PowerStateOn)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want an unconfirmed boot transition", result, err)
	}
	if station.PowerState != PowerStateBooting || station.RawPowerState != 0x08 {
		t.Fatalf("station state = %v raw %#x, want Booting with raw 0x08", station.PowerState, station.RawPowerState)
	}
}
func TestPersistentBootRawFallbackIsStickyAndCompatibilityVerified(t *testing.T) {
	const fallback = 10 * time.Second
	setTestBootFallback(t, fallback)
	power := &fakeCharacteristic{value: []byte{0x01}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	station.PowerState = PowerStateBooting
	station.bootingSince = time.Now().Add(-fallback)
	if err := ReadPowerState(station); err != nil {
		t.Fatalf("first fallback read error = %v", err)
	}
	if station.PowerState != PowerStateOn || !station.bootRawTrustedOn {
		t.Fatalf("first fallback state = %+v, trusted=%v", station.Snapshot(), station.bootRawTrustedOn)
	}
	if err := ReadPowerState(station); err != nil {
		t.Fatalf("sticky fallback read error = %v", err)
	}
	if station.PowerState != PowerStateOn || !station.bootRawTrustedOn {
		t.Fatalf("sticky fallback became unstable: %+v trusted=%v", station.Snapshot(), station.bootRawTrustedOn)
	}
	if !IsPowerStateVerified(station.PowerState, station.RawPowerState) {
		t.Fatalf("sticky compatibility fallback must count as verified for operation decisions: %+v", station.Snapshot())
	}
}

// TestDisconnectResetsBootFallbackWindow guards the connection-scoped boot
// observation window: a disconnect can outlast the fallback window, and the
// first boot-like read after reconnecting must start a fresh window instead
// of fast-forwarding to a trusted On.
func TestDisconnectResetsBootFallbackWindow(t *testing.T) {
	const fallback = 10 * time.Second
	setTestBootFallback(t, fallback)
	power := &fakeCharacteristic{value: []byte{0x01}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	if err := ReadPowerState(station); err != nil {
		t.Fatalf("initial booting read error = %v", err)
	}
	if station.PowerState != PowerStateBooting || station.bootingSince.IsZero() {
		t.Fatalf("state after first read = %v bootingSince=%v, want Booting with an open window", station.PowerState, station.bootingSince)
	}
	station.mutex.Lock()
	err := disconnectInternal(station)
	station.mutex.Unlock()
	if err != nil {
		t.Fatalf("disconnectInternal() error = %v", err)
	}
	if station.PowerState != PowerStateBooting {
		t.Fatalf("disconnect must preserve the cached state for offline display, got %v", station.PowerState)
	}
	if !station.bootingSince.IsZero() || station.bootRawTrustedOn {
		t.Fatalf("disconnect leaked the boot window: bootingSince=%v trusted=%v", station.bootingSince, station.bootRawTrustedOn)
	}
	// Simulate the reconnect: the next read happens long after the old window
	// would have elapsed, yet it must expose Booting and open a fresh window.
	station.mutex.Lock()
	station.device = fakeConnectedDevice{}
	station.characteristic = power
	station.isConnected = true
	station.mutex.Unlock()
	if err := ReadPowerState(station); err != nil {
		t.Fatalf("post-reconnect read error = %v", err)
	}
	if station.PowerState != PowerStateBooting || station.bootRawTrustedOn {
		t.Fatalf("first post-reconnect read = %v trusted=%v, want Booting without trust", station.PowerState, station.bootRawTrustedOn)
	}
	if station.bootingSince.IsZero() {
		t.Fatal("post-reconnect booting read did not open a fresh fallback window")
	}
}

// TestPowerWriteRearmsBootFallbackWindow verifies that a power command resets
// the boot observation window: a stale window must not fast-forward the first
// readback after the write to a confirmed On, because the command itself can
// reboot the station.
func TestPowerWriteRearmsBootFallbackWindow(t *testing.T) {
	const fallback = 10 * time.Second
	setTestBootFallback(t, fallback)
	power := &fakeCharacteristic{value: []byte{0x01}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateBooting
	station.bootingSince = time.Now().Add(-fallback)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := SetPowerStateContext(ctx, station, PowerStateOn)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want an unconfirmed boot transition", result, err)
	}
	if station.PowerState != PowerStateBooting || station.bootRawTrustedOn {
		t.Fatalf("state after re-armed write = %v trusted=%v, want Booting without trust", station.PowerState, station.bootRawTrustedOn)
	}
}

func TestPowerConfirmationKeepsPollingDuringGenuineBoot(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x01}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateSleep
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	station.mutex.Lock()
	err := confirmPowerStateInternalContext(ctx, station, PowerStateOn)
	station.mutex.Unlock()
	if err == nil {
		t.Fatal("confirmation unexpectedly accepted a booting station as On")
	}
	if station.PowerState != PowerStateBooting {
		t.Fatalf("station state = %v, want Booting while raw 0x01 is transitional", station.PowerState)
	}
}

// reconnectCountingAdapter makes Connect fail while counting attempts, so the
// confirmation reconnect path can be exercised deterministically.
type reconnectCountingAdapter struct {
	connectErr   error
	connectCalls atomic.Int32
}

func (a *reconnectCountingAdapter) Enable() error { return nil }
func (a *reconnectCountingAdapter) Connect(addr tinybluetooth.Address, params tinybluetooth.ConnectionParams) (tinybluetooth.Device, error) {
	a.connectCalls.Add(1)
	return tinybluetooth.Device{}, a.connectErr
}
func (a *reconnectCountingAdapter) Scan(cb func(*tinybluetooth.Adapter, tinybluetooth.ScanResult)) error {
	return nil
}
func (a *reconnectCountingAdapter) SetConnectHandler(func(tinybluetooth.Device, bool)) {}
func (a *reconnectCountingAdapter) StopScan() error                                    { return nil }

// TestPowerConfirmationKeepsBudgetAfterReconnectFailure verifies that a failed
// confirmation reconnect does not abandon the remaining confirmation attempts.
// A station that is transitioning is unreachable precisely while this polling
// exists for it, so giving up after the first reconnect failure would misreport
// a legitimate transition as an unconfirmed command. The target is standby: a
// sleep target exits early on disconnect-class read errors instead (the link
// drop is the expected sleep outcome), which has its own test.
func TestPowerConfirmationKeepsBudgetAfterReconnectFailure(t *testing.T) {
	ConfigureTiming(TimingPolicy{
		ConfirmAttemptsOff:        3,
		ConfirmPollInterval:       time.Millisecond,
		ConfirmReconnectThreshold: 1,
		ConfirmReconnectDelay:     time.Millisecond,
	})
	t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })

	originalAdapter := adapter
	counting := &reconnectCountingAdapter{connectErr: errors.New("station unreachable during boot")}
	adapter = counting
	t.Cleanup(func() { adapter = originalAdapter })

	power := &fakeCharacteristic{readErr: errors.New("radio restarting")}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateSleep

	station.mutex.Lock()
	err := confirmPowerStateInternalContext(context.Background(), station, PowerStateStandby)
	station.mutex.Unlock()

	if err == nil {
		t.Fatal("confirmation unexpectedly succeeded against an unreachable station")
	}
	// With 3 attempts and a reconnect threshold of 1, reconnects fire on
	// attempts 0 and 1 (attempt < attempts-1). Giving up after the first
	// failed reconnect would leave exactly 1 attempt; continuing the budget
	// performs 2.
	if got, want := counting.connectCalls.Load(), int32(2); got != want {
		t.Fatalf("confirmation reconnect attempts = %d, want %d (the full remaining budget)", got, want)
	}
}
func TestSnapshotStaysResponsiveDuringPowerConfirmationPolling(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x00}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateSleep
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	setDone := make(chan error, 1)
	go func() {
		_, err := SetPowerStateContext(ctx, station, PowerStateOn)
		setDone <- err
	}()
	// While the confirmation loop polls (200ms sleeps between attempts),
	// snapshots must not queue behind the station lock. With the sleeps
	// moved outside the lock, contention is limited to single short
	// read/write steps.
	deadline := time.Now().Add(5 * time.Second)
	for {
		start := time.Now()
		_ = station.Snapshot()
		if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
			t.Fatalf("Snapshot() blocked for %v during confirmation polling", elapsed)
		}
		select {
		case err := <-setDone:
			if err == nil {
				t.Fatal("SetPowerStateContext() unexpectedly confirmed a sleeping station as On")
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("SetPowerStateContext did not return")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestMarkMissedRequiresTwoConsecutiveScans(t *testing.T) {
	now := time.Now()
	station := &BaseStation{Present: true}
	station.MarkSeen(now)
	station.MarkMissed()
	firstMiss := station.Snapshot()
	if !firstMiss.Present || firstMiss.MissedScans != 1 {
		t.Fatalf("first missed scan should retain presence: %+v", firstMiss)
	}
	station.MarkMissed()
	secondMiss := station.Snapshot()
	if secondMiss.Present || secondMiss.MissedScans != 2 {
		t.Fatalf("second missed scan should mark absent: %+v", secondMiss)
	}
	station.MarkSeen(now.Add(time.Second))
	recovered := station.Snapshot()
	if !recovered.Present || recovered.MissedScans != 0 {
		t.Fatalf("seen station did not recover: %+v", recovered)
	}
}
func TestMarkMissedUsesRaisedPresenceThresholdForExistingHistory(t *testing.T) {
	original := CurrentTiming()
	t.Cleanup(func() { ConfigureTiming(original) })
	policy := original
	policy.PresenceMissThreshold = 2
	ConfigureTiming(policy)

	station := &BaseStation{Present: true}
	station.MarkSeen(time.Now())
	station.MarkMissed()
	station.MarkMissed()
	if snapshot := station.Snapshot(); snapshot.Present {
		t.Fatalf("station remained present at the original threshold: %+v", snapshot)
	}

	policy.PresenceMissThreshold = 4
	ConfigureTiming(policy)
	station.MarkMissed()
	snapshot := station.Snapshot()
	if !snapshot.Present || snapshot.MissedScans != 3 {
		t.Fatalf("raised threshold did not reclassify the next reliable miss: %+v", snapshot)
	}
}
func TestApplyPresenceMissThresholdReclassifiesCachedMisses(t *testing.T) {
	station := &BaseStation{Present: false, MissedScans: 2}
	if changed := station.ApplyPresenceMissThreshold(4); !changed {
		t.Fatal("raising the threshold did not report a presence change")
	}
	if snapshot := station.Snapshot(); !snapshot.Present || snapshot.MissedScans != 2 {
		t.Fatalf("raising the threshold did not revive cached presence: %+v", snapshot)
	}
	if changed := station.ApplyPresenceMissThreshold(2); !changed {
		t.Fatal("lowering the threshold did not report an absence change")
	}
	if snapshot := station.Snapshot(); snapshot.Present || snapshot.MissedScans != 2 {
		t.Fatalf("lowering the threshold did not mark cached presence absent: %+v", snapshot)
	}

	unknown := &BaseStation{}
	unknown.MarkMissed()
	if snapshot := unknown.Snapshot(); snapshot.Present {
		t.Fatalf("a miss incorrectly created presence without prior seen history: %+v", snapshot)
	}
}
func TestUncertainPresenceDoesNotCountAsMiss(t *testing.T) {
	now := time.Now()
	station := &BaseStation{Present: true}
	station.MarkSeen(now)
	station.MarkPresenceUncertain()
	uncertain := station.Snapshot()
	if !uncertain.Present || uncertain.MissedScans != 0 || !uncertain.PresenceUncertain {
		t.Fatalf("uncertain scan changed presence history: %+v", uncertain)
	}
	station.MarkMissed()
	firstReliableMiss := station.Snapshot()
	if !firstReliableMiss.Present || firstReliableMiss.MissedScans != 1 || firstReliableMiss.PresenceUncertain {
		t.Fatalf("first reliable miss after uncertainty = %+v", firstReliableMiss)
	}
	station.MarkSeen(now.Add(time.Second))
	recovered := station.Snapshot()
	if !recovered.Present || recovered.MissedScans != 0 || recovered.PresenceUncertain {
		t.Fatalf("seen station did not clear uncertainty: %+v", recovered)
	}
}
func TestMergeMetadataPreservesPreviouslyReadFields(t *testing.T) {
	previous := DeviceMetadata{Manufacturer: "Valve", Model: "Old model"}
	discovered := DeviceMetadata{Model: "New model", FirmwareRevision: "1.2.3"}
	merged := mergeMetadata(previous, discovered)
	if merged.Manufacturer != "Valve" || merged.Model != "New model" || merged.FirmwareRevision != "1.2.3" {
		t.Fatalf("mergeMetadata() = %+v", merged)
	}
}
func TestReconcileMetadataReplacesCompleteSnapshot(t *testing.T) {
	previous := DeviceMetadata{Manufacturer: "Valve", SerialNumber: "old serial"}
	discovered := DeviceMetadata{Manufacturer: "Valve", FirmwareRevision: "2.0"}
	now := time.Now()
	got, readAt := reconcileMetadata(previous, discovered, true, 2, 2, false, now)
	if got != discovered {
		t.Fatalf("reconcileMetadata() = %+v, want authoritative snapshot %+v", got, discovered)
	}
	if !readAt.Equal(now) {
		t.Fatalf("reconcileMetadata() read time = %v, want %v", readAt, now)
	}
}
func TestReconcileMetadataRetainsPartialValuesWithoutFreshTimestamp(t *testing.T) {
	previous := DeviceMetadata{Manufacturer: "Valve", SerialNumber: "cached serial"}
	discovered := DeviceMetadata{Manufacturer: "Valve", FirmwareRevision: "2.0"}
	got, readAt := reconcileMetadata(previous, discovered, true, 3, 2, true, time.Now())
	if got.SerialNumber != "cached serial" || got.FirmwareRevision != "2.0" {
		t.Fatalf("reconcileMetadata() partial snapshot = %+v", got)
	}
	if !readAt.IsZero() {
		t.Fatalf("partial metadata was marked fresh at %v", readAt)
	}
}
func TestReconcileMetadataInvalidatesFreshnessWithoutUsableService(t *testing.T) {
	previous := DeviceMetadata{Manufacturer: "Valve"}
	for _, test := range []struct {
		name         string
		serviceFound bool
		recognized   int
	}{
		{name: "service absent", serviceFound: false, recognized: 0},
		{name: "no recognized characteristics", serviceFound: true, recognized: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, readAt := reconcileMetadata(previous, DeviceMetadata{}, test.serviceFound, test.recognized, 0, false, time.Now())
			if got.Manufacturer != "Valve" {
				t.Fatalf("reconcileMetadata() discarded cached metadata: %+v", got)
			}
			if !readAt.IsZero() {
				t.Fatalf("unusable metadata service was marked fresh at %v", readAt)
			}
		})
	}
}

func TestApplyMetadataDiscoveryAdvancesRevisionForFailureAndRecovery(t *testing.T) {
	station := &BaseStation{}
	firstAttempt := time.Now()
	readErr := errors.New("firmware revision unavailable")

	station.applyMetadataDiscovery(
		DeviceMetadata{},
		true,
		1,
		0,
		readErr,
		firstAttempt,
	)
	if station.MetadataReadRevision != 1 {
		t.Fatalf("metadata revision after failed discovery = %d, want 1", station.MetadataReadRevision)
	}
	if !station.MetadataReadAt.IsZero() {
		t.Fatalf("failed metadata discovery was marked fresh at %v", station.MetadataReadAt)
	}
	if !errors.Is(station.metadataReadError, readErr) {
		t.Fatalf("metadata error after failed discovery = %v, want %v", station.metadataReadError, readErr)
	}

	secondAttempt := firstAttempt.Add(time.Second)
	metadata := DeviceMetadata{FirmwareRevision: "2.0"}
	station.applyMetadataDiscovery(metadata, true, 1, 1, nil, secondAttempt)
	if station.MetadataReadRevision != 2 {
		t.Fatalf("metadata revision after recovery = %d, want 2", station.MetadataReadRevision)
	}
	if station.Metadata != metadata || !station.MetadataReadAt.Equal(secondAttempt) {
		t.Fatalf("metadata after recovery = %+v at %v", station.Metadata, station.MetadataReadAt)
	}
	if station.metadataReadError != nil {
		t.Fatalf("metadata recovery retained error %v", station.metadataReadError)
	}
}

// mismatchThenUnsupportedPower reports a stable On value once so the confirm
// loop records a state-mismatch diagnostic, then rejects every later read as
// unsupported so the loop exits through its early-return path.
type mismatchThenUnsupportedPower struct {
	calls int
}

func (c *mismatchThenUnsupportedPower) Read(destination []byte) (int, error) {
	c.calls++
	if c.calls == 1 {
		destination[0] = 0x09
		return 1, nil
	}
	return 0, tinybluetooth.ErrAttReadNotPermitted
}
func (c *mismatchThenUnsupportedPower) Write(value []byte) (int, error) { return len(value), nil }
func (c *mismatchThenUnsupportedPower) WriteWithoutResponse(value []byte) (int, error) {
	return len(value), nil
}
func (c *mismatchThenUnsupportedPower) Properties() uint32 {
	return uint32(tinybluetooth.CharacteristicReadPermission)
}

// TestPowerConfirmationEarlyExitKeepsMismatchDiagnostic verifies that an early
// confirmation exit (unsupported read) still carries the state mismatch an
// earlier attempt observed. Losing it left PowerConfirmationError unable to
// explain what the device actually reported.
func TestPowerConfirmationEarlyExitKeepsMismatchDiagnostic(t *testing.T) {
	ConfigureTiming(TimingPolicy{ConfirmPollInterval: time.Millisecond})
	t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })

	power := &mismatchThenUnsupportedPower{}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true})
	station.PowerState = PowerStateSleep

	station.mutex.Lock()
	err := confirmPowerStateInternalContext(context.Background(), station, PowerStateSleep)
	station.mutex.Unlock()

	if err == nil {
		t.Fatal("confirmation unexpectedly succeeded against a mismatched, then unsupported, read")
	}
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("confirmation error = %v, want the unsupported read classification", err)
	}
	if !strings.Contains(err.Error(), "expected a confirmed sleep state") {
		t.Fatalf("early exit dropped the earlier mismatch diagnostic: %v", err)
	}
}

// TestOverlongReadValuesAreDataErrorsAndPreserveTheLink guards the read value
// validation: a characteristic reporting an unexpected byte length is malformed
// device data, not a broken link. Classifying it as a transport failure would
// discard the healthy GATT session and reconnect on every read without ever
// changing what the device reports.
func TestOverlongReadValuesAreDataErrorsAndPreserveTheLink(t *testing.T) {
	station := connectedFakeStation(
		&fakeCharacteristic{value: []byte{0x01, 0x02}},
		&fakeCharacteristic{value: []byte{0x01, 0x00, 0x00, 0x00, 0x00}},
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)

	station.mutex.Lock()
	powerErr := readPowerStateInternalContext(context.Background(), station)
	channelErr := readChannelInternalContext(context.Background(), station)
	station.mutex.Unlock()

	for name, err := range map[string]error{"power": powerErr, "channel": channelErr} {
		if !IsDeviceValueError(err) {
			t.Fatalf("%s read error = %v, want a device value error", name, err)
		}
		if RequiresReconnect(err) {
			t.Fatalf("%s value error requires a reconnect", name)
		}
		if RequiresReconnect(transportError("read "+name+" characteristic", err)) {
			t.Fatalf("%s value error wrapped in a transport error requires a reconnect", name)
		}
	}
	station.mutex.RLock()
	connected := station.isConnected && station.device != nil
	station.mutex.RUnlock()
	if !connected {
		t.Fatal("malformed read values discarded the healthy connection")
	}
	if !station.LastPowerReadAt.IsZero() || !station.LastChannelReadAt.IsZero() {
		t.Fatal("malformed read values left fresh read timestamps behind")
	}
}

// TestOverlongTransportReadsAreDataErrors guards the case where the device
// value exceeds even the oversized read buffer: the transport reports a
// buffer-overflow error (ErrReadValueTooLong), and it must still classify as
// malformed device data instead of a transport failure that would reconnect
// on every read without ever changing the reported value.
func TestOverlongTransportReadsAreDataErrors(t *testing.T) {
	tooLong := fmt.Errorf("bluetooth: read value is %d bytes, buffer holds %d: %w", 128, statusValueReadSize, tinybluetooth.ErrReadValueTooLong)
	station := connectedFakeStation(
		&fakeCharacteristic{readErr: tooLong},
		&fakeCharacteristic{readErr: tooLong},
		nil,
		Capabilities{PowerRead: true, ChannelRead: true},
	)

	station.mutex.Lock()
	powerErr := readPowerStateInternalContext(context.Background(), station)
	channelErr := readChannelInternalContext(context.Background(), station)
	station.mutex.Unlock()

	for name, err := range map[string]error{"power": powerErr, "channel": channelErr} {
		if !IsDeviceValueError(err) {
			t.Fatalf("%s read error = %v, want a device value error", name, err)
		}
		if RequiresReconnect(err) {
			t.Fatalf("%s overlong value error requires a reconnect", name)
		}
	}
	station.mutex.RLock()
	connected := station.isConnected && station.device != nil
	station.mutex.RUnlock()
	if !connected {
		t.Fatal("overlong transport reads discarded the healthy connection")
	}
}

// TestDecodeChannelStillRejectsInvalidPayload confirms the larger read buffer
// did not change channel payload validation for correctly sized values.
func TestDecodeChannelStillRejectsInvalidPayload(t *testing.T) {
	station := connectedFakeStation(
		nil,
		&fakeCharacteristic{value: []byte{0x00}},
		nil,
		Capabilities{ChannelRead: true},
	)
	station.mutex.Lock()
	err := readChannelInternalContext(context.Background(), station)
	station.mutex.Unlock()
	if err == nil {
		t.Fatal("channel read accepted a zero channel value")
	}
	if RequiresReconnect(err) {
		t.Fatalf("invalid channel value error = %v requires a reconnect", err)
	}
	// An out-of-range channel is malformed device data, like an invalid value
	// length: it must carry the DeviceValueError classification so channel
	// confirmation loops and fleet recovery treat it as unfixable-by-reconnect.
	if !IsDeviceValueError(err) {
		t.Fatalf("invalid channel value error = %v, want a device value error", err)
	}
}

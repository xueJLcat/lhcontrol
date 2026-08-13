package bluetooth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	tinybluetooth "tinygo.org/x/bluetooth"
	"unsafe"
)

type sleepFinalBlockingCharacteristic struct {
	*fakeCharacteristic
	contextWrites int
}

func (f *sleepFinalBlockingCharacteristic) WriteContext(ctx context.Context, value []byte) (int, error) {
	return f.WriteWithoutResponseContext(ctx, value)
}

func (f *sleepFinalBlockingCharacteristic) WriteWithoutResponseContext(ctx context.Context, value []byte) (int, error) {
	f.contextWrites++
	if f.contextWrites == 1 {
		return len(value), nil
	}
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestSetPowerStateContextDoesNotRearmBootStateWhenCancelledWaitingForStation(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x01}, ignoreWrite: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.PowerState = PowerStateOn
	station.RawPowerState = 0x01
	station.bootRawTrustedOn = true
	baseContext, cancel := context.WithCancel(context.Background())
	ctx := &observeFirstContextCheck{Context: baseContext, checked: make(chan struct{})}
	station.mutex.Lock()
	locked := true
	defer func() {
		if locked {
			station.mutex.Unlock()
		}
	}()
	type outcome struct {
		result PowerControlResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := SetPowerStateContext(ctx, station, PowerStateOn)
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("power operation did not reach its initial context check")
	}
	cancel()
	station.mutex.Unlock()
	locked = false

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.result != (PowerControlResult{}) {
			t.Fatalf("SetPowerStateContext() = %+v, %v; want zero result and context.Canceled", got.result, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled power operation did not return after the station lock was released")
	}
	if !station.bootRawTrustedOn {
		t.Fatal("pre-operation cancellation rearmed the station's compatibility boot state")
	}
	if len(power.writes) != 0 {
		t.Fatalf("pre-operation cancellation wrote %d command(s)", len(power.writes))
	}
}

func TestSetPowerStateContextDoesNotDisconnectWhenCancelledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	device := &trackingConnectedDevice{onConnected: cancel}
	power := &fakeCharacteristic{value: []byte{0x00}, powerSemantics: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.device = device

	result, err := SetPowerStateContext(ctx, station, PowerStateOn)
	if !errors.Is(err, context.Canceled) || result != (PowerControlResult{}) {
		t.Fatalf("SetPowerStateContext() = %+v, %v; want zero result and context.Canceled", result, err)
	}
	if len(power.writes) != 0 {
		t.Fatalf("writes after pre-write cancellation = %v, want none", power.writes)
	}
	if snapshot := station.Snapshot(); device.disconnects != 0 || !snapshot.Connected {
		t.Fatalf("pre-write cancellation changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, snapshot)
	}
}

func TestSetPowerStateConfirmsCompatibilityFirmwareAfterFreshFallbackWindow(t *testing.T) {
	// A new command must get a fresh transition window even when this connection
	// previously completed the compatibility fallback. Persistent boot-like raw
	// values can still confirm after that bounded window instead of exhausting
	// the entire power-on budget.
	ConfigureTiming(TimingPolicy{
		ConfirmAttemptsOn:   20,
		ConfirmPollInterval: time.Millisecond,
		BootFallbackAfter:   5 * time.Millisecond,
	})
	t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })
	power := &fakeCharacteristic{value: []byte{0x01}, ignoreWrite: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.setPowerStateInternal(PowerStateOn, 0x01)
	station.bootRawTrustedOn = true
	station.LastPowerReadAt = time.Now()
	start := time.Now()
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil || !result.Confirmed || result.State != PowerStateOn {
		t.Fatalf("SetPowerState() result=%+v error=%v, want compatibility confirmation after fallback", result, err)
	}
	if !station.bootRawTrustedOn {
		t.Fatal("compatibility confirmation did not establish sticky trust after the fresh fallback window")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("compatibility confirmation took %v, want it within the bounded test fallback", elapsed)
	}
	if len(power.writes) != 1 {
		t.Fatalf("writes = %d, want a single power-on command", len(power.writes))
	}
}

func TestIsGATTCommunicationFailure(t *testing.T) {
	for _, target := range []error{
		tinybluetooth.ErrGATTUnreachable,
		tinybluetooth.ErrGATTProtocol,
		tinybluetooth.ErrGATTAccessDenied,
		tinybluetooth.ErrGATTCommunication,
	} {
		if !IsGATTCommunicationFailure(errors.Join(errors.New("operation failed"), target)) {
			t.Fatalf("IsGATTCommunicationFailure() = false for %v", target)
		}
	}
	if IsGATTCommunicationFailure(errors.New("state confirmation mismatch")) {
		t.Fatal("state confirmation mismatch was classified as a GATT communication failure")
	}
}
func TestSetPowerStateStableTargetsAndConfirmation(t *testing.T) {
	for _, test := range []struct {
		name       string
		target     PowerState
		wantWrites []byte
	}{
		{"on", PowerStateOn, []byte{0x01}},
		{"standby", PowerStateStandby, []byte{0x02}},
		{"sleep", PowerStateSleep, []byte{0x01, 0x00}},
	} {
		t.Run(test.name, func(t *testing.T) {
			power := &fakeCharacteristic{value: []byte{0x00}, powerSemantics: true}
			station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true, Standby: true})
			result, err := SetPowerState(station, test.target)
			if err != nil {
				t.Fatalf("SetPowerState() error = %v", err)
			}
			if !result.Confirmed || station.PowerState != test.target {
				t.Fatalf("result = %+v, cached state = %v", result, station.PowerState)
			}
			if len(power.writes) != len(test.wantWrites) {
				t.Fatalf("writes = %v, want %v", power.writes, test.wantWrites)
			}
			for index, want := range test.wantWrites {
				if len(power.writes[index]) != 1 || power.writes[index][0] != want {
					t.Fatalf("write %d = %v, want %#x", index, power.writes[index], want)
				}
			}
		})
	}
}
func TestSetPowerStateWithoutReadReportsUnconfirmed(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x00}, powerSemantics: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerWrite: true})
	lastChannelRead := time.Now().Add(-time.Minute)
	station.PowerState = PowerStateSleep
	station.RawPowerState = 0x00
	station.LastPowerReadAt = time.Now()
	station.LastChannelReadAt = lastChannelRead
	station.LastReadAt = lastChannelRead
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if result.Confirmed || station.PowerState != PowerStateUnknown ||
		station.RawPowerState != RawPowerStateUnknown || !station.LastPowerReadAt.IsZero() {
		t.Fatalf("result = %+v, cached state = %+v", result, station.Snapshot())
	}
	if !station.LastReadAt.Equal(lastChannelRead) {
		t.Fatalf("unconfirmed power write discarded last successful status read: %v", station.LastReadAt)
	}
}
func TestWriteCharacteristicDoesNotRetryAmbiguousTransportFailure(t *testing.T) {
	characteristic := &fakeCharacteristic{
		properties: uint32(tinybluetooth.CharacteristicWriteWithoutResponsePermission |
			tinybluetooth.CharacteristicWritePermission),
		writeWithoutResponseErr: tinybluetooth.ErrGATTUnreachable,
	}
	err := writeCharacteristicValueInternal(context.Background(), characteristic, 0x01)
	if !errors.Is(err, tinybluetooth.ErrGATTUnreachable) {
		t.Fatalf("writeCharacteristicValueInternal() error = %v", err)
	}
	if !IsPossiblySent(err) {
		t.Fatalf("writeCharacteristicValueInternal() error = %v, want possibly-sent classification", err)
	}
	if characteristic.writeWithoutResponseAttempts != 1 || characteristic.writeWithResponseAttempts != 0 {
		t.Fatalf(
			"write attempts without-response=%d with-response=%d, want 1 and 0",
			characteristic.writeWithoutResponseAttempts,
			characteristic.writeWithResponseAttempts,
		)
	}
}
func TestSetPowerStateConfirmsAmbiguousWriteWithoutReplay(t *testing.T) {
	power := &fakeCharacteristic{
		value:                   []byte{0x0B},
		writeWithoutResponseErr: tinybluetooth.ErrGATTUnreachable,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil || !result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want confirmed ambiguous command", result, err)
	}
	if power.writeWithoutResponseAttempts != 1 {
		t.Fatalf("write attempts = %d, want 1", power.writeWithoutResponseAttempts)
	}
}
func TestSetPowerStateReportsUnconfirmedAmbiguousWriteWithoutReplay(t *testing.T) {
	power := &fakeCharacteristic{
		value:                   []byte{0x00},
		writeWithoutResponseErr: tinybluetooth.ErrGATTUnreachable,
		readErrAfterWrite:       tinybluetooth.ErrGATTUnreachable,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	result, err := SetPowerState(station, PowerStateOn)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed || !IsPossiblySent(err) {
		t.Fatalf("SetPowerState() result=%+v error=%v, want possibly-sent confirmation error", result, err)
	}
	if power.writeWithoutResponseAttempts != 1 {
		t.Fatalf("write attempts = %d, want 1", power.writeWithoutResponseAttempts)
	}
}
func TestSetPowerStateConfirmsAmbiguousResponseWriteWithoutReplay(t *testing.T) {
	power := &fakeCharacteristic{
		value:                []byte{0x00},
		properties:           uint32(tinybluetooth.CharacteristicReadPermission | tinybluetooth.CharacteristicWritePermission),
		writeErr:             &classifiedWriteError{possiblySent: true},
		writeErrorAfterApply: true,
		powerSemantics:       true,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil || !result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want confirmed ambiguous response write", result, err)
	}
	if power.writeWithResponseAttempts != 1 || power.writeAttempts != 1 {
		t.Fatalf("response write attempts=%d applied writes=%d, want 1 and 1", power.writeWithResponseAttempts, power.writeAttempts)
	}
}
func TestSleepDoesNotContinueAfterAmbiguousPrepareWrite(t *testing.T) {
	power := &fakeCharacteristic{
		value:                   []byte{0x00},
		writeWithoutResponseErr: tinybluetooth.ErrGATTUnreachable,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	result, err := SetPowerState(station, PowerStateSleep)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want unconfirmed prepare write", result, err)
	}
	if power.writeWithoutResponseAttempts != 1 {
		t.Fatalf("sleep write attempts = %d, want prepare only", power.writeWithoutResponseAttempts)
	}
}
func TestSleepContextCompletesFinalCommandAfterPrepareCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	power := &fakeCharacteristic{value: []byte{0x00}}
	power.onWrite = func(value []byte) {
		if len(value) == 1 && value[0] == 0x01 {
			cancel()
		}
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	result, err := SetPowerStateContext(ctx, station, PowerStateSleep)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("SetPowerStateContext() result=%+v error=%v, want cancelled confirmation error", result, err)
	}
	if result.Confirmed {
		t.Fatalf("result = %+v, want unconfirmed command", result)
	}
	if len(power.writes) != 2 || power.writes[0][0] != 0x01 || power.writes[1][0] != 0x00 {
		t.Fatalf("sleep writes = %v, want prepare then final sleep", power.writes)
	}
}
func TestSleepDoesNotReplayAfterAmbiguousFinalWrite(t *testing.T) {
	power := &fakeCharacteristic{
		value:                      []byte{0x00},
		writeWithoutResponseErrors: []error{nil, tinybluetooth.ErrGATTUnreachable},
		readErrAfterWrite:          tinybluetooth.ErrAttReadNotPermitted,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	result, err := SetPowerState(station, PowerStateSleep)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want unconfirmed final write", result, err)
	}
	if power.writeWithoutResponseAttempts != 2 {
		t.Fatalf("sleep write attempts = %d, want exactly 2", power.writeWithoutResponseAttempts)
	}
}
func TestAdapterDisconnectAsynchronouslyInvalidatesMatchingDevice(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:66")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	address := tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}}
	device := tinybluetooth.Device{Address: address}
	station := connectedFakeStation(&fakeCharacteristic{}, &fakeCharacteristic{}, &fakeCharacteristic{}, Capabilities{PowerRead: true})
	station.Address = address
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
	fake.emitConnection(device, false)
	deadline := time.Now().Add(time.Second)
	for station.Snapshot().Connected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if station.Snapshot().Connected || station.characteristic != nil || station.modeCharacteristic != nil || station.identifyCharacteristic != nil {
		t.Fatalf("disconnect callback retained cached handles: %+v", station.Snapshot())
	}
}
func TestStaleAdapterDisconnectDoesNotInvalidateReplacementDevice(t *testing.T) {
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:66")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	address := tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}}
	oldDevice := tinybluetooth.Device{Address: address}
	replacement := fakeConnectedDevice{}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.Address = address
	station.device = replacement
	invalidateDisconnectedDevice(station, oldDevice)
	if !station.Snapshot().Connected || station.device != replacement || station.characteristic == nil {
		t.Fatal("stale disconnect callback invalidated the replacement connection")
	}
}

func TestQueuedStaleDisconnectCannotReplaceCurrentDisconnect(t *testing.T) {
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:66")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	address := tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}}
	currentDevice := concreteDeviceWithTestIdentity(t, address)
	staleDevice := concreteDeviceWithTestIdentity(t, address)
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.Address = currentDevice.Address
	station.device = currentDevice

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

	// Hold the worker in its already-running state so both notifications are
	// queued deterministically. The second value represents an older callback
	// that was dispatched late and must not replace the current disconnect.
	station.invalidationMutex.Lock()
	station.invalidationRunning = true
	station.invalidationMutex.Unlock()
	station.queueDeviceInvalidation(currentDevice)
	station.queueDeviceInvalidation(staleDevice)
	station.drainDeviceInvalidations()

	if station.Snapshot().Connected || station.characteristic != nil {
		t.Fatal("late stale callback replaced the current disconnect notification")
	}
}

// concreteDeviceWithTestIdentity creates the same shape that two TinyGo
// Connect calls return for one address: equal addresses backed by distinct,
// private connection-state pointers. Only identity comparison is exercised;
// the zero-valued state is never used for transport operations.
func concreteDeviceWithTestIdentity(t *testing.T, address tinybluetooth.Address) tinybluetooth.Device {
	t.Helper()
	device := tinybluetooth.Device{Address: address}
	state := reflect.ValueOf(&device).Elem().FieldByName("state")
	if !state.IsValid() || state.Kind() != reflect.Pointer {
		t.Fatal("tinygo bluetooth Device no longer exposes the expected connection identity")
	}
	writableState := reflect.NewAt(state.Type(), unsafe.Pointer(state.UnsafeAddr())).Elem()
	writableState.Set(reflect.New(state.Type().Elem()))
	return device
}

func TestDisconnectAllStationsRetriesAdapterDisconnectedCleanup(t *testing.T) {
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:67")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	address := tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}}
	device := tinybluetooth.Device{Address: address}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{})
	station.Address = address
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
	invalidateDisconnectedDevice(station, device)
	if err := DisconnectAllStations(); err != nil {
		t.Fatalf("DisconnectAllStations() error = %v", err)
	}
	if station.pendingCleanup != nil {
		t.Fatal("pending cleanup was not retried during shutdown")
	}
}
func TestWriteCharacteristicFallsBackOnlyForUnsupportedWriteMode(t *testing.T) {
	characteristic := &fakeCharacteristic{
		properties: uint32(tinybluetooth.CharacteristicWriteWithoutResponsePermission |
			tinybluetooth.CharacteristicWritePermission),
		writeWithoutResponseErr: tinybluetooth.ErrAttRequestNotSupported,
	}
	if err := writeCharacteristicValueInternal(context.Background(), characteristic, 0x01); err != nil {
		t.Fatalf("writeCharacteristicValueInternal() error = %v", err)
	}
	if characteristic.writeWithoutResponseAttempts != 1 || characteristic.writeWithResponseAttempts != 1 {
		t.Fatalf(
			"write attempts without-response=%d with-response=%d, want 1 and 1",
			characteristic.writeWithoutResponseAttempts,
			characteristic.writeWithResponseAttempts,
		)
	}
}
func TestWriteCharacteristicPreservesDefinitelyNotSentClassification(t *testing.T) {
	writeErr := &classifiedWriteError{possiblySent: false}
	characteristic := &fakeCharacteristic{
		properties:              uint32(tinybluetooth.CharacteristicWriteWithoutResponsePermission),
		writeWithoutResponseErr: writeErr,
	}
	err := writeCharacteristicValueInternal(context.Background(), characteristic, 0x01)
	if !errors.Is(err, writeErr) || IsPossiblySent(err) {
		t.Fatalf("writeCharacteristicValueInternal() error = %v, want definitely-not-sent classification", err)
	}
}

func TestDefinitelyUnsentContextErrorHonorsTransportClassification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"bare cancellation":       {err: context.Canceled, want: true},
		"wrapped cancellation":    {err: transportError("write characteristic", context.Canceled), want: true},
		"explicitly not sent":     {err: errors.Join(context.Canceled, &classifiedWriteError{possiblySent: false}), want: true},
		"possibly sent":           {err: errors.Join(context.Canceled, &classifiedWriteError{possiblySent: true}), want: false},
		"unrelated write failure": {err: errors.New("write failed"), want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isDefinitelyUnsentContextError(ctx, test.err); got != test.want {
				t.Fatalf("isDefinitelyUnsentContextError() = %v, want %v for %v", got, test.want, test.err)
			}
		})
	}
}

func TestPowerConfirmationReadUnsupportedRetainsConnection(t *testing.T) {
	device := &trackingConnectedDevice{}
	power := &fakeCharacteristic{
		value:             []byte{0x00},
		powerSemantics:    true,
		readErrAfterWrite: tinybluetooth.ErrAttReadNotPermitted,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.device = device
	result, err := SetPowerState(station, PowerStateOn)
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) || result.Confirmed {
		t.Fatalf("SetPowerState() result=%+v error=%v, want unconfirmed result", result, err)
	}
	snapshot := station.Snapshot()
	if snapshot.Capabilities.PowerRead || !snapshot.Connected || device.disconnected {
		t.Fatalf("unsupported confirmation read damaged healthy connection: %+v", snapshot)
	}
}
func TestStandbyValueNotAllowedOnlyDisablesStandby(t *testing.T) {
	device := &trackingConnectedDevice{}
	power := &fakeCharacteristic{
		value:    []byte{0x00},
		writeErr: tinybluetooth.ErrAttValueNotAllowed,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true, Standby: true})
	station.device = device
	_, err := SetPowerState(station, PowerStateStandby)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("SetPowerState() error = %v, want unsupported standby", err)
	}
	snapshot := station.Snapshot()
	if snapshot.Capabilities.Standby || !snapshot.Capabilities.PowerWrite {
		t.Fatalf("standby rejection changed unrelated capability: %+v", snapshot.Capabilities)
	}
	if !snapshot.Connected || device.disconnected {
		t.Fatal("standby value rejection discarded a healthy connection")
	}
}
func TestSetPowerStateContextRejectsCancelledContext(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x00}, powerSemantics: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SetPowerStateContext(ctx, station, PowerStateOn); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetPowerStateContext() error = %v, want context.Canceled", err)
	}
	if len(power.writes) != 0 {
		t.Fatalf("writes after cancellation = %v, want none", power.writes)
	}
}

func TestSleepFinalWriteUsesIndependentCleanupDeadline(t *testing.T) {
	previousTimeout := finalSleepWriteTimeout
	finalSleepWriteTimeout = 100 * time.Millisecond
	defer func() { finalSleepWriteTimeout = previousTimeout }()
	power := &sleepFinalBlockingCharacteristic{fakeCharacteristic: &fakeCharacteristic{
		properties: characteristicPropertyWriteWithoutResponse,
	}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerWrite: true})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	start := time.Now()
	result, err := SetPowerStateContext(ctx, station, PowerStateSleep)
	if result.Confirmed {
		t.Fatalf("result = %+v, want unconfirmed final write", result)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleep final write error = %v, want context deadline", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond || elapsed > time.Second {
		t.Fatalf("sleep final cleanup duration = %v, want an independent bounded attempt", elapsed)
	}
	if power.contextWrites != 2 {
		t.Fatalf("context writes = %d, want prepare plus final sleep", power.contextWrites)
	}
}
func TestSetPowerStateContextCancellationDuringConfirmationKeepsCommandSent(t *testing.T) {
	// Reads never confirm the On target (raw 0x01 decodes as booting), so the
	// confirmation loop would poll for ~10s without cancellation.
	power := &fakeCharacteristic{value: []byte{0x00}}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	result, err := SetPowerStateContext(ctx, station, PowerStateOn)
	elapsed := time.Since(start)
	if result.Confirmed {
		t.Fatalf("result = %+v, want unconfirmed", result)
	}
	var confirmationErr *PowerConfirmationError
	if !errors.As(err, &confirmationErr) {
		t.Fatalf("error = %v, want PowerConfirmationError for the sent command", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want wrapped context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %v; confirmation polling was not interrupted", elapsed)
	}
	if len(power.writes) != 1 || power.writes[0][0] != 0x01 {
		t.Fatalf("writes = %v, want a single 0x01 command", power.writes)
	}
}
func TestSetPowerStateATTWriteUnsupportedUpdatesCapabilitiesWithoutDisconnect(t *testing.T) {
	device := &trackingConnectedDevice{}
	power := &fakeCharacteristic{
		value:    []byte{0x00},
		writeErr: tinybluetooth.ErrAttWriteNotPermitted,
	}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true, Standby: true})
	station.device = device
	_, err := SetPowerState(station, PowerStateOn)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("SetPowerState() error = %v, want unsupported capability", err)
	}
	snapshot := station.Snapshot()
	if snapshot.Capabilities.PowerWrite || snapshot.Capabilities.Standby {
		t.Fatalf("capabilities were not updated after ATT rejection: %+v", snapshot.Capabilities)
	}
	if !snapshot.Connected || device.disconnected {
		t.Fatal("ATT capability rejection discarded a healthy connection")
	}
}
func TestReadPowerATTUnsupportedUpdatesCapabilityWithoutReconnect(t *testing.T) {
	device := &trackingConnectedDevice{}
	power := &fakeCharacteristic{readErr: tinybluetooth.ErrAttReadNotPermitted}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})
	station.device = device
	err := ReadPowerState(station)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("ReadPowerState() error = %v, want unsupported capability", err)
	}
	snapshot := station.Snapshot()
	if snapshot.Capabilities.PowerRead || !snapshot.Capabilities.PowerWrite {
		t.Fatalf("capabilities after read rejection = %+v", snapshot.Capabilities)
	}
	if !snapshot.Connected || device.disconnected || RequiresReconnect(err) {
		t.Fatal("ATT read capability rejection was treated as a broken connection")
	}
}
func TestIdentifyWritesOne(t *testing.T) {
	identify := &fakeCharacteristic{}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, nil, identify, Capabilities{Identify: true})
	station.setOperationErrorInternal(errors.New("old identify error"))
	if err := Identify(station); err != nil {
		t.Fatalf("Identify() error = %v", err)
	}
	if len(identify.writes) != 1 || identify.writes[0][0] != 0x01 {
		t.Fatalf("identify writes = %v", identify.writes)
	}
	if station.LastError != "" {
		t.Fatalf("successful identify retained stale error %q", station.LastError)
	}
}
func TestIdentifyContextDoesNotDisconnectWhenCancelledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	device := &trackingConnectedDevice{onConnected: cancel}
	identify := &fakeCharacteristic{}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, identify, Capabilities{Identify: true})
	station.device = device

	err := IdentifyContext(ctx, station)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("IdentifyContext() error = %v, want context.Canceled", err)
	}
	if len(identify.writes) != 0 {
		t.Fatalf("writes after pre-write cancellation = %v, want none", identify.writes)
	}
	if snapshot := station.Snapshot(); device.disconnects != 0 || !snapshot.Connected {
		t.Fatalf("pre-write cancellation changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, snapshot)
	}
}
func TestIdentifySuccessPreservesUnresolvedPowerError(t *testing.T) {
	identify := &fakeCharacteristic{}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, identify, Capabilities{Identify: true})
	station.setPowerErrorInternal(errors.New("power read failed"))
	if err := Identify(station); err != nil {
		t.Fatalf("Identify() error = %v", err)
	}
	if !strings.Contains(station.LastError, "power read failed") {
		t.Fatalf("successful identify cleared unresolved power error %q", station.LastError)
	}
}
func TestErrorDomainsClearIndependently(t *testing.T) {
	station := &BaseStation{}
	station.setPowerErrorInternal(errors.New("power unavailable"))
	station.setMetadataErrorInternal(errors.New("firmware read failed"))
	station.setOperationErrorInternal(errors.New("identify failed"))
	station.setOperationErrorInternal(nil)
	if !strings.Contains(station.LastError, "power unavailable") ||
		!strings.Contains(station.LastError, "firmware read failed") ||
		strings.Contains(station.LastError, "identify failed") {
		t.Fatalf("independent error aggregation = %q", station.LastError)
	}
	station.setPowerErrorInternal(nil)
	if strings.Contains(station.LastError, "power unavailable") ||
		!strings.Contains(station.LastError, "firmware read failed") {
		t.Fatalf("clearing power error changed metadata error = %q", station.LastError)
	}
}

func TestClearOperationErrorPreservesOtherErrorDomains(t *testing.T) {
	station := &BaseStation{}
	station.setPowerErrorInternal(errors.New("power unavailable"))
	station.setMetadataErrorInternal(errors.New("firmware read failed"))
	station.setOperationErrorInternal(errors.New("identify failed"))

	station.ClearOperationError()

	if !strings.Contains(station.LastError, "power unavailable") ||
		!strings.Contains(station.LastError, "firmware read failed") ||
		strings.Contains(station.LastError, "identify failed") {
		t.Fatalf("clearing operation error changed independent error domains = %q", station.LastError)
	}
}
func TestIdentifyDoesNotRetryAmbiguousWrite(t *testing.T) {
	identify := &fakeCharacteristic{writeWithoutResponseErr: tinybluetooth.ErrGATTUnreachable}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, identify, Capabilities{Identify: true})
	err := Identify(station)
	if !IsPossiblySent(err) {
		t.Fatalf("Identify() error = %v, want possibly-sent classification", err)
	}
	if identify.writeWithoutResponseAttempts != 1 {
		t.Fatalf("identify write attempts = %d, want 1", identify.writeWithoutResponseAttempts)
	}
}
func TestIdentifyDoesNotRetryAmbiguousResponseWrite(t *testing.T) {
	identify := &fakeCharacteristic{
		properties:           uint32(tinybluetooth.CharacteristicWritePermission),
		writeWithResponseErr: &classifiedWriteError{possiblySent: true},
	}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, identify, Capabilities{Identify: true})
	err := Identify(station)
	if !IsPossiblySent(err) {
		t.Fatalf("Identify() error = %v, want possibly-sent classification", err)
	}
	if identify.writeWithResponseAttempts != 1 {
		t.Fatalf("response write attempts = %d, want 1", identify.writeWithResponseAttempts)
	}
}

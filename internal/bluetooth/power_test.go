package bluetooth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	tinybluetooth "tinygo.org/x/bluetooth"
)

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
	station.LastChannelReadAt = lastChannelRead
	station.LastReadAt = lastChannelRead
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if result.Confirmed || station.PowerState != PowerStateUnknown {
		t.Fatalf("result = %+v, cached state = %v", result, station.PowerState)
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

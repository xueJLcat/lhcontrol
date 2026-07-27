package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tinybluetooth "tinygo.org/x/bluetooth"
)

type fakeAdvertisementPayload struct {
	name     string
	services []tinybluetooth.UUID
}

func (p *fakeAdvertisementPayload) LocalName() string { return p.name }
func (p *fakeAdvertisementPayload) HasServiceUUID(uuid tinybluetooth.UUID) bool {
	for _, service := range p.services {
		if service == uuid {
			return true
		}
	}
	return false
}
func (p *fakeAdvertisementPayload) ServiceUUIDs() []tinybluetooth.UUID {
	return p.services
}
func (p *fakeAdvertisementPayload) Bytes() []byte { return nil }
func (p *fakeAdvertisementPayload) ManufacturerData() []tinybluetooth.ManufacturerDataElement {
	return nil
}
func (p *fakeAdvertisementPayload) ServiceData() []tinybluetooth.ServiceDataElement {
	return nil
}

type fakeBLEAdapter struct {
	results        []tinybluetooth.ScanResult
	scanErr        error
	panicScan      bool
	panicStop      bool
	returnEarly    bool
	started        chan struct{}
	stopped        chan struct{}
	startOnce      sync.Once
	once           sync.Once
	stopCalls      atomic.Int32
	startDelay     chan struct{}
	connectHandler func(tinybluetooth.Device, bool)
}

func newFakeBLEAdapter(results ...tinybluetooth.ScanResult) *fakeBLEAdapter {
	return &fakeBLEAdapter{results: results, started: make(chan struct{}), stopped: make(chan struct{})}
}

func (a *fakeBLEAdapter) Enable() error { return nil }
func (a *fakeBLEAdapter) Connect(tinybluetooth.Address, tinybluetooth.ConnectionParams) (tinybluetooth.Device, error) {
	return tinybluetooth.Device{}, errors.New("fake connect is not configured")
}
func (a *fakeBLEAdapter) SetConnectHandler(handler func(tinybluetooth.Device, bool)) {
	a.connectHandler = handler
}

func (a *fakeBLEAdapter) emitConnection(device tinybluetooth.Device, connected bool) {
	if a.connectHandler != nil {
		a.connectHandler(device, connected)
	}
}
func (a *fakeBLEAdapter) Scan(callback func(*tinybluetooth.Adapter, tinybluetooth.ScanResult)) error {
	return a.ScanWithStart(callback, nil)
}
func (a *fakeBLEAdapter) ScanWithStart(callback func(*tinybluetooth.Adapter, tinybluetooth.ScanResult), started func()) error {
	if a.panicScan {
		panic("scan callback boundary")
	}
	if a.startDelay != nil {
		<-a.startDelay
	}
	a.startOnce.Do(func() { close(a.started) })
	if started != nil {
		started()
	}
	for _, result := range a.results {
		callback(nil, result)
	}
	if a.scanErr != nil {
		return a.scanErr
	}
	if a.returnEarly {
		return nil
	}
	<-a.stopped
	return nil
}
func (a *fakeBLEAdapter) StopScan() error {
	a.stopCalls.Add(1)
	if a.panicStop {
		panic("stop scan boundary")
	}
	a.once.Do(func() { close(a.stopped) })
	return nil
}

type fakeCharacteristic struct {
	value                        []byte
	properties                   uint32
	readErr                      error
	readValues                   [][]byte
	readIndex                    int
	writeErr                     error
	writeWithResponseErr         error
	writeWithoutResponseErr      error
	writeWithoutResponseErrors   []error
	writeErrorAfterApply         bool
	readErrAfterWrite            error
	ignoreWrite                  bool
	powerSemantics               bool
	writeAttempts                int
	writeWithResponseAttempts    int
	writeWithoutResponseAttempts int
	writes                       [][]byte
	writeErrors                  []error
}

type classifiedWriteError struct {
	possiblySent bool
}

func (e *classifiedWriteError) Error() string      { return "classified write failure" }
func (e *classifiedWriteError) PossiblySent() bool { return e.possiblySent }

type fakeConnectedDevice struct{}

func (fakeConnectedDevice) Disconnect() error        { return nil }
func (fakeConnectedDevice) Connected() (bool, error) { return true, nil }
func (fakeConnectedDevice) DiscoverServices([]tinybluetooth.UUID) ([]tinybluetooth.DeviceService, error) {
	return nil, errors.New("fake discovery is not configured")
}
func (fakeConnectedDevice) RequestConnectionParams(tinybluetooth.ConnectionParams) error { return nil }

type trackingConnectedDevice struct {
	disconnected  bool
	disconnectErr error
	disconnects   int
}

type staleTrackingConnectedDevice struct {
	trackingConnectedDevice
}

func (*staleTrackingConnectedDevice) Connected() (bool, error) { return false, nil }

func (device *trackingConnectedDevice) Disconnect() error {
	device.disconnected = true
	device.disconnects++
	return device.disconnectErr
}

func TestDisconnectFailureRetainsDeviceUntilCleanupCanBeRetried(t *testing.T) {
	cleanupErr := errors.New("temporary cleanup failure")
	device := &trackingConnectedDevice{disconnectErr: cleanupErr}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.device = device

	if err := DisconnectStation(station); !errors.Is(err, cleanupErr) {
		t.Fatalf("DisconnectStation() error = %v, want %v", err, cleanupErr)
	}
	if station.device != nil || station.pendingCleanup == nil {
		t.Fatal("failed cleanup did not retain the device as pending cleanup")
	}
	device.disconnectErr = nil
	if err := DisconnectStation(station); err != nil {
		t.Fatalf("DisconnectStation() retry error = %v", err)
	}
	if station.device != nil || station.pendingCleanup != nil || device.disconnects != 2 {
		t.Fatalf("cleanup retry state: device=%v pending=%v disconnects=%d", station.device, station.pendingCleanup, device.disconnects)
	}
}

func TestReconnectDoesNotReplaceStationBeforeStaleCleanupSucceeds(t *testing.T) {
	cleanupErr := errors.New("stale connection cleanup failed")
	device := &staleTrackingConnectedDevice{trackingConnectedDevice: trackingConnectedDevice{disconnectErr: cleanupErr}}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.device = device
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	err := connectAndDiscoverInternal(station)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("connectAndDiscoverInternal() error = %v, want %v", err, cleanupErr)
	}
	if station.pendingCleanup != device || station.device != nil {
		t.Fatalf("stale cleanup state = pending:%v device:%v", station.pendingCleanup, station.device)
	}
}

func TestCompletedDisconnectWarningDoesNotBlockReplacementConnection(t *testing.T) {
	device := &trackingConnectedDevice{
		disconnectErr: &tinybluetooth.DisconnectCleanupError{Err: errors.New("session close warning")},
	}
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	station.device = device

	if err := DisconnectStation(station); err != nil {
		t.Fatalf("DisconnectStation() error = %v, want completed warning treated as success", err)
	}
	if station.device != nil || station.pendingCleanup != nil {
		t.Fatalf("completed cleanup retained stale handles: device=%v pending=%v", station.device, station.pendingCleanup)
	}
}

func TestCompletedPendingCleanupWarningIsCleared(t *testing.T) {
	device := &trackingConnectedDevice{
		disconnectErr: &tinybluetooth.DisconnectCleanupError{Err: errors.New("device close warning")},
	}
	station := connectedFakeStation(nil, nil, nil, Capabilities{})
	station.device = nil
	station.isConnected = false
	station.pendingCleanup = device

	if err := DisconnectStation(station); err != nil {
		t.Fatalf("DisconnectStation() pending cleanup error = %v", err)
	}
	if station.pendingCleanup != nil {
		t.Fatal("completed pending cleanup warning retained the stale device")
	}
}

func (*trackingConnectedDevice) Connected() (bool, error) { return true, nil }
func (*trackingConnectedDevice) DiscoverServices([]tinybluetooth.UUID) ([]tinybluetooth.DeviceService, error) {
	return nil, errors.New("fake discovery is not configured")
}
func (*trackingConnectedDevice) RequestConnectionParams(tinybluetooth.ConnectionParams) error {
	return nil
}

func (f *fakeCharacteristic) Read(destination []byte) (int, error) {
	if f.writeAttempts > 0 && f.readErrAfterWrite != nil {
		return 0, f.readErrAfterWrite
	}
	if f.readErr != nil {
		return 0, f.readErr
	}
	if f.readIndex < len(f.readValues) {
		value := f.readValues[f.readIndex]
		f.readIndex++
		return copy(destination, value), nil
	}
	return copy(destination, f.value), nil
}

func (f *fakeCharacteristic) Write(value []byte) (int, error) {
	f.writeWithResponseAttempts++
	if f.writeWithResponseErr != nil {
		return 0, f.writeWithResponseErr
	}
	return f.write(value)
}

func (f *fakeCharacteristic) WriteWithoutResponse(value []byte) (int, error) {
	f.writeWithoutResponseAttempts++
	writeErr := f.writeWithoutResponseErr
	if len(f.writeWithoutResponseErrors) >= f.writeWithoutResponseAttempts {
		writeErr = f.writeWithoutResponseErrors[f.writeWithoutResponseAttempts-1]
	}
	if writeErr != nil {
		return 0, writeErr
	}
	return f.write(value)
}

func (f *fakeCharacteristic) Properties() uint32 {
	return f.properties
}

func (f *fakeCharacteristic) write(value []byte) (int, error) {
	f.writeAttempts++
	writeErr := f.writeErr
	if len(f.writeErrors) >= f.writeAttempts {
		writeErr = f.writeErrors[f.writeAttempts-1]
	}
	if writeErr != nil && !f.writeErrorAfterApply {
		return 0, writeErr
	}
	f.writes = append(f.writes, append([]byte(nil), value...))
	if !f.ignoreWrite && len(value) == 1 {
		raw := value[0]
		if f.powerSemantics && raw == 0x01 {
			raw = 0x0B
		}
		f.value = []byte{raw}
	}
	if writeErr != nil {
		return 0, writeErr
	}
	return len(value), nil
}

func connectedFakeStation(power, mode, identify characteristicIO, capabilities Capabilities) *BaseStation {
	setFakeProperties := func(characteristic characteristicIO, readable, writable bool) {
		fake, ok := characteristic.(*fakeCharacteristic)
		if !ok || fake.properties != 0 {
			return
		}
		if readable {
			fake.properties |= uint32(tinybluetooth.CharacteristicReadPermission)
		}
		if writable {
			fake.properties |= uint32(tinybluetooth.CharacteristicWriteWithoutResponsePermission)
		}
	}
	setFakeProperties(power, capabilities.PowerRead, capabilities.PowerWrite)
	setFakeProperties(mode, capabilities.ChannelRead, capabilities.ChannelWrite)
	setFakeProperties(identify, false, capabilities.Identify)
	return &BaseStation{
		Name:                   "LHB-TEST",
		device:                 fakeConnectedDevice{},
		characteristic:         power,
		modeCharacteristic:     mode,
		identifyCharacteristic: identify,
		isConnected:            true,
		Capabilities:           capabilities,
		RawPowerState:          RawPowerStateUnknown,
		Channel:                ChannelUnknown,
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
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if result.Confirmed || station.PowerState != PowerStateUnknown {
		t.Fatalf("result = %+v, cached state = %v", result, station.PowerState)
	}
}

func TestWriteCharacteristicDoesNotRetryAmbiguousTransportFailure(t *testing.T) {
	characteristic := &fakeCharacteristic{
		properties: uint32(tinybluetooth.CharacteristicWriteWithoutResponsePermission |
			tinybluetooth.CharacteristicWritePermission),
		writeWithoutResponseErr: tinybluetooth.ErrGATTUnreachable,
	}

	err := writeCharacteristicValueInternal(characteristic, 0x01)
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

	if err := writeCharacteristicValueInternal(characteristic, 0x01); err != nil {
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

	err := writeCharacteristicValueInternal(characteristic, 0x01)
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
	station.LastError = "old connection error"
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

func TestStatusReadReportsMissingDeclaredPowerCharacteristic(t *testing.T) {
	station := connectedFakeStation(nil, nil, nil, Capabilities{PowerRead: true})

	err := ReadPowerState(station)
	var readErr *StatusReadError
	if !errors.As(err, &readErr) || readErr.Power == nil {
		t.Fatalf("ReadPowerState() error = %#v, want power StatusReadError", err)
	}
	if station.LastError == "" || !station.LastPowerReadAt.IsZero() {
		t.Fatalf("missing characteristic state was not recorded: %+v", station.Snapshot())
	}
}

func TestStatusReadClearsChannelWhenCapabilityIsUnavailable(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{})
	station.Channel = 7
	station.LastChannelReadAt = time.Now()

	if err := ReadPowerState(station); err != nil {
		t.Fatalf("ReadPowerState() error = %v", err)
	}
	if station.Channel != ChannelUnknown || !station.LastChannelReadAt.IsZero() {
		t.Fatalf("stale channel was retained: %+v", station.Snapshot())
	}
	if !station.LastReadAt.IsZero() {
		t.Fatalf("status read without readable values updated LastReadAt: %v", station.LastReadAt)
	}
}

func TestInitialReadClearsChannelWhenCapabilityIsUnavailable(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{})
	station.Channel = 7
	station.LastChannelReadAt = time.Now()

	if err := FetchInitialPowerState(station); err != nil {
		t.Fatalf("FetchInitialPowerState() error = %v", err)
	}
	if station.Channel != ChannelUnknown || !station.LastChannelReadAt.IsZero() {
		t.Fatalf("stale channel was retained: %+v", station.Snapshot())
	}
}

func TestSetChannelConfirmsReadback(t *testing.T) {
	mode := &fakeCharacteristic{value: []byte{0x03}}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	result, err := SetChannel(station, 5)
	if err != nil {
		t.Fatalf("SetChannel() error = %v", err)
	}
	if result.PreviousChannel != 3 || result.Channel != 5 {
		t.Fatalf("result = %+v", result)
	}
	if !result.CommandSent {
		t.Fatalf("result = %+v, want command sent", result)
	}
}

func TestSetChannelSkipsWriteWhenUnchanged(t *testing.T) {
	mode := &fakeCharacteristic{value: []byte{0x03}}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	result, err := SetChannel(station, 3)
	if err != nil {
		t.Fatalf("SetChannel() error = %v", err)
	}
	if result.PreviousChannel != 3 || result.Channel != 3 {
		t.Fatalf("result = %+v", result)
	}
	if result.CommandSent {
		t.Fatalf("result = %+v, unchanged channel must not report command sent", result)
	}
	if len(mode.writes) != 0 {
		t.Fatalf("unchanged channel produced writes: %v", mode.writes)
	}
}

func TestReadChannelRejectsOverlongValue(t *testing.T) {
	station := &BaseStation{
		Name:               "LHB-TEST",
		modeCharacteristic: &fakeCharacteristic{value: []byte{0, 0, 0, 0, 5}},
		Channel:            3,
	}
	if err := readChannelInternal(station); err == nil {
		t.Fatal("readChannelInternal() unexpectedly accepted five bytes")
	}
	if station.Channel != 3 || !station.LastChannelReadAt.IsZero() {
		t.Fatalf("failed read did not retain channel as stale: %+v", station.Snapshot())
	}
}

func TestSetChannelRequiresInitialRead(t *testing.T) {
	mode := &fakeCharacteristic{readErr: errors.New("read failed")}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	_, err := SetChannel(station, 5)
	if err == nil {
		t.Fatal("SetChannel() unexpectedly succeeded")
	}
	if len(mode.writes) != 0 {
		t.Fatalf("channel was written despite initial read failure: %v", mode.writes)
	}
	if station.Snapshot().Connected {
		t.Fatal("initial channel transport failure retained the connection")
	}
}

func TestSetChannelInitialReadUnsupportedRetainsConnection(t *testing.T) {
	device := &trackingConnectedDevice{}
	mode := &fakeCharacteristic{readErr: tinybluetooth.ErrAttReadNotPermitted}
	station := connectedFakeStation(
		&fakeCharacteristic{},
		mode,
		nil,
		Capabilities{ChannelRead: true, ChannelWrite: true},
	)
	station.device = device

	_, err := SetChannel(station, 5)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("SetChannel() error = %v, want unsupported channel read", err)
	}
	if station.Snapshot().Connected == false || device.disconnected {
		t.Fatal("unsupported initial channel read discarded a healthy connection")
	}
	if len(mode.writes) != 0 {
		t.Fatalf("channel was written despite unsupported initial read: %v", mode.writes)
	}
}

func TestSetChannelWriteFailureRetainsPreviousChannel(t *testing.T) {
	mode := &fakeCharacteristic{value: []byte{0x03}, writeErr: errors.New("connection lost")}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	result, err := SetChannel(station, 5)
	if err == nil {
		t.Fatal("SetChannel() unexpectedly succeeded")
	}
	if result.PreviousChannel != 3 || result.Channel != 3 {
		t.Fatalf("result = %+v", result)
	}
}

func TestSetChannelWriteAndReadbackFailureInvalidatesConnection(t *testing.T) {
	device := &trackingConnectedDevice{}
	mode := &fakeCharacteristic{
		value:             []byte{0x03},
		writeErr:          errors.New("write transport failed"),
		readErrAfterWrite: errors.New("readback transport failed"),
	}
	station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	station.device = device

	_, err := SetChannel(station, 5)
	if err == nil {
		t.Fatal("SetChannel() unexpectedly succeeded")
	}
	if !RequiresReconnect(err) {
		t.Fatalf("SetChannel() error = %v, want reconnect classification", err)
	}
	if station.Snapshot().Connected || !device.disconnected {
		t.Fatal("write plus readback failure retained the invalid connection")
	}
}

func TestSetChannelAmbiguousWriteAndFailedReadbackReportsCommandSent(t *testing.T) {
	mode := &fakeCharacteristic{
		value:             []byte{0x03},
		writeErr:          &classifiedWriteError{possiblySent: true},
		readErrAfterWrite: errors.New("readback transport failed"),
	}
	station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})

	result, err := SetChannel(station, 5)
	if err == nil {
		t.Fatal("SetChannel() unexpectedly succeeded")
	}
	if !result.CommandSent || result.PreviousChannel != 3 {
		t.Fatalf("result = %+v, want ambiguous command sent after channel 3", result)
	}
}

func TestSetChannelUnsupportedConfirmationReportsCommandSent(t *testing.T) {
	device := &trackingConnectedDevice{}
	mode := &fakeCharacteristic{
		value:             []byte{0x03},
		readErrAfterWrite: tinybluetooth.ErrAttReadNotPermitted,
	}
	station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	station.device = device

	result, err := SetChannel(station, 5)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("SetChannel() error = %v, want unsupported confirmation read", err)
	}
	if !result.CommandSent || result.PreviousChannel != 3 {
		t.Fatalf("result = %+v, want sent command after channel 3", result)
	}
	if station.Snapshot().Connected == false || device.disconnected {
		t.Fatal("unsupported confirmation read discarded a healthy connection")
	}
}

func TestATTCapabilityErrorsDoNotRequireReconnect(t *testing.T) {
	for _, protocolErr := range []tinybluetooth.AttributeProtocolError{
		tinybluetooth.ErrAttReadNotPermitted,
		tinybluetooth.ErrAttWriteNotPermitted,
		tinybluetooth.ErrAttRequestNotSupported,
	} {
		err := transportError("operation", protocolErr)
		if !IsCapabilityUnsupported(err) {
			t.Fatalf("%v was not classified as unsupported", protocolErr)
		}
		if RequiresReconnect(err) {
			t.Fatalf("%v incorrectly required reconnect", protocolErr)
		}
	}
	for _, protocolErr := range []tinybluetooth.AttributeProtocolError{
		tinybluetooth.ErrAttInvalidHandle,
		tinybluetooth.ErrAttOutOfSync,
		tinybluetooth.ErrAttUnlikelyError,
	} {
		if !RequiresReconnect(protocolErr) {
			t.Fatalf("%v did not require reconnect", protocolErr)
		}
	}
}

func TestCleanupTransportFailureIsNotMaskedByUnsupportedCapability(t *testing.T) {
	err := fmt.Errorf(
		"capability discovery failed: %w",
		errors.Join(
			unsupportedCapability("power control", tinybluetooth.ErrAttRequestNotSupported),
			transportError("cleanup connection", tinybluetooth.ErrGATTCommunication),
		),
	)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("joined error lost unsupported classification: %v", err)
	}
	if !RequiresReconnect(err) {
		t.Fatalf("joined cleanup failure did not require reconnect: %v", err)
	}
}

func TestSetChannelAcceptsConfirmedReadbackAfterWriteError(t *testing.T) {
	mode := &fakeCharacteristic{
		value:                []byte{0x03},
		writeErr:             errors.New("late transport error"),
		writeErrorAfterApply: true,
	}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	result, err := SetChannel(station, 5)
	if err != nil {
		t.Fatalf("SetChannel() error = %v", err)
	}
	if result.Channel != 5 || result.WriteWarning == "" {
		t.Fatalf("result = %+v, want confirmed channel and warning", result)
	}
	if !result.CommandSent {
		t.Fatalf("result = %+v, want command sent", result)
	}
}

func TestSetChannelReportsMismatchedFinalReadback(t *testing.T) {
	mode := &fakeCharacteristic{value: []byte{0x03}, ignoreWrite: true}
	power := &fakeCharacteristic{}
	station := connectedFakeStation(power, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	result, err := SetChannel(station, 5)
	if err == nil {
		t.Fatal("SetChannel() unexpectedly succeeded")
	}
	if result.PreviousChannel != 3 || result.Channel != 3 {
		t.Fatalf("result = %+v, want previous and actual channel 3", result)
	}
	if !station.Snapshot().Connected {
		t.Fatal("confirmed mismatched readback discarded a working connection")
	}
}

func TestSetChannelAcceptsTargetOnFinalReadback(t *testing.T) {
	mode := &fakeCharacteristic{
		value:       []byte{0x05},
		ignoreWrite: true,
		readValues: [][]byte{
			{0x03},
			{0x03}, {0x03}, {0x03}, {0x03}, {0x03},
			{0x05},
		},
	}
	station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})

	result, err := SetChannel(station, 5)
	if err != nil {
		t.Fatalf("SetChannel() error = %v", err)
	}
	if result.PreviousChannel != 3 || result.Channel != 5 || result.WriteWarning == "" {
		t.Fatalf("result = %+v, want delayed confirmed channel", result)
	}
	if station.LastError != "" {
		t.Fatalf("confirmed final readback retained error %q", station.LastError)
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

func TestDecodePowerStateWithHistory(t *testing.T) {
	now := time.Now()
	if got := decodePowerStateWithHistory(0x01, PowerStateOn, time.Time{}, now); got != PowerStateOn {
		t.Fatalf("0x01 after On = %v, want On", got)
	}
	if got := decodePowerStateWithHistory(0x01, PowerStateSleep, time.Time{}, now); got != PowerStateBooting {
		t.Fatalf("fresh 0x01 after Sleep = %v, want Booting", got)
	}
	if got := decodePowerStateWithHistory(0x01, PowerStateBooting, now.Add(-bootingFallbackAfter), now); got != PowerStateOn {
		t.Fatalf("persistent 0x01 = %v, want On fallback", got)
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

	err := FetchInitialPowerState(station)
	var initialErr *InitialReadError
	if !errors.As(err, &initialErr) {
		t.Fatalf("FetchInitialPowerState() error = %v, want InitialReadError", err)
	}
	if !errors.Is(err, powerErr) || !errors.Is(err, channelErr) {
		t.Fatalf("InitialReadError does not preserve underlying errors: %v", err)
	}
	if station.PowerState != PowerStateOn || station.RawPowerState != 0x09 || station.Channel != 4 ||
		!station.LastPowerReadAt.IsZero() || !station.LastChannelReadAt.IsZero() {
		t.Fatalf("failed reads did not preserve stale values: %+v", station.Snapshot())
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

func TestScanFindsServiceOnlyAdvertisement(t *testing.T) {
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
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}

	results, err := ScanForDuration(time.Millisecond)
	if err != nil {
		t.Fatalf("ScanForDuration() error = %v", err)
	}
	if len(results) != 1 || results[0].Name != "" {
		t.Fatalf("service-only scan results = %+v", results)
	}
}

func TestScanRejectsPartialResultsOnEarlyAdapterFailure(t *testing.T) {
	originalAdapter := adapter
	adapterErr := errors.New("radio failure")
	fake := newFakeBLEAdapter()
	fake.scanErr = adapterErr
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if _, err := ScanForDuration(time.Second); !errors.Is(err, adapterErr) {
		t.Fatalf("ScanForDuration() error = %v, want adapter failure", err)
	}
}

func TestScanSafelyConvertsPanicToError(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.panicScan = true
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	if err := scanSafely(func(*tinybluetooth.Adapter, tinybluetooth.ScanResult) {}, func() {}); err == nil {
		t.Fatal("scanSafely() unexpectedly ignored panic")
	}
}

func TestStopScanSafelyConvertsPanicToError(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.panicStop = true
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	if err := stopScanSafely(); err == nil {
		t.Fatal("stopScanSafely() unexpectedly ignored panic")
	}
}

func TestScanForDurationRepeatedLifecycle(t *testing.T) {
	originalAdapter := adapter
	t.Cleanup(func() { adapter = originalAdapter })

	for cycle := 0; cycle < 100; cycle++ {
		adapter = newFakeBLEAdapter()
		if _, err := ScanForDuration(time.Millisecond); err != nil {
			t.Fatalf("scan cycle %d error = %v", cycle+1, err)
		}
	}
}

func TestCancelScanStopsActiveScan(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	if err := CancelScan(); err != nil {
		t.Fatalf("CancelScan() error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDuration() after cancellation error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active scan did not stop after cancellation")
	}
}

func TestScanForDurationContextStopsActiveScan(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Hour)
		result <- err
	}()
	<-fake.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not stop active scan")
	}
}

func TestScanForDurationContextDoesNotStartWithCancelledContext(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanForDurationContext(ctx, time.Hour); !errors.Is(err, ErrScanCancelled) {
		t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
	}
	select {
	case <-fake.started:
		t.Fatal("adapter scan started with an already-cancelled context")
	default:
	}
}

func TestRequestScanCancellationBeforeWatcherStartIsNonBlocking(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		activeScanMutex.Lock()
		active := activeScan != nil
		activeScanMutex.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan session was not registered")
		}
		time.Sleep(time.Millisecond)
	}

	returned := make(chan struct{})
	go func() {
		RequestScanCancellation()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cancellation request blocked before watcher startup")
	}
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("StopScan calls before watcher start = %d, want 0", got)
	}
	close(fake.startDelay)
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not exit after watcher startup")
	}
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("StopScan calls = %d, want 1", got)
	}
}

func TestCancelScanBeforeWatcherStartReturnsWithoutWaiting(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		activeScanMutex.Lock()
		active := activeScan != nil
		activeScanMutex.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan session was not registered")
		}
		time.Sleep(time.Millisecond)
	}

	cancelled := make(chan error, 1)
	go func() { cancelled <- CancelScan() }()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("CancelScan() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("CancelScan blocked before watcher startup")
	}
	close(fake.startDelay)
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not exit after watcher startup")
	}
}

func TestScanRejectsEarlyGracefulReturn(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.returnEarly = true
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	if _, err := ScanForDuration(time.Hour); err == nil || !strings.Contains(err.Error(), "before the requested duration") {
		t.Fatalf("ScanForDuration() error = %v, want early-stop error", err)
	}
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("StopScan calls = %d, want 0", got)
	}
}

func TestScanTimerAndCancellationStopOnlyOnce(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(10 * time.Millisecond)
		result <- err
	}()
	<-fake.started
	time.Sleep(10 * time.Millisecond)
	_ = CancelScan()
	<-result
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("StopScan calls = %d, want 1", got)
	}
}

func TestScanDurationStartsAfterWatcherReportsStarted(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })

	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(20 * time.Millisecond)
		result <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("StopScan calls before watcher start = %d, want 0", got)
	}
	close(fake.startDelay)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ScanForDuration() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not stop after its post-start duration")
	}
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("StopScan calls = %d, want 1", got)
	}
}

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

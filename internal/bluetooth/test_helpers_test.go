package bluetooth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
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
	stopErr        error
	// startErr fails the scan before the platform watcher ever starts, so the
	// session's started flag stays false like a real Start rejection.
	startErr       error
	panicScan      bool
	panicStop      bool
	returnEarly    bool
	started        chan struct{}
	stopped        chan struct{}
	startOnce      sync.Once
	once           sync.Once
	stopCalls      atomic.Int32
	startDelay     chan struct{}
	stopHold       chan struct{}
	// releaseOn, when set, ends the blocking Scan once closed instead of
	// waiting for the stop handshake. It emulates the platform watcher whose
	// Scan call returns on its own budget while StopScan is still running (or
	// hung), which is exactly the state a removed or wedged radio produces.
	releaseOn      chan struct{}
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
	if a.startErr != nil {
		return a.startErr
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
	if a.releaseOn != nil {
		<-a.releaseOn
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
	if a.stopHold != nil {
		<-a.stopHold
	}
	a.once.Do(func() { close(a.stopped) })
	return a.stopErr
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
	onRead                       func()
	onWrite                      func([]byte)
}
type blockingContextCharacteristic struct {
	*fakeCharacteristic
	started     chan struct{}
	terminalErr error
}

func (f *blockingContextCharacteristic) ReadContext(ctx context.Context, _ []byte) (int, error) {
	close(f.started)
	<-ctx.Done()
	if f.terminalErr != nil {
		return 0, f.terminalErr
	}
	return 0, ctx.Err()
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
	onConnected   func()
}
type staleTrackingConnectedDevice struct {
	trackingConnectedDevice
}
type blockingDiscoveryDevice struct {
	trackingConnectedDevice
	started chan struct{}
}

func (device *blockingDiscoveryDevice) DiscoverServicesContext(ctx context.Context, _ []tinybluetooth.UUID) ([]tinybluetooth.DeviceService, error) {
	close(device.started)
	<-ctx.Done()
	return nil, ctx.Err()
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
	station.mutex.Lock()
	err := connectAndDiscoverInternal(station)
	station.mutex.Unlock()
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
func (device *trackingConnectedDevice) Connected() (bool, error) {
	if device.onConnected != nil {
		device.onConnected()
	}
	return true, nil
}
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
		n := copy(destination, value)
		if f.onRead != nil {
			f.onRead()
		}
		return n, nil
	}
	n := copy(destination, f.value)
	if f.onRead != nil {
		f.onRead()
	}
	return n, nil
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
	if f.onWrite != nil {
		f.onWrite(value)
	}
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
// deadLinkDevice reports the definitive closed-session error: the library
// already knows the link is gone, which is not a transient status-query blip.
type deadLinkDevice struct {
	trackingConnectedDevice
}

func (*deadLinkDevice) Connected() (bool, error) { return false, tinybluetooth.ErrDeviceDisconnected }

// transientStatusQueryDevice fails the status query with an unspecific RPC
// error, which must be treated as a blip instead of proof the link dropped.
type transientStatusQueryDevice struct {
	trackingConnectedDevice
}

func (*transientStatusQueryDevice) Connected() (bool, error) {
	return false, errors.New("bluetooth: GetConnectionStatus RPC failed")
}

func TestIsConnectedTearsDownDefinitivelyDeadSession(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerRead: true})
	device := &deadLinkDevice{}
	station.device = device
	if station.IsConnected() {
		t.Fatal("IsConnected() = true for a session whose link is definitively disconnected")
	}
	station.mutex.Lock()
	deviceGone := station.device == nil
	station.mutex.Unlock()
	if !deviceGone || !device.disconnected {
		t.Fatalf("dead session was not torn down: deviceGone=%v disconnected=%v", deviceGone, device.disconnected)
	}
}

func TestIsConnectedKeepsSessionOnTransientStatusQueryFailure(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerRead: true})
	device := &transientStatusQueryDevice{}
	station.device = device
	if station.IsConnected() {
		t.Fatal("IsConnected() = true for a failed status query")
	}
	station.mutex.Lock()
	deviceGone := station.device == nil
	station.mutex.Unlock()
	if deviceGone || device.disconnected {
		t.Fatal("transient status query failure tore down the cached session")
	}
}

func TestConnectFastPathRebuildsDefinitivelyDeadSession(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	device := &deadLinkDevice{}
	station.device = device
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	station.mutex.Lock()
	err := connectAndDiscoverInternal(station)
	station.mutex.Unlock()
	if err == nil {
		t.Fatal("connectAndDiscoverInternal() kept a definitively dead session instead of rebuilding it")
	}
	station.mutex.Lock()
	deviceGone := station.device == nil
	station.mutex.Unlock()
	if !deviceGone || !device.disconnected {
		t.Fatalf("dead session was not torn down before the rebuild: deviceGone=%v disconnected=%v", deviceGone, device.disconnected)
	}
}

func TestConnectFastPathKeepsSessionOnTransientStatusQueryFailure(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{PowerWrite: true})
	device := &transientStatusQueryDevice{}
	station.device = device
	station.mutex.Lock()
	err := connectAndDiscoverInternal(station)
	station.mutex.Unlock()
	if err != nil {
		t.Fatalf("connectAndDiscoverInternal() error = %v, want the cached session kept", err)
	}
	station.mutex.Lock()
	deviceKept := station.device != nil
	station.mutex.Unlock()
	if !deviceKept {
		t.Fatal("transient status query failure tore down the cached session")
	}
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

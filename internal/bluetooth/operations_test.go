package bluetooth

import (
	"errors"
	"sync"
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
	results   []tinybluetooth.ScanResult
	scanErr   error
	panicScan bool
	panicStop bool
	stopped   chan struct{}
	once      sync.Once
}

func newFakeBLEAdapter(results ...tinybluetooth.ScanResult) *fakeBLEAdapter {
	return &fakeBLEAdapter{results: results, stopped: make(chan struct{})}
}

func (a *fakeBLEAdapter) Enable() error { return nil }
func (a *fakeBLEAdapter) Connect(tinybluetooth.Address, tinybluetooth.ConnectionParams) (tinybluetooth.Device, error) {
	return tinybluetooth.Device{}, errors.New("fake connect is not configured")
}
func (a *fakeBLEAdapter) SetConnectHandler(func(tinybluetooth.Device, bool)) {}
func (a *fakeBLEAdapter) Scan(callback func(*tinybluetooth.Adapter, tinybluetooth.ScanResult)) error {
	if a.panicScan {
		panic("scan callback boundary")
	}
	for _, result := range a.results {
		callback(nil, result)
	}
	if a.scanErr != nil {
		return a.scanErr
	}
	<-a.stopped
	return nil
}
func (a *fakeBLEAdapter) StopScan() error {
	if a.panicStop {
		panic("stop scan boundary")
	}
	a.once.Do(func() { close(a.stopped) })
	return nil
}

type fakeCharacteristic struct {
	value                []byte
	properties           uint32
	readErr              error
	writeErr             error
	writeErrorAfterApply bool
	ignoreWrite          bool
	powerSemantics       bool
	writes               [][]byte
}

func (f *fakeCharacteristic) Read(destination []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return copy(destination, f.value), nil
}

func (f *fakeCharacteristic) Write(value []byte) (int, error) {
	return f.write(value)
}

func (f *fakeCharacteristic) WriteWithoutResponse(value []byte) (int, error) {
	return f.write(value)
}

func (f *fakeCharacteristic) Properties() uint32 {
	return f.properties
}

func (f *fakeCharacteristic) write(value []byte) (int, error) {
	if f.writeErr != nil && !f.writeErrorAfterApply {
		return 0, f.writeErr
	}
	f.writes = append(f.writes, append([]byte(nil), value...))
	if !f.ignoreWrite && len(value) == 1 {
		raw := value[0]
		if f.powerSemantics && raw == 0x01 {
			raw = 0x0B
		}
		f.value = []byte{raw}
	}
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(value), nil
}

func connectedFakeStation(power, mode, identify characteristicIO, capabilities Capabilities) *BaseStation {
	return &BaseStation{
		Name:                   "LHB-TEST",
		device:                 &tinybluetooth.Device{},
		characteristic:         power,
		modeCharacteristic:     mode,
		identifyCharacteristic: identify,
		isConnected:            true,
		Capabilities:           capabilities,
		RawPowerState:          RawPowerStateUnknown,
		Channel:                ChannelUnknown,
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
	if err := scanCompletionError(adapterErr, false); !errors.Is(err, adapterErr) {
		t.Fatalf("scanCompletionError() = %v, want wrapped adapter error", err)
	}
	if err := scanCompletionError(adapterErr, true); err != nil {
		t.Fatalf("timer-requested stop should be successful, got %v", err)
	}
	if err := scanCompletionError(nil, false); err != nil {
		t.Fatalf("nil scan error should be successful, got %v", err)
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

	if err := scanSafely(func(*tinybluetooth.Adapter, tinybluetooth.ScanResult) {}); err == nil {
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

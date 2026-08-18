package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	tinybluetooth "tinygo.org/x/bluetooth"
)

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
func TestStatusReadClearsPowerWhenCapabilityIsUnavailable(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{})
	station.PowerState = PowerStateOn
	station.RawPowerState = 0x0B
	station.LastPowerReadAt = time.Now()
	if err := ReadPowerState(station); err != nil {
		t.Fatalf("ReadPowerState() error = %v", err)
	}
	if station.PowerState != PowerStateUnknown || station.RawPowerState != RawPowerStateUnknown ||
		!station.LastPowerReadAt.IsZero() {
		t.Fatalf("stale power state was retained: %+v", station.Snapshot())
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
func TestInitialReadClearsPowerWhenCapabilityIsUnavailable(t *testing.T) {
	station := connectedFakeStation(&fakeCharacteristic{}, nil, nil, Capabilities{})
	station.PowerState = PowerStateOn
	station.RawPowerState = 0x0B
	station.LastPowerReadAt = time.Now()
	if err := FetchInitialPowerState(station); err != nil {
		t.Fatalf("FetchInitialPowerState() error = %v", err)
	}
	if station.PowerState != PowerStateUnknown || station.RawPowerState != RawPowerStateUnknown ||
		!station.LastPowerReadAt.IsZero() {
		t.Fatalf("initial read retained stale power state: %+v", station.Snapshot())
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
func TestSetChannelSentWriteAndExpiredReadbackRetainConnection(t *testing.T) {
	// A write submitted before the operation budget expired fails with a
	// possibly-sent classification; the following readback then dies on the
	// same expired context. Neither outcome proves the link is broken, so the
	// healthy connection must survive, matching the initial-read and
	// post-confirmation readback paths.
	device := &trackingConnectedDevice{}
	mode := &fakeCharacteristic{
		value:             []byte{0x03},
		writeErr:          &PossiblySentError{Err: context.DeadlineExceeded},
		readErrAfterWrite: context.DeadlineExceeded,
	}
	station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	station.device = device
	result, err := SetChannel(station, 5)
	if err == nil {
		t.Fatal("SetChannel() unexpectedly succeeded")
	}
	if !result.CommandSent {
		t.Fatalf("result = %+v, want the submitted command reported", result)
	}
	if device.disconnects != 0 || !station.Snapshot().Connected {
		t.Fatalf("expired readback after a submitted write discarded a healthy connection: disconnects=%d snapshot=%+v", device.disconnects, station.Snapshot())
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

// TestATTProtocolRejectionsDoNotRequireReconnect guards the security-policy
// and resource rejections: the peer received the request and answered it, so
// the link is healthy and a reconnect could never change authentication,
// encryption, or device resource state. Before this classification those
// codes were treated as transport failures and caused repeated
// disconnect/reconnect cycles on healthy connections.
func TestATTProtocolRejectionsDoNotRequireReconnect(t *testing.T) {
	for _, protocolErr := range []tinybluetooth.AttributeProtocolError{
		tinybluetooth.ErrAttInsufficientAuthentication,
		tinybluetooth.ErrAttInsufficientAuthorization,
		tinybluetooth.ErrAttInsufficientEncryption,
		tinybluetooth.ErrAttInsufficientEncKeySize,
		tinybluetooth.ErrAttInsufficientResources,
		tinybluetooth.ErrAttInvalidLength,
		tinybluetooth.ErrAttInvalidOffset,
		tinybluetooth.ErrAttInvalidPDU,
		tinybluetooth.ErrAttPrepareQueueFull,
		tinybluetooth.ErrAttUnsupportedGroupType,
	} {
		if !IsProtocolRejection(protocolErr) {
			t.Fatalf("%v was not classified as a protocol rejection", protocolErr)
		}
		if RequiresReconnect(protocolErr) {
			t.Fatalf("%v incorrectly required reconnect", protocolErr)
		}
		if RequiresReconnect(transportError("read power characteristic", protocolErr)) {
			t.Fatalf("transport-wrapped %v incorrectly required reconnect", protocolErr)
		}
	}
	// The link-health classifications must stay distinct from capability
	// support: a protocol rejection is not an unsupported capability.
	if IsCapabilityUnsupported(tinybluetooth.ErrAttInsufficientAuthentication) {
		t.Fatal("authentication rejection misclassified as unsupported capability")
	}
	if IsProtocolRejection(tinybluetooth.ErrAttInvalidHandle) ||
		IsProtocolRejection(tinybluetooth.ErrAttNotFound) ||
		IsProtocolRejection(tinybluetooth.ErrAttUnlikelyError) ||
		IsProtocolRejection(tinybluetooth.ErrAttOutOfSync) {
		t.Fatal("cache-invalidating ATT codes misclassified as protocol rejections")
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
func TestUnsupportedCapabilityRejectionNeverRequiresReconnect(t *testing.T) {
	// Standby rejections arrive as Value Not Allowed (0x13), which is outside
	// the unsupported ATT whitelist. The capability wrapper must keep the
	// healthy connection alive regardless of the wrapped rejection code.
	err := &UnsupportedCapabilityError{
		Capability: "standby",
		Err:        transportError("write characteristic", tinybluetooth.ErrAttValueNotAllowed),
	}
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("standby rejection lost unsupported classification: %v", err)
	}
	if RequiresReconnect(err) {
		t.Fatalf("standby value rejection incorrectly required reconnect: %v", err)
	}
}
func TestContextCancellationDoesNotRequireReconnect(t *testing.T) {
	// An operation's own cancellation or deadline aborts the transport call;
	// the connection is not known to be broken, so a healthy link must not pay
	// a reconnect. Both bare and transport-wrapped context errors are covered,
	// and a genuine transport failure still requires reconnect.
	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if RequiresReconnect(contextErr) {
			t.Fatalf("bare %v incorrectly required reconnect", contextErr)
		}
		if RequiresReconnect(transportError("read power characteristic", contextErr)) {
			t.Fatalf("transport-wrapped %v incorrectly required reconnect", contextErr)
		}
	}
	if !RequiresReconnect(transportError("read power characteristic", errors.New("connection reset"))) {
		t.Fatal("genuine transport failure did not require reconnect")
	}
}

func TestSetChannelContextDoesNotDisconnectWhenCancelledWaitingForStation(t *testing.T) {
	device := &trackingConnectedDevice{}
	station := connectedFakeStation(
		&fakeCharacteristic{},
		&fakeCharacteristic{value: []byte{3}},
		nil,
		Capabilities{ChannelRead: true, ChannelWrite: true},
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
	go func() {
		_, err := SetChannelContext(ctx, station, 5)
		result <- err
	}()
	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("channel operation did not reach its initial context check")
	}
	cancel()
	station.mutex.Unlock()
	locked = false

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SetChannelContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled channel operation did not return after the station lock was released")
	}
	snapshot := station.Snapshot()
	if device.disconnects != 0 || !snapshot.Connected {
		t.Fatalf("waiting cancellation changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, snapshot)
	}
}

func TestSetChannelContextDoesNotDisconnectWhenCancelledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	device := &trackingConnectedDevice{}
	mode := &fakeCharacteristic{value: []byte{3}, onRead: cancel}
	station := connectedFakeStation(
		&fakeCharacteristic{},
		mode,
		nil,
		Capabilities{ChannelRead: true, ChannelWrite: true},
	)
	station.device = device

	result, err := SetChannelContext(ctx, station, 5)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SetChannelContext() error = %v, want context.Canceled", err)
	}
	if result.PreviousChannel != 3 || result.CommandSent {
		t.Fatalf("SetChannelContext() result = %+v, want previous channel 3 and no command sent", result)
	}
	if len(mode.writes) != 0 {
		t.Fatalf("writes after pre-write cancellation = %v, want none", mode.writes)
	}
	if snapshot := station.Snapshot(); device.disconnects != 0 || !snapshot.Connected {
		t.Fatalf("pre-write cancellation changed the healthy connection: disconnects=%d snapshot=%+v", device.disconnects, snapshot)
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
func TestSetChannelPollsDelayedApplicationAfterAmbiguousWriteError(t *testing.T) {
	// An ambiguous write can be applied by the firmware only after a settling
	// delay. The confirmation must poll like the clean-write path instead
	// of deciding from a single immediate readback, which would report a
	// false failure and cache the stale channel as a fresh observation.
	mode := &fakeCharacteristic{
		value:                []byte{0x03},
		writeErr:             errors.New("late transport error"),
		writeErrorAfterApply: true,
		readValues: [][]byte{
			{0x03}, // initial pre-write read
			{0x03}, // first confirmation read: change not applied yet
			{0x05}, // second confirmation read: change applied
		},
	}
	station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
	result, err := SetChannel(station, 5)
	if err != nil {
		t.Fatalf("SetChannel() error = %v", err)
	}
	if result.Channel != 5 || !result.CommandSent || result.WriteWarning == "" {
		t.Fatalf("result = %+v, want delayed confirmation of the ambiguous write", result)
	}
	if station.Channel != 5 || station.LastChannelReadAt.IsZero() {
		t.Fatalf("station state = %+v, want the confirmed channel cached fresh", station.Snapshot())
	}
}

func TestSetChannelDoesNotClaimDefinitelyRejectedWriteWasSent(t *testing.T) {
	for name, writeErr := range map[string]error{
		"transport classification": &classifiedWriteError{possiblySent: false},
		"ATT rejection":            tinybluetooth.ErrAttInvalidHandle,
	} {
		t.Run(name, func(t *testing.T) {
			mode := &fakeCharacteristic{
				readValues: [][]byte{{0x03}, {0x05}},
				writeErr:   writeErr,
			}
			station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
			result, err := SetChannel(station, 5)
			if err != nil {
				t.Fatalf("SetChannel() error = %v", err)
			}
			if result.Channel != 5 || result.CommandSent || result.WriteWarning == "" {
				t.Fatalf("result = %+v, want independently reached channel without a sent command", result)
			}
		})
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

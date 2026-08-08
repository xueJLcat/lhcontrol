package station

import (
	"context"
	"errors"
	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"sync/atomic"
	"testing"
	"time"
	tinybluetooth "tinygo.org/x/bluetooth"
)

func TestBluetoothInitializationRecoversAfterRetry(t *testing.T) {
	manager := NewManager(config.NewConfig())
	attempts := 0
	manager.initializeBluetooth = func() error {
		attempts++
		if attempts == 1 {
			return errors.New("radio unavailable")
		}
		return nil
	}
	if err := manager.Initialize(); err == nil {
		t.Fatal("first initialization unexpectedly succeeded")
	}
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	if err := manager.ensureReady(); err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("initialization attempts = %d, want 2", attempts)
	}
}
func TestUnsupportedPowerReadDoesNotHideChannelRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:FF"
	manager.recordStructuredReadResult(
		&internalbluetooth.BaseStation{Address: mustAddress(t, address)},
		address,
		tinybluetooth.ErrAttReadNotPermitted,
		errors.New("temporary channel read failure"),
	)
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("retry = %+v tracked=%v, want channel-only retry", retry, tracked)
	}
	if disconnects.Load() != 0 {
		t.Fatalf("healthy connection was disconnected %d time(s)", disconnects.Load())
	}
}
func TestCombinedStructuredReadFailureRecordsConnectionFailureOnce(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:01"
	powerErr := &internalbluetooth.DeviceTransportError{
		Operation: "read power characteristic",
		Err:       tinybluetooth.ErrGATTUnreachable,
	}
	channelErr := &internalbluetooth.DeviceTransportError{
		Operation: "read channel characteristic",
		Err:       tinybluetooth.ErrGATTUnreachable,
	}
	manager.recordStructuredReadResult(
		&internalbluetooth.BaseStation{Address: mustAddress(t, address)},
		address,
		powerErr,
		channelErr,
	)
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("combined read failure did not schedule recovery")
	}
	// One failed operation is one connection failure, even when both reads
	// surfaced the same broken link; the channel keeps its own schedule.
	if retry.failures != 1 || retry.channelFailures != 1 {
		t.Fatalf("retry counters = connection:%d channel:%d, want 1 and 1", retry.failures, retry.channelFailures)
	}
	if disconnects.Load() != 1 {
		t.Fatalf("disconnects = %d, want exactly 1", disconnects.Load())
	}
}
func TestPermanentUnsupportedDiscoveryDoesNotRemainInBackgroundRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F9"
	station := &internalbluetooth.BaseStation{Address: mustAddress(t, address)}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		failures: 2,
		kinds:    statusRetryConnection,
		nextAt:   time.Now().Add(time.Hour),
	}
	manager.statusRetryMutex.Unlock()
	manager.recordUnstructuredStationFailure(
		station,
		address,
		tinybluetooth.ErrAttRequestNotSupported,
	)
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked || disconnects.Load() != 0 {
		t.Fatalf("unsupported station recovery tracked=%v disconnects=%d, want false and 0", tracked, disconnects.Load())
	}
}
func TestUnsupportedFailureWithCleanupErrorStillSchedulesRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F8"
	station := &internalbluetooth.BaseStation{Address: mustAddress(t, address)}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	err := errors.Join(
		tinybluetooth.ErrAttRequestNotSupported,
		&internalbluetooth.DeviceTransportError{
			Operation: "cleanup",
			Err:       tinybluetooth.ErrGATTCommunication,
		},
	)
	manager.recordUnstructuredStationFailure(station, address, err)
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || disconnects.Load() != 1 {
		t.Fatalf("mixed failure recovery tracked=%v disconnects=%d, want true and 1", tracked, disconnects.Load())
	}
}
func TestStandbyValueNotAllowedRejectionKeepsConnectionAndSkipsRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:02"
	station := &internalbluetooth.BaseStation{
		Name: "LHB-STANDBY-REJECT", Address: mustAddress(t, address), Present: true,
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true, Standby: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	stubPowerVerificationRead(manager)
	standbyErr := &internalbluetooth.UnsupportedCapabilityError{
		Capability: "standby",
		Err: &internalbluetooth.DeviceTransportError{
			Operation: "write characteristic",
			Err:       tinybluetooth.ErrAttValueNotAllowed,
		},
	}
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, standbyErr
	}
	_, err := manager.SetStationPower(address, "standby")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetStationPower() error = %v, want ErrUnsupported", err)
	}
	if disconnects.Load() != 0 {
		t.Fatalf("standby rejection discarded a healthy connection: disconnects=%d", disconnects.Load())
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("standby rejection scheduled connection recovery")
	}
}
func TestConnectionAndChannelRetrySchedulesAreIndependent(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	manager.statusRetryMax = 4 * time.Hour
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:FA"
	manager.noteStatusFailure(address)
	manager.noteStatusFailure(address)
	manager.noteChannelFailure(address)
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.failures != 2 || retry.channelFailures != 1 {
		t.Fatalf("retry counters = connection:%d channel:%d, want 2 and 1", retry.failures, retry.channelFailures)
	}
	if retry.nextAt.Sub(retry.lastAttempt) != 2*time.Hour {
		t.Fatalf("connection delay = %v, want 2h", retry.nextAt.Sub(retry.lastAttempt))
	}
	if retry.channelNextAt.Sub(retry.channelLastAttempt) != time.Hour {
		t.Fatalf("channel delay = %v, want 1h", retry.channelNextAt.Sub(retry.channelLastAttempt))
	}
	manager.clearStatusFailureKind(address, statusRetryConnection)
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.channelFailures != 1 || retry.channelNextAt.IsZero() ||
		effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("clearing connection retry damaged channel schedule: %+v tracked=%v", retry, tracked)
	}
}
func TestDisconnectedStationAddsImmediateConnectionRecoveryToChannelBackoff(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F7"
	channelNextAt := time.Now().Add(5 * time.Minute)
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:              statusRetryChannel,
		channelFailures:    3,
		channelLastAttempt: time.Now(),
		channelNextAt:      channelNextAt,
	}
	manager.statusRetryMutex.Unlock()
	startedAt := time.Now()
	manager.ensureStatusRecoveryTracked(address)
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if effectiveStatusRetryKinds(retry) != statusRetryConnection|statusRetryChannel {
		t.Fatalf("retry kinds = %v, want connection and channel", effectiveStatusRetryKinds(retry))
	}
	if retry.nextAt.Before(startedAt) || retry.nextAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("connection retry time = %v, want immediate schedule", retry.nextAt)
	}
	if retry.channelFailures != 3 || !retry.channelNextAt.Equal(channelNextAt) {
		t.Fatalf("channel retry was changed: %+v", retry)
	}
}
func TestDisconnectedStationDoesNotResetExistingConnectionBackoff(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "AA:BB:CC:DD:EE:F6"
	connectionNextAt := time.Now().Add(5 * time.Minute)
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:    statusRetryConnection,
		failures: 3,
		nextAt:   connectionNextAt,
	}
	manager.statusRetryMutex.Unlock()
	manager.ensureStatusRecoveryTracked(address)
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.failures != 3 || !retry.nextAt.Equal(connectionNextAt) {
		t.Fatalf("existing connection backoff was reset: %+v", retry)
	}
}

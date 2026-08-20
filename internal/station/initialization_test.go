package station

import (
	"context"
	"errors"
	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"strings"
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
// TestInitializeBoundsHungAdapterEnable guards app startup: Initialize must
// give up waiting after the bounded window instead of blocking startup (and
// holding initializeMutex, which would stall every concurrent ensureReady)
// on a wedged adapter.Enable.
func TestInitializeBoundsHungAdapterEnable(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.initializeWait = 20 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	var attempts atomic.Int32
	manager.initializeBluetooth = func() error {
		attempts.Add(1)
		<-release
		return nil
	}
	startedAt := time.Now()
	err := manager.Initialize()
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("Initialize() blocked %v on a hung adapter enable, want a bounded wait", elapsed)
	}
	if err == nil {
		t.Fatal("Initialize() unexpectedly succeeded while the adapter call was hung")
	}
	if strings.Contains(err.Error(), "%!") {
		t.Fatalf("Initialize() error = %q, want a real cause instead of a malformed wrap", err)
	}
	// ensureReady must reach its own bounded wait instead of queueing on
	// initializeMutex behind the hung startup call.
	startedAt = time.Now()
	if err := manager.ensureReady(); err == nil {
		t.Fatal("ensureReady() unexpectedly succeeded while the adapter call was hung")
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("ensureReady() blocked %v behind a hung Initialize(), want a bounded wait", elapsed)
	}
	if attempts.Load() != 1 {
		t.Fatalf("initialization attempts = %d, want exactly 1 concurrent adapter.Enable", attempts.Load())
	}
}

// TestEnsureReadyBoundsHungAdapterInitialization guards the background
// recovery loop against a wedged adapter.Enable: initialization must give up
// waiting after the bounded window instead of blocking the loop (started once
// via sync.Once) forever, a second Enable must never run alongside the hung
// one, and a late-completing attempt is adopted by the next caller.
func TestEnsureReadyBoundsHungAdapterInitialization(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.initializeWait = 20 * time.Millisecond
	release := make(chan struct{})
	var attempts atomic.Int32
	manager.initializeBluetooth = func() error {
		attempts.Add(1)
		<-release
		return nil
	}
	manager.initializeErr = errors.New("radio unavailable")
	manager.nextInitializeAt = time.Now().Add(-time.Second)

	startedAt := time.Now()
	err := manager.ensureReady()
	elapsed := time.Since(startedAt)
	if err == nil {
		t.Fatal("ensureReady() unexpectedly succeeded while the adapter call was hung")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ensureReady() blocked %v on a hung adapter enable, want a bounded wait", elapsed)
	}

	// A retry while the hung attempt is tracked must wait on that attempt
	// instead of starting a concurrent adapter.Enable.
	manager.initializeMutex.Lock()
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	manager.initializeMutex.Unlock()
	if err := manager.ensureReady(); err == nil {
		t.Fatal("ensureReady() unexpectedly succeeded while the adapter call was still hung")
	}
	if attempts.Load() != 1 {
		t.Fatalf("initialization attempts = %d, want exactly 1 concurrent adapter.Enable", attempts.Load())
	}

	// Once the attempt finally completes, a later caller adopts the success.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := manager.ensureReady(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ensureReady() never adopted the late initialization success")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if attempts.Load() != 1 {
		t.Fatalf("initialization attempts after adoption = %d, want 1", attempts.Load())
	}
	if manager.initializeErr != nil {
		t.Fatalf("initializeErr after adoption = %v, want nil", manager.initializeErr)
	}
}

// TestEnsureReadyAdoptsFailedAttemptKeepsCooldown verifies that a completed
// failed attempt records the normal cooldown bookkeeping instead of leaving
// the retry window open for a hot re-attempt loop.
func TestEnsureReadyAdoptsFailedAttemptKeepsCooldown(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.initializeWait = time.Second
	var attempts atomic.Int32
	manager.initializeBluetooth = func() error {
		attempts.Add(1)
		return errors.New("radio unavailable")
	}
	manager.initializeErr = errors.New("radio unavailable")
	manager.nextInitializeAt = time.Now().Add(-time.Second)

	if err := manager.ensureReady(); err == nil {
		t.Fatal("ensureReady() unexpectedly succeeded with a failing adapter enable")
	}
	// The recorded failure pushes nextInitializeAt out by the cooldown, so an
	// immediate follow-up call refuses a fresh attempt.
	if err := manager.ensureReady(); err == nil {
		t.Fatal("ensureReady() unexpectedly succeeded inside the retry cooldown")
	}
	if attempts.Load() != 1 {
		t.Fatalf("initialization attempts = %d, want 1 (cooldown must suppress an immediate retry)", attempts.Load())
	}
}

// TestStatusRecoveryRoundSurvivesHungAdapterInitialization pins the loop-level
// contract: a recovery round must return promptly even when the adapter-enable
// call hangs, and the station's retry must stay scheduled so later rounds keep
// trying. The loop is started once via sync.Once, so a single wedged
// initialization must not silence all background recovery.
func TestStatusRecoveryRoundSurvivesHungAdapterInitialization(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.initializeWait = 20 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	manager.initializeBluetooth = func() error {
		<-release
		return nil
	}
	manager.observeBluetoothError(tinybluetooth.ErrRadioNotAvailable)

	address := "AA:BB:CC:DD:EE:F5"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-HUNG-INIT", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:    statusRetryConnection,
		failures: 1,
		nextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()

	startedAt := time.Now()
	manager.runStatusRecoveryRound()
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("recovery round blocked %v on a hung adapter enable, want a bounded round", elapsed)
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("hung-initialization round dropped the station's recovery schedule")
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

// TestStructuredPowerReadDeadlineDoesNotDisconnectStation covers a power read
// whose own budget expired: the Bluetooth layer reports that as a structured
// InitialReadError whose Power error wraps context.DeadlineExceeded. Like the
// bare-deadline branches in scan, recovery, and status refresh, the outcome
// must back off without discarding a possibly-healthy session.
func TestStructuredPowerReadDeadlineDoesNotDisconnectStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:02"
	powerErr := &internalbluetooth.DeviceTransportError{
		Operation: "read power characteristic",
		Err:       context.DeadlineExceeded,
	}
	manager.recordStructuredReadResult(
		&internalbluetooth.BaseStation{Address: mustAddress(t, address)},
		address,
		powerErr,
		nil,
	)
	if disconnects.Load() != 0 {
		t.Fatalf("disconnects after a structured read-budget deadline = %d, want 0", disconnects.Load())
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.failures == 0 || effectiveStatusRetryKinds(retry)&statusRetryConnection == 0 {
		t.Fatalf("deadline retry = %+v tracked=%v, want a connection backoff", retry, tracked)
	}
}
// TestStructuredChannelReadWithContextJoinedTransportFailureTakesFailurePath
// covers a channel read whose genuine transport failure is joined with the
// cancelling context error, the shape ReadPowerStateContext produces when a
// real failure lands at the same instant as the context stopping. A plain
// context error schedules an immediate re-read, but the joined transport
// failure must take the channel backoff and its disconnect instead.
func TestStructuredChannelReadWithContextJoinedTransportFailureTakesFailurePath(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:03"
	transportErr := &internalbluetooth.DeviceTransportError{
		Operation: "read channel characteristic",
		Err:       tinybluetooth.ErrGATTUnreachable,
	}
	manager.recordStructuredReadResult(
		&internalbluetooth.BaseStation{Address: mustAddress(t, address)},
		address,
		nil,
		errors.Join(transportErr, context.Canceled),
	)
	if disconnects.Load() != 1 {
		t.Fatalf("disconnects after a joined channel transport failure = %d, want 1", disconnects.Load())
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.channelFailures == 0 || effectiveStatusRetryKinds(retry)&statusRetryChannel == 0 {
		t.Fatalf("retry = %+v tracked=%v, want a channel failure backoff", retry, tracked)
	}
}

// TestStructuredChannelReadWithPlainContextErrorKeepsImmediateReread pins the
// other half of the channel rule: a bare cancellation remains an immediate
// re-read without failure accounting or a disconnect.
func TestStructuredChannelReadWithPlainContextErrorKeepsImmediateReread(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	defer manager.Shutdown()
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	address := "AA:BB:CC:DD:EE:04"
	manager.recordStructuredReadResult(
		&internalbluetooth.BaseStation{Address: mustAddress(t, address)},
		address,
		nil,
		&internalbluetooth.DeviceTransportError{
			Operation: "read channel characteristic",
			Err:       context.Canceled,
		},
	)
	if disconnects.Load() != 0 {
		t.Fatalf("disconnects after a plain channel cancellation = %d, want 0", disconnects.Load())
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("retry = %+v tracked=%v, want an immediate re-read scheduled", retry, tracked)
	}
	if retry.channelFailures != 0 || retry.failures != 0 {
		t.Fatalf("retry = %+v, want no failure accounting for a plain cancellation", retry)
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

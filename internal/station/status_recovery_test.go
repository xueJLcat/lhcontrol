package station

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"

	tinybluetooth "tinygo.org/x/bluetooth"
)

func TestIsRecentRejectsFutureTimestamps(t *testing.T) {
	now := time.Now()
	window := 45 * time.Second
	tests := []struct {
		name  string
		value time.Time
		want  bool
	}{
		{name: "zero", value: time.Time{}, want: false},
		{name: "current", value: now, want: true},
		{name: "within window", value: now.Add(-window), want: true},
		{name: "expired", value: now.Add(-window - time.Nanosecond), want: false},
		{name: "future", value: now.Add(time.Nanosecond), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRecent(test.value, now, window); got != test.want {
				t.Fatalf("isRecent() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestApplyPresenceMissThresholdReclassifiesAndRevivesRecovery(t *testing.T) {
	cfg := config.NewConfig()
	cfg.PresenceMissThreshold = 4
	manager := NewManager(cfg)
	// Keep the test deterministic while still verifying that a newly present
	// disconnected station is put back into the recovery registry.
	manager.shuttingDown.Store(true)
	address := "11:22:33:44:55:64"
	station := &internalbluetooth.BaseStation{
		Address: mustAddress(t, address), Present: false, MissedScans: 2,
	}
	manager.stations[address] = station

	manager.ApplyPresenceMissThreshold()
	snapshot := station.Snapshot()
	if !snapshot.Present || snapshot.MissedScans != 2 {
		t.Fatalf("raised threshold did not revive station: %+v", snapshot)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[snapshot.Address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry)&statusRetryConnection == 0 {
		t.Fatalf("revived disconnected station recovery = %+v tracked=%v", retry, tracked)
	}

	cfg.PresenceMissThreshold = 2
	manager.ApplyPresenceMissThreshold()
	if snapshot := station.Snapshot(); snapshot.Present {
		t.Fatalf("lowered threshold did not mark station absent: %+v", snapshot)
	}
}

// TestPresenceThresholdReviveRebasesExistingBackoff guards the promise that a
// station revived by raising the miss threshold gets an immediate recovery
// attempt: an absent-era backoff deadline far in the future must not delay it.
func TestPresenceThresholdReviveRebasesExistingBackoff(t *testing.T) {
	cfg := config.NewConfig()
	cfg.PresenceMissThreshold = 2
	manager := NewManager(cfg)
	manager.shuttingDown.Store(true)
	address := "11:22:33:44:55:6B"
	station := &internalbluetooth.BaseStation{
		Address: mustAddress(t, address), Present: false, MissedScans: 3,
	}
	manager.stations[address] = station

	futureAttempt := time.Now().Add(time.Hour)
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:              statusRetryConnection | statusRetryChannel,
		failures:           5,
		lastAttempt:        time.Now().Add(-time.Hour),
		nextAt:             futureAttempt,
		channelFailures:    4,
		channelLastAttempt: time.Now().Add(-time.Hour),
		channelNextAt:      futureAttempt,
	}
	manager.statusRetryMutex.Unlock()

	cfg.PresenceMissThreshold = 5
	now := time.Now()
	manager.ApplyPresenceMissThreshold()

	snapshot := station.Snapshot()
	if !snapshot.Present {
		t.Fatalf("raised threshold did not revive station: %+v", snapshot)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("revived station lost its recovery entry")
	}
	if retry.nextAt.After(now) {
		t.Fatalf("revived connection recovery still backed off: nextAt=%v", retry.nextAt)
	}
	if retry.channelNextAt.After(now) {
		t.Fatalf("revived channel recovery still backed off: nextAt=%v", retry.channelNextAt)
	}
	if retry.failures != 5 || retry.channelFailures != 4 {
		t.Fatalf("rebase discarded failure history: %+v", retry)
	}
}

func TestAbsentRecoveryStopsExhaustedKindsIndependently(t *testing.T) {
	// Drive the limit through the config (a non-default value) so a wiring
	// regression that hardcodes the old constant would fail this test.
	const limit = 3
	cfg := config.NewConfig()
	cfg.AbsentStationRetryLimit = limit
	manager := NewManager(cfg)
	address := "11:22:33:44:55:65"
	station := &internalbluetooth.BaseStation{Address: mustAddress(t, address), Present: false}
	manager.statusRetries[address] = statusRetry{
		failures:        limit,
		channelFailures: limit - 1,
		nextAt:          time.Now(),
		channelNextAt:   time.Now(),
		kinds:           statusRetryConnection | statusRetryChannel,
	}

	manager.stopExhaustedAbsentRecovery(address, station)
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel || retry.channelFailures != limit-1 {
		t.Fatalf("retry after connection exhaustion = %+v tracked=%v, want channel-only", retry, tracked)
	}

	manager.statusRetryMutex.Lock()
	retry.channelFailures = limit
	manager.statusRetries[address] = retry
	manager.statusRetryMutex.Unlock()
	manager.stopExhaustedAbsentRecovery(address, station)
	manager.statusRetryMutex.Lock()
	_, tracked = manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("absent station remained scheduled after both retry kinds were exhausted")
	}
}

func TestRecoverySchedulerIncludesKnownAbsentStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Millisecond
	address := "11:22:33:44:55:69"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-ABSENT", Address: mustAddress(t, address), Present: false,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	manager.statusRetryMutex.Unlock()
	var recovered atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered.Add(1)
		return nil
	}

	manager.runStatusRecoveryRound()
	if recovered.Load() != 1 {
		t.Fatalf("absent known station recovery attempts = %d, want 1", recovered.Load())
	}
}

func TestRecoverySchedulerTreatsZeroScheduleAsImmediatelyDue(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:87"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-ZERO-SCHEDULE", Address: mustAddress(t, address), Present: false,
	}
	manager.statusRetryMutex.Lock()
	// A legacy-shaped entry carries no explicit kinds and no schedule time;
	// it must be treated as due immediately instead of waiting for a wake.
	manager.statusRetries[address] = statusRetry{}
	manager.statusRetryMutex.Unlock()

	delay, scheduled := manager.nextStatusRecoveryDelay()
	if !scheduled || delay != 0 {
		t.Fatalf("nextStatusRecoveryDelay() = %v, %v; want 0, true", delay, scheduled)
	}

	var recovered atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered.Add(1)
		return nil
	}
	manager.runStatusRecoveryRound()
	if recovered.Load() != 1 {
		t.Fatalf("zero-schedule recovery attempts = %d, want 1", recovered.Load())
	}
}

func TestStationGATTFailureInvalidatesConnectionAndRegistersRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:61"
	station := &internalbluetooth.BaseStation{
		Name:              "LHB-61",
		Address:           mustAddress(t, address),
		Present:           true,
		Capabilities:      internalbluetooth.Capabilities{PowerWrite: true},
		CapabilitiesKnown: true,
	}
	manager.stations[address] = station
	manager.bluetoothOps.setPowerState = func(context.Context, *internalbluetooth.BaseStation, internalbluetooth.PowerState) (internalbluetooth.PowerControlResult, error) {
		return internalbluetooth.PowerControlResult{}, tinybluetooth.ErrGATTUnreachable
	}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}

	_, err := manager.SetStationPower(address, "on")
	if !errors.Is(err, tinybluetooth.ErrGATTUnreachable) {
		t.Fatalf("SetStationPower() error = %v, want ErrGATTUnreachable", err)
	}
	if got := disconnects.Load(); got != 1 {
		t.Fatalf("disconnect calls = %d, want 1", got)
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("GATT communication failure did not register status recovery")
	}
}

func TestStatusCheckSchedulesInitialRecoveryForDisconnectedStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.stations["disconnected"] = &internalbluetooth.BaseStation{
		Name:          "LHB-DISCONNECTED",
		Present:       true,
		PowerState:    internalbluetooth.PowerStateSleep,
		RawPowerState: 0x00,
	}

	infos, err := manager.CheckAllStationStatuses()
	if err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	if len(infos) != 1 || infos[0].PowerState != int(internalbluetooth.PowerStateSleep) || infos[0].ConnectionState != "disconnected" {
		t.Fatalf("failed recovery did not preserve stale station state: %+v", infos)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.statusRetryMutex.Lock()
		_, tracked := manager.statusRetries[infos[0].Address]
		manager.statusRetryMutex.Unlock()
		if tracked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background recovery did not create a retry backoff")
}

func TestStatusCheckReadsConnectedAndTracksDisconnectedStationsTogether(t *testing.T) {
	manager := NewManager(config.NewConfig())
	connectedAddress := "11:22:33:44:55:71"
	disconnectedAddress := "11:22:33:44:55:72"
	connected := &internalbluetooth.BaseStation{
		Name: "LHB-CONNECTED", Address: mustAddress(t, connectedAddress), Present: true,
	}
	disconnected := &internalbluetooth.BaseStation{
		Name: "LHB-DISCONNECTED", Address: mustAddress(t, disconnectedAddress), Present: true,
	}
	manager.stations[connectedAddress] = connected
	manager.stations[disconnectedAddress] = disconnected
	manager.bluetoothOps.stationConnected = func(station *internalbluetooth.BaseStation) bool {
		return station == connected
	}
	var connectedReads atomic.Int32
	manager.bluetoothOps.readPowerStateContext = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		if station != connected {
			t.Fatalf("unexpected status read for %s", station.Snapshot().Address)
		}
		connectedReads.Add(1)
		return nil
	}
	recoveryStarted := make(chan struct{})
	releaseRecovery := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		if station == disconnected {
			close(recoveryStarted)
			<-releaseRecovery
		}
		return nil
	}

	if _, err := manager.CheckAllStationStatuses(); err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	if connectedReads.Load() != 1 {
		t.Fatalf("connected reads = %d, want 1", connectedReads.Load())
	}
	select {
	case <-recoveryStarted:
	case <-time.After(time.Second):
		t.Fatal("disconnected station was not scheduled while another station was connected")
	}
	close(releaseRecovery)
	manager.Shutdown()
}

func TestStatusRefreshTimeoutBoundsWholeFleet(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusReadTimeout = time.Hour
	manager.statusRefreshTimeout = 40 * time.Millisecond
	for index := 1; index <= 5; index++ {
		address := fmt.Sprintf("11:22:33:44:77:%02X", index)
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name: "LHB-STATUS-PHASE", Address: mustAddress(t, address), Present: true,
		}
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	manager.bluetoothOps.readPowerStateContext = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		<-ctx.Done()
		return ctx.Err()
	}

	started := time.Now()
	_, err := manager.CheckAllStationStatuses()
	if err == nil {
		t.Fatal("CheckAllStationStatuses() error = nil, want incomplete refresh")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("fleet status refresh took %v, want a fleet-wide bound", elapsed)
	}
	manager.statusRetryMutex.Lock()
	for address, station := range manager.stations {
		retry, tracked := manager.statusRetries[address]
		if !tracked || effectiveStatusRetryKinds(retry)&statusRetryRefresh == 0 {
			t.Fatalf("phase-limited status retry for %s = %+v, tracked=%v", address, retry, tracked)
		}
		if station.Snapshot().LastError != "" {
			t.Fatalf("phase budget marked %s as a device failure: %q", address, station.Snapshot().LastError)
		}
	}
	manager.statusRetryMutex.Unlock()
}

// TestStatusRefreshAbandonsWedgedWorker guards the bounded worker join: a
// read blocked inside an adapter call that ignores cancellation (a wedged
// WinRT cleanup holding the transport lock) must not hold the refresh lock
// forever. The refresh is abandoned with a retryable Busy error and every
// candidate is registered for recovery instead.
func TestStatusRefreshAbandonsWedgedWorker(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRecoveryStart.Do(func() {})
	manager.statusReadTimeout = time.Hour
	manager.statusRefreshTimeout = time.Hour
	manager.statusRefreshJoinWait = 20 * time.Millisecond
	address := "11:22:33:44:55:78"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-WEDGED", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	release := make(chan struct{})
	defer close(release)
	manager.bluetoothOps.readPowerStateContext = func(context.Context, *internalbluetooth.BaseStation) error {
		<-release // Ignores ctx: simulates a worker blocked in an adapter call.
		return nil
	}

	started := time.Now()
	_, err := manager.CheckAllStationStatuses()
	if !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("CheckAllStationStatuses() error = %v, want a retryable busy error", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("wedged status refresh took %v, want the bounded join", elapsed)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("abandoned status retry = %+v, tracked=%v; want a refresh retry", retry, tracked)
	}
}

func TestStatusCheckLeavesOneSlotForForegroundWork(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	connectedAddresses := []string{"11:22:33:44:55:73", "11:22:33:44:55:74"}
	connectedStations := make(map[*internalbluetooth.BaseStation]struct{})
	for _, address := range connectedAddresses {
		station := &internalbluetooth.BaseStation{
			Name: "LHB-" + address[len(address)-2:], Address: mustAddress(t, address), Present: true,
		}
		manager.stations[address] = station
		connectedStations[station] = struct{}{}
	}
	disconnectedAddress := "11:22:33:44:55:75"
	disconnected := &internalbluetooth.BaseStation{
		Name: "LHB-DISCONNECTED", Address: mustAddress(t, disconnectedAddress), Present: true,
	}
	manager.stations[disconnectedAddress] = disconnected
	manager.bluetoothOps.stationConnected = func(station *internalbluetooth.BaseStation) bool {
		_, connected := connectedStations[station]
		return connected
	}
	readStarted := make(chan struct{}, len(connectedAddresses))
	releaseReads := make(chan struct{})
	manager.bluetoothOps.readPowerStateContext = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		readStarted <- struct{}{}
		select {
		case <-releaseReads:
		case <-manager.shutdownCh:
		}
		return nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.CheckAllStationStatuses()
		result <- err
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("status read did not acquire a GATT slot")
	}
	if err := manager.beginForegroundStationOperation("11:22:33:44:55:76"); err != nil {
		t.Fatalf("foreground operation could not use the reserved GATT slot: %v", err)
	}
	manager.endStationOperation("11:22:33:44:55:76")
	close(releaseReads)
	if err := <-result; err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
}

func TestStatusRecoveryProcessesScheduleRequestedDuringActiveRound(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	firstAddress := "11:22:33:44:55:61"
	secondAddress := "11:22:33:44:55:62"
	manager.stations[firstAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY-1", Address: mustAddress(t, firstAddress), Present: true,
	}
	manager.statusRetries[firstAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}

	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	var calls atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-firstRelease
		}
		return nil
	}

	manager.scheduleStatusRecovery()
	<-firstStarted
	manager.stationsMutex.Lock()
	manager.stations[secondAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY-2", Address: mustAddress(t, secondAddress), Present: true,
	}
	manager.stationsMutex.Unlock()
	manager.statusRetryMutex.Lock()
	manager.statusRetries[secondAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	manager.statusRetryMutex.Unlock()
	manager.scheduleStatusRecovery()
	close(firstRelease)

	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("recovery calls = %d, want 2", got)
	}
}

func TestAbsentStationRecoveryStopsAfterBoundedFailures(t *testing.T) {
	const limit = 3
	cfg := config.NewConfig()
	cfg.AbsentStationRetryLimit = limit
	manager := NewManager(cfg)
	defer manager.Shutdown()
	address := "11:22:33:44:55:79"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-GONE", Address: mustAddress(t, address), Present: false,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		failures: limit - 1,
		kinds:    statusRetryConnection,
		nextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return errors.New("station remains unreachable")
	}

	manager.runStatusRecoveryRound()

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("absent station remained scheduled after the bounded failure limit")
	}
}

func TestRecoveryReadDeadlineDoesNotDisconnectStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	address := "11:22:33:44:55:7A"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-SLOW", Address: mustAddress(t, address), Present: false,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:  statusRetryConnection,
		nextAt: time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	// A read-budget deadline is not evidence the link is broken; a slow but
	// reachable station must not be disconnected for it.
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return fmt.Errorf("read power: %w", context.DeadlineExceeded)
	}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}

	manager.runStatusRecoveryRound()

	if got := disconnects.Load(); got != 0 {
		t.Fatalf("disconnect calls after a read-budget deadline = %d, want 0", got)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked {
		t.Fatal("deadline failure did not leave the station scheduled for retry")
	}
	if retry.failures == 0 {
		t.Fatal("deadline failure did not record a backoff failure")
	}
}

func TestStatusRefreshBareDeadlineDoesNotDisconnectStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:7B"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-SLOW-STATUS", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	manager.bluetoothOps.readPowerStateContext = func(context.Context, *internalbluetooth.BaseStation) error {
		// ReadPowerStateContext can return this bare error when its context
		// expires before the function begins its structured reads.
		return context.DeadlineExceeded
	}
	var disconnects atomic.Int32
	manager.bluetoothOps.disconnectStation = func(*internalbluetooth.BaseStation) error {
		disconnects.Add(1)
		return nil
	}
	releaseRecovery := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		select {
		case <-releaseRecovery:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer manager.Shutdown()
	defer close(releaseRecovery)

	_, err := manager.CheckAllStationStatuses()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CheckAllStationStatuses() error = %v, want context deadline", err)
	}
	if got := disconnects.Load(); got != 0 {
		t.Fatalf("disconnect calls after a bare status deadline = %d, want 0", got)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || retry.failures == 0 || effectiveStatusRetryKinds(retry)&statusRetryRefresh == 0 {
		t.Fatalf("status deadline retry = %+v, tracked=%v; want backoff plus pending refresh", retry, tracked)
	}
}

func TestStatusFailureSchedulesRecoveryWithoutStatusPolling(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRetryBase = 5 * time.Millisecond
	manager.statusRetryMax = 20 * time.Millisecond
	address := "11:22:33:44:55:71"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-AUTOMATIC", Address: mustAddress(t, address), Present: true,
	}
	recovered := make(chan struct{}, 1)
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered <- struct{}{}
		return nil
	}

	manager.noteStatusFailure(address)

	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("status failure did not trigger recovery without a polling request")
	}
}

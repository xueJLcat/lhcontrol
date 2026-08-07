package station

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

func TestOperationCoordinator(t *testing.T) {
	manager := NewManager(config.NewConfig())

	if err := manager.beginOperation(); err != nil {
		t.Fatalf("first operation should start: %v", err)
	}
	if !manager.IsBusy() {
		t.Fatal("manager should report busy while an operation owns the coordinator")
	}
	if err := manager.beginOperation(); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("second operation should be rejected with ErrOperationInProgress, got %v", err)
	}

	manager.endOperation()
	if manager.IsBusy() {
		t.Fatal("manager should report idle after the operation ends")
	}
}

func TestStationOperationsRejectDuplicateAndLimitConcurrency(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginStationOperation("AA"); err != nil {
		t.Fatalf("first station operation error = %v", err)
	}
	if err := manager.beginStationOperation("aa"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("duplicate station operation error = %v", err)
	}
	if err := manager.beginStationOperation("BB"); err != nil {
		t.Fatalf("second independent operation error = %v", err)
	}
	if err := manager.beginStationOperation("CC"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("third concurrent operation error = %v", err)
	}
	manager.endStationOperation("BB")
	manager.endStationOperation("AA")
	if manager.IsBusy() {
		t.Fatal("manager remained busy after station operations ended")
	}
}

func TestEndStationOperationWithoutBeginIsHarmless(t *testing.T) {
	manager := NewManager(config.NewConfig())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.endStationOperation("MISSING")
		manager.endStationOperation("MISSING")
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("endStationOperation blocked without a matching begin")
	}
	if err := manager.beginStationOperation("AA"); err != nil {
		t.Fatalf("first device slot unavailable after defensive end: %v", err)
	}
	if err := manager.beginStationOperation("BB"); err != nil {
		t.Fatalf("second device slot unavailable after defensive end: %v", err)
	}
	if err := manager.beginStationOperation("CC"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("slot limit was broken by defensive end: %v", err)
	}
	manager.endStationOperation("AA")
	manager.endStationOperation("BB")
	if manager.IsBusy() {
		t.Fatal("manager remained busy after station operations ended")
	}
}

func TestShutdownCannotMissScanAfterReadinessCheck(t *testing.T) {
	manager := NewManager(config.NewConfig())
	ready := make(chan struct{})
	release := make(chan struct{})
	manager.scanReadyHook = func() {
		close(ready)
		<-release
	}
	var scanCalls atomic.Int32
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		scanCalls.Add(1)
		return nil, nil
	}

	result := make(chan error, 1)
	go func() {
		_, err := manager.ScanAndFetchStations()
		result <- err
	}()
	<-ready
	manager.BeginShutdown()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("ScanAndFetchStations() error = %v, want ErrShuttingDown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not leave readiness handoff after shutdown")
	}
	if scanCalls.Load() != 0 {
		t.Fatalf("platform scan started %d time(s) after shutdown", scanCalls.Load())
	}
	manager.Shutdown()
}

func TestStatusCheckDefersBusyStationAndContinuesOtherReads(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	addresses := []string{"11:22:33:44:55:61", "11:22:33:44:55:62"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name:    "LHB-" + address[len(address)-2:],
			Address: mustAddress(t, address),
			Present: true,
		}
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool {
		return true
	}
	var readAddressesMutex sync.Mutex
	readAddresses := make([]string, 0, 1)
	manager.bluetoothOps.readPowerStateContext = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		readAddressesMutex.Lock()
		readAddresses = append(readAddresses, station.Snapshot().Address)
		readAddressesMutex.Unlock()
		return nil
	}

	if err := manager.beginStationOperation(addresses[0]); err != nil {
		t.Fatalf("reserve busy station: %v", err)
	}

	if _, err := manager.CheckAllStationStatuses(); err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	readAddressesMutex.Lock()
	if len(readAddresses) != 1 || readAddresses[0] != addresses[1] {
		readAddressesMutex.Unlock()
		t.Fatalf("status reads = %v, want only %s", readAddresses, addresses[1])
	}
	readAddressesMutex.Unlock()
	manager.statusRetryMutex.Lock()
	retry, busyTracked := manager.statusRetries[addresses[0]]
	manager.statusRetryMutex.Unlock()
	if !busyTracked || effectiveStatusRetryKinds(retry) != statusRetryRefresh {
		t.Fatalf("busy station retry = %+v, tracked=%v; want refresh-only", retry, busyTracked)
	}

	manager.endStationOperation(addresses[0])
	var recovered atomic.Int32
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		recovered.Add(1)
		return nil
	}
	manager.runStatusRecoveryRound()
	manager.statusRetryMutex.Lock()
	_, busyTracked = manager.statusRetries[addresses[0]]
	manager.statusRetryMutex.Unlock()
	if recovered.Load() != 1 || busyTracked {
		t.Fatalf("deferred refresh recovered=%d, still tracked=%v", recovered.Load(), busyTracked)
	}
}

func TestForegroundCancelsStatusReadOnSameStation(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:60"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name:    "LHB-60",
		Address: mustAddress(t, address),
		Present: true,
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool {
		return true
	}
	statusStarted := make(chan struct{})
	manager.bluetoothOps.readPowerStateContext = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(statusStarted)
		<-ctx.Done()
		return ctx.Err()
	}

	statusDone := make(chan error, 1)
	go func() {
		_, err := manager.CheckAllStationStatuses()
		statusDone <- err
	}()
	<-statusStarted

	foregroundDone := make(chan error, 1)
	go func() {
		err := manager.beginForegroundStationOperation(address)
		if err == nil {
			manager.endStationOperation(address)
		}
		foregroundDone <- err
	}()
	if err := <-statusDone; err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("preempted status read retry kinds = %v, want refresh retry", retry.kinds)
	}
	select {
	case err := <-foregroundDone:
		if err != nil {
			t.Fatalf("foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground operation did not continue after the status read completed")
	}
}

func TestForegroundGlobalOperationCancelsStatusRefresh(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:64"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-64", Address: mustAddress(t, address), Present: true,
	}
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	statusStarted := make(chan struct{})
	manager.bluetoothOps.readPowerStateContext = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(statusStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	statusResult := make(chan error, 1)
	go func() {
		_, err := manager.CheckAllStationStatuses()
		statusResult <- err
	}()
	<-statusStarted

	globalResult := make(chan error, 1)
	go func() {
		err := manager.beginForegroundGlobalOperation()
		if err == nil {
			manager.endForegroundGlobalOperation()
		}
		globalResult <- err
	}()
	if err := <-statusResult; err != nil {
		t.Fatalf("CheckAllStationStatuses() error = %v", err)
	}
	manager.statusRetryMutex.Lock()
	retry := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if retry.kinds&statusRetryRefresh == 0 {
		t.Fatalf("preempted status refresh retry kinds = %v, want refresh retry", retry.kinds)
	}
	select {
	case err := <-globalResult:
		if err != nil {
			t.Fatalf("global foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("global foreground operation did not continue after status refresh")
	}
}

func TestForegroundGlobalOperationWaitsForRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}
	globalResult := make(chan error, 1)
	go func() {
		err := manager.beginForegroundGlobalOperation()
		if err == nil {
			manager.endForegroundGlobalOperation()
		}
		globalResult <- err
	}()
	select {
	case err := <-globalResult:
		t.Fatalf("global foreground operation returned before recovery completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	manager.endRecoveryStationOperation("RECOVERY")
	select {
	case err := <-globalResult:
		if err != nil {
			t.Fatalf("global foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("global foreground operation did not continue after recovery")
	}
}

func TestStatusRecoveryBackfillsBusyCandidatesAndLimitsConcurrency(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	now := time.Now()
	addresses := []string{"11:22:33:44:55:61", "11:22:33:44:55:62", "11:22:33:44:55:63"}
	for _, address := range addresses {
		manager.stations[address] = &internalbluetooth.BaseStation{
			Name:    "LHB-" + address[len(address)-2:],
			Address: mustAddress(t, address),
			Present: true,
		}
		manager.statusRetries[address] = statusRetry{nextAt: now.Add(-time.Second)}
	}
	if err := manager.beginStationOperation(addresses[0]); err != nil {
		t.Fatalf("reserve busy station: %v", err)
	}
	defer manager.endStationOperation(addresses[0])

	var active atomic.Int32
	var maximum atomic.Int32
	var recoveredMutex sync.Mutex
	recovered := make([]string, 0, 2)
	manager.bluetoothOps.fetchInitialPowerState = func(_ context.Context, station *internalbluetooth.BaseStation) error {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		recoveredMutex.Lock()
		recovered = append(recovered, station.Snapshot().Address)
		recoveredMutex.Unlock()
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		return nil
	}

	manager.scheduleStatusRecovery()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recoveredMutex.Lock()
		complete := len(recovered) == 2
		recoveredMutex.Unlock()
		if complete {
			break
		}
		time.Sleep(time.Millisecond)
	}
	recoveredMutex.Lock()
	defer recoveredMutex.Unlock()
	if len(recovered) != 2 || recovered[0] != addresses[1] || recovered[1] != addresses[2] {
		t.Fatalf("recovered addresses = %v, want busy candidate skipped and remaining candidates recovered in order", recovered)
	}
	if maximum.Load() > 1 {
		t.Fatalf("recovery concurrency = %d, want at most 1", maximum.Load())
	}
}

func TestStatusRecoveryLeavesOneSlotForForegroundWork(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	recoveryAddress := "11:22:33:44:55:61"
	foregroundAddress := "11:22:33:44:55:62"
	manager.stations[recoveryAddress] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY", Address: mustAddress(t, recoveryAddress), Present: true,
	}
	manager.statusRetries[recoveryAddress] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	recoveryStarted := make(chan struct{})
	recoveryRelease := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		close(recoveryStarted)
		<-recoveryRelease
		return nil
	}

	manager.scheduleStatusRecovery()
	<-recoveryStarted
	if err := manager.beginStationOperation(foregroundAddress); err != nil {
		t.Fatalf("foreground operation could not use reserved slot: %v", err)
	}
	manager.endStationOperation(foregroundAddress)
	close(recoveryRelease)
	deadline := time.Now().Add(time.Second)
	for manager.statusRecoveryRunning.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.statusRecoveryRunning.Load() {
		t.Fatal("recovery did not finish")
	}
}

func TestShutdownCancelsBlockingStatusRecoveryRead(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.initialReadTimeout = time.Hour
	address := "11:22:33:44:55:63"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-RECOVERY", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{nextAt: time.Now().Add(-time.Second)}
	manager.statusRetryMutex.Unlock()
	readStarted := make(chan struct{})
	readCancelled := make(chan struct{})
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		close(readStarted)
		<-ctx.Done()
		close(readCancelled)
		return ctx.Err()
	}
	manager.scheduleStatusRecovery()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("status recovery did not start")
	}

	shutdownDone := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not cancel the background recovery read")
	}
	select {
	case <-readCancelled:
	default:
		t.Fatal("Shutdown returned before the recovery read observed cancellation")
	}
}

func TestSecondForegroundOperationWaitsForHiddenRecoverySlot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("beginRecoveryStationOperation() error = %v", err)
	}
	if err := manager.beginForegroundStationOperation("FIRST"); err != nil {
		t.Fatalf("first foreground operation error = %v", err)
	}

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- manager.beginForegroundStationOperation("SECOND")
	}()
	select {
	case err := <-secondResult:
		t.Fatalf("second foreground operation returned before recovery ended: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	manager.endRecoveryStationOperation("RECOVERY")
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatalf("second foreground operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second foreground operation did not acquire the released recovery slot")
	}
	manager.endStationOperation("SECOND")
	manager.endStationOperation("FIRST")
}

func TestForegroundRecoveryWaitHonorsOperationDeadline(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	if err := manager.beginStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve foreground slot: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := manager.beginForegroundStationOperationContext(ctx, "SECOND")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("foreground recovery wait error = %v, want context deadline", err)
	}

	manager.endRecoveryStationOperation("RECOVERY")
	manager.endStationOperation("FIRST")
}

func TestForegroundRetriesWhenRecoveryEndsDuringSlotMiss(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	if err := manager.beginStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve foreground slot: %v", err)
	}
	defer manager.endStationOperation("FIRST")

	var hookOnce sync.Once
	manager.foregroundSlotMissHook = func() {
		hookOnce.Do(func() {
			manager.endRecoveryStationOperation("RECOVERY")
		})
	}
	if err := manager.beginForegroundStationOperation("SECOND"); err != nil {
		t.Fatalf("foreground operation did not retry released recovery slot: %v", err)
	}
	manager.endStationOperation("SECOND")
}

func TestForegroundWaitingForRecoveryReturnsOnShutdown(t *testing.T) {
	manager := NewManager(config.NewConfig())
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	if err := manager.beginStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve foreground slot: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		result <- manager.beginForegroundStationOperation("SECOND")
	}()
	time.Sleep(20 * time.Millisecond)
	manager.BeginShutdown()
	select {
	case err := <-result:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("foreground error = %v, want ErrShuttingDown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wake foreground recovery waiter")
	}
	manager.endRecoveryStationOperation("RECOVERY")
	manager.endStationOperation("FIRST")
	manager.Shutdown()
}

func TestForegroundCancelsReadOnlyRecoveryToAcquireSlot(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	if err := manager.beginRecoveryStationOperation("RECOVERY"); err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	manager.recoveryOperationMutex.Lock()
	recoveryContext := manager.recoveryContext
	manager.recoveryOperationMutex.Unlock()
	recoveryCancelled := make(chan struct{})
	go func() {
		<-recoveryContext.Done()
		close(recoveryCancelled)
		manager.endRecoveryStationOperation("RECOVERY")
	}()
	if err := manager.beginForegroundStationOperation("FIRST"); err != nil {
		t.Fatalf("reserve first foreground slot: %v", err)
	}
	defer manager.endStationOperation("FIRST")

	started := time.Now()
	if err := manager.beginForegroundStationOperation("SECOND"); err != nil {
		t.Fatalf("second foreground operation: %v", err)
	}
	defer manager.endStationOperation("SECOND")
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("foreground waited %v for read-only recovery to yield", elapsed)
	}
	select {
	case <-recoveryCancelled:
	default:
		t.Fatal("foreground slot pressure did not cancel read-only recovery")
	}
}

func TestPreemptedRecoveryMovesBehindOtherDueCandidates(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	first := "11:22:33:44:99:01"
	second := "11:22:33:44:99:02"
	due := time.Now().Add(-time.Second)
	manager.statusRetries[first] = statusRetry{kinds: statusRetryConnection, nextAt: due}
	manager.statusRetries[second] = statusRetry{kinds: statusRetryConnection, nextAt: due}

	manager.deferStatusRecovery(first, 250*time.Millisecond)
	manager.statusRetryMutex.Lock()
	_, _, firstNext := statusRetryOrder(manager.statusRetries[first])
	_, _, secondNext := statusRetryOrder(manager.statusRetries[second])
	manager.statusRetryMutex.Unlock()
	if !secondNext.Before(firstNext) {
		t.Fatalf("preempted candidate nextAt=%v, other nextAt=%v; want other candidate first", firstNext, secondNext)
	}
}

package station

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	internalbluetooth "lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"

	tinybluetooth "tinygo.org/x/bluetooth"
)

func TestScanMetadataFailureSchedulesSilentRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:67"
	manager.bluetoothOps.scanForDurationContext = func(context.Context, time.Duration) ([]internalbluetooth.DiscoveredStation, error) {
		return []internalbluetooth.DiscoveredStation{{
			Name: "LHB-METADATA", Address: mustAddress(t, address),
		}}, nil
	}
	metadataErr := errors.New("firmware metadata unavailable")
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Metadata: metadataErr}
	}

	if _, err := manager.ScanAndFetchStations(); err != nil {
		t.Fatalf("ScanAndFetchStations() error = %v", err)
	}
	if status := manager.GetScanStatus(); len(status.Warnings) != 0 {
		t.Fatalf("metadata-only failure produced scan warnings: %+v", status)
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryMetadata ||
		retry.metadataFailures != 1 || retry.metadataNextAt.IsZero() {
		t.Fatalf("metadata retry = %+v tracked=%v", retry, tracked)
	}
}

func TestMetadataRecoveryForcesCapabilityRefresh(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:68"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-METADATA", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:            statusRetryMetadata,
		metadataFailures: 1,
		metadataNextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	var refreshes atomic.Int32
	var reads atomic.Int32
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		refreshes.Add(1)
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		reads.Add(1)
		return nil
	}

	manager.runStatusRecoveryRound()

	if refreshes.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("metadata recovery refreshes=%d reads=%d, want one of each", refreshes.Load(), reads.Load())
	}
	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("successful metadata recovery remained scheduled")
	}
}

func TestMetadataRecoveryFailureKeepsIndependentBackoff(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRetryBase = time.Hour
	manager.statusRetryMax = 4 * time.Hour
	address := "11:22:33:44:55:69"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-METADATA", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:            statusRetryMetadata,
		metadataFailures: 1,
		metadataNextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		return internalbluetooth.Capabilities{}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Metadata: errors.New("metadata still unavailable")}
	}

	manager.runStatusRecoveryRound()

	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryMetadata ||
		retry.metadataFailures != 2 || retry.metadataNextAt.Sub(retry.metadataLastAttempt) != 2*time.Hour {
		t.Fatalf("metadata retry after failure = %+v tracked=%v", retry, tracked)
	}
}

func TestMetadataRecoveryKeepsIndependentStatusReadBudget(t *testing.T) {
	manager := NewManager(config.NewConfig())
	defer manager.Shutdown()
	manager.statusRetryBase = time.Hour
	manager.initialReadTimeout = 300 * time.Millisecond
	address := "11:22:33:44:55:85"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-META-BUDGET", Address: mustAddress(t, address), Present: true,
	}
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:          statusRetryMetadata,
		metadataNextAt: time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	var reads atomic.Int32
	manager.bluetoothOps.refreshCapabilities = func(context.Context, *internalbluetooth.BaseStation) (internalbluetooth.Capabilities, error) {
		time.Sleep(200 * time.Millisecond)
		return internalbluetooth.Capabilities{DeviceInformation: true}, nil
	}
	manager.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *internalbluetooth.BaseStation) error {
		reads.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 250*time.Millisecond {
			return context.DeadlineExceeded
		}
		return nil
	}

	manager.runStatusRecoveryRound()

	if reads.Load() != 1 {
		t.Fatalf("status reads = %d, want 1", reads.Load())
	}
	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatalf("slow metadata refresh poisoned the status read budget: %+v", retry)
	}
}

func TestUnsupportedMetadataFailureDoesNotScheduleRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	address := "11:22:33:44:55:6A"

	manager.recordMetadataReadResult(address, &internalbluetooth.UnsupportedCapabilityError{
		Capability: "metadata read",
		Err:        tinybluetooth.ErrAttReadNotPermitted,
	})

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatal("permanently unsupported metadata read scheduled recovery")
	}
}

func TestMetadataRecoveryStopsAfterBoundedFailures(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:6B"
	for attempt := 0; attempt < metadataRetryLimit; attempt++ {
		manager.recordMetadataReadResult(address, errors.New("metadata unavailable"))
	}

	manager.statusRetryMutex.Lock()
	_, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked {
		t.Fatalf("metadata recovery remained scheduled after %d failures", metadataRetryLimit)
	}
}

func TestConnectedStaleMetadataErrorDoesNotRelightExhaustedRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:6C"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-STALE", Address: mustAddress(t, address), Present: true,
	}
	// Metadata retries were previously exhausted and dropped; a channel
	// retry is due. The station stays connected, so the fetch takes the
	// "already good" path and can only report the stale cached metadata
	// error of an old discovery — it must not relight metadata recovery.
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return true }
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:           statusRetryChannel,
		channelFailures: 1,
		channelNextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Metadata: errors.New("stale metadata error")}
	}

	manager.runStatusRecoveryRound()

	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if tracked && effectiveStatusRetryKinds(retry)&statusRetryMetadata != 0 {
		t.Fatalf("stale metadata error relight metadata recovery: %+v", retry)
	}
}

func TestDisconnectedFreshMetadataErrorStillRelightsRecovery(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.statusRecoveryStart.Do(func() {})
	address := "11:22:33:44:55:6D"
	manager.stations[address] = &internalbluetooth.BaseStation{
		Name: "LHB-FRESH", Address: mustAddress(t, address), Present: true,
	}
	// Disconnected start: the fetch reconnects and performs a fresh
	// discovery, so its metadata error is new evidence and still relights
	// metadata recovery after the previous schedule was exhausted.
	manager.bluetoothOps.stationConnected = func(*internalbluetooth.BaseStation) bool { return false }
	manager.statusRetryMutex.Lock()
	manager.statusRetries[address] = statusRetry{
		kinds:           statusRetryChannel,
		channelFailures: 1,
		channelNextAt:   time.Now().Add(-time.Second),
	}
	manager.statusRetryMutex.Unlock()
	manager.bluetoothOps.fetchInitialPowerState = func(context.Context, *internalbluetooth.BaseStation) error {
		return &internalbluetooth.InitialReadError{Metadata: errors.New("fresh metadata error")}
	}

	manager.runStatusRecoveryRound()

	manager.statusRetryMutex.Lock()
	retry, tracked := manager.statusRetries[address]
	manager.statusRetryMutex.Unlock()
	if !tracked || effectiveStatusRetryKinds(retry)&statusRetryMetadata == 0 ||
		retry.metadataFailures != 1 {
		t.Fatalf("fresh metadata error did not relight recovery: %+v tracked=%v", retry, tracked)
	}
}

func TestAdapterUnavailableForcesInitializationRetry(t *testing.T) {
	manager := NewManager(config.NewConfig())
	manager.observeBluetoothError(tinybluetooth.ErrRadioNotAvailable)
	attempts := 0
	manager.initializeBluetooth = func() error {
		attempts++
		return nil
	}
	manager.nextInitializeAt = time.Now().Add(-time.Second)
	if err := manager.ensureReady(); err != nil {
		t.Fatalf("ensureReady() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("initialization attempts = %d, want 1", attempts)
	}
}

func TestMetadataFreshnessExpires(t *testing.T) {
	manager := NewManager(config.NewConfig())
	now := time.Now()
	manager.stations["fresh"] = &internalbluetooth.BaseStation{
		Name: "fresh", Address: mustAddress(t, "11:22:33:44:55:82"), MetadataReadAt: now,
	}
	manager.stations["stale"] = &internalbluetooth.BaseStation{
		Name: "stale", Address: mustAddress(t, "11:22:33:44:55:83"),
		Metadata:       internalbluetooth.DeviceMetadata{FirmwareRevision: "1.2.3"},
		MetadataReadAt: now.Add(-metadataFreshnessWindow - time.Minute),
	}

	infos := manager.GetStationInfo()
	freshness := make(map[string]bool, len(infos))
	for _, info := range infos {
		freshness[info.Name] = info.MetadataFresh
	}
	if !freshness["fresh"] {
		t.Fatal("recent metadata was marked stale")
	}
	if freshness["stale"] {
		t.Fatal("expired metadata was marked fresh")
	}
}

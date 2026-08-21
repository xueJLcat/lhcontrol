package station

import (
	"errors"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"testing"
	"time"
)

func TestApplyRecoverySettingsRebasesQueuedSchedules(t *testing.T) {
	cfg := config.NewConfig()
	cfg.RecoveryRetryBaseSeconds = 5
	cfg.RecoveryRetryMaxSeconds = 60
	manager := NewManager(cfg)
	t.Cleanup(manager.Shutdown)

	address := "AA:BB:CC:DD:EE:01"
	lastAttempt := time.Now()
	refreshAt := lastAttempt.Add(45 * time.Second)
	manager.statusRetries[address] = statusRetry{
		kinds:               statusRetryConnection | statusRetryChannel | statusRetryMetadata | statusRetryRefresh,
		failures:            3,
		lastAttempt:         lastAttempt,
		nextAt:              lastAttempt.Add(5 * time.Minute),
		channelFailures:     2,
		channelLastAttempt:  lastAttempt,
		channelNextAt:       lastAttempt.Add(5 * time.Minute),
		metadataFailures:    1,
		metadataLastAttempt: lastAttempt,
		metadataNextAt:      lastAttempt.Add(5 * time.Minute),
		refreshNextAt:       refreshAt,
	}

	manager.ApplyRecoverySettings()

	retry := manager.statusRetrySnapshot(address)
	if got := retry.nextAt.Sub(lastAttempt); got != 20*time.Second {
		t.Fatalf("connection retry delay = %v, want 20s", got)
	}
	if got := retry.channelNextAt.Sub(lastAttempt); got != 10*time.Second {
		t.Fatalf("channel retry delay = %v, want 10s", got)
	}
	if got := retry.metadataNextAt.Sub(lastAttempt); got != 5*time.Second {
		t.Fatalf("metadata retry delay = %v, want 5s", got)
	}
	if !retry.refreshNextAt.Equal(refreshAt) {
		t.Fatalf("refresh retry changed from %v to %v", refreshAt, retry.refreshNextAt)
	}
}

func TestApplyRecoverySettingsPrunesNewlyExhaustedAbsentKinds(t *testing.T) {
	cfg := config.NewConfig()
	cfg.AbsentStationRetryLimit = 2
	manager := NewManager(cfg)
	t.Cleanup(manager.Shutdown)

	address := "AA:BB:CC:DD:EE:02"
	manager.stations[address] = &bluetooth.BaseStation{Address: mustAddress(t, address), Present: false}
	lastAttempt := time.Now()
	manager.statusRetries[address] = statusRetry{
		kinds:              statusRetryConnection | statusRetryChannel,
		failures:           2,
		lastAttempt:        lastAttempt,
		nextAt:             lastAttempt.Add(time.Minute),
		channelFailures:    1,
		channelLastAttempt: lastAttempt,
		channelNextAt:      lastAttempt.Add(time.Minute),
	}

	manager.ApplyRecoverySettings()

	retry, tracked := manager.statusRetries[address]
	if !tracked || effectiveStatusRetryKinds(retry) != statusRetryChannel {
		t.Fatalf("retry after lowering absent limit = %+v tracked=%v, want channel-only", retry, tracked)
	}
	if retry.failures != 0 || !retry.nextAt.IsZero() {
		t.Fatalf("exhausted connection schedule was not cleared: %+v", retry)
	}
	if got := retry.channelNextAt.Sub(lastAttempt); got != time.Duration(config.DefaultRecoveryRetryBaseSeconds)*time.Second {
		t.Fatalf("remaining channel retry delay = %v, want configured base", got)
	}
}

func TestReviveAbsentStationRecoveryRestartsOnlyExhaustedAbsentStations(t *testing.T) {
	cfg := config.NewConfig()
	cfg.AbsentStationRetryLimit = 3
	manager := NewManager(cfg)
	manager.shuttingDown.Store(true) // Keep the scheduler dormant while inspecting its queue.
	t.Cleanup(manager.Shutdown)

	disconnectedAddress := "AA:BB:CC:DD:EE:03"
	presentAddress := "AA:BB:CC:DD:EE:04"
	activeAddress := "AA:BB:CC:DD:EE:05"
	manager.stations[disconnectedAddress] = &bluetooth.BaseStation{
		Address: mustAddress(t, disconnectedAddress),
		Present: false,
	}
	manager.stations[presentAddress] = &bluetooth.BaseStation{
		Address: mustAddress(t, presentAddress),
		Present: true,
	}
	manager.stations[activeAddress] = &bluetooth.BaseStation{
		Address: mustAddress(t, activeAddress),
		Present: false,
	}
	lastAttempt := time.Now().Add(-time.Second)
	manager.statusRetries[disconnectedAddress] = statusRetry{
		kinds:       statusRetryConnection,
		failures:    3,
		lastAttempt: lastAttempt,
		nextAt:      lastAttempt.Add(time.Minute),
	}
	activeNextAt := lastAttempt.Add(2 * time.Minute)
	manager.statusRetries[activeAddress] = statusRetry{
		kinds:       statusRetryConnection,
		failures:    2,
		lastAttempt: lastAttempt,
		nextAt:      activeNextAt,
	}

	manager.ApplyRecoverySettings()
	if _, tracked := manager.statusRetries[disconnectedAddress]; tracked {
		t.Fatal("exhausted absent retry remained tracked at the old limit")
	}
	rebasedActiveNextAt := manager.statusRetries[activeAddress].nextAt

	cfg.AbsentStationRetryLimit = 5
	manager.ReviveAbsentStationRecovery()

	retry, tracked := manager.statusRetries[disconnectedAddress]
	wantKinds := statusRetryConnection | statusRetryChannel
	if !tracked || effectiveStatusRetryKinds(retry)&wantKinds != wantKinds {
		t.Fatalf("absent retry = %+v tracked=%v, want revived connection and channel recovery", retry, tracked)
	}
	if retry.failures != 0 || retry.channelFailures != 0 ||
		retry.nextAt.IsZero() || retry.channelNextAt.IsZero() ||
		retry.nextAt.After(time.Now()) || retry.channelNextAt.After(time.Now()) {
		t.Fatalf("revived recovery schedule = %+v, want fresh immediate budgets", retry)
	}
	if _, tracked := manager.statusRetries[presentAddress]; tracked {
		t.Fatal("present station unexpectedly received absent-station recovery")
	}
	activeRetry := manager.statusRetries[activeAddress]
	if effectiveStatusRetryKinds(activeRetry) != statusRetryConnection ||
		activeRetry.failures != 2 || !activeRetry.nextAt.Equal(rebasedActiveNextAt) ||
		!activeRetry.channelNextAt.IsZero() {
		t.Fatalf("active recovery budget was reset: %+v", activeRetry)
	}
}

func TestApplyRecoverySettingsRebasesAdapterInitializationCooldown(t *testing.T) {
	cfg := config.NewConfig()
	cfg.BluetoothInitRetrySeconds = 3
	manager := NewManager(cfg)

	failedAt := time.Now().Add(-time.Second)
	manager.initializeErr = errors.New("adapter unavailable")
	manager.initializeFailedAt = failedAt
	manager.nextInitializeAt = failedAt.Add(30 * time.Second)

	manager.ApplyRecoverySettings()

	if got := manager.nextInitializeAt.Sub(failedAt); got != 3*time.Second {
		t.Fatalf("adapter retry cooldown = %v, want 3s", got)
	}
}

// TestRefreshMarkerRespectsRecordedConnectionBackoff guards the pending-refresh
// invariant: a marker stamped while a connection backoff is already recorded
// must not fall due before that backoff. Recovery picks the earliest due
// schedule, so an immediate marker would re-run the read that just failed,
// negate the backoff, and count failures against the absent-station budget
// ahead of schedule.
func TestRefreshMarkerRespectsRecordedConnectionBackoff(t *testing.T) {
	cfg := config.NewConfig()
	manager := NewManager(cfg)
	manager.shuttingDown.Store(true) // Keep the scheduler dormant while inspecting its queue.
	t.Cleanup(manager.Shutdown)

	address := "AA:BB:CC:DD:EE:06"
	nextAt := time.Now().Add(time.Minute)
	manager.statusRetries[address] = statusRetry{
		kinds:       statusRetryConnection,
		failures:    1,
		lastAttempt: time.Now(),
		nextAt:      nextAt,
	}

	manager.trackStatusRefreshPending(address)

	retry, tracked := manager.statusRetries[address]
	if !tracked {
		t.Fatal("refresh marker dropped the retry entry")
	}
	if retry.refreshNextAt.Before(nextAt) {
		t.Fatalf("refresh marker due at %v falls before the connection backoff at %v", retry.refreshNextAt, nextAt)
	}
	kind, _, _, selectedNextAt := statusRetryOrderAndKind(retry)
	if kind != statusRetryConnection || !selectedNextAt.Equal(nextAt) {
		t.Fatalf("recovery order = kind %v nextAt %v, want the connection backoff to stay the earliest schedule", kind, selectedNextAt)
	}
}

// TestConnectionBackoffClampsEarlierRefreshMarker guards the inverse ordering
// of the pending-refresh invariant: when a refresh marker was recorded before
// a connection backoff, the later backoff must clamp the stale marker. An
// unclamped marker stays due in the past, so recovery would re-run the read
// that just failed and negate the backoff the failure recorded.
func TestConnectionBackoffClampsEarlierRefreshMarker(t *testing.T) {
	cfg := config.NewConfig()
	manager := NewManager(cfg)
	manager.shuttingDown.Store(true) // Keep the scheduler dormant while inspecting its queue.
	t.Cleanup(manager.Shutdown)

	address := "AA:BB:CC:DD:EE:08"
	manager.trackStatusRefreshPending(address)
	retry, tracked := manager.statusRetries[address]
	if !tracked || retry.refreshNextAt.IsZero() {
		t.Fatalf("refresh marker did not create a retry entry: %+v tracked=%v", retry, tracked)
	}

	manager.noteStatusFailureKind(address, statusRetryConnection)

	retry, tracked = manager.statusRetries[address]
	if !tracked {
		t.Fatal("connection backoff dropped the retry entry")
	}
	if retry.refreshNextAt.Before(retry.nextAt) {
		t.Fatalf("refresh marker due at %v falls before the connection backoff at %v", retry.refreshNextAt, retry.nextAt)
	}
	kind, _, _, selectedNextAt := statusRetryOrderAndKind(retry)
	if kind != statusRetryConnection || !selectedNextAt.Equal(retry.nextAt) {
		t.Fatalf("recovery order = kind %v nextAt %v, want the connection backoff to stay the earliest schedule", kind, selectedNextAt)
	}
}

// TestRefreshMarkerWithoutBackoffFallsDueImmediately keeps the no-backoff
// behavior: a pending-refresh marker for a station without a recorded
// connection failure is due immediately so recovery re-reads it without delay.
func TestRefreshMarkerWithoutBackoffFallsDueImmediately(t *testing.T) {
	cfg := config.NewConfig()
	manager := NewManager(cfg)
	manager.shuttingDown.Store(true) // Keep the scheduler dormant while inspecting its queue.
	t.Cleanup(manager.Shutdown)

	address := "AA:BB:CC:DD:EE:07"
	before := time.Now()
	manager.trackStatusRefreshPending(address)

	retry, tracked := manager.statusRetries[address]
	if !tracked {
		t.Fatal("refresh marker did not create a retry entry")
	}
	kind, _, _, selectedNextAt := statusRetryOrderAndKind(retry)
	if kind != statusRetryRefresh {
		t.Fatalf("recovery order = kind %v, want the refresh marker", kind)
	}
	if selectedNextAt.Before(before) || selectedNextAt.After(time.Now()) {
		t.Fatalf("refresh marker due at %v, want immediately", selectedNextAt)
	}
}

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

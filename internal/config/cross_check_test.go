package config

import (
	"testing"
)

// TestSetStationOperationTimeoutComparesSanitizedCrossValues covers a config
// whose coupled timeout fields carry raw zero/out-of-range values: the cross
// validation must compare the sanitized values instead of rejecting a legal
// setting against a corrupted baseline.
func TestSetStationOperationTimeoutComparesSanitizedCrossValues(t *testing.T) {
	cfg := NewConfig()
	cfg.BulkPowerTimeoutSeconds = 0
	cfg.InitialReadTimeoutSeconds = 0

	if err := cfg.SetStationOperationTimeoutSeconds(DefaultStationOperationTimeoutSeconds); err != nil {
		t.Fatalf("SetStationOperationTimeoutSeconds(%d) error = %v, want the sanitized cross values to accept it",
			DefaultStationOperationTimeoutSeconds, err)
	}
	if got := cfg.GetStationOperationTimeoutSeconds(); got != DefaultStationOperationTimeoutSeconds {
		t.Fatalf("station operation timeout = %d, want %d", got, DefaultStationOperationTimeoutSeconds)
	}
}

func TestSetBulkPowerTimeoutComparesSanitizedCrossValues(t *testing.T) {
	cfg := NewConfig()
	// A corrupted raw value above every legal station timeout must not block
	// a legal bulk timeout; the sanitized station timeout is the baseline.
	cfg.StationOperationTimeoutSeconds = MaxStationOperationTimeoutSeconds * 2

	if err := cfg.SetBulkPowerTimeoutSeconds(MinBulkPowerTimeoutSeconds); err != nil {
		t.Fatalf("SetBulkPowerTimeoutSeconds(%d) error = %v, want the sanitized cross values to accept it",
			MinBulkPowerTimeoutSeconds, err)
	}
	if got := cfg.GetBulkPowerTimeoutSeconds(); got != MinBulkPowerTimeoutSeconds {
		t.Fatalf("bulk power timeout = %d, want %d", got, MinBulkPowerTimeoutSeconds)
	}
}

// TestRecoverySettersCompareSanitizedCrossValues covers the recovery and
// timeout setters whose coupled fields carry raw zero/out-of-range values: a
// corrupted companion must not reject a legal setting, matching the rule the
// preference setters already follow.
func TestRecoverySettersCompareSanitizedCrossValues(t *testing.T) {
	for name, scenario := range map[string]struct {
		corrupt func(cfg *Config)
		set     func(cfg *Config) error
		get     func(cfg *Config) int
		value   int
	}{
		"recovery retry base": {
			corrupt: func(cfg *Config) { cfg.RecoveryRetryMaxSeconds = 0 },
			set:     func(cfg *Config) error { return cfg.SetRecoveryRetryBaseSeconds(DefaultRecoveryRetryBaseSeconds) },
			get:     func(cfg *Config) int { return cfg.GetRecoveryRetryBaseSeconds() },
			value:   DefaultRecoveryRetryBaseSeconds,
		},
		"recovery retry maximum": {
			corrupt: func(cfg *Config) { cfg.RecoveryRetryBaseSeconds = MaxRecoveryRetryBaseSeconds * 2 },
			set:     func(cfg *Config) error { return cfg.SetRecoveryRetryMaxSeconds(MinRecoveryRetryMaxSeconds) },
			get:     func(cfg *Config) int { return cfg.GetRecoveryRetryMaxSeconds() },
			value:   MinRecoveryRetryMaxSeconds,
		},
		"initial read timeout": {
			corrupt: func(cfg *Config) {
				cfg.StationOperationTimeoutSeconds = 0
				cfg.ScanReadPhaseTimeoutSeconds = 0
			},
			set:   func(cfg *Config) error { return cfg.SetInitialReadTimeoutSeconds(DefaultInitialReadTimeoutSeconds) },
			get:   func(cfg *Config) int { return cfg.GetInitialReadTimeoutSeconds() },
			value: DefaultInitialReadTimeoutSeconds,
		},
		"scan read phase timeout": {
			corrupt: func(cfg *Config) { cfg.InitialReadTimeoutSeconds = MaxInitialReadTimeoutSeconds * 2 },
			set:     func(cfg *Config) error { return cfg.SetScanReadPhaseTimeoutSeconds(DefaultScanReadPhaseTimeoutSeconds) },
			get:     func(cfg *Config) int { return cfg.GetScanReadPhaseTimeoutSeconds() },
			value:   DefaultScanReadPhaseTimeoutSeconds,
		},
		"status read timeout": {
			corrupt: func(cfg *Config) { cfg.StatusRefreshTimeoutSeconds = 0 },
			set:     func(cfg *Config) error { return cfg.SetStatusReadTimeoutSeconds(DefaultStatusReadTimeoutSeconds) },
			get:     func(cfg *Config) int { return cfg.GetStatusReadTimeoutSeconds() },
			value:   DefaultStatusReadTimeoutSeconds,
		},
		"status refresh timeout": {
			corrupt: func(cfg *Config) { cfg.StatusReadTimeoutSeconds = MaxStatusReadTimeoutSeconds * 2 },
			set:     func(cfg *Config) error { return cfg.SetStatusRefreshTimeoutSeconds(DefaultStatusRefreshTimeoutSeconds) },
			get:     func(cfg *Config) int { return cfg.GetStatusRefreshTimeoutSeconds() },
			value:   DefaultStatusRefreshTimeoutSeconds,
		},
	} {
		t.Run(name, func(t *testing.T) {
			useTemporaryConfigDirectory(t)
			cfg := NewConfig()
			scenario.corrupt(cfg)

			if err := scenario.set(cfg); err != nil {
				t.Fatalf("set %d error = %v, want the sanitized cross values to accept it", scenario.value, err)
			}
			if got := scenario.get(cfg); got != scenario.value {
				t.Fatalf("stored value = %d, want %d", got, scenario.value)
			}
		})
	}
}

// TestRecoverySettersStillRejectIllegalCrossValues guards the companion
// sanitization against over-acceptance: an in-range value that violates the
// coupling invariant against the sanitized companion must still be rejected.
func TestRecoverySettersStillRejectIllegalCrossValues(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if err := cfg.SetRecoveryRetryMaxSeconds(MinRecoveryRetryMaxSeconds); err != nil {
		t.Fatalf("setup: SetRecoveryRetryMaxSeconds(%d) error = %v", MinRecoveryRetryMaxSeconds, err)
	}
	// The retry maximum now sits at its minimum (60s). A base at its own
	// legal maximum (120s) is in range but above the sanitized maximum, so
	// the coupling check must reject it.
	if err := cfg.SetRecoveryRetryBaseSeconds(MaxRecoveryRetryBaseSeconds); err == nil {
		t.Fatalf("SetRecoveryRetryBaseSeconds(%d) unexpectedly succeeded against retry maximum %d",
			MaxRecoveryRetryBaseSeconds, cfg.GetRecoveryRetryMaxSeconds())
	}
}

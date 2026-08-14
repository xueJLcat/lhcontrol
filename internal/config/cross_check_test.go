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

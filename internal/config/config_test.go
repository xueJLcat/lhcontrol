package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicallyCreatesAndReplacesFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")

	if err := writeFileAtomically(path, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatalf("initial writeFileAtomically() error = %v", err)
	}
	if err := writeFileAtomically(path, []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatalf("replacement writeFileAtomically() error = %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(content), `{"version":2}`; got != want {
		t.Fatalf("config content = %q, want %q", got, want)
	}

	temporaryFiles, err := filepath.Glob(filepath.Join(directory, ".lhcontrol-config-*.tmp"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary config files were not cleaned up: %v", temporaryFiles)
	}
}

func TestRenameRollsBackInMemoryWhenPersistenceFails(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { configFileWriter = originalWriter })

	cfg := NewConfig()
	cfg.RenamedStations["LHB-OLD"] = "Before"
	if err := cfg.SetRenamedStation("LHB-OLD", "After"); err == nil {
		t.Fatal("SetRenamedStation() unexpectedly succeeded")
	}
	if got, _ := cfg.GetRenamedStation("LHB-OLD"); got != "Before" {
		t.Fatalf("in-memory name = %q, want rollback value", got)
	}
}

func TestAddressRenameTakesPriorityOverLegacyName(t *testing.T) {
	cfg := NewConfig()
	cfg.RenamedStations["LHB-OLD"] = "Legacy name"
	cfg.RenamedStationsByAddress["11:22:33:44:55:66"] = "Address name"

	if got, ok := cfg.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); !ok || got != "Address name" {
		t.Fatalf("address rename = %q, %v", got, ok)
	}
	if got, ok := cfg.GetStationDisplayName("AA:BB:CC:DD:EE:FF", "LHB-OLD"); !ok || got != "Legacy name" {
		t.Fatalf("legacy fallback rename = %q, %v", got, ok)
	}
}

func TestResetAddressRenameAlsoRemovesLegacyFallback(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	cfg := NewConfig()
	cfg.RenamedStations["LHB-OLD"] = "Legacy name"
	cfg.RenamedStationsByAddress["11:22:33:44:55:66"] = "Address name"

	if err := cfg.SetRenamedStationByAddress("11:22:33:44:55:66", "LHB-OLD", ""); err != nil {
		t.Fatalf("SetRenamedStationByAddress() error = %v", err)
	}
	if got, ok := cfg.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); ok {
		t.Fatalf("reset rename unexpectedly returned %q", got)
	}
}

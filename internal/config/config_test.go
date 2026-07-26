package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestResetAddressRenameOnlySuppressesLegacyFallbackForThatDevice(t *testing.T) {
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
	if got, ok := cfg.GetStationDisplayName("AA:BB:CC:DD:EE:FF", "LHB-OLD"); !ok || got != "Legacy name" {
		t.Fatalf("reset removed another device's legacy alias: %q, %v", got, ok)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Config.Load() error = %v", err)
	}
	if got, ok := reloaded.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); ok {
		t.Fatalf("reloaded reset rename unexpectedly returned %q", got)
	}
	if got, ok := reloaded.GetStationDisplayName("AA:BB:CC:DD:EE:FF", "LHB-OLD"); !ok || got != "Legacy name" {
		t.Fatalf("reloaded config lost shared legacy alias: %q, %v", got, ok)
	}
}

func TestInvalidConfigIsPreservedBeforeLaterSave(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	invalidContent := []byte(`{"renamedStations":{"LHB-OLD":"Before"}`)
	if err := os.WriteFile(configPath, invalidContent, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := NewConfig()
	loadErr := cfg.Load()
	if loadErr == nil || !strings.Contains(loadErr.Error(), "invalid file preserved") {
		t.Fatalf("Config.Load() error = %v, want preserved invalid-file error", loadErr)
	}
	preserved, err := filepath.Glob(configPath + ".invalid-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(preserved) != 1 {
		t.Fatalf("preserved invalid files = %v, want exactly one", preserved)
	}
	content, err := os.ReadFile(preserved[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(invalidContent) {
		t.Fatalf("preserved content = %q, want %q", content, invalidContent)
	}
	if err := cfg.SetRenamedStation("LHB-NEW", "After"); err != nil {
		t.Fatalf("SetRenamedStation() error = %v", err)
	}
	if _, err := os.Stat(preserved[0]); err != nil {
		t.Fatalf("later save removed preserved invalid config: %v", err)
	}
}

func TestInvalidConfigBlocksSaveWhenItCannotBePreserved(t *testing.T) {
	cfg := NewConfig()
	cfg.persistenceBlockedErr = errors.New("rename denied")
	if err := cfg.SetRenamedStation("LHB-OLD", "After"); err == nil || !strings.Contains(err.Error(), "save blocked") {
		t.Fatalf("SetRenamedStation() error = %v, want blocked save", err)
	}
	if _, ok := cfg.GetRenamedStation("LHB-OLD"); ok {
		t.Fatal("failed blocked save retained the in-memory rename")
	}
}

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lhcontrol/internal/autosleep"
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
	if err := cfg.PersistenceError(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("PersistenceError() = %v, want write failure", err)
	}
}

func TestSuccessfulSaveClearsPreviousPersistenceFailure(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { configFileWriter = originalWriter })

	cfg := NewConfig()
	if err := cfg.Save(); err == nil {
		t.Fatal("Save() unexpectedly succeeded")
	}
	configFileWriter = originalWriter
	if err := cfg.Save(); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	if err := cfg.PersistenceError(); err != nil {
		t.Fatalf("successful save retained persistence error: %v", err)
	}
}

func TestLoadPathFailureMarksPersistenceUnavailable(t *testing.T) {
	blockedRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedRoot, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AppData", blockedRoot)

	cfg := NewConfig()
	loadErr := cfg.Load()
	if loadErr == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
	persistenceErr := cfg.PersistenceError()
	if persistenceErr == nil || !errors.Is(persistenceErr, loadErr) {
		t.Fatalf("PersistenceError() = %v, want load path error %v", persistenceErr, loadErr)
	}
}

func TestLoadReadFailureBlocksSavesToProtectExistingConfig(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	originalReader := configFileReader
	originalWriter := configFileWriter
	readErr := errors.New("file locked by backup software")
	configFileReader = func(string) ([]byte, error) { return nil, readErr }
	writeCalls := 0
	configFileWriter = func(string, []byte, os.FileMode) error {
		writeCalls++
		return nil
	}
	t.Cleanup(func() {
		configFileReader = originalReader
		configFileWriter = originalWriter
	})

	cfg := NewConfig()
	cfg.RenamedStations["LHB-EXISTING"] = "Kept in memory"
	loadErr := cfg.Load()
	if loadErr == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
	if got, _ := cfg.GetRenamedStation("LHB-EXISTING"); got != "Kept in memory" {
		t.Fatalf("read failure wiped in-memory renames: %q", got)
	}

	// While the file is unreadable, saves must be blocked instead of
	// overwriting it with the sparse in-memory state.
	if err := cfg.SetRenamedStation("LHB-OLD", "After"); err == nil {
		t.Fatal("SetRenamedStation() unexpectedly succeeded while blocked")
	}
	if err := cfg.Save(); err == nil {
		t.Fatal("Save() unexpectedly succeeded while blocked")
	}
	if writeCalls != 0 {
		t.Fatalf("writer invoked %d time(s) while persistence was blocked", writeCalls)
	}
	if got, _ := cfg.GetRenamedStation("LHB-OLD"); got == "After" {
		t.Fatal("blocked rename leaked into in-memory state")
	}
	if err := cfg.PersistenceError(); err == nil || !errors.Is(err, loadErr) {
		t.Fatalf("PersistenceError() = %v, want the load failure", err)
	}

	// A successful load lifts the block and restores renames.
	configFileReader = func(string) ([]byte, error) {
		return []byte(`{"renamedStations":{"LHB-OLD":"Earlier"}}`), nil
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("recovery Load() error = %v", err)
	}
	if err := cfg.SetRenamedStation("LHB-OLD", "After"); err != nil {
		t.Fatalf("SetRenamedStation() after recovery error = %v", err)
	}
	if got, _ := cfg.GetRenamedStation("LHB-OLD"); got != "After" {
		t.Fatalf("renamed name after recovery = %q, want %q", got, "After")
	}
	if writeCalls != 1 {
		t.Fatalf("writer calls after recovery = %d, want 1", writeCalls)
	}
	if err := cfg.PersistenceError(); err != nil {
		t.Fatalf("recovered config retained persistence error: %v", err)
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
	if err := cfg.PersistenceError(); err != nil {
		t.Fatalf("successfully preserved invalid config blocked persistence: %v", err)
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

func TestInvalidConfigBackupFailureBlocksSaveAndRollsBackMutation(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	invalidContent := []byte(`{"renamedStations":`)
	if err := os.WriteFile(configPath, invalidContent, 0o644); err != nil {
		t.Fatal(err)
	}

	originalRenamer := configFileRenamer
	configFileRenamer = func(string, string) error { return errors.New("rename denied") }
	t.Cleanup(func() { configFileRenamer = originalRenamer })

	cfg := NewConfig()
	if err := cfg.Load(); err == nil || !strings.Contains(err.Error(), "failed to preserve invalid file") {
		t.Fatalf("Config.Load() error = %v, want backup failure", err)
	}
	if err := cfg.PersistenceError(); err == nil || !strings.Contains(err.Error(), "rename denied") {
		t.Fatalf("PersistenceError() = %v, want backup failure", err)
	}
	if err := cfg.SetRenamedStation("LHB-OLD", "After"); err == nil || !strings.Contains(err.Error(), "save blocked") {
		t.Fatalf("SetRenamedStation() error = %v, want blocked save", err)
	}
	if _, ok := cfg.GetRenamedStation("LHB-OLD"); ok {
		t.Fatal("failed blocked save retained the in-memory rename")
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(invalidContent) {
		t.Fatalf("invalid config content changed to %q", content)
	}
}

func TestLoadClearsAliasesWhenConfigFileWasRemoved(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "renamedStations": {"LHB-OLD": "Legacy"},
  "renamedStationsByAddress": {"11:22:33:44:55:66": "Address"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("initial Load() error = %v", err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() after removal error = %v", err)
	}
	if _, ok := cfg.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); ok {
		t.Fatal("Load() retained aliases after the config file was removed")
	}
}

func TestBluetoothAdapterPreferencePersistsAcrossReloads(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)

	cfg := NewConfig()
	if got := cfg.GetBluetoothAdapter(); got != "" {
		t.Fatalf("fresh config adapter = %q, want empty default", got)
	}
	if err := cfg.SetBluetoothAdapter("Bluetooth#Bluetooth00:11:22:33:44:55"); err != nil {
		t.Fatalf("SetBluetoothAdapter() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(configRoot, "lhcontrol", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"bluetoothAdapter": "Bluetooth#Bluetooth00:11:22:33:44:55"`) {
		t.Fatalf("persisted config missing adapter preference: %s", content)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reloaded Load() error = %v", err)
	}
	if got := reloaded.GetBluetoothAdapter(); got != "Bluetooth#Bluetooth00:11:22:33:44:55" {
		t.Fatalf("reloaded adapter = %q, want persisted value", got)
	}

	// Clearing the preference removes the key entirely (omitempty).
	if err := reloaded.SetBluetoothAdapter(""); err != nil {
		t.Fatalf("clearing adapter preference error = %v", err)
	}
	content, err = os.ReadFile(filepath.Join(configRoot, "lhcontrol", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "bluetoothAdapter") {
		t.Fatalf("cleared adapter preference still persisted: %s", content)
	}
}

func TestSetBluetoothAdapterRollsBackWhenPersistenceFails(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { configFileWriter = originalWriter })

	cfg := NewConfig()
	if err := cfg.SetBluetoothAdapter("Bluetooth#Bluetooth00:11:22:33:44:55"); err == nil {
		t.Fatal("SetBluetoothAdapter() unexpectedly succeeded")
	}
	if got := cfg.GetBluetoothAdapter(); got != "" {
		t.Fatalf("in-memory adapter = %q, want rollback to empty", got)
	}
	if err := cfg.PersistenceError(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("PersistenceError() = %v, want write failure", err)
	}
}

func TestLoadWithoutBluetoothAdapterKeepsDefault(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"),
		[]byte(`{"renamedStations":{"LHB-OLD":"Legacy"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetBluetoothAdapter(); got != "" {
		t.Fatalf("adapter after legacy config load = %q, want empty", got)
	}
}

func TestSuccessfulInvalidConfigQuarantineResetsPreviousLoadState(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"renamedStations":`), 0o644); err != nil {
		t.Fatal(err)
	}
	originalRenamer := configFileRenamer
	configFileRenamer = func(string, string) error { return errors.New("rename denied") }
	t.Cleanup(func() { configFileRenamer = originalRenamer })
	cfg := NewConfig()
	cfg.RenamedStations["LHB-OLD"] = "Old"
	if err := cfg.Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded while quarantine failed")
	}
	configFileRenamer = os.Rename
	if err := cfg.Load(); err == nil || !strings.Contains(err.Error(), "invalid file preserved") {
		t.Fatalf("Load() error = %v, want preserved invalid-file error", err)
	}
	if _, ok := cfg.GetRenamedStation("LHB-OLD"); ok {
		t.Fatal("successful quarantine retained stale aliases")
	}
	if err := cfg.SetRenamedStation("LHB-NEW", "New"); err != nil {
		t.Fatalf("SetRenamedStation() remained blocked after successful quarantine: %v", err)
	}
}

func TestAutoSleepSettingsPersistAcrossReloads(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)

	cfg := NewConfig()
	defaults := cfg.GetAutoSleep()
	if defaults.Enabled || defaults.Target != string(autosleep.DefaultTarget) || defaults.DelaySeconds != autosleep.DefaultDelaySeconds {
		t.Fatalf("fresh auto-sleep defaults = %+v", defaults)
	}
	settings := defaults
	settings.Enabled = true
	settings.Target = "steam"
	settings.DelaySeconds = 90
	if err := cfg.SetAutoSleep(settings); err != nil {
		t.Fatalf("SetAutoSleep() error = %v", err)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reloaded Load() error = %v", err)
	}
	got := reloaded.GetAutoSleep()
	if !got.Enabled || got.Target != "steam" || got.DelaySeconds != 90 {
		t.Fatalf("reloaded auto-sleep = %+v, want enabled/steam/90", got)
	}
}

func TestSetAutoSleepRejectsInvalidSettings(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	cfg := NewConfig()
	bad := cfg.GetAutoSleep()
	bad.Target = "chrome"
	if err := cfg.SetAutoSleep(bad); err == nil {
		t.Fatal("SetAutoSleep() accepted an unknown target")
	}
	bad = cfg.GetAutoSleep()
	bad.DelaySeconds = 1
	if err := cfg.SetAutoSleep(bad); err == nil {
		t.Fatal("SetAutoSleep() accepted a below-minimum delay")
	}
}

func TestLoadSanitizesInvalidPersistedAutoSleep(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	// A future version persisted a target and delay this build rejects.
	content := []byte(`{"autoSleep":{"enabled":true,"target":"chrome","delaySeconds":999999}}`)
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := cfg.GetAutoSleep()
	if !got.Enabled {
		t.Fatal("sanitization dropped the enabled flag")
	}
	if got.Target == "chrome" {
		t.Fatalf("sanitization kept invalid target %q", got.Target)
	}
	if got.DelaySeconds == 999999 {
		t.Fatalf("sanitization kept invalid delay %d", got.DelaySeconds)
	}
}

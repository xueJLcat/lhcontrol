package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lhcontrol/internal/autosleep"
)

func useTemporaryConfigDirectory(t *testing.T) string {
	t.Helper()
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	return configDirectory
}

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

// TestLoadSweepsStaleTemporaryFiles covers the crash window between CreateTemp
// and Rename: scratch files a previous run left behind must be removed by the
// next Load so the config directory does not accumulate them forever.
func TestLoadSweepsStaleTemporaryFiles(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	stale := filepath.Join(configDirectory, ".lhcontrol-config-123456.tmp")
	if err := os.WriteFile(stale, []byte(`{"partial":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	config := NewConfig()
	if err := config.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temporary file stat error = %v, want removed", err)
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

// TestLegacyRenameKeepsResetTombstoneAfterLegacyCleared covers the sequence
// legacy rename → explicit per-device reset → legacy clear → legacy rename
// again. The reset writes a tombstone that must survive the final legacy
// rename: the user explicitly restored the factory name for that device.
func TestLegacyRenameKeepsResetTombstoneAfterLegacyCleared(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	address := "11:22:33:44:55:90"
	if err := cfg.SetRenamedStationForAddresses("LHB-X", "Studio", []string{address}); err != nil {
		t.Fatalf("legacy rename error = %v", err)
	}
	if err := cfg.SetRenamedStationByAddress(address, "LHB-X", ""); err != nil {
		t.Fatalf("per-device reset error = %v", err)
	}
	if err := cfg.SetRenamedStationForAddresses("LHB-X", "", []string{address}); err != nil {
		t.Fatalf("legacy clear error = %v", err)
	}
	if err := cfg.SetRenamedStationForAddresses("LHB-X", "Theater", []string{address}); err != nil {
		t.Fatalf("second legacy rename error = %v", err)
	}
	if name, renamed := cfg.GetStationDisplayName(address, "LHB-X"); renamed || name != "LHB-X" {
		t.Fatalf("display name = %q (renamed=%v), want the tombstone to keep the factory name", name, renamed)
	}
}

func TestAddressRenameRollbackSurvivesDuplicateAddresses(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	cfg := NewConfig()
	if err := cfg.SetRenamedStationForAddresses("LHB-DUP", "Before", []string{"11:22:33:44:55:66"}); err != nil {
		t.Fatalf("initial rename failed: %v", err)
	}
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { configFileWriter = originalWriter })

	// A duplicate address must not corrupt the rollback snapshot: the second
	// occurrence may only capture the original persisted value, not the one
	// the first occurrence just wrote.
	if err := cfg.SetRenamedStationForAddresses("LHB-DUP", "After", []string{"11:22:33:44:55:66", "11:22:33:44:55:66"}); err == nil {
		t.Fatal("SetRenamedStationForAddresses() unexpectedly succeeded")
	}
	if got, ok := cfg.GetStationDisplayName("11:22:33:44:55:66", "LHB-DUP"); !ok || got != "Before" {
		t.Fatalf("in-memory alias = %q (found=%v), want rollback value Before", got, ok)
	}
}

// TestRenameInitializesNilAliasMaps covers a Config whose alias maps were
// never initialized (a zero-value Config created outside NewConfig): writes
// must allocate the maps instead of panicking on a nil-map assignment.
func TestRenameInitializesNilAliasMaps(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := &Config{}
	if err := cfg.SetRenamedStation("LHB-OLD", "Desk"); err != nil {
		t.Fatalf("SetRenamedStation() on a zero-value config error = %v", err)
	}
	if got, ok := cfg.GetRenamedStation("LHB-OLD"); !ok || got != "Desk" {
		t.Fatalf("GetRenamedStation() = %q, %v; want Desk, true", got, ok)
	}

	cfg = &Config{}
	if err := cfg.SetRenamedStationForAddresses("LHB-OLD", "Desk", []string{"11:22:33:44:55:66"}); err != nil {
		t.Fatalf("SetRenamedStationForAddresses() on a zero-value config error = %v", err)
	}
	if got, ok := cfg.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); !ok || got != "Desk" {
		t.Fatalf("GetStationDisplayName() = %q, %v; want Desk, true", got, ok)
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

// TestBlockedAliasResetForUnscannedStationReportsBlockedSave covers the one
// rename path that writes nothing: resetting the alias of a station that was
// never scanned this session (unknown factory name, no existing entry). Even
// though there is nothing to persist, the call must report the block like
// every other setter instead of claiming success while the config stays
// read-only.
func TestBlockedAliasResetForUnscannedStationReportsBlockedSave(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	originalReader := configFileReader
	configFileReader = func(string) ([]byte, error) {
		return nil, errors.New("file locked by backup software")
	}
	t.Cleanup(func() { configFileReader = originalReader })

	cfg := NewConfig()
	if err := cfg.Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
	err := cfg.SetRenamedStationByAddress("11:22:33:44:55:77", "", "")
	if err == nil || !strings.Contains(err.Error(), "save blocked") {
		t.Fatalf("SetRenamedStationByAddress() error = %v, want blocked save", err)
	}
}

// TestTransientLoadFailureRecoversOnNextSetter guards the lazy recovery: when
// the startup Load hit a transient read failure (a backup tool holding the
// file), saves are blocked to protect the unreadable config, but the very next
// setting change must re-attempt the load instead of staying blocked until a
// restart. The recovered disk contents become authoritative, so a pre-existing
// rename must survive rather than being wiped by the in-memory defaults.
func TestTransientLoadFailureRecoversOnNextSetter(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)

	originalReader := configFileReader
	readErr := errors.New("file locked by backup software")
	calls := 0
	configFileReader = func(path string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, readErr
		}
		return originalReader(path)
	}
	t.Cleanup(func() { configFileReader = originalReader })

	// Seed a real config whose read fails once at startup but succeeds later.
	seed := `{"language":"zh-CN","renamedStations":{"LHB-EXISTING":"Kept"}}`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := NewConfig()
	if err := cfg.Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded on the transient failure")
	}
	if cfg.PersistenceError() == nil {
		t.Fatal("transient Load failure did not block persistence")
	}
	generationBefore := cfg.RecoveryGeneration()

	// The next setter re-attempts the load; the reader now succeeds, the block
	// lifts, and the change is applied on top of the recovered disk contents.
	if err := cfg.SetLanguage(LanguageEnglish); err != nil {
		t.Fatalf("SetLanguage() after transient recovery error = %v", err)
	}
	if err := cfg.PersistenceError(); err != nil {
		t.Fatalf("recovered persistence still reports an error: %v", err)
	}
	if got := cfg.GetLanguage(); got != LanguageEnglish {
		t.Fatalf("language after recovery = %q, want %q", got, LanguageEnglish)
	}
	if got, ok := cfg.GetRenamedStation("LHB-EXISTING"); !ok || got != "Kept" {
		t.Fatalf("recovered rename = %q,%v; want the disk value preserved", got, ok)
	}
	// The recovery must be observable so runtime layers re-apply their
	// startup-derived side effects on the recovered contents.
	if got := cfg.RecoveryGeneration(); got != generationBefore+1 {
		t.Fatalf("RecoveryGeneration() after recovery = %d, want %d", got, generationBefore+1)
	}

	// A later setter that does not recover again must not bump the generation.
	if err := cfg.SetLanguage(LanguageSimplifiedChinese); err != nil {
		t.Fatalf("SetLanguage() error = %v", err)
	}
	if got := cfg.RecoveryGeneration(); got != generationBefore+1 {
		t.Fatalf("RecoveryGeneration() after a plain setter = %d, want unchanged %d", got, generationBefore+1)
	}
}

// TestSetAbsentStationRetryLimitWithPreviousReportsRecoveredBaseline guards
// the reported baseline across a blocked-save recovery: the setter reloads the
// persisted file internally, and the caller must compare the new limit
// against the recovered value, not the stale startup default it read before
// the setter ran.
func TestSetAbsentStationRetryLimitWithPreviousReportsRecoveredBaseline(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)

	originalReader := configFileReader
	originalWriter := configFileWriter
	readErr := errors.New("file locked by backup software")
	calls := 0
	configFileReader = func(path string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, readErr
		}
		return originalReader(path)
	}
	configFileWriter = func(string, []byte, os.FileMode) error { return nil }
	t.Cleanup(func() {
		configFileReader = originalReader
		configFileWriter = originalWriter
	})

	seed := fmt.Sprintf(`{"absentStationRetryLimit":%d}`, MaxAbsentStationRetryLimit)
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := NewConfig()
	if err := cfg.Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded on the transient failure")
	}
	staleBaseline := cfg.GetAbsentStationRetryLimit()
	if staleBaseline == MaxAbsentStationRetryLimit {
		t.Fatalf("startup baseline %d already equals the recovered value; the test cannot observe the difference", staleBaseline)
	}

	previous, err := cfg.SetAbsentStationRetryLimitWithPrevious(MinAbsentStationRetryLimit)
	if err != nil {
		t.Fatalf("SetAbsentStationRetryLimitWithPrevious() error = %v", err)
	}
	if previous != MaxAbsentStationRetryLimit {
		t.Fatalf("reported previous limit = %d, want the recovered %d", previous, MaxAbsentStationRetryLimit)
	}
	if got := cfg.GetAbsentStationRetryLimit(); got != MinAbsentStationRetryLimit {
		t.Fatalf("limit after the write = %d, want %d", got, MinAbsentStationRetryLimit)
	}

	// Without a recovery the reported baseline is simply the value in place.
	previous, err = cfg.SetAbsentStationRetryLimitWithPrevious(MinAbsentStationRetryLimit + 1)
	if err != nil {
		t.Fatalf("second SetAbsentStationRetryLimitWithPrevious() error = %v", err)
	}
	if previous != MinAbsentStationRetryLimit {
		t.Fatalf("reported previous limit = %d, want %d", previous, MinAbsentStationRetryLimit)
	}
}

// TestSameAPIAddressSaveClearsPersistenceError guards the same-value
// short-circuit: re-saving an unchanged API listen address must still persist
// when a prior save failure left a pending persistence error, because that
// rewrite is what clears the error state for the health endpoint and UI.
func TestSameAPIAddressSaveClearsPersistenceError(t *testing.T) {
	useTemporaryConfigDirectory(t)

	originalWriter := configFileWriter
	writeCalls := 0
	configFileWriter = func(path string, data []byte, perm os.FileMode) error {
		writeCalls++
		if writeCalls == 1 {
			return errors.New("disk full")
		}
		return originalWriter(path, data, perm)
	}
	t.Cleanup(func() { configFileWriter = originalWriter })

	cfg := NewConfig()
	// A first save fails and leaves lastPersistenceErr set (value rolled back).
	if err := cfg.SetLanguage(LanguageEnglish); err == nil {
		t.Fatal("first save unexpectedly succeeded")
	}
	if cfg.PersistenceError() == nil {
		t.Fatal("failed save did not record a persistence error")
	}

	// Re-saving the unchanged API address must not short-circuit while an error
	// is pending: the successful rewrite clears it.
	if err := cfg.SetAPIListenAddress(DefaultAPIListenAddress); err != nil {
		t.Fatalf("SetAPIListenAddress() same value error = %v", err)
	}
	if err := cfg.PersistenceError(); err != nil {
		t.Fatalf("same-value API address save retained persistence error: %v", err)
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

// TestResetUnknownFactoryNameKeepsTombstoneAgainstLegacyAlias guards the
// pre-scan reset: when the station has not been scanned this session the
// factory name is unknown, so deleting the per-address alias would
// resurrect the legacy alias as soon as a scan rediscovers the device.
func TestResetUnknownFactoryNameKeepsTombstoneAgainstLegacyAlias(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	cfg := NewConfig()
	cfg.RenamedStations["LHB-OLD"] = "Legacy name"
	cfg.RenamedStationsByAddress["11:22:33:44:55:66"] = "Legacy name"

	if err := cfg.SetRenamedStationByAddress("11:22:33:44:55:66", "", ""); err != nil {
		t.Fatalf("SetRenamedStationByAddress() error = %v", err)
	}
	if got, ok := cfg.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); ok || got != "LHB-OLD" {
		t.Fatalf("reset with unknown factory name = %q, %v, want the tombstone to suppress the legacy alias", got, ok)
	}
	if got, ok := cfg.GetStationDisplayName("AA:BB:CC:DD:EE:FF", "LHB-OLD"); !ok || got != "Legacy name" {
		t.Fatalf("reset removed another device's legacy alias: %q, %v", got, ok)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Config.Load() error = %v", err)
	}
	if got, ok := reloaded.GetStationDisplayName("11:22:33:44:55:66", "LHB-OLD"); ok || got != "LHB-OLD" {
		t.Fatalf("reloaded reset with unknown factory name = %q, %v, want the persisted tombstone", got, ok)
	}

	// A reset for a device that carries no alias and whose factory name is
	// unknown has nothing to suppress and must not create a tombstone.
	if err := cfg.SetRenamedStationByAddress("AA:BB:CC:DD:EE:00", "", ""); err != nil {
		t.Fatalf("no-op reset error = %v", err)
	}
	if _, exists := cfg.RenamedStationsByAddress["AA:BB:CC:DD:EE:00"]; exists {
		t.Fatal("no-op reset created a tombstone entry")
	}
}

// TestLoadToleratesUTF8ByteOrderMark guards hand-edited configs saved with a
// BOM (for example by Notepad): the marker must be stripped instead of
// quarantining an otherwise valid file and falling back to defaults.
func TestLoadToleratesUTF8ByteOrderMark(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"scanOnStartup":false}`)...)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Config.Load() error = %v, want the BOM tolerated", err)
	}
	if cfg.ScanOnStartup {
		t.Fatal("BOM-prefixed config fell back to defaults instead of loading")
	}
	preserved, err := filepath.Glob(configPath + ".invalid-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(preserved) != 0 {
		t.Fatalf("BOM-prefixed config was quarantined: %v", preserved)
	}
}

// TestBlockedSaveRecoveryQuarantineSurfacesWarning guards the mid-session
// recovery path: when an unreadable startup load is later unblocked by a file
// whose contents cannot be parsed, the recovery quarantines the file and
// applies defaults. Saves succeed again afterwards, so no persistence error
// would surface the lost contents; a session warning must instead.
func TestBlockedSaveRecoveryQuarantineSurfacesWarning(t *testing.T) {
	blockedRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedRoot, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AppData", blockedRoot)

	cfg := NewConfig()
	if err := cfg.Load(); err == nil {
		t.Fatal("Config.Load() unexpectedly succeeded against an unreadable path")
	}
	if cfg.PersistenceError() == nil {
		t.Fatal("unreadable config path did not block persistence")
	}

	validRoot := t.TempDir()
	t.Setenv("AppData", validRoot)
	configDirectory := filepath.Join(validRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cfg.SetLanguage(LanguageEnglish); err != nil {
		t.Fatalf("SetLanguage() error = %v, want the recovery to unblock the save", err)
	}
	if cfg.PersistenceError() != nil {
		t.Fatalf("recovery kept persistence blocked: %v", cfg.PersistenceError())
	}
	warning := cfg.RecoveryLoadWarning()
	if warning == "" || !strings.Contains(warning, "reset to defaults") {
		t.Fatalf("RecoveryLoadWarning() = %q, want the quarantine surfaced", warning)
	}
	preserved, err := filepath.Glob(configPath + ".invalid-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(preserved) != 1 {
		t.Fatalf("quarantined files = %v, want exactly one", preserved)
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

func TestLegacyBluetoothAdapterPreferenceIsIgnoredAndDroppedOnSave(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "renamedStations": {"LHB-OLD": "Legacy"},
  "bluetoothAdapter": "Bluetooth#Bluetooth00:11:22:33:44:55"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "bluetoothAdapter") {
		t.Fatalf("legacy adapter preference still persisted: %s", content)
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
	bad.Enabled = true
	bad.DelaySeconds = 1
	if err := cfg.SetAutoSleep(bad); err == nil {
		t.Fatal("SetAutoSleep() accepted a below-minimum delay for an enabled configuration")
	}
	// A disabled configuration may carry a stale delay: validation accepts it
	// because the watcher never consults the delay while the feature is off,
	// and rejecting it would make the feature impossible to disable. The
	// persisted delay is normalized so re-enabling starts from a valid value.
	staleDelay := cfg.GetAutoSleep()
	staleDelay.Enabled = false
	staleDelay.DelaySeconds = 1
	if err := cfg.SetAutoSleep(staleDelay); err != nil {
		t.Fatalf("SetAutoSleep() rejected a disabled configuration with a stale delay: %v", err)
	}
	if got := cfg.GetAutoSleep(); got.DelaySeconds != autosleep.DefaultDelaySeconds {
		t.Fatalf("disabled settings persisted delay %d, want normalization to %d", got.DelaySeconds, autosleep.DefaultDelaySeconds)
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

func TestLanguagePersistsAcrossReloads(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	cfg := NewConfig()
	if got := cfg.GetLanguage(); got != "" {
		t.Fatalf("fresh language = %q, want system default marker", got)
	}
	if err := cfg.SetLanguage(LanguageSimplifiedChinese); err != nil {
		t.Fatalf("SetLanguage() error = %v", err)
	}
	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetLanguage(); got != LanguageSimplifiedChinese {
		t.Fatalf("reloaded language = %q, want %q", got, LanguageSimplifiedChinese)
	}
}

func TestLanguageCanReturnToSystemDefault(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	cfg := NewConfig()
	if err := cfg.SetLanguage(LanguageSimplifiedChinese); err != nil {
		t.Fatalf("SetLanguage(zh-CN) error = %v", err)
	}
	if err := cfg.SetLanguage(""); err != nil {
		t.Fatalf("SetLanguage(system) error = %v", err)
	}
	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetLanguage(); got != "" {
		t.Fatalf("reloaded language = %q, want system default", got)
	}
}

func TestSetLanguageRejectsInvalidValue(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	cfg := NewConfig()
	if err := cfg.SetLanguage("zh-TW"); err == nil {
		t.Fatal("SetLanguage() accepted unsupported locale")
	}
	if got := cfg.GetLanguage(); got != "" {
		t.Fatalf("invalid language changed value to %q", got)
	}
}

func TestSetLanguageRollsBackWhenPersistenceFails(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { configFileWriter = originalWriter })
	cfg := NewConfig()
	if err := cfg.SetLanguage(LanguageSimplifiedChinese); err == nil {
		t.Fatal("SetLanguage() unexpectedly succeeded")
	}
	if got := cfg.GetLanguage(); got != "" {
		t.Fatalf("failed save retained language %q", got)
	}
}

// TestFailedSaveRestoresNormalizedUnrelatedFields covers the saveLocked
// normalization that runs in place before the write: when the write fails,
// every setter only rolls back its own field, so the normalization of
// unrelated fields must be rolled back too. Otherwise memory silently holds
// values that were never persisted and diverges from the file.
func TestFailedSaveRestoresNormalizedUnrelatedFields(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { configFileWriter = originalWriter })
	cfg := NewConfig()
	// Simulate a directly-assigned out-of-range field: saveLocked sanitizes
	// it in place before the write.
	cfg.ScanDurationSeconds = 0
	original := cfg.ScanDurationSeconds
	if err := cfg.SetLanguage(LanguageSimplifiedChinese); err == nil {
		t.Fatal("SetLanguage() unexpectedly succeeded")
	}
	if cfg.ScanDurationSeconds != original {
		t.Fatalf(
			"failed save left unrelated normalized field changed: ScanDurationSeconds = %d, want restored %d",
			cfg.ScanDurationSeconds, original,
		)
	}
	if cfg.GetLanguage() != "" {
		t.Fatalf("failed save retained language %q", cfg.GetLanguage())
	}
}

func TestBulkPowerTimeoutPersistsAndValidates(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if got := cfg.GetBulkPowerTimeoutSeconds(); got != DefaultBulkPowerTimeoutSeconds {
		t.Fatalf("default bulk timeout = %d, want %d", got, DefaultBulkPowerTimeoutSeconds)
	}
	if err := cfg.SetBulkPowerTimeoutSeconds(MinBulkPowerTimeoutSeconds - 1); err == nil {
		t.Fatal("SetBulkPowerTimeoutSeconds accepted a below-minimum value")
	}
	if err := cfg.SetBulkPowerTimeoutSeconds(180); err != nil {
		t.Fatalf("SetBulkPowerTimeoutSeconds() error = %v", err)
	}
	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetBulkPowerTimeoutSeconds(); got != 180 {
		t.Fatalf("reloaded bulk timeout = %d, want 180", got)
	}
}

func TestLoadSanitizesInvalidBulkPowerTimeout(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"bulkPowerTimeoutSeconds":5}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetBulkPowerTimeoutSeconds(); got != DefaultBulkPowerTimeoutSeconds {
		t.Fatalf("sanitized bulk timeout = %d, want %d", got, DefaultBulkPowerTimeoutSeconds)
	}
}

func TestStatusPollIntervalPersistsAndValidates(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if got := cfg.GetStatusPollIntervalSeconds(); got != DefaultStatusPollIntervalSeconds {
		t.Fatalf("default status poll interval = %d, want %d", got, DefaultStatusPollIntervalSeconds)
	}
	if err := cfg.SetStatusPollIntervalSeconds(MinStatusPollIntervalSeconds - 1); err == nil {
		t.Fatal("SetStatusPollIntervalSeconds accepted a below-minimum value")
	}
	if err := cfg.SetStatusPollIntervalSeconds(45); err != nil {
		t.Fatalf("SetStatusPollIntervalSeconds() error = %v", err)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetStatusPollIntervalSeconds(); got != 45 {
		t.Fatalf("reloaded status poll interval = %d, want 45", got)
	}
}

func TestLoadSanitizesInvalidStatusPollInterval(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"statusPollIntervalSeconds":1}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetStatusPollIntervalSeconds(); got != DefaultStatusPollIntervalSeconds {
		t.Fatalf("sanitized status poll interval = %d, want %d", got, DefaultStatusPollIntervalSeconds)
	}
}

func TestSetStatusPollIntervalRollsBackWhenPersistenceFails(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { configFileWriter = originalWriter })
	cfg := NewConfig()
	if err := cfg.SetStatusPollIntervalSeconds(45); err == nil {
		t.Fatal("SetStatusPollIntervalSeconds() unexpectedly succeeded")
	}
	if got := cfg.GetStatusPollIntervalSeconds(); got != DefaultStatusPollIntervalSeconds {
		t.Fatalf("failed save retained status poll interval %d", got)
	}
}

func TestNewSettingsRollBackWhenPersistenceFails(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	originalWriter := configFileWriter
	configFileWriter = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { configFileWriter = originalWriter })
	cfg := NewConfig()

	if err := cfg.SetScanOnStartup(false); err == nil {
		t.Fatal("SetScanOnStartup() unexpectedly succeeded")
	}
	if !cfg.GetScanOnStartup() {
		t.Fatal("failed save retained scan-on-startup disabled")
	}
	if err := cfg.SetStatusPollingEnabled(false); err == nil {
		t.Fatal("SetStatusPollingEnabled() unexpectedly succeeded")
	}
	if !cfg.GetStatusPollingEnabled() {
		t.Fatal("failed save retained status polling disabled")
	}
	if err := cfg.SetScanDurationSeconds(20); err == nil {
		t.Fatal("SetScanDurationSeconds() unexpectedly succeeded")
	}
	if got := cfg.GetScanDurationSeconds(); got != DefaultScanDurationSeconds {
		t.Fatalf("failed save retained scan duration %d", got)
	}
	if err := cfg.SetBulkPowerTimeoutSeconds(300); err == nil {
		t.Fatal("SetBulkPowerTimeoutSeconds() unexpectedly succeeded")
	}
	if got := cfg.GetBulkPowerTimeoutSeconds(); got != DefaultBulkPowerTimeoutSeconds {
		t.Fatalf("failed save retained bulk timeout %d", got)
	}
}

func TestLoadBlocksPersistenceWhenConfigPathUnresolvable(t *testing.T) {
	// An empty AppData leaves os.UserConfigDir without a usable directory, so
	// Load cannot even locate a stored config. Persistence must stay blocked
	// afterwards; otherwise the first successful save would overwrite a config
	// that was never read.
	t.Setenv("AppData", "")
	cfg := NewConfig()
	if err := cfg.Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded with an unresolvable config path")
	}
	if cfg.PersistenceError() == nil {
		t.Fatal("Load() did not record a persistence error")
	}
	if err := cfg.SetScanDurationSeconds(10); err == nil {
		t.Fatal("SetScanDurationSeconds() unexpectedly succeeded while persistence is blocked")
	}
}

func TestSaveSanitizesDirectlyAssignedFields(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	cfg.ScanDurationSeconds = 0
	cfg.BulkPowerTimeoutSeconds = 99999
	cfg.StatusPollIntervalSeconds = -5
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(configDirectory, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	written := string(content)
	for _, want := range []string{
		fmt.Sprintf(`"scanDurationSeconds": %d`, DefaultScanDurationSeconds),
		fmt.Sprintf(`"bulkPowerTimeoutSeconds": %d`, DefaultBulkPowerTimeoutSeconds),
		fmt.Sprintf(`"statusPollIntervalSeconds": %d`, DefaultStatusPollIntervalSeconds),
	} {
		if !strings.Contains(written, want) {
			t.Fatalf("persisted config = %s, want it to contain %s", written, want)
		}
	}
}

func TestSaveRepairsCrossItemInvariantsBeforeWriting(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	// Each value is individually valid, but the pairs contradict the runtime
	// budgeting rules. Save must not write a file whose meaning changes on the
	// next Load.
	cfg.BulkPowerTimeoutSeconds = 30
	cfg.StationOperationTimeoutSeconds = 60
	cfg.InitialReadTimeoutSeconds = 60
	cfg.ScanReadPhaseTimeoutSeconds = 45
	cfg.StatusReadTimeoutSeconds = 60
	cfg.StatusRefreshTimeoutSeconds = 30
	cfg.RecoveryRetryBaseSeconds = 120
	cfg.RecoveryRetryMaxSeconds = 60

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(configDirectory, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var written persistedConfig
	if err := json.Unmarshal(content, &written); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for name, check := range map[string]struct {
		value *int
		want  int
	}{
		"bulk power timeout":      {written.BulkPowerTimeoutSeconds, 60},
		"scan read phase timeout": {written.ScanReadPhaseTimeoutSeconds, 60},
		"status refresh timeout":  {written.StatusRefreshTimeoutSeconds, 60},
		"recovery retry base":     {written.RecoveryRetryBaseSeconds, 60},
	} {
		if check.value == nil || *check.value != check.want {
			t.Errorf("persisted %s = %v, want %d", name, check.value, check.want)
		}
	}
	for name, check := range map[string]struct {
		value int
		want  int
	}{
		"runtime bulk power timeout":      {cfg.GetBulkPowerTimeoutSeconds(), 60},
		"runtime scan read phase timeout": {cfg.GetScanReadPhaseTimeoutSeconds(), 60},
		"runtime status refresh timeout":  {cfg.GetStatusRefreshTimeoutSeconds(), 60},
		"runtime recovery retry base":     {cfg.GetRecoveryRetryBaseSeconds(), 60},
	} {
		if check.value != check.want {
			t.Errorf("%s = %d, want %d after Save", name, check.value, check.want)
		}
	}
}

func TestStatusDisplayFreshnessWindowHasSafeMinimum(t *testing.T) {
	cfg := NewConfig()
	cfg.StatusPollIntervalSeconds = MinStatusPollIntervalSeconds
	want := time.Duration(MinimumDisplayFreshnessSeconds) * time.Second
	if got := cfg.StatusDisplayFreshnessWindow(); got != want {
		t.Fatalf("StatusDisplayFreshnessWindow() = %v, want the %v minimum", got, want)
	}
}

func TestStationOperationTimeoutPersistsValidatesAndCouplesToBulk(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if got := cfg.GetStationOperationTimeoutSeconds(); got != DefaultStationOperationTimeoutSeconds {
		t.Fatalf("default station operation timeout = %d, want %d", got, DefaultStationOperationTimeoutSeconds)
	}
	if err := cfg.SetStationOperationTimeoutSeconds(MinStationOperationTimeoutSeconds - 1); err == nil {
		t.Fatal("SetStationOperationTimeoutSeconds accepted a below-minimum value")
	}
	if err := cfg.SetStationOperationTimeoutSeconds(MaxStationOperationTimeoutSeconds + 1); err == nil {
		t.Fatal("SetStationOperationTimeoutSeconds accepted an above-maximum value")
	}
	if err := cfg.SetStationOperationTimeoutSeconds(60); err != nil {
		t.Fatalf("SetStationOperationTimeoutSeconds() error = %v", err)
	}
	// The bulk budget must never fall below the per-station budget.
	if err := cfg.SetBulkPowerTimeoutSeconds(45); err == nil {
		t.Fatal("SetBulkPowerTimeoutSeconds accepted a value below the station operation timeout")
	}
	if err := cfg.SetBulkPowerTimeoutSeconds(90); err != nil {
		t.Fatalf("SetBulkPowerTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetStationOperationTimeoutSeconds(110); err == nil {
		t.Fatal("SetStationOperationTimeoutSeconds accepted a value above the bulk timeout")
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetStationOperationTimeoutSeconds(); got != 60 {
		t.Fatalf("reloaded station operation timeout = %d, want 60", got)
	}
	if got := reloaded.GetBulkPowerTimeoutSeconds(); got != 90 {
		t.Fatalf("reloaded bulk timeout = %d, want 90", got)
	}
}

func TestLoadRepairsBulkTimeoutBelowStationOperationTimeout(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"bulkPowerTimeoutSeconds":45,"stationOperationTimeoutSeconds":60}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetBulkPowerTimeoutSeconds(); got != 60 {
		t.Fatalf("repaired bulk timeout = %d, want it raised to the station operation timeout 60", got)
	}
}

func TestPowerTimingSettingsPersistValidateAndSanitize(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if err := cfg.SetPowerConfirmAttemptsOn(MinPowerConfirmAttempts - 1); err == nil {
		t.Fatal("SetPowerConfirmAttemptsOn accepted a below-minimum value")
	}
	if err := cfg.SetPowerConfirmAttemptsOn(25); err != nil {
		t.Fatalf("SetPowerConfirmAttemptsOn() error = %v", err)
	}
	if err := cfg.SetPowerConfirmAttemptsOff(MaxPowerConfirmAttempts + 1); err == nil {
		t.Fatal("SetPowerConfirmAttemptsOff accepted an above-maximum value")
	}
	if err := cfg.SetPowerConfirmAttemptsOff(12); err != nil {
		t.Fatalf("SetPowerConfirmAttemptsOff() error = %v", err)
	}
	if err := cfg.SetPowerConfirmPollIntervalMs(MinPowerConfirmPollIntervalMs - 1); err == nil {
		t.Fatal("SetPowerConfirmPollIntervalMs accepted a below-minimum value")
	}
	if err := cfg.SetPowerConfirmPollIntervalMs(500); err != nil {
		t.Fatalf("SetPowerConfirmPollIntervalMs() error = %v", err)
	}
	if err := cfg.SetBootFallbackSeconds(MaxBootFallbackSeconds + 1); err == nil {
		t.Fatal("SetBootFallbackSeconds accepted an above-maximum value")
	}
	if err := cfg.SetBootFallbackSeconds(20); err != nil {
		t.Fatalf("SetBootFallbackSeconds() error = %v", err)
	}
	if got := cfg.PowerConfirmPollInterval(); got != 500*time.Millisecond {
		t.Fatalf("PowerConfirmPollInterval() = %v, want 500ms", got)
	}
	if got := cfg.BootFallback(); got != 20*time.Second {
		t.Fatalf("BootFallback() = %v, want 20s", got)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetPowerConfirmAttemptsOn(); got != 25 {
		t.Fatalf("reloaded confirm attempts on = %d, want 25", got)
	}
	if got := reloaded.GetPowerConfirmAttemptsOff(); got != 12 {
		t.Fatalf("reloaded confirm attempts off = %d, want 12", got)
	}
	if got := reloaded.GetPowerConfirmPollIntervalMs(); got != 500 {
		t.Fatalf("reloaded confirm poll interval = %d, want 500", got)
	}
	if got := reloaded.GetBootFallbackSeconds(); got != 20 {
		t.Fatalf("reloaded boot fallback = %d, want 20", got)
	}
}

func TestConnectionTimingSettingsPersistAndValidate(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if err := cfg.SetSleepFinalWriteTimeoutSeconds(MinSleepFinalWriteTimeoutSeconds - 1); err == nil {
		t.Fatal("SetSleepFinalWriteTimeoutSeconds accepted a below-minimum value")
	}
	if err := cfg.SetSleepFinalWriteTimeoutSeconds(60); err != nil {
		t.Fatalf("SetSleepFinalWriteTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetSleepPrepareGapMs(MaxSleepPrepareGapMs + 1); err == nil {
		t.Fatal("SetSleepPrepareGapMs accepted an above-maximum value")
	}
	if err := cfg.SetSleepPrepareGapMs(0); err != nil {
		t.Fatalf("SetSleepPrepareGapMs() error = %v, want 0 to disable the gap", err)
	}
	if err := cfg.SetDiscoveryAttempts(MinDiscoveryAttempts - 1); err == nil {
		t.Fatal("SetDiscoveryAttempts accepted a below-minimum value")
	}
	if err := cfg.SetDiscoveryAttempts(5); err != nil {
		t.Fatalf("SetDiscoveryAttempts() error = %v", err)
	}
	if err := cfg.SetDiscoveryRetryDelayMs(MinDiscoveryRetryDelayMs - 1); err == nil {
		t.Fatal("SetDiscoveryRetryDelayMs accepted a below-minimum value")
	}
	if err := cfg.SetDiscoveryRetryDelayMs(1000); err != nil {
		t.Fatalf("SetDiscoveryRetryDelayMs() error = %v", err)
	}
	if got := cfg.SleepFinalWriteTimeout(); got != 60*time.Second {
		t.Fatalf("SleepFinalWriteTimeout() = %v, want 60s", got)
	}
	if got := cfg.SleepPrepareGap(); got != 0 {
		t.Fatalf("SleepPrepareGap() = %v, want 0", got)
	}
	if got := cfg.DiscoveryRetryDelay(); got != time.Second {
		t.Fatalf("DiscoveryRetryDelay() = %v, want 1s", got)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.GetSleepFinalWriteTimeoutSeconds() != 60 || reloaded.GetSleepPrepareGapMs() != 0 ||
		reloaded.GetDiscoveryAttempts() != 5 || reloaded.GetDiscoveryRetryDelayMs() != 1000 {
		t.Fatalf("reloaded connection timing = final %d, gap %d, attempts %d, delay %d",
			reloaded.GetSleepFinalWriteTimeoutSeconds(), reloaded.GetSleepPrepareGapMs(),
			reloaded.GetDiscoveryAttempts(), reloaded.GetDiscoveryRetryDelayMs())
	}
}

func TestAPIListenAddressValidatesAndPersists(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if got := cfg.GetAPIListenAddress(); got != DefaultAPIListenAddress {
		t.Fatalf("default API listen address = %q", got)
	}
	for _, invalid := range []string{
		"", "no-port", "127.0.0.1:http", "127.0.0.1:80", ":7575", "[::1]:99999",
		// Hostnames are rejected: net.Listen would block the listener loop in
		// an uncancellable DNS resolution, hanging restart and shutdown.
		"localhost:7575", "example.invalid:7575",
	} {
		if err := cfg.SetAPIListenAddress(invalid); err == nil {
			t.Fatalf("SetAPIListenAddress(%q) unexpectedly succeeded", invalid)
		}
	}
	if err := cfg.SetAPIListenAddress("127.0.0.1:8080"); err != nil {
		t.Fatalf("SetAPIListenAddress() error = %v", err)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := reloaded.GetAPIListenAddress(); got != "127.0.0.1:8080" {
		t.Fatalf("reloaded API listen address = %q", got)
	}
}

func TestAdvancedSettingsPersistValidateAndCouple(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()

	if err := cfg.SetPowerWriteAttempts(MaxPowerWriteAttempts + 1); err == nil {
		t.Fatal("SetPowerWriteAttempts accepted an above-maximum value")
	}
	if err := cfg.SetPowerWriteAttempts(3); err != nil {
		t.Fatalf("SetPowerWriteAttempts() error = %v", err)
	}
	if err := cfg.SetOperationRetryDelayMs(250); err != nil {
		t.Fatalf("SetOperationRetryDelayMs() error = %v", err)
	}
	if err := cfg.SetChannelConfirmAttempts(0); err == nil {
		t.Fatal("SetChannelConfirmAttempts accepted a below-minimum value")
	}
	if err := cfg.SetChannelConfirmAttempts(8); err != nil {
		t.Fatalf("SetChannelConfirmAttempts() error = %v", err)
	}
	if err := cfg.SetConfirmReconnectThreshold(3); err != nil {
		t.Fatalf("SetConfirmReconnectThreshold() error = %v", err)
	}
	if err := cfg.SetConfirmReconnectDelayMs(500); err != nil {
		t.Fatalf("SetConfirmReconnectDelayMs() error = %v", err)
	}
	if err := cfg.SetIdentifyAttempts(4); err != nil {
		t.Fatalf("SetIdentifyAttempts() error = %v", err)
	}
	if err := cfg.SetPresenceMissThreshold(MaxPresenceMissThreshold + 1); err == nil {
		t.Fatal("SetPresenceMissThreshold accepted an above-maximum value")
	}
	if err := cfg.SetPresenceMissThreshold(3); err != nil {
		t.Fatalf("SetPresenceMissThreshold() error = %v", err)
	}
	if err := cfg.SetAbsentStationRetryLimit(8); err != nil {
		t.Fatalf("SetAbsentStationRetryLimit() error = %v", err)
	}
	if err := cfg.SetBluetoothInitRetrySeconds(5); err != nil {
		t.Fatalf("SetBluetoothInitRetrySeconds() error = %v", err)
	}

	// Coupled recovery schedule: base may not exceed max and vice versa. All
	// rejected values here stay inside their own ranges, so the failures prove
	// the cross-field checks rather than plain range validation.
	if err := cfg.SetRecoveryRetryBaseSeconds(90); err != nil {
		t.Fatalf("SetRecoveryRetryBaseSeconds() error = %v", err)
	}
	if err := cfg.SetRecoveryRetryMaxSeconds(60); err == nil {
		t.Fatal("SetRecoveryRetryMaxSeconds accepted a value below the retry base")
	}
	if err := cfg.SetRecoveryRetryMaxSeconds(90); err != nil {
		t.Fatalf("SetRecoveryRetryMaxSeconds() error = %v", err)
	}
	if err := cfg.SetRecoveryRetryBaseSeconds(100); err == nil {
		t.Fatal("SetRecoveryRetryBaseSeconds accepted a value above the retry maximum")
	}
	if err := cfg.SetRecoveryRetryBaseSeconds(45); err != nil {
		t.Fatalf("SetRecoveryRetryBaseSeconds() error = %v", err)
	}
	if err := cfg.SetRecoveryRetryMaxSeconds(600); err != nil {
		t.Fatalf("SetRecoveryRetryMaxSeconds() error = %v", err)
	}

	// Coupled read budgets: the initial read stays inside the per-station
	// operation budget and inside the scan read phase, while the status
	// refresh covers the status read. The default station operation timeout is
	// 30, so a 60s initial read must be rejected until the budget is raised.
	if err := cfg.SetInitialReadTimeoutSeconds(MaxInitialReadTimeoutSeconds); err == nil {
		t.Fatal("SetInitialReadTimeoutSeconds accepted a value above the station operation timeout")
	}
	if err := cfg.SetStationOperationTimeoutSeconds(60); err != nil {
		t.Fatalf("SetStationOperationTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetInitialReadTimeoutSeconds(60); err == nil {
		t.Fatal("SetInitialReadTimeoutSeconds accepted a value above the scan read phase timeout")
	}
	if err := cfg.SetScanReadPhaseTimeoutSeconds(20); err == nil {
		t.Fatal("SetScanReadPhaseTimeoutSeconds accepted a value below the initial read timeout")
	}
	if err := cfg.SetScanReadPhaseTimeoutSeconds(60); err != nil {
		t.Fatalf("SetScanReadPhaseTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetInitialReadTimeoutSeconds(60); err != nil {
		t.Fatalf("SetInitialReadTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetStationOperationTimeoutSeconds(35); err == nil {
		t.Fatal("SetStationOperationTimeoutSeconds accepted a value below the initial read timeout")
	}
	if err := cfg.SetScanReadPhaseTimeoutSeconds(45); err == nil {
		t.Fatal("SetScanReadPhaseTimeoutSeconds accepted a value below the initial read timeout")
	}
	if err := cfg.SetStatusReadTimeoutSeconds(60); err == nil {
		t.Fatal("SetStatusReadTimeoutSeconds accepted a value above the status refresh timeout")
	}
	if err := cfg.SetStatusReadTimeoutSeconds(25); err != nil {
		t.Fatalf("SetStatusReadTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetStatusRefreshTimeoutSeconds(10); err == nil {
		t.Fatal("SetStatusRefreshTimeoutSeconds accepted a value below the status read timeout")
	}
	if err := cfg.SetStatusRefreshTimeoutSeconds(40); err != nil {
		t.Fatalf("SetStatusRefreshTimeoutSeconds() error = %v", err)
	}
	if err := cfg.SetChannelScanFreshnessSeconds(240); err != nil {
		t.Fatalf("SetChannelScanFreshnessSeconds() error = %v", err)
	}

	if got := cfg.OperationRetryDelay(); got != 250*time.Millisecond {
		t.Fatalf("OperationRetryDelay() = %v, want 250ms", got)
	}
	if got := cfg.ChannelConfirmInterval(); got != time.Duration(DefaultChannelConfirmIntervalMs)*time.Millisecond {
		t.Fatalf("ChannelConfirmInterval() = %v, want the default %dms", got, DefaultChannelConfirmIntervalMs)
	}
	if got := cfg.RecoveryRetryBase(); got != 45*time.Second {
		t.Fatalf("RecoveryRetryBase() = %v, want 45s", got)
	}
	if got := cfg.InitialReadTimeout(); got != 60*time.Second {
		t.Fatalf("InitialReadTimeout() = %v, want 60s", got)
	}
	if got := cfg.ChannelScanFreshness(); got != 240*time.Second {
		t.Fatalf("ChannelScanFreshness() = %v, want 240s", got)
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.GetPowerWriteAttempts() != 3 || reloaded.GetOperationRetryDelayMs() != 250 ||
		reloaded.GetChannelConfirmAttempts() != 8 || reloaded.GetConfirmReconnectThreshold() != 3 ||
		reloaded.GetConfirmReconnectDelayMs() != 500 || reloaded.GetIdentifyAttempts() != 4 ||
		reloaded.GetPresenceMissThreshold() != 3 || reloaded.GetRecoveryRetryBaseSeconds() != 45 ||
		reloaded.GetRecoveryRetryMaxSeconds() != 600 || reloaded.GetAbsentStationRetryLimit() != 8 ||
		reloaded.GetInitialReadTimeoutSeconds() != 60 || reloaded.GetScanReadPhaseTimeoutSeconds() != 60 ||
		reloaded.GetStatusReadTimeoutSeconds() != 25 || reloaded.GetStatusRefreshTimeoutSeconds() != 40 ||
		reloaded.GetChannelScanFreshnessSeconds() != 240 || reloaded.GetBluetoothInitRetrySeconds() != 5 {
		t.Fatalf("reloaded advanced settings = pw %d, ord %d, cca %d, crt %d, crd %d, ia %d, pmt %d, rrb %d, rrm %d, arl %d, irt %d, srp %d, srt %d, srf %d, csf %d, bir %d",
			reloaded.GetPowerWriteAttempts(), reloaded.GetOperationRetryDelayMs(), reloaded.GetChannelConfirmAttempts(),
			reloaded.GetConfirmReconnectThreshold(), reloaded.GetConfirmReconnectDelayMs(), reloaded.GetIdentifyAttempts(),
			reloaded.GetPresenceMissThreshold(), reloaded.GetRecoveryRetryBaseSeconds(), reloaded.GetRecoveryRetryMaxSeconds(),
			reloaded.GetAbsentStationRetryLimit(), reloaded.GetInitialReadTimeoutSeconds(), reloaded.GetScanReadPhaseTimeoutSeconds(),
			reloaded.GetStatusReadTimeoutSeconds(), reloaded.GetStatusRefreshTimeoutSeconds(), reloaded.GetChannelScanFreshnessSeconds(),
			reloaded.GetBluetoothInitRetrySeconds())
	}
}

func TestLoadRepairsContradictoryAdvancedSettings(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"stationOperationTimeoutSeconds":30,"initialReadTimeoutSeconds":60,"scanReadPhaseTimeoutSeconds":15,"statusReadTimeoutSeconds":60,"statusRefreshTimeoutSeconds":10,"recoveryRetryBaseSeconds":120,"recoveryRetryMaxSeconds":60}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetInitialReadTimeoutSeconds(); got != 30 {
		t.Fatalf("repaired initial read timeout = %d, want it clamped to the station operation timeout 30", got)
	}
	if got := cfg.GetScanReadPhaseTimeoutSeconds(); got != 30 {
		t.Fatalf("repaired scan read phase timeout = %d, want it raised to the initial read timeout 30", got)
	}
	if got := cfg.GetStatusRefreshTimeoutSeconds(); got != 60 {
		t.Fatalf("repaired status refresh timeout = %d, want it raised to the status read timeout 60", got)
	}
	if got := cfg.GetRecoveryRetryBaseSeconds(); got != 60 {
		t.Fatalf("repaired recovery retry base = %d, want it clamped to the retry maximum 60", got)
	}
}

// TestLoadRepairPrefersPersistedValuesOverCorruptedAnchors guards the repair
// direction for corrupted files: when one side of an invariant fell back to
// its default (out-of-range or missing), the repair must pull that defaulted
// side toward the persisted value instead of demoting the value the user
// actually saved.
func TestLoadRepairPrefersPersistedValuesOverCorruptedAnchors(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	// stationOperationTimeoutSeconds is out of range and falls back to its
	// default (30); the persisted initial read timeout (50) must survive the
	// cross-item repair instead of being clamped down to the corrupted anchor.
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"stationOperationTimeoutSeconds":999,"initialReadTimeoutSeconds":50}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetInitialReadTimeoutSeconds(); got != 50 {
		t.Fatalf("repaired initial read timeout = %d, want the persisted 50 kept", got)
	}
	if got := cfg.GetStationOperationTimeoutSeconds(); got != 50 {
		t.Fatalf("repaired station operation timeout = %d, want it raised to the persisted initial read timeout 50", got)
	}
}

func TestLoadRepairsBulkTimeoutBelowStationTimeout(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"stationOperationTimeoutSeconds":120,"bulkPowerTimeoutSeconds":30}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetBulkPowerTimeoutSeconds(); got != 120 {
		t.Fatalf("repaired bulk power timeout = %d, want it raised to the station operation timeout 120", got)
	}
}

func TestLoadSanitizesInvalidAPIListenAddress(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"apiListenAddress":"not-an-address"}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetAPIListenAddress(); got != DefaultAPIListenAddress {
		t.Fatalf("sanitized API listen address = %q, want the default", got)
	}
}

func TestLoadSanitizesInvalidPowerTimingValues(t *testing.T) {
	configDirectory := useTemporaryConfigDirectory(t)
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"powerConfirmAttemptsOn":1,"powerConfirmAttemptsOff":9999,"powerConfirmPollIntervalMs":-5,"bootFallbackSeconds":0}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetPowerConfirmAttemptsOn(); got != DefaultPowerConfirmAttemptsOn {
		t.Fatalf("sanitized confirm attempts on = %d, want %d", got, DefaultPowerConfirmAttemptsOn)
	}
	if got := cfg.GetPowerConfirmAttemptsOff(); got != DefaultPowerConfirmAttemptsOff {
		t.Fatalf("sanitized confirm attempts off = %d, want %d", got, DefaultPowerConfirmAttemptsOff)
	}
	if got := cfg.GetPowerConfirmPollIntervalMs(); got != DefaultPowerConfirmPollIntervalMs {
		t.Fatalf("sanitized confirm poll interval = %d, want %d", got, DefaultPowerConfirmPollIntervalMs)
	}
	if got := cfg.GetBootFallbackSeconds(); got != DefaultBootFallbackSeconds {
		t.Fatalf("sanitized boot fallback = %d, want %d", got, DefaultBootFallbackSeconds)
	}
}

func TestScanAndPollingPreferencesPersist(t *testing.T) {
	useTemporaryConfigDirectory(t)
	cfg := NewConfig()
	if !cfg.GetScanOnStartup() || !cfg.GetStatusPollingEnabled() {
		t.Fatal("new configurations should enable startup scan and status polling")
	}
	if err := cfg.SetScanOnStartup(false); err != nil {
		t.Fatalf("SetScanOnStartup() error = %v", err)
	}
	if err := cfg.SetStatusPollingEnabled(false); err != nil {
		t.Fatalf("SetStatusPollingEnabled() error = %v", err)
	}
	if err := cfg.SetScanDurationSeconds(12); err != nil {
		t.Fatalf("SetScanDurationSeconds() error = %v", err)
	}
	if err := cfg.SetScanDurationSeconds(MinScanDurationSeconds - 1); err == nil {
		t.Fatal("SetScanDurationSeconds() accepted a below-minimum value")
	}

	reloaded := NewConfig()
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.GetScanOnStartup() || reloaded.GetStatusPollingEnabled() || reloaded.GetScanDurationSeconds() != 12 {
		t.Fatalf("reloaded preferences = startup %v, polling %v, scan %d", reloaded.GetScanOnStartup(), reloaded.GetStatusPollingEnabled(), reloaded.GetScanDurationSeconds())
	}
}

func TestStatusDisplayFreshnessWindowTracksLongPollingIntervals(t *testing.T) {
	tests := []struct {
		intervalSeconds int
		want            time.Duration
	}{
		{intervalSeconds: 60, want: 125 * time.Second},
		{intervalSeconds: 120, want: 245 * time.Second},
		{intervalSeconds: 300, want: 605 * time.Second},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("%d_seconds", test.intervalSeconds), func(t *testing.T) {
			cfg := NewConfig()
			cfg.StatusPollIntervalSeconds = test.intervalSeconds
			if got := cfg.StatusDisplayFreshnessWindow(); got != test.want {
				t.Fatalf("StatusDisplayFreshnessWindow() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLoadSanitizesUnknownLanguage(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(`{"language":"fr"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GetLanguage(); got != "" {
		t.Fatalf("unknown persisted language = %q, want system default marker", got)
	}
}

// TestLoadDropsEmptyLegacyAliases guards the load sanitization: the setter
// treats an empty alias as a removal, but a hand-edited file can carry
// {"LHB-X":""}. Loading it verbatim would make GetStationDisplayName return
// ("", true) and blank the station's display name; the entry is dropped
// instead, matching the setter's delete-on-empty semantics.
func TestLoadDropsEmptyLegacyAliases(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	configDirectory := filepath.Join(configRoot, "lhcontrol")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"renamedStations":{"LHB-EMPTY":"","LHB-KEPT":"Named"}}`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig()
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if name, ok := cfg.GetStationDisplayName("AA:BB:CC:DD:EE:01", "LHB-EMPTY"); ok {
		t.Fatalf("empty legacy alias loaded as %q,%v, want the tombstone treated as absent", name, ok)
	}
	if name, ok := cfg.GetStationDisplayName("AA:BB:CC:DD:EE:02", "LHB-KEPT"); !ok || name != "Named" {
		t.Fatalf("valid legacy alias = %q,%v, want Named,true", name, ok)
	}
}

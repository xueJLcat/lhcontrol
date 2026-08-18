package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lhcontrol/internal/autosleep"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

func TestBluetoothAdapterSettingsBindings(t *testing.T) {

	app := NewApp()

	adapters, err := app.ListBluetoothAdapters()

	if err != nil {

		t.Fatalf("ListBluetoothAdapters() error = %v", err)

	}

	if adapters == nil {

		t.Fatal("ListBluetoothAdapters() returned a nil slice")

	}

	// The test machine decides how many radios exist; only the shape is

	// asserted here, so the test stays valid with or without hardware.

	for _, adapter := range adapters {

		if adapter.DeviceID == "" {

			t.Fatalf("adapter %q has an empty device id", adapter.Name)

		}

	}

}

func TestAutoSleepSettingsBindings(t *testing.T) {

	t.Setenv("AppData", t.TempDir())

	app := NewApp()

	defaults := app.GetAutoSleepSettings()

	if defaults.Enabled {

		t.Fatal("fresh auto-sleep settings must be disabled by default")

	}

	invalid := defaults

	invalid.Target = "chrome"

	if err := app.SetAutoSleepSettings(invalid); err == nil {

		t.Fatal("SetAutoSleepSettings() accepted an invalid target")

	}

	invalid = defaults

	invalid.Enabled = true

	invalid.DelaySeconds = 5

	if err := app.SetAutoSleepSettings(invalid); err == nil {

		t.Fatal("SetAutoSleepSettings() accepted a below-minimum delay for an enabled configuration")

	}

	// Disabling with a stale or zero delay must succeed: the delay is not
	// consulted while the feature is off, and rejecting it would make the
	// feature impossible to turn off. The persisted delay is normalized so a
	// later re-enable starts from a valid value.
	staleDelay := defaults

	staleDelay.Enabled = false

	staleDelay.DelaySeconds = 0

	if err := app.SetAutoSleepSettings(staleDelay); err != nil {

		t.Fatalf("SetAutoSleepSettings() rejected a disabled configuration with a stale delay: %v", err)

	}

	if got := app.GetAutoSleepSettings(); got.DelaySeconds != autosleep.DefaultDelaySeconds {

		t.Fatalf("disabled settings persisted delay %d, want normalization to %d", got.DelaySeconds, autosleep.DefaultDelaySeconds)

	}

	enabled := defaults

	enabled.Enabled = true

	enabled.Target = "steam"

	enabled.DelaySeconds = 120

	if err := app.SetAutoSleepSettings(enabled); err != nil {

		t.Fatalf("SetAutoSleepSettings() error = %v", err)

	}

	defer app.stopAutoSleep()

	restarted := NewApp()

	if err := restarted.config.Load(); err != nil {

		t.Fatalf("config.Load() error = %v", err)

	}

	got := restarted.GetAutoSleepSettings()

	if !got.Enabled || got.Target != "steam" || got.DelaySeconds != 120 {

		t.Fatalf("auto-sleep settings after simulated restart = %+v", got)

	}

}

func TestAutoSleepSettingsSerializePersistenceWithWatcherReplacement(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	first := autosleep.Settings{Enabled: false, Target: "steam", DelaySeconds: 60}
	second := autosleep.Settings{Enabled: false, Target: "steamvr", DelaySeconds: 120}

	// Pause the first call at watcher application. The second call must not
	// persist a value whose watcher cannot yet be installed, otherwise the
	// eventual application order can diverge from the saved configuration.
	app.autoSleepMutex.Lock()
	watcherLockHeld := true
	releaseWatcherLock := func() {
		if watcherLockHeld {
			app.autoSleepMutex.Unlock()
			watcherLockHeld = false
		}
	}
	defer releaseWatcherLock()

	firstResult := make(chan error, 1)
	go func() { firstResult <- app.SetAutoSleepSettings(first) }()
	deadline := time.Now().Add(time.Second)
	for app.GetAutoSleepSettings() != first && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := app.GetAutoSleepSettings(); got != first {
		releaseWatcherLock()
		t.Fatalf("first auto-sleep settings were not persisted: %+v", got)
	}

	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- app.SetAutoSleepSettings(second)
	}()
	<-secondStarted
	time.Sleep(50 * time.Millisecond)
	if got := app.GetAutoSleepSettings(); got != first {
		releaseWatcherLock()
		t.Fatalf("second auto-sleep settings persisted before the first watcher application completed: %+v", got)
	}

	releaseWatcherLock()
	for name, result := range map[string]<-chan error{"first": firstResult, "second": secondResult} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("%s SetAutoSleepSettings() error = %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s SetAutoSleepSettings() did not finish", name)
		}
	}
	if got := app.GetAutoSleepSettings(); got != second {
		t.Fatalf("final auto-sleep settings = %+v, want %+v", got, second)
	}
}

func TestAutoSleepSettingsNoOpDoesNotCancelRunningWatcher(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	settings := autosleep.Settings{Enabled: true, Target: "steamvr", DelaySeconds: 60}
	if err := app.config.SetAutoSleep(settings); err != nil {
		t.Fatalf("config.SetAutoSleep() error = %v", err)
	}
	monitor := autosleep.NewMonitor(time.Minute)
	monitor.Poll(true, time.Now())
	cancelled := false
	app.autoSleepWatcher = &autosleep.Watcher{Settings: settings, Monitor: monitor}
	app.autoSleepCancel = func() { cancelled = true }

	if err := app.SetAutoSleepSettings(settings); err != nil {
		t.Fatalf("SetAutoSleepSettings() error = %v", err)
	}
	if cancelled {
		t.Fatal("saving unchanged settings cancelled the active watcher")
	}
	// Clear the synthetic watcher without invoking its sentinel cancel function.
	app.autoSleepMutex.Lock()
	app.autoSleepWatcher = nil
	app.autoSleepCancel = nil
	app.autoSleepMutex.Unlock()
}

func TestAutoSleepSettingsNoOpRepairsMissingWatcher(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	settings := autosleep.Settings{Enabled: true, Target: "steamvr", DelaySeconds: 60}
	if err := app.config.SetAutoSleep(settings); err != nil {
		t.Fatalf("config.SetAutoSleep() error = %v", err)
	}

	if err := app.SetAutoSleepSettings(settings); err != nil {
		t.Fatalf("SetAutoSleepSettings() error = %v", err)
	}
	defer app.stopAutoSleep()
	app.autoSleepMutex.Lock()
	watcher, cancel := app.autoSleepWatcher, app.autoSleepCancel
	app.autoSleepMutex.Unlock()
	if watcher == nil || cancel == nil || watcher.Settings != settings {
		t.Fatalf("missing runtime watcher was not repaired: watcher=%+v cancel=%v", watcher, cancel != nil)
	}
}

// TestRecoveryReplayDoesNotCancelMatchingAutoSleepWatcher guards the replay
// invariant that SetAutoSleepSettings documents for re-saves: when the
// recovered configuration matches the watcher that is already running, the
// replay must not rebuild it. Rebuilding cancels the watcher's context,
// aborting a sleep action mid-flight and re-arming the same session debt on
// the replacement for a duplicate scan/sleep cycle.
func TestRecoveryReplayDoesNotCancelMatchingAutoSleepWatcher(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	settings := autosleep.Settings{Enabled: true, Target: "steamvr", DelaySeconds: 60}
	if err := app.config.SetAutoSleep(settings); err != nil {
		t.Fatalf("config.SetAutoSleep() error = %v", err)
	}
	cancelled := false
	app.autoSleepMutex.Lock()
	app.autoSleepWatcher = &autosleep.Watcher{Settings: settings, Monitor: autosleep.NewMonitor(settings.Delay())}
	app.autoSleepCancel = func() { cancelled = true }
	app.autoSleepMutex.Unlock()
	// Force one replay pass: the observed generation lags the real one.
	app.configReplayGeneration.Store(app.config.RecoveryGeneration() + 1)

	app.replayRecoveredConfigRuntime()

	if cancelled {
		t.Fatal("recovery replay cancelled a watcher whose settings already match the recovered configuration")
	}
	app.autoSleepMutex.Lock()
	watcherPresent := app.autoSleepWatcher != nil
	// Clear the synthetic watcher without invoking its sentinel cancel.
	app.autoSleepWatcher = nil
	app.autoSleepCancel = nil
	app.autoSleepMutex.Unlock()
	if !watcherPresent {
		t.Fatal("recovery replay dropped the healthy watcher")
	}
}

func TestLanguageBindingsPersistAndValidate(t *testing.T) {

	t.Setenv("AppData", t.TempDir())

	app := NewApp()

	if got := app.GetLanguage(); got != "" {

		t.Fatalf("fresh language = %q, want system default marker", got)

	}

	if err := app.SetLanguage("fr"); err == nil {

		t.Fatal("SetLanguage() accepted unsupported language")

	}

	if err := app.SetLanguage("zh-CN"); err != nil {

		t.Fatalf("SetLanguage() error = %v", err)

	}

	if err := app.SetLanguage(""); err != nil {

		t.Fatalf("SetLanguage(system) error = %v", err)

	}

	if err := app.SetLanguage("zh-CN"); err != nil {

		t.Fatalf("SetLanguage() second error = %v", err)

	}

	restarted := NewApp()

	if err := restarted.config.Load(); err != nil {

		t.Fatalf("config.Load() error = %v", err)

	}

	if got := restarted.GetLanguage(); got != "zh-CN" {

		t.Fatalf("language after simulated restart = %q, want zh-CN", got)

	}

}

func TestStatusPollIntervalBindingsPersistAndValidate(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	if got := app.GetStatusPollIntervalSeconds(); got != 15 {
		t.Fatalf("fresh status poll interval = %d, want 15", got)
	}
	if err := app.SetStatusPollIntervalSeconds(4); err == nil {
		t.Fatal("SetStatusPollIntervalSeconds() accepted a below-minimum interval")
	}
	if err := app.SetStatusPollIntervalSeconds(45); err != nil {
		t.Fatalf("SetStatusPollIntervalSeconds() error = %v", err)
	}

	restarted := NewApp()
	if err := restarted.config.Load(); err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if got := restarted.GetStatusPollIntervalSeconds(); got != 45 {
		t.Fatalf("status poll interval after simulated restart = %d, want 45", got)
	}
}

func TestScanAndStatusPollingBindingsPersist(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	if !app.GetScanOnStartup() || !app.GetStatusPollingEnabled() || app.GetScanDurationSeconds() != 5 {
		t.Fatalf("fresh scan settings = startup %v, polling %v, duration %d", app.GetScanOnStartup(), app.GetStatusPollingEnabled(), app.GetScanDurationSeconds())
	}
	if err := app.SetScanOnStartup(false); err != nil {
		t.Fatalf("SetScanOnStartup() error = %v", err)
	}
	if err := app.SetStatusPollingEnabled(false); err != nil {
		t.Fatalf("SetStatusPollingEnabled() error = %v", err)
	}
	if err := app.SetScanDurationSeconds(12); err != nil {
		t.Fatalf("SetScanDurationSeconds() error = %v", err)
	}

	restarted := NewApp()
	if err := restarted.config.Load(); err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if restarted.GetScanOnStartup() || restarted.GetStatusPollingEnabled() || restarted.GetScanDurationSeconds() != 12 {
		t.Fatalf("restarted scan settings = startup %v, polling %v, duration %d", restarted.GetScanOnStartup(), restarted.GetStatusPollingEnabled(), restarted.GetScanDurationSeconds())
	}
}

func TestStationOperationTimeoutBindingsPersistAndCouple(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	if got := app.GetStationOperationTimeoutSeconds(); got != 30 {
		t.Fatalf("default station operation timeout = %d, want 30", got)
	}
	if err := app.SetStationOperationTimeoutSeconds(29); err == nil {
		t.Fatal("SetStationOperationTimeoutSeconds accepted a below-minimum value")
	}
	if err := app.SetStationOperationTimeoutSeconds(60); err != nil {
		t.Fatalf("SetStationOperationTimeoutSeconds() error = %v", err)
	}
	if err := app.SetBulkPowerTimeoutSeconds(45); err == nil {
		t.Fatal("SetBulkPowerTimeoutSeconds accepted a value below the station operation timeout")
	}

	restarted := NewApp()
	if err := restarted.config.Load(); err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if got := restarted.GetStationOperationTimeoutSeconds(); got != 60 {
		t.Fatalf("station operation timeout after restart = %d, want 60", got)
	}
}

func TestPowerTimingBindingsApplyToBluetoothPolicy(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	app.applyBluetoothTiming()
	base := bluetooth.CurrentTiming()
	if base.ConfirmAttemptsOn == 0 || base.ConfirmAttemptsOff == 0 || base.ConfirmPollInterval <= 0 || base.BootFallbackAfter <= 0 {
		t.Fatalf("applyBluetoothTiming left zero policy values: %+v", base)
	}

	if err := app.SetPowerConfirmAttemptsOn(33); err != nil {
		t.Fatalf("SetPowerConfirmAttemptsOn() error = %v", err)
	}
	if err := app.SetPowerConfirmAttemptsOff(9); err != nil {
		t.Fatalf("SetPowerConfirmAttemptsOff() error = %v", err)
	}
	if err := app.SetPowerConfirmPollIntervalMs(400); err != nil {
		t.Fatalf("SetPowerConfirmPollIntervalMs() error = %v", err)
	}
	if err := app.SetBootFallbackSeconds(15); err != nil {
		t.Fatalf("SetBootFallbackSeconds() error = %v", err)
	}
	if err := app.SetPowerConfirmAttemptsOn(config.MinPowerConfirmAttempts - 1); err == nil {
		t.Fatal("SetPowerConfirmAttemptsOn accepted a below-minimum value")
	}

	applied := bluetooth.CurrentTiming()
	if applied.ConfirmAttemptsOn != 33 || applied.ConfirmAttemptsOff != 9 ||
		applied.ConfirmPollInterval != 400*time.Millisecond || applied.BootFallbackAfter != 15*time.Second {
		t.Fatalf("bluetooth timing policy = %+v, want the configured values applied", applied)
	}

	restarted := NewApp()
	if err := restarted.config.Load(); err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if restarted.GetPowerConfirmAttemptsOn() != 33 || restarted.GetPowerConfirmAttemptsOff() != 9 ||
		restarted.GetPowerConfirmPollIntervalMs() != 400 || restarted.GetBootFallbackSeconds() != 15 {
		t.Fatalf("power timing after restart = on %d, off %d, interval %d, fallback %d",
			restarted.GetPowerConfirmAttemptsOn(), restarted.GetPowerConfirmAttemptsOff(),
			restarted.GetPowerConfirmPollIntervalMs(), restarted.GetBootFallbackSeconds())
	}
}

func TestConnectionTimingBindingsApplyToBluetoothPolicy(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	if err := app.SetSleepFinalWriteTimeoutSeconds(60); err != nil {
		t.Fatalf("SetSleepFinalWriteTimeoutSeconds() error = %v", err)
	}
	if err := app.SetSleepPrepareGapMs(100); err != nil {
		t.Fatalf("SetSleepPrepareGapMs() error = %v", err)
	}
	if err := app.SetDiscoveryAttempts(5); err != nil {
		t.Fatalf("SetDiscoveryAttempts() error = %v", err)
	}
	if err := app.SetDiscoveryRetryDelayMs(1000); err != nil {
		t.Fatalf("SetDiscoveryRetryDelayMs() error = %v", err)
	}
	if err := app.SetSleepFinalWriteTimeoutSeconds(config.MinSleepFinalWriteTimeoutSeconds - 1); err == nil {
		t.Fatal("SetSleepFinalWriteTimeoutSeconds accepted a below-minimum value")
	}

	applied := bluetooth.CurrentTiming()
	if applied.FinalSleepWrite != 60*time.Second || applied.PrepareGap != 100*time.Millisecond ||
		applied.DiscoveryAttempts != 5 || applied.DiscoveryRetryDelay != time.Second {
		t.Fatalf("bluetooth connection timing policy = %+v, want the configured values applied", applied)
	}
}

func TestAdvancedTimingBindingsApplyToBluetoothPolicy(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	if err := app.SetPowerWriteAttempts(3); err != nil {
		t.Fatalf("SetPowerWriteAttempts() error = %v", err)
	}
	if err := app.SetOperationRetryDelayMs(300); err != nil {
		t.Fatalf("SetOperationRetryDelayMs() error = %v", err)
	}
	if err := app.SetChannelConfirmAttempts(8); err != nil {
		t.Fatalf("SetChannelConfirmAttempts() error = %v", err)
	}
	if err := app.SetChannelConfirmIntervalMs(400); err != nil {
		t.Fatalf("SetChannelConfirmIntervalMs() error = %v", err)
	}
	if err := app.SetConfirmReconnectThreshold(3); err != nil {
		t.Fatalf("SetConfirmReconnectThreshold() error = %v", err)
	}
	if err := app.SetConfirmReconnectDelayMs(600); err != nil {
		t.Fatalf("SetConfirmReconnectDelayMs() error = %v", err)
	}
	if err := app.SetIdentifyAttempts(4); err != nil {
		t.Fatalf("SetIdentifyAttempts() error = %v", err)
	}
	if err := app.SetPresenceMissThreshold(3); err != nil {
		t.Fatalf("SetPresenceMissThreshold() error = %v", err)
	}
	if err := app.SetPowerWriteAttempts(config.MaxPowerWriteAttempts + 1); err == nil {
		t.Fatal("SetPowerWriteAttempts accepted an above-maximum value")
	}

	applied := bluetooth.CurrentTiming()
	if applied.WriteAttempts != 3 || applied.OperationRetryDelay != 300*time.Millisecond ||
		applied.ChannelConfirmAttempts != 8 || applied.ChannelConfirmInterval != 400*time.Millisecond ||
		applied.ConfirmReconnectThreshold != 3 || applied.ConfirmReconnectDelay != 600*time.Millisecond ||
		applied.IdentifyAttempts != 4 || applied.PresenceMissThreshold != 3 {
		t.Fatalf("bluetooth advanced timing policy = %+v, want the configured values applied", applied)
	}
}

func TestAdvancedStationBindingsPersist(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewApp()
	if err := app.SetRecoveryRetryBaseSeconds(45); err != nil {
		t.Fatalf("SetRecoveryRetryBaseSeconds() error = %v", err)
	}
	if err := app.SetRecoveryRetryMaxSeconds(600); err != nil {
		t.Fatalf("SetRecoveryRetryMaxSeconds() error = %v", err)
	}
	if err := app.SetAbsentStationRetryLimit(8); err != nil {
		t.Fatalf("SetAbsentStationRetryLimit() error = %v", err)
	}
	if err := app.SetInitialReadTimeoutSeconds(30); err != nil {
		t.Fatalf("SetInitialReadTimeoutSeconds() error = %v", err)
	}
	if err := app.SetScanReadPhaseTimeoutSeconds(60); err != nil {
		t.Fatalf("SetScanReadPhaseTimeoutSeconds() error = %v", err)
	}
	if err := app.SetStatusReadTimeoutSeconds(25); err != nil {
		t.Fatalf("SetStatusReadTimeoutSeconds() error = %v", err)
	}
	if err := app.SetStatusRefreshTimeoutSeconds(40); err != nil {
		t.Fatalf("SetStatusRefreshTimeoutSeconds() error = %v", err)
	}
	if err := app.SetChannelScanFreshnessSeconds(240); err != nil {
		t.Fatalf("SetChannelScanFreshnessSeconds() error = %v", err)
	}
	if err := app.SetBluetoothInitRetrySeconds(5); err != nil {
		t.Fatalf("SetBluetoothInitRetrySeconds() error = %v", err)
	}
	// Cross-field rule proven inside the ranges: max drops to 60, so a base of
	// 90 (legal on its own) must be rejected.
	if err := app.SetRecoveryRetryMaxSeconds(config.MinRecoveryRetryMaxSeconds); err != nil {
		t.Fatalf("SetRecoveryRetryMaxSeconds() error = %v", err)
	}
	if err := app.SetRecoveryRetryBaseSeconds(90); err == nil {
		t.Fatal("SetRecoveryRetryBaseSeconds accepted a value above the retry maximum")
	}
	if err := app.SetRecoveryRetryMaxSeconds(600); err != nil {
		t.Fatalf("SetRecoveryRetryMaxSeconds() error = %v", err)
	}

	restarted := NewApp()
	if err := restarted.config.Load(); err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if restarted.GetRecoveryRetryBaseSeconds() != 45 || restarted.GetRecoveryRetryMaxSeconds() != 600 ||
		restarted.GetAbsentStationRetryLimit() != 8 || restarted.GetInitialReadTimeoutSeconds() != 30 ||
		restarted.GetScanReadPhaseTimeoutSeconds() != 60 || restarted.GetStatusReadTimeoutSeconds() != 25 ||
		restarted.GetStatusRefreshTimeoutSeconds() != 40 || restarted.GetChannelScanFreshnessSeconds() != 240 ||
		restarted.GetBluetoothInitRetrySeconds() != 5 {
		t.Fatalf("advanced station settings after restart = base %d, max %d, limit %d, irt %d, srp %d, srt %d, srf %d, csf %d, bir %d",
			restarted.GetRecoveryRetryBaseSeconds(), restarted.GetRecoveryRetryMaxSeconds(), restarted.GetAbsentStationRetryLimit(),
			restarted.GetInitialReadTimeoutSeconds(), restarted.GetScanReadPhaseTimeoutSeconds(), restarted.GetStatusReadTimeoutSeconds(),
			restarted.GetStatusRefreshTimeoutSeconds(), restarted.GetChannelScanFreshnessSeconds(), restarted.GetBluetoothInitRetrySeconds())
	}
}

// TestBlockedConfigRecoveryReappliesRuntimeSettings guards the blocked-save
// recovery replay: when the startup Load could not read the file and a later
// setter lifts the block, the runtime state derived at startup (Bluetooth
// timing, the auto-sleep watcher, the API listener) must be re-applied from
// the recovered contents instead of silently staying on the defaults.
func TestBlockedConfigRecoveryReappliesRuntimeSettings(t *testing.T) {
	configDirectory := t.TempDir()
	t.Setenv("AppData", configDirectory)

	appConfigDir := filepath.Join(configDirectory, "lhcontrol")
	// A directory where config.json belongs makes the startup load unreadable
	// without touching the real persistence hooks.
	if err := os.MkdirAll(filepath.Join(appConfigDir, "config.json"), 0o755); err != nil {
		t.Fatalf("seed unreadable config path: %v", err)
	}

	app := NewApp()
	app.apiRetryDelay = 5 * time.Millisecond
	app.apiBindVerifyWait = 2 * time.Second
	app.listen = func(_, address string) (net.Listener, error) {
		return newFakeAPIListener(address), nil
	}
	app.serveListener = func(listener net.Listener) error {
		fake := listener.(*fakeAPIListener)
		<-fake.closed
		return nil
	}
	if err := app.config.Load(); err == nil {
		t.Fatal("config.Load() unexpectedly succeeded on the unreadable file")
	}
	app.configReplayGeneration.Store(app.config.RecoveryGeneration())
	app.startAPIServer()
	t.Cleanup(func() {
		app.stopAutoSleep()
		app.apiLifecycleMutex.Lock()
		cancel := app.apiCancel
		app.apiCancel = nil
		app.apiLifecycleMutex.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = app.api.Shutdown()
		app.apiWG.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	for !app.GetAPIStatus().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := app.GetAPIStatus(); !status.Running || status.Address != config.DefaultAPIListenAddress {
		t.Fatalf("initial API status = %+v, want running on %s", status, config.DefaultAPIListenAddress)
	}

	// Heal the file with recovered contents that differ from every default.
	if err := os.RemoveAll(filepath.Join(appConfigDir, "config.json")); err != nil {
		t.Fatalf("clear unreadable config: %v", err)
	}
	seed := `{"powerConfirmAttemptsOn":77,"apiListenAddress":"127.0.0.1:7979","autoSleep":{"enabled":true,"target":"steam","delaySeconds":60}}`
	if err := os.WriteFile(filepath.Join(appConfigDir, "config.json"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed recovered config: %v", err)
	}

	// An unrelated setter lifts the block; the replay must follow.
	if err := app.SetLanguage(config.LanguageEnglish); err != nil {
		t.Fatalf("SetLanguage() error = %v", err)
	}

	if got := bluetooth.CurrentTiming().ConfirmAttemptsOn; got != 77 {
		t.Fatalf("recovered Bluetooth timing ConfirmAttemptsOn = %d, want 77", got)
	}
	app.autoSleepMutex.Lock()
	watcherRunning := app.autoSleepWatcher != nil
	watcherSettings := autosleep.Settings{}
	if watcherRunning {
		watcherSettings = app.autoSleepWatcher.Settings
	}
	app.autoSleepMutex.Unlock()
	if !watcherRunning || !watcherSettings.Enabled || watcherSettings.Target != "steam" || watcherSettings.DelaySeconds != 60 {
		t.Fatalf("recovered auto-sleep state: running=%v settings=%+v, want an enabled steam watcher with 60s delay", watcherRunning, watcherSettings)
	}
	deadline = time.Now().Add(2 * time.Second)
	for !(app.GetAPIStatus().Running && app.GetAPIStatus().Address == "127.0.0.1:7979") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := app.GetAPIStatus(); !status.Running || status.Address != "127.0.0.1:7979" {
		t.Fatalf("API status after recovery = %+v, want running on 127.0.0.1:7979", status)
	}
}

package main

import "lhcontrol/internal/bluetooth"

func (a *App) applyBluetoothTiming() {
	// Separate settings controls can persist concurrently. Serialize the full
	// config snapshot and replacement so an older partial read cannot become
	// the final active Bluetooth policy.
	a.bluetoothTimingMutex.Lock()
	defer a.bluetoothTimingMutex.Unlock()

	bluetooth.ConfigureTiming(bluetooth.TimingPolicy{
		ConfirmAttemptsOn:         a.config.GetPowerConfirmAttemptsOn(),
		ConfirmAttemptsOff:        a.config.GetPowerConfirmAttemptsOff(),
		ConfirmPollInterval:       a.config.PowerConfirmPollInterval(),
		BootFallbackAfter:         a.config.BootFallback(),
		FinalSleepWrite:           a.config.SleepFinalWriteTimeout(),
		PrepareGap:                a.config.SleepPrepareGap(),
		DiscoveryAttempts:         a.config.GetDiscoveryAttempts(),
		DiscoveryRetryDelay:       a.config.DiscoveryRetryDelay(),
		WriteAttempts:             a.config.GetPowerWriteAttempts(),
		OperationRetryDelay:       a.config.OperationRetryDelay(),
		ChannelConfirmAttempts:    a.config.GetChannelConfirmAttempts(),
		ChannelConfirmInterval:    a.config.ChannelConfirmInterval(),
		ConfirmReconnectThreshold: a.config.GetConfirmReconnectThreshold(),
		ConfirmReconnectDelay:     a.config.ConfirmReconnectDelay(),
		IdentifyAttempts:          a.config.GetIdentifyAttempts(),
		PresenceMissThreshold:     a.config.GetPresenceMissThreshold(),
	})

}

// applyAutoSleep (re)starts the auto-sleep watcher goroutine to match the
// given settings. Calling it repeatedly is safe: the previous watcher is
// cancelled (and joined at shutdown), never torn down mid-trigger.

package main

func (a *App) GetRecoveryRetryBaseSeconds() int {

	return a.config.GetRecoveryRetryBaseSeconds()

}

func (a *App) SetRecoveryRetryBaseSeconds(baseSeconds int) error {
	a.recoverySettingsMutex.Lock()
	defer a.recoverySettingsMutex.Unlock()

	err := a.config.SetRecoveryRetryBaseSeconds(baseSeconds)

	a.setConfigPersistenceStatus()
	if err == nil {
		a.stationManager.ApplyRecoverySettings()
	}

	return err

}

func (a *App) GetRecoveryRetryMaxSeconds() int {

	return a.config.GetRecoveryRetryMaxSeconds()

}

func (a *App) SetRecoveryRetryMaxSeconds(maxSeconds int) error {
	a.recoverySettingsMutex.Lock()
	defer a.recoverySettingsMutex.Unlock()

	err := a.config.SetRecoveryRetryMaxSeconds(maxSeconds)

	a.setConfigPersistenceStatus()
	if err == nil {
		a.stationManager.ApplyRecoverySettings()
	}

	return err

}

func (a *App) GetAbsentStationRetryLimit() int {

	return a.config.GetAbsentStationRetryLimit()

}

func (a *App) SetAbsentStationRetryLimit(limit int) error {
	a.recoverySettingsMutex.Lock()
	defer a.recoverySettingsMutex.Unlock()

	// The setter reports the baseline itself: a blocked-save recovery inside
	// it can restore the persisted limit after this layer last read the
	// field, and comparing against that stale read would misjudge a raise.
	previousLimit, err := a.config.SetAbsentStationRetryLimitWithPrevious(limit)

	a.setConfigPersistenceStatus()
	if err == nil {
		a.stationManager.ApplyRecoverySettings()
		if a.config.GetAbsentStationRetryLimit() > previousLimit {
			a.stationManager.ReviveAbsentStationRecovery()
		}
	}

	return err

}

func (a *App) GetInitialReadTimeoutSeconds() int {

	return a.config.GetInitialReadTimeoutSeconds()

}

func (a *App) SetInitialReadTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetInitialReadTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetScanReadPhaseTimeoutSeconds() int {

	return a.config.GetScanReadPhaseTimeoutSeconds()

}

func (a *App) SetScanReadPhaseTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetScanReadPhaseTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusReadTimeoutSeconds() int {

	return a.config.GetStatusReadTimeoutSeconds()

}

func (a *App) SetStatusReadTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetStatusReadTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusRefreshTimeoutSeconds() int {

	return a.config.GetStatusRefreshTimeoutSeconds()

}

func (a *App) SetStatusRefreshTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetStatusRefreshTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetChannelScanFreshnessSeconds() int {

	return a.config.GetChannelScanFreshnessSeconds()

}

func (a *App) SetChannelScanFreshnessSeconds(freshnessSeconds int) error {

	err := a.config.SetChannelScanFreshnessSeconds(freshnessSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetBluetoothInitRetrySeconds() int {

	return a.config.GetBluetoothInitRetrySeconds()

}

func (a *App) SetBluetoothInitRetrySeconds(retrySeconds int) error {
	a.recoverySettingsMutex.Lock()
	defer a.recoverySettingsMutex.Unlock()

	err := a.config.SetBluetoothInitRetrySeconds(retrySeconds)

	a.setConfigPersistenceStatus()
	if err == nil {
		a.stationManager.ApplyRecoverySettings()
	}

	return err

}

// applyBluetoothTiming pushes the persisted protocol-timing settings into the
// bluetooth layer, which intentionally does not read the config package.

package main

func (a *App) GetPowerWriteAttempts() int {

	return a.config.GetPowerWriteAttempts()

}

func (a *App) SetPowerWriteAttempts(attempts int) error {

	err := a.config.SetPowerWriteAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetOperationRetryDelayMs() int {

	return a.config.GetOperationRetryDelayMs()

}

func (a *App) SetOperationRetryDelayMs(delayMs int) error {

	err := a.config.SetOperationRetryDelayMs(delayMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetChannelConfirmAttempts() int {

	return a.config.GetChannelConfirmAttempts()

}

func (a *App) SetChannelConfirmAttempts(attempts int) error {

	err := a.config.SetChannelConfirmAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetChannelConfirmIntervalMs() int {

	return a.config.GetChannelConfirmIntervalMs()

}

func (a *App) SetChannelConfirmIntervalMs(intervalMs int) error {

	err := a.config.SetChannelConfirmIntervalMs(intervalMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetConfirmReconnectThreshold() int {

	return a.config.GetConfirmReconnectThreshold()

}

func (a *App) SetConfirmReconnectThreshold(threshold int) error {

	err := a.config.SetConfirmReconnectThreshold(threshold)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetConfirmReconnectDelayMs() int {

	return a.config.GetConfirmReconnectDelayMs()

}

func (a *App) SetConfirmReconnectDelayMs(delayMs int) error {

	err := a.config.SetConfirmReconnectDelayMs(delayMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetIdentifyAttempts() int {

	return a.config.GetIdentifyAttempts()

}

func (a *App) SetIdentifyAttempts(attempts int) error {

	err := a.config.SetIdentifyAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetPresenceMissThreshold() int {

	return a.config.GetPresenceMissThreshold()

}

func (a *App) SetPresenceMissThreshold(threshold int) error {
	a.presenceSettingsMutex.Lock()
	defer a.presenceSettingsMutex.Unlock()

	err := a.config.SetPresenceMissThreshold(threshold)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

		a.stationManager.ApplyPresenceMissThreshold()

	}

	return err

}

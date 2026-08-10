package main

func (a *App) GetPowerConfirmAttemptsOn() int {

	return a.config.GetPowerConfirmAttemptsOn()

}

func (a *App) SetPowerConfirmAttemptsOn(attempts int) error {

	err := a.config.SetPowerConfirmAttemptsOn(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetPowerConfirmAttemptsOff() int {

	return a.config.GetPowerConfirmAttemptsOff()

}

func (a *App) SetPowerConfirmAttemptsOff(attempts int) error {

	err := a.config.SetPowerConfirmAttemptsOff(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetPowerConfirmPollIntervalMs() int {

	return a.config.GetPowerConfirmPollIntervalMs()

}

func (a *App) SetPowerConfirmPollIntervalMs(intervalMs int) error {

	err := a.config.SetPowerConfirmPollIntervalMs(intervalMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetBootFallbackSeconds() int {

	return a.config.GetBootFallbackSeconds()

}

func (a *App) SetBootFallbackSeconds(fallbackSeconds int) error {

	err := a.config.SetBootFallbackSeconds(fallbackSeconds)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetSleepFinalWriteTimeoutSeconds() int {

	return a.config.GetSleepFinalWriteTimeoutSeconds()

}

func (a *App) SetSleepFinalWriteTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetSleepFinalWriteTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetSleepPrepareGapMs() int {

	return a.config.GetSleepPrepareGapMs()

}

func (a *App) SetSleepPrepareGapMs(gapMs int) error {

	err := a.config.SetSleepPrepareGapMs(gapMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

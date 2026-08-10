package main

func (a *App) GetLanguage() string {

	return a.config.GetLanguage()

}

// SetLanguage validates and persists the UI language.

func (a *App) SetLanguage(language string) error {

	err := a.config.SetLanguage(language)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetBulkPowerTimeoutSeconds() int {

	return a.config.GetBulkPowerTimeoutSeconds()

}

func (a *App) GetScanOnStartup() bool {

	return a.config.GetScanOnStartup()

}

func (a *App) SetScanOnStartup(enabled bool) error {

	err := a.config.SetScanOnStartup(enabled)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetScanDurationSeconds() int {

	return a.config.GetScanDurationSeconds()

}

func (a *App) SetScanDurationSeconds(durationSeconds int) error {

	err := a.config.SetScanDurationSeconds(durationSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusPollingEnabled() bool {

	return a.config.GetStatusPollingEnabled()

}

func (a *App) SetStatusPollingEnabled(enabled bool) error {

	err := a.config.SetStatusPollingEnabled(enabled)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) SetBulkPowerTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetBulkPowerTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusPollIntervalSeconds() int {

	return a.config.GetStatusPollIntervalSeconds()

}

func (a *App) SetStatusPollIntervalSeconds(intervalSeconds int) error {

	err := a.config.SetStatusPollIntervalSeconds(intervalSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStationOperationTimeoutSeconds() int {

	return a.config.GetStationOperationTimeoutSeconds()

}

func (a *App) SetStationOperationTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetStationOperationTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

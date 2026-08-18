<script lang="ts">
  import { onDestroy } from 'svelte';
  import { History, Hourglass, LoaderCircle, Network, RefreshCw, Shuffle } from 'lucide-svelte';
  import { t } from '../../i18n.svelte';
  import { useAdvancedSettings } from './context';
  import { numericDraft } from './numeric-draft.svelte';
  import * as R from './ranges';

  const settings = useAdvancedSettings();
  const channelConfirmAttemptsDraft = numericDraft(() => settings.channelConfirmAttempts, R.MIN_CHANNEL_CONFIRM_ATTEMPTS, R.MAX_CHANNEL_CONFIRM_ATTEMPTS, settings.onChannelConfirmAttemptsChange);
  const channelConfirmIntervalDraft = numericDraft(() => settings.channelConfirmIntervalMs, R.MIN_CHANNEL_CONFIRM_INTERVAL_MS, R.MAX_CHANNEL_CONFIRM_INTERVAL_MS, settings.onChannelConfirmIntervalChange);
  const presenceMissThresholdDraft = numericDraft(() => settings.presenceMissThreshold, R.MIN_PRESENCE_MISS_THRESHOLD, R.MAX_PRESENCE_MISS_THRESHOLD, settings.onPresenceMissThresholdChange);
  const initialReadTimeoutDraft = numericDraft(() => settings.initialReadTimeoutSeconds, R.MIN_INITIAL_READ_TIMEOUT_SECONDS, R.MAX_INITIAL_READ_TIMEOUT_SECONDS, settings.onInitialReadTimeoutChange);
  const scanReadPhaseTimeoutDraft = numericDraft(() => settings.scanReadPhaseTimeoutSeconds, R.MIN_SCAN_READ_PHASE_TIMEOUT_SECONDS, R.MAX_SCAN_READ_PHASE_TIMEOUT_SECONDS, settings.onScanReadPhaseTimeoutChange);
  const statusReadTimeoutDraft = numericDraft(() => settings.statusReadTimeoutSeconds, R.MIN_STATUS_READ_TIMEOUT_SECONDS, R.MAX_STATUS_READ_TIMEOUT_SECONDS, settings.onStatusReadTimeoutChange);
  const statusRefreshTimeoutDraft = numericDraft(() => settings.statusRefreshTimeoutSeconds, R.MIN_STATUS_REFRESH_TIMEOUT_SECONDS, R.MAX_STATUS_REFRESH_TIMEOUT_SECONDS, settings.onStatusRefreshTimeoutChange);
  const channelScanFreshnessDraft = numericDraft(() => settings.channelScanFreshnessSeconds, R.MIN_CHANNEL_SCAN_FRESHNESS_SECONDS, R.MAX_CHANNEL_SCAN_FRESHNESS_SECONDS, settings.onChannelScanFreshnessChange);
  const recoveryRetryBaseDraft = numericDraft(() => settings.recoveryRetryBaseSeconds, R.MIN_RECOVERY_RETRY_BASE_SECONDS, R.MAX_RECOVERY_RETRY_BASE_SECONDS, settings.onRecoveryRetryBaseChange);
  const recoveryRetryMaxDraft = numericDraft(() => settings.recoveryRetryMaxSeconds, R.MIN_RECOVERY_RETRY_MAX_SECONDS, R.MAX_RECOVERY_RETRY_MAX_SECONDS, settings.onRecoveryRetryMaxChange);
  const absentStationRetryLimitDraft = numericDraft(() => settings.absentStationRetryLimit, R.MIN_ABSENT_STATION_RETRY_LIMIT, R.MAX_ABSENT_STATION_RETRY_LIMIT, settings.onAbsentStationRetryLimitChange);
  const bluetoothInitRetryDraft = numericDraft(() => settings.bluetoothInitRetrySeconds, R.MIN_BLUETOOTH_INIT_RETRY_SECONDS, R.MAX_BLUETOOTH_INIT_RETRY_SECONDS, settings.onBluetoothInitRetryChange);

  let apiAddressDraft = $state('');
  let knownAPIAddress: string | null = null;
  $effect(() => {
    const current = settings.apiListenAddress;
    if (current === null || current === knownAPIAddress) return;
    if (knownAPIAddress === null || apiAddressDraft === knownAPIAddress) apiAddressDraft = current;
    knownAPIAddress = current;
  });

  function commitAPIListenAddress() {
    const current = settings.apiListenAddress;
    if (current === null) return;
    const address = apiAddressDraft.trim();
    if (address === '') {
      apiAddressDraft = current;
      return;
    }
    apiAddressDraft = address;
    if (address !== current) settings.onAPIListenAddressChange(address);
  }

  // Closing the drawer with Escape or the scrim unmounts the focused input
  // without firing change; commit the edited drafts so typed values are not
  // silently discarded. commit is idempotent for unchanged values.
  onDestroy(() => {
    channelConfirmAttemptsDraft.commit();
    channelConfirmIntervalDraft.commit();
    presenceMissThresholdDraft.commit();
    commitAPIListenAddress();
    initialReadTimeoutDraft.commit();
    scanReadPhaseTimeoutDraft.commit();
    statusReadTimeoutDraft.commit();
    statusRefreshTimeoutDraft.commit();
    channelScanFreshnessDraft.commit();
    recoveryRetryBaseDraft.commit();
    recoveryRetryMaxDraft.commit();
    absentStationRetryLimitDraft.commit();
    bluetoothInitRetryDraft.commit();
  });
</script>

<section>
    <h4><Shuffle size={12} /> {t('Channel and presence')}</h4>
    {#if settings.channelConfirmAttempts !== null}
      <div class="delay-row">
        <label for="channel-confirm-attempts">{t('Channel confirmation attempts')}</label>
        <span class="delay-input">
          <input id="channel-confirm-attempts" type="number" min={R.MIN_CHANNEL_CONFIRM_ATTEMPTS} max={R.MAX_CHANNEL_CONFIRM_ATTEMPTS} step="1" bind:value={channelConfirmAttemptsDraft.draft} onchange={channelConfirmAttemptsDraft.commit} disabled={settings.channelConfirmAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many readbacks are tried after a channel change before it is reported unconfirmed. Allowed range: 1–20.')}</p>
    {:else if settings.channelConfirmAttemptsError}
      <div class="alert danger">{settings.channelConfirmAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onChannelConfirmAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading channel and presence settings...')}</p>
    {/if}

    {#if settings.channelConfirmIntervalMs !== null}
      <div class="delay-row">
        <label for="channel-confirm-interval">{t('Channel confirmation interval')}</label>
        <span class="delay-input">
          <input id="channel-confirm-interval" type="number" min={R.MIN_CHANNEL_CONFIRM_INTERVAL_MS} max={R.MAX_CHANNEL_CONFIRM_INTERVAL_MS} step="50" bind:value={channelConfirmIntervalDraft.draft} onchange={channelConfirmIntervalDraft.commit} disabled={settings.channelConfirmIntervalBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Pause between channel confirmation readbacks. Allowed range: 50–2000 milliseconds.')}</p>
    {:else if settings.channelConfirmIntervalError}
      <div class="alert danger">{settings.channelConfirmIntervalError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onChannelConfirmIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.presenceMissThreshold !== null}
      <div class="delay-row">
        <label for="presence-miss-threshold">{t('Absence miss threshold')}</label>
        <span class="delay-input">
          <input id="presence-miss-threshold" type="number" min={R.MIN_PRESENCE_MISS_THRESHOLD} max={R.MAX_PRESENCE_MISS_THRESHOLD} step="1" bind:value={presenceMissThresholdDraft.draft} onchange={presenceMissThresholdDraft.commit} disabled={settings.presenceMissThresholdBusy} />
          {t('scans')}
        </span>
      </div>
      <p class="hint">{t('How many consecutive scans must miss a station before it is marked absent. Allowed range: 1–10.')}</p>
    {:else if settings.presenceMissThresholdError}
      <div class="alert danger">{settings.presenceMissThresholdError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onPresenceMissThresholdRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Network size={12} /> {t('HTTP API')}</h4>
    {#if settings.apiListenAddress !== null}
      <div class="delay-row">
        <label for="api-listen-address">{t('Listen address')}</label>
        <span class="delay-input wide">
          <input id="api-listen-address" type="text" bind:value={apiAddressDraft} onchange={commitAPIListenAddress} disabled={settings.apiListenAddressBusy} />
        </span>
      </div>
      <p class="hint">{t('Host and port for the local HTTP API used by external tools. The server restarts on the new address after saving. Example: 127.0.0.1:7575. Ports must be between 1024 and 65535.')}</p>
    {:else if settings.apiListenAddressError}
      <div class="alert danger">{settings.apiListenAddressError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onAPIListenAddressRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading HTTP API settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><Hourglass size={12} /> {t('Read budgets')}</h4>
    {#if settings.initialReadTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="initial-read-timeout">{t('Initial read timeout')}</label>
        <span class="delay-input">
          <input id="initial-read-timeout" type="number" min={R.MIN_INITIAL_READ_TIMEOUT_SECONDS} max={R.MAX_INITIAL_READ_TIMEOUT_SECONDS} step="1" bind:value={initialReadTimeoutDraft.draft} onchange={initialReadTimeoutDraft.commit} disabled={settings.initialReadTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Per-station budget for connection plus the first state read. Cannot exceed the station operation timeout. Allowed range: 10–60 seconds.')}</p>
    {:else if settings.initialReadTimeoutError}
      <div class="alert danger">{settings.initialReadTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onInitialReadTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading read budget settings...')}</p>
    {/if}

    {#if settings.scanReadPhaseTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="scan-read-phase-timeout">{t('Scan read phase timeout')}</label>
        <span class="delay-input">
          <input id="scan-read-phase-timeout" type="number" min={R.MIN_SCAN_READ_PHASE_TIMEOUT_SECONDS} max={R.MAX_SCAN_READ_PHASE_TIMEOUT_SECONDS} step="1" bind:value={scanReadPhaseTimeoutDraft.draft} onchange={scanReadPhaseTimeoutDraft.commit} disabled={settings.scanReadPhaseTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Total budget for the initial-read phase after a scan; must cover the initial read timeout. Allowed range: 15–120 seconds.')}</p>
    {:else if settings.scanReadPhaseTimeoutError}
      <div class="alert danger">{settings.scanReadPhaseTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onScanReadPhaseTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.statusReadTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="status-read-timeout">{t('Status read timeout')}</label>
        <span class="delay-input">
          <input id="status-read-timeout" type="number" min={R.MIN_STATUS_READ_TIMEOUT_SECONDS} max={R.MAX_STATUS_READ_TIMEOUT_SECONDS} step="1" bind:value={statusReadTimeoutDraft.draft} onchange={statusReadTimeoutDraft.commit} disabled={settings.statusReadTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Per-station timeout during periodic status refreshes. Allowed range: 5–60 seconds.')}</p>
    {:else if settings.statusReadTimeoutError}
      <div class="alert danger">{settings.statusReadTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onStatusReadTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.statusRefreshTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="status-refresh-timeout">{t('Status refresh timeout')}</label>
        <span class="delay-input">
          <input id="status-refresh-timeout" type="number" min={R.MIN_STATUS_REFRESH_TIMEOUT_SECONDS} max={R.MAX_STATUS_REFRESH_TIMEOUT_SECONDS} step="1" bind:value={statusRefreshTimeoutDraft.draft} onchange={statusRefreshTimeoutDraft.commit} disabled={settings.statusRefreshTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Total budget for one periodic fleet status refresh; must cover the status read timeout. Allowed range: 10–120 seconds.')}</p>
    {:else if settings.statusRefreshTimeoutError}
      <div class="alert danger">{settings.statusRefreshTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onStatusRefreshTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.channelScanFreshnessSeconds !== null}
      <div class="delay-row">
        <label for="channel-scan-freshness">{t('Channel scan freshness')}</label>
        <span class="delay-input">
          <input id="channel-scan-freshness" type="number" min={R.MIN_CHANNEL_SCAN_FRESHNESS_SECONDS} max={R.MAX_CHANNEL_SCAN_FRESHNESS_SECONDS} step="10" bind:value={channelScanFreshnessDraft.draft} onchange={channelScanFreshnessDraft.commit} disabled={settings.channelScanFreshnessBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('How long ago a station may have last been seen in a scan before its channel is treated as unknown. Allowed range: 30–600 seconds.')}</p>
    {:else if settings.channelScanFreshnessError}
      <div class="alert danger">{settings.channelScanFreshnessError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onChannelScanFreshnessRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><History size={12} /> {t('Background recovery')}</h4>
    {#if settings.recoveryRetryBaseSeconds !== null}
      <div class="delay-row">
        <label for="recovery-retry-base">{t('Recovery retry base')}</label>
        <span class="delay-input">
          <input id="recovery-retry-base" type="number" min={R.MIN_RECOVERY_RETRY_BASE_SECONDS} max={R.MAX_RECOVERY_RETRY_BASE_SECONDS} step="5" bind:value={recoveryRetryBaseDraft.draft} onchange={recoveryRetryBaseDraft.commit} disabled={settings.recoveryRetryBaseBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Base delay for reconnecting a lost station; doubles per failure up to the maximum. Must not exceed the recovery retry maximum. Allowed range: 5–120 seconds.')}</p>
    {:else if settings.recoveryRetryBaseError}
      <div class="alert danger">{settings.recoveryRetryBaseError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onRecoveryRetryBaseRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading recovery settings...')}</p>
    {/if}

    {#if settings.recoveryRetryMaxSeconds !== null}
      <div class="delay-row">
        <label for="recovery-retry-max">{t('Recovery retry maximum')}</label>
        <span class="delay-input">
          <input id="recovery-retry-max" type="number" min={R.MIN_RECOVERY_RETRY_MAX_SECONDS} max={R.MAX_RECOVERY_RETRY_MAX_SECONDS} step="30" bind:value={recoveryRetryMaxDraft.draft} onchange={recoveryRetryMaxDraft.commit} disabled={settings.recoveryRetryMaxBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Upper bound of the escalating station recovery delay. Must not fall below the recovery retry base. Allowed range: 60–1800 seconds.')}</p>
    {:else if settings.recoveryRetryMaxError}
      <div class="alert danger">{settings.recoveryRetryMaxError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onRecoveryRetryMaxRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.absentStationRetryLimit !== null}
      <div class="delay-row">
        <label for="absent-station-retry-limit">{t('Absent station retry limit')}</label>
        <span class="delay-input">
          <input id="absent-station-retry-limit" type="number" min={R.MIN_ABSENT_STATION_RETRY_LIMIT} max={R.MAX_ABSENT_STATION_RETRY_LIMIT} step="1" bind:value={absentStationRetryLimitDraft.draft} onchange={absentStationRetryLimitDraft.commit} disabled={settings.absentStationRetryLimitBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many recovery attempts an absent station gets before its recovery schedule stops. Allowed range: 1–20.')}</p>
    {:else if settings.absentStationRetryLimitError}
      <div class="alert danger">{settings.absentStationRetryLimitError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onAbsentStationRetryLimitRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.bluetoothInitRetrySeconds !== null}
      <div class="delay-row">
        <label for="bluetooth-init-retry">{t('Adapter init retry')}</label>
        <span class="delay-input">
          <input id="bluetooth-init-retry" type="number" min={R.MIN_BLUETOOTH_INIT_RETRY_SECONDS} max={R.MAX_BLUETOOTH_INIT_RETRY_SECONDS} step="1" bind:value={bluetoothInitRetryDraft.draft} onchange={bluetoothInitRetryDraft.commit} disabled={settings.bluetoothInitRetryBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Cooldown before the Bluetooth adapter initialization is retried after a failure. Allowed range: 1–30 seconds.')}</p>
    {:else if settings.bluetoothInitRetryError}
      <div class="alert danger">{settings.bluetoothInitRetryError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onBluetoothInitRetryRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

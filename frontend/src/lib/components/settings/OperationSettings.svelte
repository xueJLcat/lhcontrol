<script lang="ts">
  import { onDestroy } from 'svelte';
  import { Cable, Gauge, LoaderCircle, RefreshCw, Timer } from 'lucide-svelte';
  import { t } from '../../i18n.svelte';
  import { useOperationSettings } from './context';
  import { numericDraft } from './numeric-draft.svelte';
  import * as R from './ranges';

  const settings = useOperationSettings();
  const bulkTimeoutDraft = numericDraft(() => settings.bulkPowerTimeoutSeconds, R.MIN_BULK_TIMEOUT_SECONDS, R.MAX_BULK_TIMEOUT_SECONDS, settings.onBulkPowerTimeoutChange);
  const stationTimeoutDraft = numericDraft(() => settings.stationOperationTimeoutSeconds, R.MIN_STATION_TIMEOUT_SECONDS, R.MAX_STATION_TIMEOUT_SECONDS, settings.onStationOperationTimeoutChange);
  const confirmAttemptsOnDraft = numericDraft(() => settings.powerConfirmAttemptsOn, R.MIN_POWER_CONFIRM_ATTEMPTS, R.MAX_POWER_CONFIRM_ATTEMPTS, settings.onPowerConfirmAttemptsOnChange);
  const confirmAttemptsOffDraft = numericDraft(() => settings.powerConfirmAttemptsOff, R.MIN_POWER_CONFIRM_ATTEMPTS, R.MAX_POWER_CONFIRM_ATTEMPTS, settings.onPowerConfirmAttemptsOffChange);
  const confirmPollIntervalDraft = numericDraft(() => settings.powerConfirmPollIntervalMs, R.MIN_POWER_CONFIRM_POLL_MS, R.MAX_POWER_CONFIRM_POLL_MS, settings.onPowerConfirmPollIntervalChange);
  const bootFallbackDraft = numericDraft(() => settings.bootFallbackSeconds, R.MIN_BOOT_FALLBACK_SECONDS, R.MAX_BOOT_FALLBACK_SECONDS, settings.onBootFallbackChange);
  const sleepFinalWriteDraft = numericDraft(() => settings.sleepFinalWriteTimeoutSeconds, R.MIN_SLEEP_FINAL_WRITE_SECONDS, R.MAX_SLEEP_FINAL_WRITE_SECONDS, settings.onSleepFinalWriteTimeoutChange);
  const sleepPrepareGapDraft = numericDraft(() => settings.sleepPrepareGapMs, R.MIN_SLEEP_PREPARE_GAP_MS, R.MAX_SLEEP_PREPARE_GAP_MS, settings.onSleepPrepareGapChange);
  const powerWriteAttemptsDraft = numericDraft(() => settings.powerWriteAttempts, R.MIN_POWER_WRITE_ATTEMPTS, R.MAX_POWER_WRITE_ATTEMPTS, settings.onPowerWriteAttemptsChange);
  const operationRetryDelayDraft = numericDraft(() => settings.operationRetryDelayMs, R.MIN_OPERATION_RETRY_DELAY_MS, R.MAX_OPERATION_RETRY_DELAY_MS, settings.onOperationRetryDelayChange);
  const discoveryAttemptsDraft = numericDraft(() => settings.discoveryAttempts, R.MIN_DISCOVERY_ATTEMPTS, R.MAX_DISCOVERY_ATTEMPTS, settings.onDiscoveryAttemptsChange);
  const discoveryRetryDelayDraft = numericDraft(() => settings.discoveryRetryDelayMs, R.MIN_DISCOVERY_RETRY_DELAY_MS, R.MAX_DISCOVERY_RETRY_DELAY_MS, settings.onDiscoveryRetryDelayChange);
  const identifyAttemptsDraft = numericDraft(() => settings.identifyAttempts, R.MIN_IDENTIFY_ATTEMPTS, R.MAX_IDENTIFY_ATTEMPTS, settings.onIdentifyAttemptsChange);
  const confirmReconnectThresholdDraft = numericDraft(() => settings.confirmReconnectThreshold, R.MIN_CONFIRM_RECONNECT_THRESHOLD, R.MAX_CONFIRM_RECONNECT_THRESHOLD, settings.onConfirmReconnectThresholdChange);
  const confirmReconnectDelayDraft = numericDraft(() => settings.confirmReconnectDelayMs, R.MIN_CONFIRM_RECONNECT_DELAY_MS, R.MAX_CONFIRM_RECONNECT_DELAY_MS, settings.onConfirmReconnectDelayChange);

  // Closing the drawer with Escape or the scrim unmounts the focused input
  // without firing change; commit the edited drafts so typed values are not
  // silently discarded. commit is idempotent for unchanged values.
  onDestroy(() => {
    bulkTimeoutDraft.commit();
    stationTimeoutDraft.commit();
    confirmAttemptsOnDraft.commit();
    confirmAttemptsOffDraft.commit();
    confirmPollIntervalDraft.commit();
    bootFallbackDraft.commit();
    sleepFinalWriteDraft.commit();
    sleepPrepareGapDraft.commit();
    powerWriteAttemptsDraft.commit();
    operationRetryDelayDraft.commit();
    discoveryAttemptsDraft.commit();
    discoveryRetryDelayDraft.commit();
    identifyAttemptsDraft.commit();
    confirmReconnectThresholdDraft.commit();
    confirmReconnectDelayDraft.commit();
  });
</script>

<section>
    <h4><Timer size={12} /> {t('Operation safety')}</h4>
    {#if settings.bulkPowerTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="bulk-power-timeout">{t('Bulk power timeout')}</label>
        <span class="delay-input">
          <input id="bulk-power-timeout" type="number" min={R.MIN_BULK_TIMEOUT_SECONDS} max={R.MAX_BULK_TIMEOUT_SECONDS} step="1" bind:value={bulkTimeoutDraft.draft} onchange={bulkTimeoutDraft.commit} disabled={settings.bulkPowerTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('A bulk power action is stopped when this total time limit is reached. Allowed range: 30–600 seconds. It must stay at least as large as the per-station operation timeout.')}</p>
    {:else if settings.bulkPowerTimeoutError}
      <div class="alert danger">{settings.bulkPowerTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onBulkPowerTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading operation safety settings...')}</p>
    {/if}

    {#if settings.stationOperationTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="station-operation-timeout">{t('Station operation timeout')}</label>
        <span class="delay-input">
          <input id="station-operation-timeout" type="number" min={R.MIN_STATION_TIMEOUT_SECONDS} max={R.MAX_STATION_TIMEOUT_SECONDS} step="1" bind:value={stationTimeoutDraft.draft} onchange={stationTimeoutDraft.commit} disabled={settings.stationOperationTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Hard limit for a single station command, including connection, verification read, write, and confirmation. Allowed range: 30–120 seconds. It cannot exceed the bulk power timeout.')}</p>
    {:else if settings.stationOperationTimeoutError}
      <div class="alert danger">{settings.stationOperationTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onStationOperationTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Gauge size={12} /> {t('Power operation timing')}</h4>
    {#if settings.powerConfirmAttemptsOn !== null}
      <div class="delay-row">
        <label for="power-confirm-attempts-on">{t('Power-on confirmation attempts')}</label>
        <span class="delay-input">
          <input id="power-confirm-attempts-on" type="number" min={R.MIN_POWER_CONFIRM_ATTEMPTS} max={R.MAX_POWER_CONFIRM_ATTEMPTS} step="1" bind:value={confirmAttemptsOnDraft.draft} onchange={confirmAttemptsOnDraft.commit} disabled={settings.powerConfirmAttemptsOnBusy} />
          {t('reads')}
        </span>
      </div>
      <p class="hint">{t('How many readbacks are tried after a power-on command before it is reported unconfirmed. Allowed range: 5–200.')}</p>
    {:else if settings.powerConfirmAttemptsOnError}
      <div class="alert danger">{settings.powerConfirmAttemptsOnError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onPowerConfirmAttemptsOnRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading power timing settings...')}</p>
    {/if}

    {#if settings.powerConfirmAttemptsOff !== null}
      <div class="delay-row">
        <label for="power-confirm-attempts-off">{t('Sleep/standby confirmation attempts')}</label>
        <span class="delay-input">
          <input id="power-confirm-attempts-off" type="number" min={R.MIN_POWER_CONFIRM_ATTEMPTS} max={R.MAX_POWER_CONFIRM_ATTEMPTS} step="1" bind:value={confirmAttemptsOffDraft.draft} onchange={confirmAttemptsOffDraft.commit} disabled={settings.powerConfirmAttemptsOffBusy} />
          {t('reads')}
        </span>
      </div>
      <p class="hint">{t('How many readbacks are tried after a sleep or standby command. Allowed range: 5–200.')}</p>
    {:else if settings.powerConfirmAttemptsOffError}
      <div class="alert danger">{settings.powerConfirmAttemptsOffError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onPowerConfirmAttemptsOffRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.powerConfirmPollIntervalMs !== null}
      <div class="delay-row">
        <label for="power-confirm-poll-interval">{t('Confirmation read interval')}</label>
        <span class="delay-input">
          <input id="power-confirm-poll-interval" type="number" min={R.MIN_POWER_CONFIRM_POLL_MS} max={R.MAX_POWER_CONFIRM_POLL_MS} step="10" bind:value={confirmPollIntervalDraft.draft} onchange={confirmPollIntervalDraft.commit} disabled={settings.powerConfirmPollIntervalBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Pause between confirmation readbacks. Allowed range: 50–2000 milliseconds.')}</p>
    {:else if settings.powerConfirmPollIntervalError}
      <div class="alert danger">{settings.powerConfirmPollIntervalError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onPowerConfirmPollIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.bootFallbackSeconds !== null}
      <div class="delay-row">
        <label for="boot-fallback">{t('Boot fallback window')}</label>
        <span class="delay-input">
          <input id="boot-fallback" type="number" min={R.MIN_BOOT_FALLBACK_SECONDS} max={R.MAX_BOOT_FALLBACK_SECONDS} step="1" bind:value={bootFallbackDraft.draft} onchange={bootFallbackDraft.commit} disabled={settings.bootFallbackBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Firmware that keeps reporting boot-like values is treated as on after this observation window. Lower it for slow-booting stations, raise it for firmware that flashes boot values while awake. Allowed range: 2–60 seconds.')}</p>
    {:else if settings.bootFallbackError}
      <div class="alert danger">{settings.bootFallbackError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onBootFallbackRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.sleepFinalWriteTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="sleep-final-write-timeout">{t('Sleep final write timeout')}</label>
        <span class="delay-input">
          <input id="sleep-final-write-timeout" type="number" min={R.MIN_SLEEP_FINAL_WRITE_SECONDS} max={R.MAX_SLEEP_FINAL_WRITE_SECONDS} step="1" bind:value={sleepFinalWriteDraft.draft} onchange={sleepFinalWriteDraft.commit} disabled={settings.sleepFinalWriteTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Hard limit for the final sleep command of the paired sleep write, which completes even when the application is shutting down. Allowed range: 5–120 seconds.')}</p>
    {:else if settings.sleepFinalWriteTimeoutError}
      <div class="alert danger">{settings.sleepFinalWriteTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onSleepFinalWriteTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.sleepPrepareGapMs !== null}
      <div class="delay-row">
        <label for="sleep-prepare-gap">{t('Sleep prepare gap')}</label>
        <span class="delay-input">
          <input id="sleep-prepare-gap" type="number" min={R.MIN_SLEEP_PREPARE_GAP_MS} max={R.MAX_SLEEP_PREPARE_GAP_MS} step="10" bind:value={sleepPrepareGapDraft.draft} onchange={sleepPrepareGapDraft.commit} disabled={settings.sleepPrepareGapBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Firmware settling delay between the prepare and final writes of the paired sleep sequence. Set 0 to skip the gap. Allowed range: 0–500 milliseconds.')}</p>
    {:else if settings.sleepPrepareGapError}
      <div class="alert danger">{settings.sleepPrepareGapError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onSleepPrepareGapRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.powerWriteAttempts !== null}
      <div class="delay-row">
        <label for="power-write-attempts">{t('Power write attempts')}</label>
        <span class="delay-input">
          <input id="power-write-attempts" type="number" min={R.MIN_POWER_WRITE_ATTEMPTS} max={R.MAX_POWER_WRITE_ATTEMPTS} step="1" bind:value={powerWriteAttemptsDraft.draft} onchange={powerWriteAttemptsDraft.commit} disabled={settings.powerWriteAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many times a power command write is retried, reconnecting between attempts. Allowed range: 1–5.')}</p>
    {:else if settings.powerWriteAttemptsError}
      <div class="alert danger">{settings.powerWriteAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onPowerWriteAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.operationRetryDelayMs !== null}
      <div class="delay-row">
        <label for="power-retry-delay">{t('Power retry delay')}</label>
        <span class="delay-input">
          <input id="power-retry-delay" type="number" min={R.MIN_OPERATION_RETRY_DELAY_MS} max={R.MAX_OPERATION_RETRY_DELAY_MS} step="100" bind:value={operationRetryDelayDraft.draft} onchange={operationRetryDelayDraft.commit} disabled={settings.operationRetryDelayBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Backoff between power command retries. Allowed range: 100–5000 milliseconds.')}</p>
    {:else if settings.operationRetryDelayError}
      <div class="alert danger">{settings.operationRetryDelayError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onOperationRetryDelayRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Cable size={12} /> {t('Connection timing')}</h4>
    {#if settings.discoveryAttempts !== null}
      <div class="delay-row">
        <label for="discovery-attempts">{t('GATT discovery attempts')}</label>
        <span class="delay-input">
          <input id="discovery-attempts" type="number" min={R.MIN_DISCOVERY_ATTEMPTS} max={R.MAX_DISCOVERY_ATTEMPTS} step="1" bind:value={discoveryAttemptsDraft.draft} onchange={discoveryAttemptsDraft.commit} disabled={settings.discoveryAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many times service discovery is retried when connecting to a station fails. Allowed range: 1–10.')}</p>
    {:else if settings.discoveryAttemptsError}
      <div class="alert danger">{settings.discoveryAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onDiscoveryAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading connection timing settings...')}</p>
    {/if}

    {#if settings.discoveryRetryDelayMs !== null}
      <div class="delay-row">
        <label for="discovery-retry-delay">{t('Discovery retry delay')}</label>
        <span class="delay-input">
          <input id="discovery-retry-delay" type="number" min={R.MIN_DISCOVERY_RETRY_DELAY_MS} max={R.MAX_DISCOVERY_RETRY_DELAY_MS} step="100" bind:value={discoveryRetryDelayDraft.draft} onchange={discoveryRetryDelayDraft.commit} disabled={settings.discoveryRetryDelayBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Pause before a discovery retry after a failed connection. Allowed range: 100–5000 milliseconds.')}</p>
    {:else if settings.discoveryRetryDelayError}
      <div class="alert danger">{settings.discoveryRetryDelayError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onDiscoveryRetryDelayRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.identifyAttempts !== null}
      <div class="delay-row">
        <label for="identify-attempts">{t('Identify attempts')}</label>
        <span class="delay-input">
          <input id="identify-attempts" type="number" min={R.MIN_IDENTIFY_ATTEMPTS} max={R.MAX_IDENTIFY_ATTEMPTS} step="1" bind:value={identifyAttemptsDraft.draft} onchange={identifyAttemptsDraft.commit} disabled={settings.identifyAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many times the identify (flash) command is retried. Allowed range: 1–5.')}</p>
    {:else if settings.identifyAttemptsError}
      <div class="alert danger">{settings.identifyAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onIdentifyAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.confirmReconnectThreshold !== null}
      <div class="delay-row">
        <label for="confirm-reconnect-threshold">{t('Confirmation reconnect threshold')}</label>
        <span class="delay-input">
          <input id="confirm-reconnect-threshold" type="number" min={R.MIN_CONFIRM_RECONNECT_THRESHOLD} max={R.MAX_CONFIRM_RECONNECT_THRESHOLD} step="1" bind:value={confirmReconnectThresholdDraft.draft} onchange={confirmReconnectThresholdDraft.commit} disabled={settings.confirmReconnectThresholdBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('Consecutive confirmation read errors before a reconnect is attempted. Allowed range: 1–5.')}</p>
    {:else if settings.confirmReconnectThresholdError}
      <div class="alert danger">{settings.confirmReconnectThresholdError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onConfirmReconnectThresholdRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.confirmReconnectDelayMs !== null}
      <div class="delay-row">
        <label for="confirm-reconnect-delay">{t('Confirmation reconnect delay')}</label>
        <span class="delay-input">
          <input id="confirm-reconnect-delay" type="number" min={R.MIN_CONFIRM_RECONNECT_DELAY_MS} max={R.MAX_CONFIRM_RECONNECT_DELAY_MS} step="50" bind:value={confirmReconnectDelayDraft.draft} onchange={confirmReconnectDelayDraft.commit} disabled={settings.confirmReconnectDelayBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Stabilization wait before a confirmation reconnect. Allowed range: 50–2000 milliseconds.')}</p>
    {:else if settings.confirmReconnectDelayError}
      <div class="alert danger">{settings.confirmReconnectDelayError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onConfirmReconnectDelayRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

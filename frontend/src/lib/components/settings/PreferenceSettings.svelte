<script lang="ts">
  import { Languages, LoaderCircle, MoonStar, Radar, RefreshCw } from 'lucide-svelte';
  import { autosleep } from '../../../../wailsjs/go/models';
  import { t } from '../../i18n.svelte';
  import { usePreferenceSettings } from './context';
  import { numericDraft } from './numeric-draft.svelte';
  import * as R from './ranges';

  const settings = usePreferenceSettings();
  const languagePreference = $derived(settings.languagePreference ?? 'system');
  const scanDurationDraft = numericDraft(
    () => settings.scanDurationSeconds,
    R.MIN_SCAN_DURATION_SECONDS,
    R.MAX_SCAN_DURATION_SECONDS,
    settings.onScanDurationChange
  );
  const statusPollIntervalDraft = numericDraft(
    () => settings.statusPollIntervalSeconds,
    R.MIN_STATUS_POLL_INTERVAL_SECONDS,
    R.MAX_STATUS_POLL_INTERVAL_SECONDS,
    settings.onStatusPollIntervalChange
  );

  let delayDraft = $state<string | number | null>('');
  let knownDelaySeconds: number | null = null;

  // Two decimal places in minutes are enough to round-trip every whole
  // second (0.01 minute is 0.6 seconds) while keeping hand-edited values such
  // as 90 seconds readable as 1.5 minutes instead of misreporting 2 minutes.
  function formatDelayMinutes(delaySeconds: number): string {
    return (delaySeconds / 60).toFixed(2).replace(/\.?0+$/, '');
  }

  $effect(() => {
    const current = settings.autoSleep ? settings.autoSleep.delaySeconds : null;
    if (current === null || current === knownDelaySeconds) return;
    if (knownDelaySeconds === null || String(delayDraft ?? '') === formatDelayMinutes(knownDelaySeconds)) {
      delayDraft = formatDelayMinutes(current);
    }
    knownDelaySeconds = current;
  });

  function commitDelay() {
    const autoSleep = settings.autoSleep;
    if (!autoSleep) return;
    const text = String(delayDraft ?? '').trim();
    const parsed = Number(text);
    if (text === '' || !Number.isFinite(parsed)) {
      delayDraft = formatDelayMinutes(autoSleep.delaySeconds);
      return;
    }
    const minutes = Math.min(R.MAX_DELAY_MINUTES, Math.max(R.MIN_DELAY_MINUTES, parsed));
    const delaySeconds = Math.round(minutes * 60);
    delayDraft = formatDelayMinutes(delaySeconds);
    if (delaySeconds !== autoSleep.delaySeconds) {
      settings.onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, delaySeconds }));
    }
  }
</script>

<section>
    <h4><Languages size={12} /> {t('Language')}</h4>
    <div class="adapter-list" role="radiogroup" aria-label={t('Display language')}>
      <label class="adapter-option" class:selected={languagePreference === 'system'}>
        <input type="radio" name="display-language" value="system" checked={languagePreference === 'system'} disabled={settings.languageBusy} onchange={() => settings.onLanguageChange?.('system')} />
        <span class="adapter-name">{t('Follow system')}</span>
      </label>
      <label class="adapter-option" class:selected={languagePreference === 'en'}>
        <input type="radio" name="display-language" value="en" checked={languagePreference === 'en'} disabled={settings.languageBusy} onchange={() => settings.onLanguageChange?.('en')} />
        <span class="adapter-name">English</span>
      </label>
      <label class="adapter-option" class:selected={languagePreference === 'zh-CN'}>
        <input type="radio" name="display-language" value="zh-CN" checked={languagePreference === 'zh-CN'} disabled={settings.languageBusy} onchange={() => settings.onLanguageChange?.('zh-CN')} />
        <span class="adapter-name">简体中文</span>
      </label>
    </div>
    <p class="hint">{t('Changes apply immediately and are saved for the next start.')}</p>
  </section>

  <section>
    <h4><Radar size={12} /> {t('Scanning and refresh')}</h4>
    {#if settings.scanOnStartup !== null}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Scan when the application starts')}</span>
          <span class="adapter-desc">{t('Discover nearby stations automatically after startup.')}</span>
        </span>
        <input type="checkbox" class="switch" aria-label={t('Scan when the application starts')} checked={settings.scanOnStartup} disabled={settings.scanOnStartupBusy} onchange={() => settings.onScanOnStartupChange(!settings.scanOnStartup)} />
      </label>
    {:else if settings.scanOnStartupError}
      <div class="alert danger">{settings.scanOnStartupError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onScanOnStartupRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.scanDurationSeconds !== null}
      <div class="delay-row">
        <label for="scan-duration">{t('Bluetooth scan duration')}</label>
        <span class="delay-input">
          <input id="scan-duration" type="number" min={R.MIN_SCAN_DURATION_SECONDS} max={R.MAX_SCAN_DURATION_SECONDS} step="1" bind:value={scanDurationDraft.draft} onchange={scanDurationDraft.commit} disabled={settings.scanDurationBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Longer scans can find slow advertisers but take more time. Allowed range: 2–30 seconds.')}</p>
    {:else if settings.scanDurationError}
      <div class="alert danger">{settings.scanDurationError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onScanDurationRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.statusPollingEnabled !== null}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Refresh station status automatically')}</span>
          <span class="adapter-desc">{t('API health continues to be monitored when station refresh is disabled.')}</span>
        </span>
        <input type="checkbox" class="switch" aria-label={t('Refresh station status automatically')} checked={settings.statusPollingEnabled} disabled={settings.statusPollingEnabledBusy} onchange={() => settings.onStatusPollingEnabledChange(!settings.statusPollingEnabled)} />
      </label>
    {:else if settings.statusPollingEnabledError}
      <div class="alert danger">{settings.statusPollingEnabledError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onStatusPollingEnabledRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if settings.statusPollIntervalSeconds !== null}
      <div class="delay-row">
        <label for="status-poll-interval">{t('Status polling interval')}</label>
        <span class="delay-input">
          <input id="status-poll-interval" type="number" min={R.MIN_STATUS_POLL_INTERVAL_SECONDS} max={R.MAX_STATUS_POLL_INTERVAL_SECONDS} step="1" bind:value={statusPollIntervalDraft.draft} onchange={statusPollIntervalDraft.commit} disabled={settings.statusPollIntervalBusy || settings.statusPollingEnabled === false} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Displayed state remains valid long enough for the selected interval. Allowed range: 5–300 seconds.')}</p>
    {:else if settings.statusPollIntervalError}
      <div class="alert danger">{settings.statusPollIntervalError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => settings.onStatusPollIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else if !settings.scanOnStartupError && !settings.scanDurationError && !settings.statusPollingEnabledError}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading scan and refresh settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><MoonStar size={12} /> {t('Auto sleep')}</h4>
    {#if settings.autoSleep}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Sleep all stations after a session ends')}</span>
          <span class="adapter-desc">{t('Scans and puts every known station to sleep')}</span>
        </span>
        <input
          type="checkbox"
          class="switch"
          aria-label={t('Enable auto sleep')}
          checked={settings.autoSleep.enabled}
          disabled={settings.autoSleepBusy}
          onchange={() => settings.onAutoSleepChange(autosleep.Settings.createFrom({ ...settings.autoSleep!, enabled: !settings.autoSleep!.enabled }))}
        />
      </label>
      {#if settings.autoSleep.enabled}
        <div class="adapter-list" role="radiogroup" aria-label={t('Auto sleep trigger')}>
          <label class="adapter-option" class:selected={settings.autoSleep.target === 'steamvr'}>
            <input
              type="radio"
              name="auto-sleep-target"
              value="steamvr"
              checked={settings.autoSleep.target === 'steamvr'}
              disabled={settings.autoSleepBusy}
              onchange={() => settings.onAutoSleepChange(autosleep.Settings.createFrom({ ...settings.autoSleep!, target: 'steamvr' }))}
            />
            <span class="adapter-text">
              <span class="adapter-name">SteamVR</span>
              <span class="adapter-desc">vrserver.exe — {t('fires while the Steam client stays open')}</span>
            </span>
          </label>
          <label class="adapter-option" class:selected={settings.autoSleep.target === 'steam'}>
            <input
              type="radio"
              name="auto-sleep-target"
              value="steam"
              checked={settings.autoSleep.target === 'steam'}
              disabled={settings.autoSleepBusy}
              onchange={() => settings.onAutoSleepChange(autosleep.Settings.createFrom({ ...settings.autoSleep!, target: 'steam' }))}
            />
            <span class="adapter-text">
              <span class="adapter-name">Steam</span>
              <span class="adapter-desc">steam.exe — {t('fires only when the Steam client fully exits')}</span>
            </span>
          </label>
        </div>
        <div class="delay-row">
          <label for="auto-sleep-delay">{t('Wait before sleeping')}</label>
          <span class="delay-input">
            <input
              id="auto-sleep-delay"
              type="number"
              min={R.MIN_DELAY_MINUTES}
              max={R.MAX_DELAY_MINUTES}
              step="0.01"
              bind:value={delayDraft}
              onchange={commitDelay}
              disabled={settings.autoSleepBusy}
            />
            {t('minutes')}
          </span>
        </div>
      {/if}
      <p class="hint">
        {t('The timer starts when the watched process closes. Reopening it cancels pending or in-progress automatic sleep. Commands already completed are kept and reported; commands not yet sent are skipped. When the timer fires, a Bluetooth operation from you skips this round instead of retrying. Settings are saved and restored on the next start.')}
      </p>
    {:else if settings.autoSleepError}
      <div class="alert danger">{settings.autoSleepError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={() => settings.onAutoSleepRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button>
      </div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading auto sleep settings...')}</p>
    {/if}
  </section>

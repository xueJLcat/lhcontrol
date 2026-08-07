<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { Bluetooth, Languages, LoaderCircle, MoonStar, RefreshCw, Timer, X } from 'lucide-svelte';
  import { autosleep } from '../../../wailsjs/go/models';
  import type { bluetooth } from '../../../wailsjs/go/models';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';
  import { t, type LanguagePreference } from '../i18n.svelte';

  const MIN_DELAY_MINUTES = 1;
  const MAX_DELAY_MINUTES = 120;
  const MIN_BULK_TIMEOUT_SECONDS = 30;
  const MAX_BULK_TIMEOUT_SECONDS = 600;
  const MIN_STATUS_POLL_INTERVAL_SECONDS = 5;
  const MAX_STATUS_POLL_INTERVAL_SECONDS = 300;

  let {
    adapters,
    loading,
    loadError,
    autoSleep,
    autoSleepError = null,
    autoSleepBusy = false,
    bulkPowerTimeoutSeconds,
    bulkPowerTimeoutError = null,
    bulkPowerTimeoutBusy = false,
    statusPollIntervalSeconds,
    statusPollIntervalError = null,
    statusPollIntervalBusy = false,
    languagePreference = 'system',
    languageBusy = false,
    inactive = false,
    onClose,
    onRefresh,
    onAutoSleepChange,
    onAutoSleepRetry,
    onBulkPowerTimeoutChange,
    onBulkPowerTimeoutRetry,
    onStatusPollIntervalChange,
    onStatusPollIntervalRetry,
    onLanguageChange = () => {}
  }: {
    adapters: bluetooth.AdapterInfo[];
    loading: boolean;
    loadError: string | null;
    autoSleep: autosleep.Settings | null;
    autoSleepError?: string | null;
    autoSleepBusy?: boolean;
    bulkPowerTimeoutSeconds: number | null;
    bulkPowerTimeoutError?: string | null;
    bulkPowerTimeoutBusy?: boolean;
    statusPollIntervalSeconds: number | null;
    statusPollIntervalError?: string | null;
    statusPollIntervalBusy?: boolean;
    languagePreference?: LanguagePreference;
    languageBusy?: boolean;
    inactive?: boolean;
    onClose: () => void;
    onRefresh: () => void;
    onAutoSleepChange: (settings: autosleep.Settings) => void;
    onAutoSleepRetry?: () => void;
    onBulkPowerTimeoutChange: (timeoutSeconds: number) => void;
    onBulkPowerTimeoutRetry?: () => void;
    onStatusPollIntervalChange: (intervalSeconds: number) => void;
    onStatusPollIntervalRetry?: () => void;
    onLanguageChange?: (language: LanguagePreference) => void;
  } = $props();

  // The delay input keeps a draft copy so typing never saves; only commit
  // (blur/Enter) pushes a change, matching the rest of the drawer.
  let delayDraft = $state('');
  $effect(() => {
    if (autoSleep) delayDraft = String(Math.round(autoSleep.delaySeconds / 60));
  });

  let bulkTimeoutDraft = $state('');
  $effect(() => {
    if (bulkPowerTimeoutSeconds !== null) bulkTimeoutDraft = String(bulkPowerTimeoutSeconds);
  });

  let statusPollIntervalDraft = $state('');
  $effect(() => {
    if (statusPollIntervalSeconds !== null) statusPollIntervalDraft = String(statusPollIntervalSeconds);
  });

  function commitDelay() {
    if (!autoSleep) return;
    const parsed = Number(delayDraft);
    const minutes = Number.isFinite(parsed)
      ? Math.min(MAX_DELAY_MINUTES, Math.max(MIN_DELAY_MINUTES, Math.round(parsed)))
      : Math.round(autoSleep.delaySeconds / 60);
    delayDraft = String(minutes);
    const delaySeconds = minutes * 60;
    if (delaySeconds !== autoSleep.delaySeconds) {
      onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, delaySeconds }));
    }
  }

  function commitBulkTimeout() {
    if (bulkPowerTimeoutSeconds === null) return;
    const parsed = Number(bulkTimeoutDraft);
    const seconds = Number.isFinite(parsed)
      ? Math.min(MAX_BULK_TIMEOUT_SECONDS, Math.max(MIN_BULK_TIMEOUT_SECONDS, Math.round(parsed)))
      : bulkPowerTimeoutSeconds;
    bulkTimeoutDraft = String(seconds);
    if (seconds !== bulkPowerTimeoutSeconds) onBulkPowerTimeoutChange(seconds);
  }

  function commitStatusPollInterval() {
    if (statusPollIntervalSeconds === null) return;
    const parsed = Number(statusPollIntervalDraft);
    const seconds = Number.isFinite(parsed)
      ? Math.min(MAX_STATUS_POLL_INTERVAL_SECONDS, Math.max(MIN_STATUS_POLL_INTERVAL_SECONDS, Math.round(parsed)))
      : statusPollIntervalSeconds;
    statusPollIntervalDraft = String(seconds);
    if (seconds !== statusPollIntervalSeconds) onStatusPollIntervalChange(seconds);
  }
</script>

<div
  class="drawer"
  role="dialog"
  aria-modal={inactive ? undefined : 'true'}
  aria-hidden={inactive ? 'true' : undefined}
  inert={inactive}
  aria-label={t('Settings')}
  tabindex="-1"
  use:focusTrap
  in:fly={dur({ x: 64, duration: 320, easing: cubicOut })}
  out:fly={dur({ x: 64, duration: 180 })}
>
  <div class="drawer-head">
    <div>
      <small>{t('Settings')}</small>
      <div class="drawer-title"><h2>{t('Preferences')}</h2></div>
    </div>
    <button type="button" class="icon-btn" title={t('Close')} aria-label={t('Close settings')} onclick={onClose}><X size={18} /></button>
  </div>

  <section>
    <h4><Languages size={12} /> {t('Language')}</h4>
    <div class="adapter-list" role="radiogroup" aria-label={t('Display language')}>
      <label class="adapter-option" class:selected={languagePreference === 'system'}>
        <input type="radio" name="display-language" value="system" checked={languagePreference === 'system'} disabled={languageBusy} onchange={() => onLanguageChange('system')} />
        <span class="adapter-name">{t('Follow system')}</span>
      </label>
      <label class="adapter-option" class:selected={languagePreference === 'en'}>
        <input type="radio" name="display-language" value="en" checked={languagePreference === 'en'} disabled={languageBusy} onchange={() => onLanguageChange('en')} />
        <span class="adapter-name">English</span>
      </label>
      <label class="adapter-option" class:selected={languagePreference === 'zh-CN'}>
        <input type="radio" name="display-language" value="zh-CN" checked={languagePreference === 'zh-CN'} disabled={languageBusy} onchange={() => onLanguageChange('zh-CN')} />
        <span class="adapter-name">简体中文</span>
      </label>
    </div>
    <p class="hint">{t('Changes apply immediately and are saved for the next start.')}</p>
  </section>

  <section>
    <h4><RefreshCw size={12} /> {t('Automatic refresh')}</h4>
    {#if statusPollIntervalSeconds !== null}
      <div class="delay-row">
        <label for="status-poll-interval">{t('Status polling interval')}</label>
        <span class="delay-input">
          <input
            id="status-poll-interval"
            type="number"
            min={MIN_STATUS_POLL_INTERVAL_SECONDS}
            max={MAX_STATUS_POLL_INTERVAL_SECONDS}
            step="1"
            bind:value={statusPollIntervalDraft}
            onchange={commitStatusPollInterval}
            disabled={statusPollIntervalBusy}
          />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Controls how often station states and application health are refreshed automatically. Allowed range: 5–300 seconds.')}</p>
    {:else if statusPollIntervalError}
      <div class="alert danger">{statusPollIntervalError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={() => onStatusPollIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button>
      </div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading automatic refresh settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><Bluetooth size={12} /> {t('Bluetooth adapter')}</h4>
    {#if loading}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Detecting Bluetooth adapters...')}</p>
    {:else if loadError}
      <div class="alert danger">{loadError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={onRefresh}><RefreshCw size={15} /> {t('Retry')}</button>
      </div>
    {:else}
      <div class="adapter-list" aria-label={t('Detected Bluetooth adapters')}>
        {#each adapters as adapter (adapter.deviceId)}
          <div class="adapter-option">
            <span class="adapter-text">
              <span class="adapter-name">{adapter.name}</span>
              <span class="adapter-id">{adapter.deviceId}</span>
            </span>
          </div>
        {:else}
          <p class="hint">{t('No Bluetooth adapters were detected on this system.')}</p>
        {/each}
      </div>
      <div class="drawer-actions">
        <button class="btn" onclick={onRefresh} disabled={loading}>
          {#if loading}<LoaderCircle class="spin" size={15} />{:else}<RefreshCw size={15} />{/if}
          {t('Refresh adapters')}
        </button>
      </div>
      <p class="hint">
        {t('Windows controls which radio handles BLE discovery and connections. The application cannot route a Lighthouse operation through one specific adapter.')}
      </p>
    {/if}
  </section>

  <section>
    <h4><Timer size={12} /> {t('Operation safety')}</h4>
    {#if bulkPowerTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="bulk-power-timeout">{t('Bulk power timeout')}</label>
        <span class="delay-input">
          <input
            id="bulk-power-timeout"
            type="number"
            min={MIN_BULK_TIMEOUT_SECONDS}
            max={MAX_BULK_TIMEOUT_SECONDS}
            step="1"
            bind:value={bulkTimeoutDraft}
            onchange={commitBulkTimeout}
            disabled={bulkPowerTimeoutBusy}
          />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('A bulk power action is stopped when this total time limit is reached. Allowed range: 30–600 seconds.')}</p>
    {:else if bulkPowerTimeoutError}
      <div class="alert danger">{bulkPowerTimeoutError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={() => onBulkPowerTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button>
      </div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading operation safety settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><MoonStar size={12} /> {t('Auto sleep')}</h4>
    {#if autoSleep}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Sleep all stations after a session ends')}</span>
          <span class="adapter-desc">{t('Scans and puts every known station to sleep')}</span>
        </span>
        <input
          type="checkbox"
          class="switch"
          aria-label={t('Enable auto sleep')}
          checked={autoSleep.enabled}
          disabled={autoSleepBusy}
          onchange={() => onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, enabled: !autoSleep.enabled }))}
        />
      </label>
      {#if autoSleep.enabled}
        <div class="adapter-list" role="radiogroup" aria-label={t('Auto sleep trigger')}>
          <label class="adapter-option" class:selected={autoSleep.target === 'steamvr'}>
            <input
              type="radio"
              name="auto-sleep-target"
              value="steamvr"
              checked={autoSleep.target === 'steamvr'}
              disabled={autoSleepBusy}
              onchange={() => onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, target: 'steamvr' }))}
            />
            <span class="adapter-text">
              <span class="adapter-name">SteamVR</span>
              <span class="adapter-desc">vrserver.exe — {t('fires while the Steam client stays open')}</span>
            </span>
          </label>
          <label class="adapter-option" class:selected={autoSleep.target === 'steam'}>
            <input
              type="radio"
              name="auto-sleep-target"
              value="steam"
              checked={autoSleep.target === 'steam'}
              disabled={autoSleepBusy}
              onchange={() => onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, target: 'steam' }))}
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
              min={MIN_DELAY_MINUTES}
              max={MAX_DELAY_MINUTES}
              step="1"
              bind:value={delayDraft}
              onchange={commitDelay}
              disabled={autoSleepBusy}
            />
            {t('minutes')}
          </span>
        </div>
      {/if}
      <p class="hint">
        {t('The timer starts when the watched process closes. Reopening it cancels pending or in-progress automatic sleep. Commands already completed are kept and reported; commands not yet sent are skipped. When the timer fires, a Bluetooth operation from you skips this round instead of retrying. Settings are saved and restored on the next start.')}
      </p>
    {:else if autoSleepError}
      <div class="alert danger">{autoSleepError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={() => onAutoSleepRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button>
      </div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading auto sleep settings...')}</p>
    {/if}
  </section>
</div>

<style>
  .drawer {
    position: fixed;
    z-index: 11;
    right: 0; top: 0; bottom: 0;
    width: min(390px, 92vw);
    background:
      linear-gradient(180deg,
        color-mix(in srgb, var(--color-primary) 65%, transparent),
        color-mix(in srgb, var(--color-on) 65%, transparent) 33%,
        color-mix(in srgb, var(--color-standby) 65%, transparent) 66%,
        color-mix(in srgb, var(--color-sleep) 65%, transparent))
        0 12px / 2px calc(100% - 24px) no-repeat,
      var(--bg-surface-solid);
    border-left: 1px solid var(--color-border);
    border-radius: var(--radius-lg) 0 0 var(--radius-lg);
    padding: 1rem 1rem 1.25rem;
    overflow: auto;
    box-shadow: var(--shadow-lg);
  }
  section {
    border-top: 1px solid var(--color-border);
    padding-top: 0.85rem;
    margin-top: 0.85rem;
    animation: rise var(--dur-3) var(--ease) backwards;
    animation-delay: 60ms;
  }
  h4 {
    margin: 0 0 0.55rem;
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
  }
  h4::before {
    content: '';
    width: 3px;
    height: 11px;
    border-radius: 2px;
    background: linear-gradient(180deg, var(--color-primary), var(--color-sleep));
  }
  .adapter-list { display: flex; flex-direction: column; gap: 0.4rem; }
  .adapter-option {
    display: flex;
    align-items: flex-start;
    gap: 0.55rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    cursor: default;
    transition:
      border-color var(--dur-1) var(--ease),
      background-color var(--dur-1) var(--ease),
      box-shadow var(--dur-1) var(--ease);
  }
  .adapter-option.selected {
    border-color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 9%, var(--bg-input));
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-primary) 10%, transparent);
  }
  .adapter-text { display: flex; flex-direction: column; gap: 0.1rem; min-width: 0; }
  .adapter-name {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: var(--fs-sm);
    font-weight: 700;
    color: var(--text-primary);
  }
  .adapter-desc { font-size: var(--fs-micro); font-weight: 600; color: var(--text-muted); }
  .adapter-id {
    font-family: var(--font-mono);
    font-size: 0.65rem;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .drawer-actions { display: flex; flex-wrap: wrap; gap: 0.3rem; margin-top: 0.8rem; }
  .drawer-actions .btn {
    flex: 0 0 auto;
    min-height: 30px;
    gap: 0.25rem;
    padding: 0.35rem 0.55rem;
    font-size: var(--fs-micro);
    white-space: nowrap;
  }
  .switch-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    cursor: pointer;
  }
  .switch-row:has(input:disabled) { cursor: wait; }
  .switch {
    appearance: none;
    position: relative;
    flex-shrink: 0;
    width: 36px;
    height: 20px;
    border-radius: var(--radius-pill);
    border: 1px solid var(--color-border-strong);
    background: var(--bg-inset);
    cursor: pointer;
    transition: background-color var(--dur-1) var(--ease), border-color var(--dur-1) var(--ease);
  }
  .switch:checked {
    background: var(--color-primary);
    border-color: var(--color-primary);
  }
  .switch::after {
    content: '';
    position: absolute;
    top: 1px; left: 1px;
    width: 16px; height: 16px;
    border-radius: 50%;
    background: #fff;
    box-shadow: var(--shadow-sm);
    transition: transform var(--dur-1) var(--ease);
  }
  .switch:checked::after { transform: translateX(16px); }
  .switch:disabled { opacity: 0.5; cursor: not-allowed; }
  .delay-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin-top: 0.55rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    font-size: var(--fs-sm);
    font-weight: 700;
    color: var(--text-primary);
  }
  .delay-input {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-muted);
  }
  .delay-input input { width: 4.2rem; text-align: right; }
  .hint {
    margin: 0.6rem 0 0;
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .hint.loading { display: flex; align-items: center; gap: 0.4rem; }
</style>

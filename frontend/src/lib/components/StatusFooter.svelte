<script lang="ts">
  import { fade } from 'svelte/transition';
  import { Activity } from 'lucide-svelte';
  import { dur } from '../motion';
  import { t } from '../i18n.svelte';

  let {
    statusMessage,
    apiRunning,
    apiError,
    apiAddress = '',
    configWarnings = [],
    configWritable = true,
    inactive = false
  }: {
    statusMessage: string;
    apiRunning: boolean;
    apiError: string;
    apiAddress?: string;
    configWarnings?: string[];
    configWritable?: boolean;
    inactive?: boolean;
  } = $props();

  let detail = $state<'config' | 'api' | null>(null);

  const apiTitle = $derived(apiError || (apiAddress ? `HTTP API ${apiAddress}` : t('HTTP API unavailable')));
  const configTitle = $derived(configWarnings.join('\n') || t('Configuration changes cannot be saved'));

  // Forget hidden detail state while an overlay owns the UI, and when the
  // config pill's own visibility condition disappears. Otherwise a panel can
  // reappear after the overlay closes, or the next pill click can close stale
  // state instead of opening the requested detail.
  $effect(() => {
    if (inactive || (detail === 'config' && !(apiRunning && (configWarnings.length > 0 || !configWritable)))) {
      detail = null;
    }
  });

  function toggle(target: 'config' | 'api') {
    detail = detail === target ? null : target;
  }
</script>

<footer>
  <Activity size={12} />
  <span class="status-text" role="status" aria-live="polite" title={statusMessage}>
    {#key statusMessage}
      <!-- fade only: the parent ellipsis container clips vertical motion. -->
      <span class="status-msg" in:fade={dur({ duration: 160 })}>{statusMessage}</span>
    {/key}
  </span>
  <!-- Hidden while the API is offline: the warning values may be stale, and
       the API pill already carries the outage. -->
  {#if apiRunning && (configWarnings.length > 0 || !configWritable)}
    <button
      type="button"
      class="config-status pill-btn"
      title={configTitle}
      aria-expanded={!inactive && detail === 'config'}
      onclick={() => toggle('config')}
    >
      {configWritable ? t('Config warning') : t('Config read-only')}
    </button>
  {/if}
  <button
    type="button"
    class="api-status pill-btn"
    class:ok={apiRunning}
    title={apiTitle}
    aria-expanded={!inactive && detail === 'api'}
    onclick={() => toggle('api')}
  >
    <span class="api-dot" aria-hidden="true"></span>
    {apiRunning ? t('API ready') : t('API offline')}
  </button>
  <!-- Detail panels also follow the footer's active state so their elevated
       layer cannot remain visible above a drawer or modal. -->
  {#if !inactive && detail === 'config' && apiRunning && (configWarnings.length > 0 || !configWritable)}
    <div class="footer-detail" transition:fade={dur({ duration: 140 })}>
      {#each configWarnings as warning}<p>{warning}</p>{/each}
      {#if !configWritable}<p>{t('Configuration changes cannot be saved.')}</p>{/if}
    </div>
  {:else if !inactive && detail === 'api'}
    <div class="footer-detail" transition:fade={dur({ duration: 140 })}>
      <p>{apiTitle}</p>
    </div>
  {/if}
</footer>

<style>
  footer {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-height: var(--footer-height);
    border-top: 1px solid var(--color-border);
    background: var(--bg-surface);
    backdrop-filter: blur(14px);
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-secondary);
    padding: 0.15rem 0.6rem;
  }
  footer > :global(svg) { flex-shrink: 0; color: var(--color-primary); }
  .pill-btn {
    appearance: none;
    background: transparent;
    border: 0;
    padding: 0;
    font: inherit;
    color: inherit;
    cursor: pointer;
    border-radius: var(--radius-xs);
  }
  .pill-btn:focus-visible { outline-offset: 1px; }
  .footer-detail {
    position: absolute;
    right: 0.6rem;
    bottom: calc(100% + 0.3rem);
    z-index: 30;
    max-width: min(340px, calc(100vw - 1.2rem));
    padding: 0.45rem 0.6rem;
    border: 1px solid var(--color-border-strong);
    border-radius: var(--radius-md);
    background: var(--bg-surface-solid);
    box-shadow: var(--shadow-md);
    font-size: var(--fs-sm);
    font-weight: 600;
    color: var(--text-secondary);
    text-align: left;
  }
  .footer-detail p { margin: 0; overflow-wrap: anywhere; }
  .footer-detail p + p { margin-top: 0.3rem; }
  .status-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* The keyed swap span needs its own box for the fade. */
  .status-msg { display: inline-block; }
  .api-status {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-weight: 800;
    color: var(--fb-error);
  }
  .config-status {
    margin-left: auto;
    flex-shrink: 0;
    font-weight: 800;
    color: var(--fb-warning);
  }
  .config-status + .api-status { margin-left: 0.4rem; }
  .status-text + .api-status { margin-left: auto; }
  .api-status.ok { color: var(--fb-success); }
  .api-dot {
    width: 6px;
    height: 6px;
    border-radius: var(--radius-pill);
    background: var(--color-danger);
    box-shadow: 0 0 6px color-mix(in srgb, var(--color-danger) 65%, transparent);
  }
  .api-status.ok .api-dot {
    background: var(--color-on);
    box-shadow: 0 0 6px color-mix(in srgb, var(--color-on) 65%, transparent);
    animation: glow-breath 2.4s ease-in-out infinite;
  }
  @keyframes glow-breath {
    50% { box-shadow: 0 0 9px color-mix(in srgb, var(--color-on) 85%, transparent); }
  }
</style>

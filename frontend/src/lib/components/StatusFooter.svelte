<script lang="ts">
  import { fade } from 'svelte/transition';
  import { Activity } from 'lucide-svelte';

  export let statusMessage: string;
  export let apiRunning: boolean;
  export let apiError: string;
  export let apiAddress = '';
  export let configWarnings: string[] = [];
  export let configWritable = true;

  $: apiTitle = apiError || (apiAddress ? `HTTP API ${apiAddress}` : 'HTTP API unavailable');
</script>

<footer>
  <Activity size={12} />
  <span class="status-text" role="status" aria-live="polite" title={statusMessage}>
    {#key statusMessage}
      <!-- fade only: the parent ellipsis container clips vertical motion. -->
      <span class="status-msg" in:fade={{ duration: 160 }}>{statusMessage}</span>
    {/key}
  </span>
  {#if configWarnings.length > 0 || !configWritable}
    <span
      class="config-status"
      title={configWarnings.join('\n') || 'Configuration changes cannot be saved'}
    >
      {configWritable ? 'Config warning' : 'Config read-only'}
    </span>
  {/if}
  <span class="api-status" class:ok={apiRunning} title={apiTitle}>
    <span class="api-dot" aria-hidden="true"></span>
    {apiRunning ? 'API ready' : 'API offline'}
  </span>
</footer>

<style>
  footer {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 28px;
    border-top: 1px solid var(--color-border);
    background: var(--bg-surface);
    backdrop-filter: blur(14px);
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-secondary);
    padding: 0.15rem 0.6rem;
  }
  footer > :global(svg) { flex-shrink: 0; color: var(--color-primary); }
  .status-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
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

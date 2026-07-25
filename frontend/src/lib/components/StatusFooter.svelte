<script lang="ts">
  import { Activity } from 'lucide-svelte';

  export let statusMessage: string;
  export let apiRunning: boolean;
  export let apiError: string;
</script>

<footer>
  <Activity size={12} />
  <span class="status-text" title={statusMessage}>{statusMessage}</span>
  <span class="api-status" class:ok={apiRunning} title={apiError || 'HTTP API 127.0.0.1:7575'}>
    <span class="api-dot" aria-hidden="true"></span>
    {apiRunning ? 'API ready' : 'API offline'}
  </span>
</footer>

<style>
  footer {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 26px;
    border-top: 1px solid var(--color-border);
    background: var(--bg-surface);
    backdrop-filter: blur(12px);
    font-size: 0.72rem;
    color: var(--text-secondary);
    padding: 0.1rem 0.6rem;
  }
  footer > :global(svg) { flex-shrink: 0; }
  .status-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .api-status {
    margin-left: auto;
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-weight: 700;
    color: #fca5a5;
  }
  .api-status.ok { color: #86efac; }
  .api-dot {
    width: 6px;
    height: 6px;
    border-radius: 999px;
    background: var(--color-danger);
    box-shadow: 0 0 6px color-mix(in srgb, var(--color-danger) 60%, transparent);
  }
  .api-status.ok .api-dot {
    background: var(--color-on);
    box-shadow: 0 0 6px color-mix(in srgb, var(--color-on) 60%, transparent);
  }
</style>

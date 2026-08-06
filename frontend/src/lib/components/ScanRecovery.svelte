<script lang="ts">
  import { RefreshCw, CircleAlert } from 'lucide-svelte';
  import type { ScanErrorKind } from '../scan-error';
  import { scanErrorCopy } from '../scan-error';

  let { kind, detail, retryDisabled = false, onRetry }: {
    kind: ScanErrorKind;
    detail: string;
    retryDisabled?: boolean;
    onRetry: () => void;
  } = $props();

  const copy = $derived(scanErrorCopy({ kind, detail }));
</script>

<div class="recovery" role="alert">
  <span class="recovery-icon"><CircleAlert size={26} /></span>
  <div class="recovery-body">
    <h3>{copy.heading}</h3>
    <p>{copy.explanation}</p>
    <ol>
      {#each copy.steps as step}
        <li>{step}</li>
      {/each}
    </ol>
    {#if detail}<p class="recovery-detail mono">{detail}</p>{/if}
  </div>
  <button class="btn primary" onclick={onRetry} disabled={retryDisabled}>
    <RefreshCw size={15} /> Retry scan
  </button>
</div>

<style>
  .recovery {
    display: flex;
    align-items: flex-start;
    gap: 0.7rem;
    padding: 0.8rem 0.85rem;
    margin-bottom: 0.7rem;
    border: 1px solid color-mix(in srgb, var(--color-danger) 38%, transparent);
    border-radius: var(--radius-lg);
    background: linear-gradient(135deg, color-mix(in srgb, var(--color-danger) 9%, var(--bg-surface-solid)), var(--bg-surface-solid));
    box-shadow: var(--shadow-sm);
  }
  .recovery-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 40px;
    height: 40px;
    flex-shrink: 0;
    border-radius: var(--radius-pill);
    color: var(--fb-error);
    background: color-mix(in srgb, var(--color-danger) 12%, var(--bg-surface-solid));
    border: 1px solid color-mix(in srgb, var(--color-danger) 32%, transparent);
  }
  .recovery-body { flex: 1; min-width: 0; }
  .recovery-body h3 {
    margin: 0;
    font-size: 0.9rem;
    font-weight: 800;
    color: var(--fb-error);
  }
  .recovery-body p { margin: 0.25rem 0 0; font-size: var(--fs-sm); font-weight: 600; color: var(--text-secondary); }
  .recovery-body ol {
    margin: 0.45rem 0 0;
    padding-left: 1.1rem;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    font-size: var(--fs-sm);
    font-weight: 600;
    color: var(--text-secondary);
  }
  .recovery-detail {
    margin-top: 0.45rem !important;
    padding: 0.25rem 0.45rem;
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-muted);
    font-size: var(--fs-xs);
    overflow-wrap: anywhere;
  }
  .recovery .btn { flex-shrink: 0; align-self: center; }
  @media (max-width: 520px) {
    .recovery { flex-wrap: wrap; }
    .recovery .btn { align-self: stretch; justify-content: center; }
  }
</style>

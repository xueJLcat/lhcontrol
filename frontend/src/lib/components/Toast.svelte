<script lang="ts">
  import { fly } from 'svelte/transition';
  import { CircleAlert, CircleCheck, Info, X } from 'lucide-svelte';
  import { dismissToast, toasts } from '../toast';
</script>

<!-- The live region stays mounted even when empty: screen readers only
     announce insertions into a region that already exists. -->
<div class="toast-stack" aria-live="polite">
  {#each $toasts as toast (toast.id)}
    <div class="toast {toast.kind}" transition:fly={{ x: 24, duration: 220 }}>
      {#if toast.kind === 'success'}<CircleCheck size={15} />
      {:else if toast.kind === 'info'}<Info size={15} />
      {:else}<CircleAlert size={15} />{/if}
      <span class="toast-text">{toast.text}</span>
      <button class="icon-btn" title="Dismiss" aria-label="Dismiss notification" on:click={() => dismissToast(toast.id)}><X size={14} /></button>
    </div>
  {/each}
</div>

<style>
  .toast-stack {
    /* Bottom-right keeps the header controls (scan, bulk power, fleet
       summary) visible while bulk operations produce toasts. */
    position: fixed;
    bottom: calc(28px + 0.75rem);
    right: 0.75rem;
    z-index: 40;
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
    width: min(360px, calc(100vw - 1.5rem));
  }
  .toast {
    position: relative;
    display: flex;
    align-items: flex-start;
    gap: 0.45rem;
    padding: 0.55rem 0.65rem 0.55rem 0.8rem;
    border-radius: var(--radius-md);
    background: var(--bg-surface-solid);
    border: 1px solid color-mix(in srgb, var(--color-danger) 40%, transparent);
    background-image: linear-gradient(90deg, color-mix(in srgb, var(--color-danger) 8%, transparent), transparent 34%);
    box-shadow: var(--shadow-lg);
    font-size: var(--fs-sm);
    font-weight: 700;
    color: var(--fb-error);
    overflow: hidden;
  }
  .toast::before {
    content: '';
    position: absolute;
    /* Inset vertically and round the strip so the toast's own border-radius
       never clips it into cut corners. */
    left: 0;
    top: 6px;
    bottom: 6px;
    width: 3px;
    border-radius: var(--radius-pill);
    background: var(--color-danger);
  }
  .toast.info { border-color: var(--color-border-strong); color: var(--text-secondary); background-image: none; }
  .toast.info::before { background: linear-gradient(180deg, var(--color-primary), var(--color-sleep)); }
  .toast.warning {
    border-color: color-mix(in srgb, var(--color-warning) 45%, transparent);
    background-image: linear-gradient(90deg, color-mix(in srgb, var(--color-warning) 8%, transparent), transparent 34%);
    color: var(--fb-warning);
  }
  .toast.warning::before { background: var(--color-warning); }
  .toast.success {
    border-color: color-mix(in srgb, var(--color-on) 40%, transparent);
    background-image: linear-gradient(90deg, color-mix(in srgb, var(--color-on) 8%, transparent), transparent 34%);
    color: var(--fb-success);
  }
  .toast.success::before { background: var(--color-on); }
  .toast > :global(svg) { flex-shrink: 0; margin-top: 0.1rem; }
  .toast-text { flex: 1; min-width: 0; overflow-wrap: anywhere; }
</style>

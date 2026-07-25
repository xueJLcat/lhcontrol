<script lang="ts">
  import { fly } from 'svelte/transition';
  import { CircleAlert, X } from 'lucide-svelte';
  import { dismissToast, toasts } from '../toast';
</script>

{#if $toasts.length}
  <div class="toast-stack" aria-live="polite">
    {#each $toasts as toast (toast.id)}
      <div class="toast {toast.kind}" transition:fly={{ x: 24, duration: 220 }}>
        <CircleAlert size={15} />
        <span class="toast-text">{toast.text}</span>
        <button class="icon-btn" title="Dismiss" on:click={() => dismissToast(toast.id)}><X size={14} /></button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .toast-stack {
    position: fixed;
    top: 0.75rem;
    right: 0.75rem;
    z-index: 40;
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
    width: min(360px, calc(100vw - 1.5rem));
  }
  .toast {
    display: flex;
    align-items: flex-start;
    gap: 0.45rem;
    padding: 0.55rem 0.65rem;
    border-radius: var(--radius-md);
    background: var(--bg-surface-solid);
    border: 1px solid rgba(248, 113, 113, 0.45);
    box-shadow: var(--shadow-lg);
    font-size: 0.78rem;
    color: #fca5a5;
  }
  .toast.info { border-color: var(--color-border-strong); color: var(--text-secondary); }
  .toast > :global(svg) { flex-shrink: 0; margin-top: 0.1rem; }
  .toast-text { flex: 1; min-width: 0; overflow-wrap: anywhere; }
</style>

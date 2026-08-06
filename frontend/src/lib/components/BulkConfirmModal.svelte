<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { scale } from 'svelte/transition';
  import { LoaderCircle, TriangleAlert, X } from 'lucide-svelte';
  import type { PowerTarget } from '../types';
  import { powerTargetLabel } from '../station';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';

  let {
    target,
    visibleCount,
    invisibleCount,
    uncertainCount,
    actionableCount,
    busy,
    onCancel,
    onConfirm
  }: {
    target: PowerTarget;
    visibleCount: number;
    invisibleCount: number;
    uncertainCount: number;
    actionableCount: number;
    busy: boolean;
    onCancel: () => void;
    onConfirm: () => void;
  } = $props();

  const targetLabel = $derived(powerTargetLabel(target).toLowerCase());
  const confirmVerb = $derived(target === 'on' ? 'Turn on' : target === 'standby' ? 'Set to standby' : 'Put to sleep');
  const confirmLabel = $derived(`${confirmVerb} ${actionableCount} ${actionableCount === 1 ? 'station' : 'stations'}`);
</script>

<!-- The dialog stops propagation so clicks inside it never dismiss the
     modal through the scrim behind it. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="modal"
  role="dialog"
  aria-modal="true"
  aria-label="Confirm bulk power"
  tabindex="-1"
  use:focusTrap
  transition:scale={dur({ start: 0.96, duration: 180, easing: cubicOut })}
  onclick={(event) => event.stopPropagation()}
>
  <div class="drawer-head">
    <div><small>Bulk power</small><h2>Set all known stations to {targetLabel}</h2></div>
    <button type="button" class="icon-btn" title="Close" aria-label="Close bulk power confirmation" onclick={onCancel} disabled={busy}><X size={18} /></button>
  </div>
  <p class="modal-note">
    <TriangleAlert size={14} aria-hidden="true" />
    Some stations are not fully verified. Commands are sent to every station the
    backend still considers reachable; results may come back unconfirmed.
  </p>
  <dl class="scope">
    <dt>Visible &amp; verified</dt><dd class="mono">{visibleCount}</dd>
    {#if uncertainCount > 0}<dt>Presence uncertain</dt><dd class="mono">{uncertainCount}</dd>{/if}
    {#if invisibleCount > 0}<dt>Not seen in latest scan</dt><dd class="mono">{invisibleCount}</dd>{/if}
    <dt>Actionable for {targetLabel}</dt><dd class="mono">{actionableCount}</dd>
  </dl>
  {#if busy}<p class="busy-note" role="status"><LoaderCircle class="spin" size={12} /> Applying bulk power...</p>{/if}
  <div class="modal-actions">
    <button class="btn" onclick={onCancel} disabled={busy}>Cancel</button>
    <button class="btn primary" onclick={onConfirm} disabled={busy || actionableCount === 0}>{confirmLabel}</button>
  </div>
</div>

<style>
  .modal {
    width: min(420px, 100%);
    max-height: calc(100vh - 2rem);
    overflow-y: auto;
    box-sizing: border-box;
    background: var(--bg-surface-solid);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    padding: 1rem;
    box-shadow: var(--shadow-lg), inset 0 1px 0 rgba(255, 255, 255, 0.9);
  }
  .modal-note {
    display: flex;
    align-items: flex-start;
    gap: 0.4rem;
    margin: 0.7rem 0 0;
    padding: 0.5rem 0.6rem;
    border-radius: var(--radius-md);
    border: 1px solid color-mix(in srgb, var(--color-warning) 40%, transparent);
    background: linear-gradient(135deg, color-mix(in srgb, var(--color-warning) 10%, var(--bg-surface-solid)), color-mix(in srgb, var(--color-warning) 4%, var(--bg-surface-solid)));
    color: var(--fb-warning);
    font-size: var(--fs-sm);
    font-weight: 600;
    line-height: 1.4;
  }
  .modal-note > :global(svg) { flex-shrink: 0; margin-top: 0.1rem; }
  .scope {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 0.35rem 0.5rem;
    margin: 0.75rem 0 0;
    font-size: var(--fs-sm);
  }
  .scope dt { color: var(--text-muted); font-weight: 600; }
  .scope dd { margin: 0; color: var(--text-primary); font-weight: 800; text-align: right; }
  .busy-note {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin: 0.7rem 0 0;
    font-size: var(--fs-sm);
    font-weight: 800;
    color: var(--color-primary-deep);
  }
  .busy-note > :global(svg) { flex-shrink: 0; }
  .modal-actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; justify-content: flex-end; margin-top: 0.9rem; }
</style>

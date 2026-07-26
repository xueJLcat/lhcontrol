<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { scale } from 'svelte/transition';
  import { Eye, X } from 'lucide-svelte';
  import type { StationInfo } from '../types';
  import { focusTrap } from '../actions';

  export let station: StationInfo;
  export let occupiedChannels: Map<number, string>;
  export let hasUnknownVisibleChannel: boolean;
  export let error: string;
  export let busy: boolean;
  export let locked: boolean;
  export let onClose: () => void;
  export let onSave: (channel: number, allowUnknownConflictRisk: boolean) => void;
  export let onIdentify: (station: StationInfo) => void;

  let targetChannel = station.channel > 0 ? station.channel : 1;
  let confirmUnknownChannelRisk = false;

  $: unchanged = station.channel > 0 && station.channel === targetChannel;
  $: saveDisabled = !station.scanFresh || unchanged || occupiedChannels.has(targetChannel) ||
    (hasUnknownVisibleChannel && !confirmUnknownChannelRisk) || busy || locked;
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="modal"
  role="dialog"
  aria-modal="true"
  aria-label="Change channel"
  tabindex="-1"
  use:focusTrap
  transition:scale={{ start: 0.96, duration: 180, easing: cubicOut }}
  on:click|stopPropagation
>
  <div class="drawer-head">
    <div><small>Safe channel change</small><h2>{station.name}</h2></div>
    <button class="icon-btn" title="Close" on:click={onClose} disabled={busy}><X size={18} /></button>
  </div>
  <dl>
    <dt>Original name</dt><dd>{station.originalName}</dd>
    <dt>Address</dt><dd class="mono">{station.address}</dd>
    <dt>Current channel</dt><dd class="mono">{station.channel || 'Unknown'}</dd>
  </dl>
  <label class="field">Target channel
    <select bind:value={targetChannel} disabled={busy || locked}>
      {#each Array.from({ length: 16 }, (_, index) => index + 1) as channel}
        <option value={channel} disabled={occupiedChannels.has(channel)}>
          {channel}{occupiedChannels.has(channel) ? ` — ${occupiedChannels.get(channel)}` : ''}
        </option>
      {/each}
    </select>
  </label>
  {#if hasUnknownVisibleChannel}
    <label class="risk"><input type="checkbox" bind:checked={confirmUnknownChannelRisk} disabled={busy || locked} /> I understand that a visible station has an unknown channel, so a conflict cannot be fully ruled out.</label>
  {/if}
  {#if error}<div class="alert danger">{error}</div>{/if}
  <p class="hint">The value is only accepted after the base station reads back the requested channel. Failure will not trigger an automatic rollback.</p>
  <div class="modal-actions">
    <button class="btn" on:click={() => onIdentify(station)} disabled={busy || locked}><Eye size={15} /> Identify this station</button>
    <button class="btn primary" on:click={() => onSave(targetChannel, confirmUnknownChannelRisk)} disabled={saveDisabled}>Confirm change</button>
  </div>
</div>

<style>
  .modal {
    width: min(440px, 100%);
    background: var(--bg-surface-solid);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    padding: 1rem;
    box-shadow: var(--shadow-lg);
  }
  .drawer-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .drawer-head small {
    color: var(--text-muted);
    font-size: 0.66rem;
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .drawer-head h2 { margin: 0.1rem 0 0; font-size: 1.05rem; font-weight: 800; color: var(--text-primary); overflow-wrap: anywhere; }
  dl { display: grid; grid-template-columns: 7.5rem minmax(0, 1fr); gap: 0.4rem; margin: 0.8rem 0 0; font-size: 0.78rem; }
  dt { color: var(--text-muted); }
  dd { margin: 0; color: var(--text-primary); overflow-wrap: anywhere; }
  .field { display: flex; flex-direction: column; gap: 0.35rem; margin-top: 0.9rem; font-size: 0.8rem; color: var(--text-secondary); }
  .risk { display: flex; gap: 0.5rem; align-items: flex-start; margin-top: 0.8rem; font-size: 0.75rem; line-height: 1.4; color: var(--color-warning); }
  .risk input { margin-top: 0.15rem; }
  .hint { font-size: 0.72rem; color: var(--text-muted); line-height: 1.45; }
  .modal-actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; justify-content: flex-end; margin-top: 0.8rem; }
</style>

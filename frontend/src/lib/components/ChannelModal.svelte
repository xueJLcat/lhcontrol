<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { scale } from 'svelte/transition';
  import { Eye, X } from 'lucide-svelte';
  import type { StationInfo } from '../types';
  import { focusTrap } from '../actions';

  export let station: StationInfo;
  export let occupiedChannels: Map<number, string[]>;
  export let hasUnknownVisibleChannel: boolean;
  export let error: string;
  export let warning = false;
  export let busy: boolean;
  export let locked: boolean;
  export let onClose: () => void;
  export let onSave: (channel: number, allowUnknownConflictRisk: boolean) => void;
  export let onIdentify: (station: StationInfo) => void;

  function initialTargetChannel(channel: number, occupied: Map<number, string[]>): number {
    if (channel > 0 && !occupied.has(channel)) return channel;
    for (let candidate = 1; candidate <= 16; candidate += 1) {
      if (!occupied.has(candidate)) return candidate;
    }
    return channel > 0 ? channel : 1;
  }

  let targetChannel = initialTargetChannel(station.channel, occupiedChannels);
  let confirmUnknownChannelRisk = false;

  $: unchanged = station.isPresent && station.scanFresh && station.channelFresh &&
    station.channel > 0 && station.channel === targetChannel;
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
  <fieldset class="ch-field">
    <legend>Target channel</legend>
    <div class="ch-grid" role="group" aria-label="Target channel">
      {#each Array.from({ length: 16 }, (_, index) => index + 1) as channel}
        <!-- Occupied cells stay focusable and keep their tooltip: Chromium
             suppresses hover events and title tooltips on truly disabled
             buttons, which would hide who occupies the channel. -->
        <button
          type="button"
          class="ch-cell mono"
          class:selected={targetChannel === channel}
          class:current={station.channel > 0 && station.channel === channel}
          class:occupied={occupiedChannels.has(channel)}
          disabled={busy || locked}
          aria-disabled={occupiedChannels.has(channel)}
          title={occupiedChannels.has(channel) ? `Occupied by ${occupiedChannels.get(channel)?.join(', ')}` : `Channel ${channel}`}
          aria-pressed={targetChannel === channel}
          on:click={() => { if (!occupiedChannels.has(channel)) targetChannel = channel; }}
        >{channel}{#if station.channel > 0 && station.channel === channel}<span class="ch-dot" aria-hidden="true"></span>{/if}</button>
      {/each}
    </div>
    <p class="hint ch-hint">Struck-through channels are occupied by a visible station. The dot marks the current channel.</p>
  </fieldset>
  {#if hasUnknownVisibleChannel}
    <label class="risk"><input type="checkbox" bind:checked={confirmUnknownChannelRisk} disabled={busy || locked} /> I understand that a visible station has an unknown channel, so a conflict cannot be fully ruled out.</label>
  {/if}
  {#if error}<div class="alert" class:danger={!warning} class:warning>{error}</div>{/if}
  <p class="hint">The value is only accepted after the base station reads back the requested channel. Failure will not trigger an automatic rollback.</p>
  <div class="modal-actions">
    <button class="btn" on:click={() => onIdentify(station)} disabled={busy || locked}><Eye size={15} /> Identify this station</button>
    <button class="btn primary" on:click={() => onSave(targetChannel, confirmUnknownChannelRisk)} disabled={saveDisabled}>Confirm change</button>
  </div>
</div>

<style>
  .modal {
    width: min(440px, 100%);
    max-height: calc(100vh - 2rem);
    overflow-y: auto;
    box-sizing: border-box;
    background: var(--bg-surface-solid);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    padding: 1rem;
    box-shadow: var(--shadow-lg);
  }
  .drawer-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .drawer-head small {
    color: var(--text-muted);
    font-size: var(--fs-micro);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .drawer-head h2 { margin: 0.1rem 0 0; font-size: var(--fs-h2); font-weight: 800; color: var(--text-primary); overflow-wrap: anywhere; }
  dl { display: grid; grid-template-columns: 7.5rem minmax(0, 1fr); gap: 0.4rem; margin: 0.8rem 0 0; font-size: var(--fs-sm); }
  dt { color: var(--text-muted); }
  dd { margin: 0; color: var(--text-primary); overflow-wrap: anywhere; }

  .ch-field { border: 0; padding: 0; margin: 0.9rem 0 0; min-width: 0; }
  .ch-field legend {
    padding: 0;
    margin-bottom: 0.45rem;
    font-size: 0.8rem;
    color: var(--text-secondary);
  }
  .ch-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 0.4rem;
  }
  .ch-cell {
    position: relative;
    min-height: 38px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    background: var(--bg-input);
    color: var(--text-primary);
    font-size: 0.85rem;
    font-weight: 700;
    cursor: pointer;
    transition: background-color var(--dur-1) var(--ease), border-color var(--dur-1) var(--ease),
      color var(--dur-1) var(--ease), box-shadow var(--dur-1) var(--ease), transform 80ms var(--ease);
  }
  .ch-cell:hover:not(:disabled):not(.occupied) {
    background: var(--bg-surface-hover);
    border-color: var(--color-border-strong);
  }
  .ch-cell:active:not(:disabled):not(.occupied) { transform: translateY(1px); }
  .ch-cell.selected {
    border-color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 20%, var(--bg-input));
    color: #fff;
    box-shadow: 0 0 12px color-mix(in srgb, var(--color-primary) 30%, transparent);
  }
  .ch-cell.current:not(.selected) { border-color: var(--color-border-strong); }
  .ch-cell:disabled, .ch-cell.occupied {
    cursor: not-allowed;
    opacity: 0.35;
    text-decoration: line-through;
  }
  .ch-dot {
    position: absolute;
    top: 4px;
    right: 4px;
    width: 5px;
    height: 5px;
    border-radius: var(--radius-pill);
    background: var(--color-primary);
    box-shadow: 0 0 5px color-mix(in srgb, var(--color-primary) 70%, transparent);
  }
  .ch-hint { margin: 0.5rem 0 0; }

  .risk { display: flex; gap: 0.5rem; align-items: flex-start; margin-top: 0.8rem; font-size: 0.75rem; line-height: 1.4; color: var(--color-warning); }
  .risk input { margin-top: 0.15rem; }
  .hint { font-size: var(--fs-sm); color: var(--text-muted); line-height: 1.45; }
  .modal-actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; justify-content: flex-end; margin-top: 0.8rem; }
</style>

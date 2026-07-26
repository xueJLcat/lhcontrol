<script lang="ts">
  import {
    Bluetooth, Check, ChevronRight, LoaderCircle, Moon, Pause,
    SquarePen, TriangleAlert, X, Zap
  } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
  import { canSetPower, channelLabel, isCurrentPowerState, stateLabel } from '../station';
  import { autofocus } from '../actions';

  export let station: StationInfo;
  export let renaming: boolean;
  export let feedback: PowerFeedback | undefined = undefined;
  export let pendingTarget: PowerTarget | undefined = undefined;
  export let busy: boolean;
  export let locked: boolean;
  export let onPower: (station: StationInfo, state: PowerTarget) => void;
  export let onOpenDetails: (station: StationInfo) => void;
  export let onStartRename: (station: StationInfo) => void;
  export let onSaveRename: (station: StationInfo, name: string) => void;
  export let onCancelRename: () => void;

  let localName = '';
  let wasRenaming = false;

  $: if (renaming !== wasRenaming) {
    if (renaming) localName = station.name;
    wasRenaming = renaming;
  }

  $: hasKnownPower = station.powerState >= 0;
  $: stalePower = hasKnownPower && !station.powerFresh;
  $: unverified = hasKnownPower && station.powerFresh && !station.powerStateConfirmed;

  function openDetails() {
    if (!renaming) onOpenDetails(station);
  }

  function handleRenameKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.stopPropagation();
      onSaveRename(station, localName.trim());
    } else if (event.key === 'Escape') {
      event.stopPropagation();
      onCancelRename();
    }
  }
</script>

<div
  class="station-card state-{stateLabel(station)}"
  class:offline={!station.isPresent}
  class:conflict={station.channelConflict}
  class:renaming
>
  {#if renaming}
    <div class="rename-row">
      <input
        use:autofocus
        bind:value={localName}
        maxlength="32"
        aria-label="Station name"
        on:keydown={handleRenameKeydown}
        on:blur={() => onSaveRename(station, localName.trim())}
        on:click|stopPropagation
      />
      <button class="icon-btn" title="Save name" on:mousedown|preventDefault={() => onSaveRename(station, localName.trim())}><Check size={16} /></button>
      <button class="icon-btn" title="Cancel" on:mousedown|preventDefault={onCancelRename}><X size={16} /></button>
    </div>
  {:else}
    <div class="card-top">
      <span class="status-dot dot-{stateLabel(station)}" class:breathe={station.powerFresh && station.powerState === 3} aria-hidden="true"></span>
      <h3 title={station.name}>{station.name}</h3>
      <button class="icon-btn rename-btn" title="Rename" aria-label={`Rename ${station.name}`} on:click|stopPropagation={() => onStartRename(station)}><SquarePen size={13} /></button>
      <span class="spacer"></span>
      {#if station.channelConflict}<TriangleAlert size={13} class="conflict-icon" />{/if}
      <span class="channel-chip mono" class:warn={station.channelConflict}>{channelLabel(station.channel)}</span>
    </div>
    <div class="card-sub">
      <Bluetooth size={12} />
      <span class="mono addr">{station.address}</span>
      <span class="state-badge" class:unverified class:stale={stalePower} title={stalePower ? `Last known state; last successful read ${station.lastPowerReadAt || 'unknown'}` : ''}>
        {#if station.powerFresh && station.powerState === 3}<LoaderCircle class="spin" size={10} />{/if}
        {stateLabel(station)}{stalePower ? ' · stale' : unverified ? ' ?' : ''}
      </span>
      {#if !station.isPresent}<span class="muted-badge" title="Not detected in the latest scan; direct power control can still be attempted">not visible</span>{/if}
      {#if station.isPresent && !station.seenInLatestScan}<span class="muted-badge" title="Missed by one scan; retained until a second consecutive miss">scan stale</span>{/if}
    </div>
    {#if feedback}
      <div class="power-feedback {feedback.kind}">
        {#if feedback.kind === 'pending'}<LoaderCircle class="spin" size={11} />{/if}
        {feedback.text}
      </div>
    {/if}
    <div class="card-actions">
      <div class="power-segment" aria-label={`Power control for ${station.name}`}>
        <button
          class="seg-on"
          class:active={isCurrentPowerState(station, 'on')}
          class:pending={pendingTarget === 'on'}
          aria-pressed={isCurrentPowerState(station, 'on')}
          title="Turn lasers and motor on"
          on:click|stopPropagation={() => onPower(station, 'on')}
          disabled={!canSetPower(station, 'on') || busy || locked}
        ><Zap size={12} /> On</button>
        <button
          class="seg-standby"
          class:active={isCurrentPowerState(station, 'standby')}
          class:pending={pendingTarget === 'standby'}
          aria-pressed={isCurrentPowerState(station, 'standby')}
          title="Lasers off, motor remains powered"
          on:click|stopPropagation={() => onPower(station, 'standby')}
          disabled={!canSetPower(station, 'standby') || busy || locked}
        ><Pause size={12} /> Standby</button>
        <button
          class="seg-sleep"
          class:active={isCurrentPowerState(station, 'sleep')}
          class:pending={pendingTarget === 'sleep'}
          aria-pressed={isCurrentPowerState(station, 'sleep')}
          title="Turn lasers and motor off"
          on:click|stopPropagation={() => onPower(station, 'sleep')}
          disabled={!canSetPower(station, 'sleep') || busy || locked}
        ><Moon size={12} /> Sleep</button>
      </div>
      <button class="icon-btn details" title="Details" aria-label={`Details for ${station.name}`} on:click|stopPropagation={openDetails}><ChevronRight size={17} /></button>
    </div>
  {/if}
</div>

<style>
  .station-card {
    background: var(--bg-surface);
    backdrop-filter: blur(12px);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    padding: 0.6rem 0.7rem;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    box-shadow: var(--shadow-sm);
    transition: border-color var(--dur-2) var(--ease), background-color var(--dur-2) var(--ease),
      box-shadow var(--dur-2) var(--ease);
  }
  .station-card:hover { border-color: var(--color-border-strong); box-shadow: var(--shadow-md); }
  .station-card.conflict { border-color: rgba(248, 113, 113, 0.55); }
  .station-card.offline { border-style: dashed; }
  .station-card.offline .card-top, .station-card.offline .card-sub { opacity: 0.65; }
  .station-card.renaming { cursor: default; }

  .card-top { display: flex; align-items: center; gap: 0.4rem; min-width: 0; }
  .card-top h3 {
    margin: 0;
    font-size: 0.95rem;
    font-weight: 700;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .spacer { flex: 1; }
  .rename-btn { min-width: 24px; min-height: 24px; padding: 0.2rem; opacity: 0; transition: opacity var(--dur-1) var(--ease), background-color var(--dur-1) var(--ease), color var(--dur-1) var(--ease); }
  .station-card:hover .rename-btn, .station-card:focus-within .rename-btn { opacity: 1; }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    flex-shrink: 0;
    background: var(--text-muted);
  }
  .dot-on { background: var(--color-on); box-shadow: 0 0 8px color-mix(in srgb, var(--color-on) 60%, transparent); }
  .dot-sleep { background: var(--color-sleep); box-shadow: 0 0 8px color-mix(in srgb, var(--color-sleep) 50%, transparent); }
  .dot-standby { background: var(--color-standby); box-shadow: 0 0 8px color-mix(in srgb, var(--color-standby) 60%, transparent); }
  .dot-booting { background: var(--color-booting); box-shadow: 0 0 8px color-mix(in srgb, var(--color-booting) 60%, transparent); }
  .dot-unknown { background: var(--text-muted); }
  .status-dot.breathe { animation: dot-breathe 1.2s ease-in-out infinite; }

  .channel-chip { color: var(--text-secondary); text-transform: none; letter-spacing: 0.02em; }
  .channel-chip.warn { color: #fca5a5; border-color: rgba(248, 113, 113, 0.55); }
  .conflict-icon { color: var(--color-danger); flex-shrink: 0; }

  .card-sub {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    flex-wrap: wrap;
    color: var(--text-secondary);
    font-size: 0.72rem;
  }
  .addr { font-size: 0.72rem; color: var(--text-muted); }

  .power-feedback {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.7rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .power-feedback.pending { color: #93c5fd; }
  .power-feedback.success { color: #86efac; }
  .power-feedback.warning { color: var(--color-warning); }
  .power-feedback.error { color: #fca5a5; }

  .card-actions { display: flex; align-items: center; gap: 0.4rem; }
  .power-segment { flex: 1; }
  .power-segment button { flex: 1; }
  .details {
    width: 32px;
    height: 32px;
    padding: 0;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    flex-shrink: 0;
  }

  .rename-row { display: flex; align-items: center; gap: 0.35rem; }
  .rename-row input { flex: 1; min-width: 0; }
</style>

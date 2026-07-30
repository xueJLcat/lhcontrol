<script lang="ts">
  import { onDestroy } from 'svelte';
  import {
    Bluetooth, Check, ChevronRight, CircleCheck, CircleHelp, CircleX, History,
    LoaderCircle, Moon, Pause, SquarePen, TriangleAlert, X, Zap
  } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
  import { canSetPower, channelLabel, isCurrentPowerState, stateLabel } from '../station';
  import { relativeTime } from '../relative-time';
  import { autofocus } from '../actions';
  import StateBadge from './StateBadge.svelte';

  export let station: StationInfo;
  // Display-only fallback for the channel chip, bridging transient backend
  // channel wipes. All interaction logic keeps using the live station data.
  export let channelDisplay: number | undefined = undefined;
  export let renaming: boolean;
  export let feedback: PowerFeedback | undefined = undefined;
  export let pendingTarget: PowerTarget | undefined = undefined;
  export let gattBusy: boolean;
  export let configBusy: boolean;
  export let gattLocked: boolean;
  export let renameLocked: boolean;
  export let onPower: (station: StationInfo, state: PowerTarget) => void;
  export let onOpenDetails: (station: StationInfo) => void;
  export let onStartRename: (station: StationInfo) => void;
  export let onSaveRename: (station: StationInfo, name: string) => void;
  export let onCancelRename: () => void;

  let localName = '';
  let wasRenaming = false;
  let prevPowerState: number | null = null;
  let flash = false;
  let flashTimer: ReturnType<typeof setTimeout> | null = null;

  $: if (renaming !== wasRenaming) {
    if (renaming) localName = station.name;
    wasRenaming = renaming;
  }

  $: if (station.powerState !== prevPowerState) {
    if (prevPowerState !== null && station.powerState >= 0) {
      flash = true;
      if (flashTimer) clearTimeout(flashTimer);
      flashTimer = setTimeout(() => {
        flash = false;
        flashTimer = null;
      }, 1100);
    }
    prevPowerState = station.powerState;
  }

  onDestroy(() => {
    if (flashTimer) clearTimeout(flashTimer);
  });

  $: hasKnownPower = station.powerState >= 0;
  $: stalePower = hasKnownPower && !station.powerFresh;
  $: unverified = hasKnownPower && station.powerFresh && !station.powerStateConfirmed;
  $: shownChannel = station.channel > 0 ? station.channel : (channelDisplay ?? 0);
  $: channelLastKnown = station.channel <= 0 && shownChannel > 0;

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
  class:flash
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
      <button type="button" class="icon-btn" title="Save name" on:mousedown|preventDefault on:click|stopPropagation={() => onSaveRename(station, localName.trim())}><Check size={16} /></button>
      <button type="button" class="icon-btn" title="Cancel" on:mousedown|preventDefault on:click|stopPropagation={onCancelRename}><X size={16} /></button>
    </div>
  {:else}
    <div class="card-top">
      <span class="status-dot dot-{stateLabel(station)}" aria-hidden="true"></span>
      <h3 title={station.name}>{station.name}</h3>
      <button
        class="icon-btn rename-btn"
        title="Rename"
        aria-label={`Rename ${station.name}`}
        on:click|stopPropagation={() => onStartRename(station)}
        disabled={gattBusy || configBusy || renameLocked}
      ><SquarePen size={13} /></button>
      <span class="spacer"></span>
      {#if station.channelConflict}<TriangleAlert size={13} class="conflict-icon" />{/if}
      <span
        class="channel-chip mono"
        class:warn={station.channelConflict}
        class:stale={channelLastKnown}
        title={channelLastKnown ? 'Last known channel' : undefined}
      >{channelLabel(shownChannel)}</span>
    </div>
    <div class="card-sub">
      <Bluetooth size={12} />
      <span class="mono addr">{station.address}</span>
      <StateBadge label={stateLabel(station)} {unverified} stale={stalePower} booting={station.powerFresh && station.powerState === 3} />
      {#if stalePower}
        <span class="fresh-icon stale" title={`Last known state; last successful read ${relativeTime(station.lastPowerReadAt) || 'unknown'}`}><History size={11} /></span>
      {:else if unverified}
        <span class="fresh-icon unverified" title="State reported by the station but not confirmed by readback"><CircleHelp size={11} /></span>
      {/if}
      {#if !station.isPresent}<span class="muted-badge" title="Not detected in the latest scan; direct power control can still be attempted">not visible</span>{/if}
      {#if station.isPresent && !station.seenInLatestScan}<span class="muted-badge" title="Missed by one scan; retained until a second consecutive miss">scan stale</span>{/if}
    </div>
    {#if feedback}
      <div class="power-feedback {feedback.kind}">
        {#if feedback.kind === 'pending'}<LoaderCircle class="spin" size={11} />
        {:else if feedback.kind === 'success'}<CircleCheck size={11} />
        {:else if feedback.kind === 'warning'}<TriangleAlert size={11} />
        {:else}<CircleX size={11} />{/if}
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
          aria-label={`Turn ${station.name} on`}
          title="Turn lasers and motor on"
          on:click|stopPropagation={() => onPower(station, 'on')}
          disabled={!canSetPower(station, 'on') || gattBusy || configBusy || gattLocked}
        ><Zap size={12} /> On</button>
        <button
          class="seg-standby"
          class:active={isCurrentPowerState(station, 'standby')}
          class:pending={pendingTarget === 'standby'}
          aria-pressed={isCurrentPowerState(station, 'standby')}
          aria-label={`Set ${station.name} to standby`}
          title="Lasers off, motor remains powered"
          on:click|stopPropagation={() => onPower(station, 'standby')}
          disabled={!canSetPower(station, 'standby') || gattBusy || configBusy || gattLocked}
        ><Pause size={12} /> Standby</button>
        <button
          class="seg-sleep"
          class:active={isCurrentPowerState(station, 'sleep')}
          class:pending={pendingTarget === 'sleep'}
          aria-pressed={isCurrentPowerState(station, 'sleep')}
          aria-label={`Put ${station.name} to sleep`}
          title="Turn lasers and motor off"
          on:click|stopPropagation={() => onPower(station, 'sleep')}
          disabled={!canSetPower(station, 'sleep') || gattBusy || configBusy || gattLocked}
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
    border-left: 2px solid transparent;
    border-radius: var(--radius-lg);
    padding: 0.6rem 0.7rem;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    box-shadow: var(--shadow-sm);
    transition: border-color var(--dur-2) var(--ease), background-color var(--dur-2) var(--ease),
      box-shadow var(--dur-2) var(--ease), transform var(--dur-2) var(--ease);
  }
  .station-card:hover { border-color: var(--color-border-strong); box-shadow: var(--shadow-md); transform: translateY(-1px); }
  .station-card.state-on { border-left-color: color-mix(in srgb, var(--color-on) 65%, transparent); --flash: var(--color-on); }
  .station-card.state-standby { border-left-color: color-mix(in srgb, var(--color-standby) 65%, transparent); --flash: var(--color-standby); }
  .station-card.state-sleep { border-left-color: color-mix(in srgb, var(--color-sleep) 55%, transparent); --flash: var(--color-sleep); }
  .station-card.state-booting { border-left-color: color-mix(in srgb, var(--color-booting) 65%, transparent); --flash: var(--color-booting); }
  .station-card.flash { animation: state-flash 1.1s var(--ease); }
  @keyframes state-flash {
    0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--flash, var(--color-primary)) 55%, transparent); }
    100% { box-shadow: 0 0 0 14px transparent; }
  }
  .station-card.conflict { border-color: color-mix(in srgb, var(--color-danger) 55%, transparent); }
  .station-card.conflict:hover { border-color: color-mix(in srgb, var(--color-danger) 70%, transparent); }
  .station-card.offline { border-style: dashed; border-left-style: solid; border-left-color: color-mix(in srgb, var(--text-muted) 40%, transparent); }
  .station-card.offline .card-top, .station-card.offline .card-sub { opacity: 0.65; }
  .station-card.offline .state-badge {
    color: var(--text-muted);
    border-color: var(--color-border);
    background: transparent;
  }
  .station-card.renaming { cursor: default; }

  .card-top { display: flex; align-items: center; gap: 0.4rem; min-width: 0; }
  .card-top h3 {
    margin: 0;
    font-size: var(--fs-title);
    font-weight: 800;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .spacer { flex: 1; }
  .rename-btn { min-width: 24px; min-height: 24px; padding: 0.2rem; opacity: 0.45; transition: opacity var(--dur-1) var(--ease), background-color var(--dur-1) var(--ease), color var(--dur-1) var(--ease); }
  .station-card:hover .rename-btn, .station-card:focus-within .rename-btn { opacity: 1; }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: var(--radius-pill);
    flex-shrink: 0;
    background: var(--text-muted);
    transition: background-color var(--dur-2) var(--ease), box-shadow var(--dur-2) var(--ease);
  }
  .dot-on { background: var(--color-on); box-shadow: 0 0 8px color-mix(in srgb, var(--color-on) 60%, transparent); }
  .dot-sleep { background: var(--color-sleep); box-shadow: 0 0 8px color-mix(in srgb, var(--color-sleep) 45%, transparent); }
  .dot-standby { background: var(--color-standby); box-shadow: 0 0 8px color-mix(in srgb, var(--color-standby) 60%, transparent); }
  .dot-booting { background: var(--color-booting); box-shadow: 0 0 8px color-mix(in srgb, var(--color-booting) 60%, transparent); }
  .dot-unknown { background: transparent; border: 1.5px solid var(--text-muted); box-sizing: border-box; }
  .station-card.offline .status-dot { background: transparent; border: 1.5px solid var(--text-muted); box-shadow: none; box-sizing: border-box; }

  .channel-chip { color: var(--text-secondary); text-transform: none; letter-spacing: 0.02em; }
  .channel-chip.warn { color: var(--fb-error); border-color: color-mix(in srgb, var(--color-danger) 55%, transparent); }
  .channel-chip.stale { opacity: 0.65; border-bottom: 1px dashed var(--color-border-strong); }
  .conflict-icon { color: var(--color-danger); flex-shrink: 0; }

  .card-sub {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    flex-wrap: wrap;
    color: var(--text-secondary);
    font-size: var(--fs-sm);
  }
  .addr { font-size: 0.72rem; color: var(--text-muted); }

  .fresh-icon { display: inline-flex; align-items: center; flex-shrink: 0; }
  .fresh-icon.stale { color: var(--text-muted); }
  .fresh-icon.unverified { color: var(--color-warning); }

  .power-feedback {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: var(--fs-micro);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .power-feedback > :global(svg) { flex-shrink: 0; }
  .power-feedback.pending { color: var(--fb-pending); }
  .power-feedback.success { color: var(--fb-success); }
  .power-feedback.warning { color: var(--fb-warning); }
  .power-feedback.error { color: var(--fb-error); }

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

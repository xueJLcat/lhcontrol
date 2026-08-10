<script lang="ts">
  import { onDestroy } from 'svelte';
  import { cubicOut } from 'svelte/easing';
  import { fade, fly } from 'svelte/transition';
  import {
    Bluetooth, Check, ChevronRight, CircleCheck, CircleHelp, CircleX, History,
    LoaderCircle, Moon, Pause, SquarePen, TriangleAlert, X, Zap
  } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
  import { canSetPower, channelLabel, isCurrentPowerState, stateClass, stateLabel } from '../station';
  import { relativeTime } from '../relative-time';
  import { autofocus } from '../actions';
  import { dur } from '../motion';
  import { t } from '../i18n.svelte';
  import StateBadge from './StateBadge.svelte';

  let {
    station,
    channelDisplay = undefined,
    renaming,
    feedback = undefined,
    pendingTarget = undefined,
    gattBusy,
    configBusy,
    gattLocked,
    renameLocked,
    onPower,
    onOpenDetails,
    onStartRename,
    onSaveRename,
    onCancelRename
  }: {
    station: StationInfo;
    // Display-only fallback for the channel chip, bridging transient backend
    // channel wipes. All interaction logic keeps using the live station data.
    channelDisplay?: number | undefined;
    renaming: boolean;
    feedback?: PowerFeedback | undefined;
    pendingTarget?: PowerTarget | undefined;
    gattBusy: boolean;
    configBusy: boolean;
    gattLocked: boolean;
    renameLocked: boolean;
    onPower: (station: StationInfo, state: PowerTarget) => void;
    onOpenDetails: (station: StationInfo) => void;
    onStartRename: (station: StationInfo) => void;
    onSaveRename: (station: StationInfo, name: string) => void;
    onCancelRename: () => void;
  } = $props();

  let localName = $state('');
  let wasRenaming = false;
  let prevPowerState: number | null = null;
  let flash = $state(false);
  let flashTimer: ReturnType<typeof setTimeout> | null = null;

  $effect(() => {
    if (renaming !== wasRenaming) {
      if (renaming) localName = station.name;
      wasRenaming = renaming;
    }
  });

  function commitRename() {
    onSaveRename(station, localName.trim());
  }

  function discardRename() {
    onCancelRename();
  }

  function handleRenameBlur(event: FocusEvent) {
    // Chromium fires a synchronous blur when the focused input is removed
    // from the DOM; the node is already detached at that point. A genuine
    // focus loss targets the still-connected input. Suppressing only the
    // removal-triggered blur keeps an explicit save or cancel from double
    // saving the draft while a parent that rejects the action (leaving the
    // row open) keeps blur-to-save fully functional.
    const target = event.target;
    if (target instanceof Element && !target.isConnected) return;
    onSaveRename(station, localName.trim());
  }

  $effect(() => {
    if (station.powerState !== prevPowerState) {
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
  });

  // Confirmed-state index drives the sliding segment thumb: 0=On 1=Standby
  // 2=Sleep, -1 keeps the thumb hidden.
  const activePowerIndex = $derived(isCurrentPowerState(station, 'on')
    ? 0
    : isCurrentPowerState(station, 'standby')
      ? 1
      : isCurrentPowerState(station, 'sleep')
        ? 2
        : -1);

  // One-shot pop on the active segment when a power operation settles
  // successfully; keyed by createdAt so a re-shown feedback does not replay.
  let popActive = $state(false);
  let popTimer: ReturnType<typeof setTimeout> | null = null;
  let lastPopCreatedAt: number | undefined;
  $effect(() => {
    if (feedback && feedback.kind === 'success' && feedback.createdAt !== lastPopCreatedAt) {
      lastPopCreatedAt = feedback.createdAt;
      popActive = true;
      if (popTimer) clearTimeout(popTimer);
      popTimer = setTimeout(() => {
        popActive = false;
        popTimer = null;
      }, 700);
    }
  });

  onDestroy(() => {
    if (flashTimer) clearTimeout(flashTimer);
    if (popTimer) clearTimeout(popTimer);
  });

  const hasKnownPower = $derived(station.powerState >= 0);
  const stalePower = $derived(hasKnownPower && !station.powerFresh);
  const unverified = $derived(hasKnownPower && station.powerFresh && !station.powerStateConfirmed);

  // Display hysteresis for the channel chip. The keyed each upstream reuses
  // this card while the station object is unchanged, so the expiry must run
  // here: while the live channel is wiped the last known value persists for
  // CHANNEL_MEMORY_MS before falling back to CH --.
  const CHANNEL_MEMORY_MS = 45_000;
  // Intentional one-time capture: channelDisplay seeds the hysteresis when a
  // card mounts during a transient wipe; after that the card tracks changes
  // through the station prop itself.
  // svelte-ignore state_referenced_locally
  let lastKnownChannel = $state(channelDisplay ?? 0);
  // The memory window runs from the first observed wipe, not from the last
  // confirmed channel: unchanged snapshots reuse the same object reference,
  // so an "age of the last confirmed value" timestamp silently goes stale
  // while a long-lived channel looks unchanged and the bridge collapses the
  // moment the wipe happens.
  // svelte-ignore state_referenced_locally
  let wipeStartedAt = $state<number | null>(station.channel > 0 ? null : Date.now());
  $effect(() => {
    if (station.channel > 0) {
      lastKnownChannel = station.channel;
      wipeStartedAt = null;
    } else if (wipeStartedAt === null) {
      wipeStartedAt = Date.now();
    }
  });
  $effect(() => {
    if (station.channel > 0 || lastKnownChannel <= 0) return;
    const timer = setInterval(() => {
      if (wipeStartedAt !== null && Date.now() - wipeStartedAt > CHANNEL_MEMORY_MS) lastKnownChannel = 0;
    }, 15_000);
    return () => clearInterval(timer);
  });
  const shownChannel = $derived(station.channel > 0 ? station.channel : lastKnownChannel);
  const channelLastKnown = $derived(station.channel <= 0 && shownChannel > 0);

  function openDetails() {
    if (!renaming) onOpenDetails(station);
  }

  function handleRenameKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.stopPropagation();
      commitRename();
    } else if (event.key === 'Escape') {
      event.stopPropagation();
      // The save already runs; discarding now would only hide a rename that
      // still lands on the backend.
      if (!configBusy) discardRename();
    }
  }
</script>

<!-- Card-wide click is a mouse convenience; keyboard users open details
     through the dedicated Details button. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="station-card state-{stateClass(station)}"
  class:offline={!station.isPresent}
  class:conflict={station.channelConflict}
  class:renaming
  class:flash
  onclick={openDetails}
>
  {#if renaming}
    <div class="rename-row">
      <input
        use:autofocus
        bind:value={localName}
        maxlength="32"
        placeholder={station.originalName}
        title={t('Save an empty name to restore the original name')}
        aria-label={t('Station name')}
        aria-describedby={`rename-hint-${station.address}`}
        onkeydown={handleRenameKeydown}
        onblur={handleRenameBlur}
        onclick={(event) => event.stopPropagation()}
      />
      <button type="button" class="icon-btn" title={t('Save name')} aria-label={t('Save name')} onmousedown={(event) => event.preventDefault()} onclick={(event) => { event.stopPropagation(); commitRename(); }} disabled={configBusy}><Check size={16} /></button>
      <button type="button" class="icon-btn" title={t('Cancel')} aria-label={t('Cancel rename')} onmousedown={(event) => event.preventDefault()} onclick={(event) => { event.stopPropagation(); discardRename(); }} disabled={configBusy}><X size={16} /></button>
      <span class="sr-only" id={`rename-hint-${station.address}`}>{t('Saving an empty name restores the original name: {name}.', { name: station.originalName })}</span>
    </div>
  {:else}
    <div class="card-top">
      <span class="status-dot dot-{stateClass(station)}" aria-hidden="true"></span>
      <h3 title={station.name}>{station.name}</h3>
      <button
        class="icon-btn rename-btn"
        title={t('Rename')}
        aria-label={t('Rename {name}', { name: station.name })}
        onclick={(event) => { event.stopPropagation(); onStartRename(station); }}
        disabled={gattBusy || configBusy || renameLocked}
      ><SquarePen size={13} /></button>
      <span class="spacer"></span>
      {#if station.channelConflict}<TriangleAlert size={13} class="conflict-icon" />{/if}
      <span
        class="channel-chip mono"
        class:warn={station.channelConflict}
        class:stale={channelLastKnown}
        title={channelLastKnown ? t('Last known channel') : undefined}
      >{channelLabel(shownChannel)}</span>
    </div>
  {/if}
  <!-- Sub-state, feedback and actions stay mounted while renaming so the
       card keeps its height and the grid row does not jump. -->
  <div class="card-sub">
    <Bluetooth size={12} />
    <span class="mono addr">{station.address}</span>
    <StateBadge label={stateLabel(station)} state={stateClass(station)} {unverified} stale={stalePower} booting={station.powerFresh && station.powerState === 3} />
    {#if stalePower}
      <span class="fresh-icon stale" role="img" title={t('Last known state; last successful read {time}', { time: relativeTime(station.lastPowerReadAt) || t('unknown') })} aria-label={t('Last known state; last successful read {time}', { time: relativeTime(station.lastPowerReadAt) || t('unknown') })}><History size={11} aria-hidden="true" /></span>
    {:else if unverified}
      <span class="fresh-icon unverified" role="img" title={t('State reported by the station but not confirmed by readback')} aria-label={t('State reported by the station but not confirmed by readback')}><CircleHelp size={11} aria-hidden="true" /></span>
    {/if}
    {#if !station.isPresent}<span class="muted-badge" title={t('Not detected in the latest scan; direct power control can still be attempted')}>{t('not visible')}<span class="sr-only"> — {t('not detected in the latest scan, but direct power control can still be attempted')}</span></span>{/if}
    {#if station.isPresent && station.presenceUncertain}<span class="muted-badge" title={t('Its connection could not be fully released before the last scan, so the advertisement may have been missed')}>{t('presence uncertain')}<span class="sr-only"> — {t('the connection could not be fully released before the last scan, so the advertisement may have been missed')}</span></span>{/if}
    {#if station.isPresent && !station.presenceUncertain && !station.seenInLatestScan}<span class="muted-badge" title={t('Missed by one scan; retained until a second consecutive miss')}>{t('scan stale')}<span class="sr-only"> — {t('missed by one scan, retained until a second consecutive miss')}</span></span>{/if}
  </div>
  {#if feedback}
    <div
      class="power-feedback {feedback.kind}"
      title={feedback.text}
      in:fly={dur({ y: 4, duration: 160, easing: cubicOut })}
      out:fade={dur({ duration: 120 })}
    >
      {#if feedback.kind === 'pending'}<LoaderCircle class="spin" size={11} />
      {:else if feedback.kind === 'success'}<CircleCheck size={11} />
      {:else if feedback.kind === 'warning'}<TriangleAlert size={11} />
      {:else}<CircleX size={11} />{/if}
      {feedback.text}
    </div>
  {/if}
  <div class="card-actions">
    <div class="power-segment" role="group" class:pop={popActive} aria-label={t('Power control for {name}', { name: station.name })}>
      <div
        class="seg-thumb"
        class:seg-thumb-on={activePowerIndex === 0}
        class:seg-thumb-standby={activePowerIndex === 1}
        class:seg-thumb-sleep={activePowerIndex === 2}
        style:transform={`translateX(${activePowerIndex * 100}%)`}
        style:opacity={activePowerIndex >= 0 ? 1 : 0}
        aria-hidden="true"
      ></div>
      <button
        class="seg-on"
        class:active={isCurrentPowerState(station, 'on')}
        class:pending={pendingTarget === 'on'}
        aria-pressed={isCurrentPowerState(station, 'on')}
        aria-label={t('Turn {name} on', { name: station.name })}
        title={t('Turn lasers and motor on')}
        onclick={(event) => { event.stopPropagation(); onPower(station, 'on'); }}
        disabled={renaming || !canSetPower(station, 'on') || gattBusy || configBusy || gattLocked}
      ><Zap size={12} /> {t('On')}</button>
      <button
        class="seg-standby"
        class:active={isCurrentPowerState(station, 'standby')}
        class:pending={pendingTarget === 'standby'}
        aria-pressed={isCurrentPowerState(station, 'standby')}
        aria-label={t('Set {name} to standby', { name: station.name })}
        title={t('Lasers off, motor remains powered')}
        onclick={(event) => { event.stopPropagation(); onPower(station, 'standby'); }}
        disabled={renaming || !canSetPower(station, 'standby') || gattBusy || configBusy || gattLocked}
      ><Pause size={12} /> {t('Standby')}</button>
      <button
        class="seg-sleep"
        class:active={isCurrentPowerState(station, 'sleep')}
        class:pending={pendingTarget === 'sleep'}
        aria-pressed={isCurrentPowerState(station, 'sleep')}
        aria-label={t('Put {name} to sleep', { name: station.name })}
        title={t('Turn lasers and motor off')}
        onclick={(event) => { event.stopPropagation(); onPower(station, 'sleep'); }}
        disabled={renaming || !canSetPower(station, 'sleep') || gattBusy || configBusy || gattLocked}
      ><Moon size={12} /> {t('Sleep')}</button>
    </div>
    <button class="icon-btn details" title={t('Details')} aria-label={t('Details for {name}', { name: station.name })} onclick={(event) => { event.stopPropagation(); openDetails(); }} disabled={renaming}><ChevronRight size={17} /></button>
  </div>
</div>

<style>
  .station-card {
    position: relative;
    /* Fill the flip wrapper so cards in the same grid row share one height
       even when a neighbour carries an extra feedback line. */
    height: 100%;
    box-sizing: border-box;
    background: var(--bg-surface-solid);
    /* Uniform 1px border: the 3px transparent left edge used to break the
       corner arcs; the gradient bar (::before below) carries the accent.
       overflow:hidden clips that bar to the rounded silhouette so its ends
       never poke past the 20px corner arcs. */
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    overflow: hidden;
    padding: 0.65rem 0.7rem;
    display: flex;
    flex-direction: column;
    gap: 0.42rem;
    box-shadow: var(--shadow-sm), inset 0 1px 0 rgba(255, 255, 255, 0.8);
    cursor: pointer;
    transition: border-color var(--dur-2) var(--ease), background-color var(--dur-2) var(--ease),
      box-shadow var(--dur-2) var(--ease), transform var(--dur-2) var(--ease-spring);
  }
  .station-card:hover {
    border-color: var(--color-border-strong);
    box-shadow: 0 10px 24px -10px var(--glow, rgba(16, 24, 40, 0.16)), var(--shadow-sm),
      inset 0 1px 0 rgba(255, 255, 255, 0.8);
    transform: translateY(-2px);
  }
  .station-card.state-on { --flash: var(--color-on); --glow: var(--glow-on); }
  .station-card.state-standby { --flash: var(--color-standby); --glow: var(--glow-standby); }
  .station-card.state-sleep { --flash: var(--color-sleep); --glow: var(--glow-sleep); }
  .station-card.state-booting { --flash: var(--color-booting); --glow: var(--glow-booting); }
  /* Quiet edge hint: a thin solid strip at reduced opacity. The state color
     is already carried by the status dot, badge and segment thumb, so this
     stays a subtle accent instead of a fourth loud repetition. */
  .station-card::before {
    content: '';
    position: absolute;
    left: 0;
    top: 12px;
    bottom: 12px;
    width: 2px;
    border-radius: var(--radius-pill);
    opacity: 0;
    transition: opacity var(--dur-2) var(--ease);
  }
  .station-card.state-on::before {
    opacity: 0.65;
    background: var(--color-on);
  }
  .station-card.state-standby::before {
    opacity: 0.65;
    background: var(--color-standby);
  }
  .station-card.state-sleep::before {
    opacity: 0.65;
    background: var(--color-sleep);
  }
  .station-card.state-booting::before {
    opacity: 0.65;
    background: var(--color-booting);
  }
  .station-card.offline::before { opacity: 0.25; }
  .station-card.flash { animation: state-flash 1.1s var(--ease); }
  @keyframes state-flash {
    0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--flash, var(--color-primary)) 45%, transparent); }
    100% { box-shadow: 0 0 0 14px transparent; }
  }
  .station-card.conflict { border-color: color-mix(in srgb, var(--color-danger) 55%, transparent); }
  .station-card.conflict:hover { border-color: color-mix(in srgb, var(--color-danger) 75%, transparent); box-shadow: 0 8px 22px -8px var(--glow-danger), var(--shadow-sm); }
  .station-card.offline { border-style: dashed; background: color-mix(in srgb, var(--bg-surface-solid) 70%, var(--bg-app)); }
  .station-card.offline .card-top, .station-card.offline .card-sub { opacity: 0.65; }
  .station-card.offline .state-badge {
    color: var(--text-muted);
    border-color: var(--color-border-strong);
    background: transparent;
  }
  .station-card.renaming { cursor: default; }

  .card-top { display: flex; align-items: center; gap: 0.42rem; min-width: 0; }
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
  .rename-btn { min-width: 24px; min-height: 24px; padding: 0.2rem; opacity: 0.4; transition: opacity var(--dur-1) var(--ease), background-color var(--dur-1) var(--ease), color var(--dur-1) var(--ease); }
  .station-card:hover .rename-btn, .station-card:focus-within .rename-btn { opacity: 1; }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: var(--radius-pill);
    flex-shrink: 0;
    background: var(--text-muted);
    transition: background-color var(--dur-2) var(--ease), box-shadow var(--dur-2) var(--ease);
  }
  .dot-on { background: var(--color-on); box-shadow: 0 0 8px color-mix(in srgb, var(--color-on) 65%, transparent); }
  .dot-sleep { background: var(--color-sleep); box-shadow: 0 0 8px color-mix(in srgb, var(--color-sleep) 60%, transparent); }
  .dot-standby { background: var(--color-standby); box-shadow: 0 0 8px color-mix(in srgb, var(--color-standby) 65%, transparent); }
  .dot-booting { background: var(--color-booting); box-shadow: 0 0 8px color-mix(in srgb, var(--color-booting) 65%, transparent); animation: pulse-dot 1.2s ease-in-out infinite; }
  @keyframes pulse-dot { 50% { opacity: 0.45; } }
  .dot-unknown { background: transparent; border: 1.5px solid var(--text-muted); box-sizing: border-box; }
  .station-card.offline .status-dot { background: transparent; border: 1.5px solid var(--text-muted); box-shadow: none; box-sizing: border-box; }

  .channel-chip { color: var(--text-secondary); text-transform: none; letter-spacing: 0.02em; background: var(--bg-input); border-color: var(--color-border-strong); }
  .channel-chip.warn { color: var(--fb-error); border-color: color-mix(in srgb, var(--color-danger) 50%, transparent); background: linear-gradient(135deg, color-mix(in srgb, var(--color-danger) 10%, white), color-mix(in srgb, var(--color-warning) 6%, white)); }
  .channel-chip.stale { opacity: 0.7; border-bottom: 1px dashed var(--color-border-strong); }
  .conflict-icon { color: var(--color-danger); flex-shrink: 0; }

  .card-sub {
    display: flex;
    align-items: center;
    gap: 0.32rem;
    flex-wrap: wrap;
    color: var(--text-secondary);
    font-size: var(--fs-sm);
  }
  .card-sub > :global(svg) { color: color-mix(in srgb, var(--color-primary) 70%, var(--text-muted)); flex-shrink: 0; }
  .addr { font-size: var(--fs-xs); color: var(--text-muted); }

  .fresh-icon { display: inline-flex; align-items: center; flex-shrink: 0; }
  .fresh-icon.stale { color: var(--text-muted); }
  .fresh-icon.unverified { color: var(--color-warning); }

  .power-feedback {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: var(--fs-micro);
    font-weight: 700;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .power-feedback > :global(svg) { flex-shrink: 0; }
  .power-feedback.pending { color: var(--fb-pending); }
  .power-feedback.success { color: var(--fb-success); }
  .power-feedback.warning { color: var(--fb-warning); }
  .power-feedback.error { color: var(--fb-error); }

  /* margin-top:auto pins the actions to the bottom when the grid stretches
     this card to the row height, instead of leaving dead space under them. */
  .card-actions { display: flex; align-items: center; gap: 0.4rem; margin-top: auto; }
  .power-segment { flex: 1; }
  .power-segment button { flex: 1; min-width: 0; }
  .details {
    width: 34px;
    height: 34px;
    padding: 0;
    background: var(--bg-surface-solid);
    border: 1px solid var(--color-border-strong);
    border-radius: var(--radius-pill);
    flex-shrink: 0;
    box-shadow: var(--shadow-sm);
    transition: background-color var(--dur-1) var(--ease), color var(--dur-1) var(--ease),
      border-color var(--dur-1) var(--ease), transform var(--dur-1) var(--ease-spring),
      box-shadow var(--dur-2) var(--ease);
  }
  .details:hover:not(:disabled) {
    background: color-mix(in srgb, var(--color-primary) 9%, white);
    border-color: color-mix(in srgb, var(--color-primary) 38%, transparent);
    color: var(--color-primary-deep);
    transform: translateX(1px);
    /* Compact shadow: the card's overflow:hidden would clip a --shadow-md
       spread at the bottom-right padding. */
    box-shadow: 0 4px 12px -4px color-mix(in srgb, var(--color-primary) 35%, transparent), var(--shadow-sm);
  }

  .rename-row { display: flex; align-items: center; gap: 0.35rem; }
  /* Slightly tighter than the global input so the rename row stays close to
     the normal card-top height and the grid row barely grows. */
  .rename-row input { flex: 1; min-width: 0; padding: 0.3rem 0.5rem; }
  /* Self-contained focus ring instead of the global outline, which would
     stack a second ring around the hover shadow. */
  .rename-row input:focus {
    border-color: color-mix(in srgb, var(--color-primary) 55%, var(--color-border-strong));
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 12%, transparent);
    outline: none;
  }
</style>
